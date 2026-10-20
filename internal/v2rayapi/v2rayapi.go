// Package v2rayapi queries sing-box's V2Ray-compatible stats service
// (experimental.v2ray_api). It speaks just enough gRPC — one unary call over
// cleartext HTTP/2 with hand-encoded protobuf — to avoid pulling in the
// whole gRPC stack for a single RPC.
package v2rayapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// sing-box renames its stats service at init for V2Ray compatibility
// (experimental/v2rayapi/stats.go), so the .proto package name is not the
// one that answers.
const queryStatsPath = "/v2ray.core.app.stats.command.StatsService/QueryStats"

// Stat is one counter, e.g. "user>>>alice>>>traffic>>>uplink".
type Stat struct {
	Name  string
	Value int64
}

// Client calls the stats service at Addr (host:port). It is cheap to
// create per call: all Clients without HTTP share one transport.
type Client struct {
	Addr string
	HTTP *http.Client
}

// sharedHTTP is the default HTTP client. Each http.Transport keeps its own
// HTTP/2 connection open, so a transport per Client leaked one connection
// to sing-box per collection; the idle timeout also closes the shared one
// once collections stop (e.g. after the API address changes).
var sharedHTTP = sync.OnceValue(func() *http.Client {
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	return &http.Client{
		Transport: &http.Transport{Protocols: &p, IdleConnTimeout: 5 * time.Minute},
		Timeout:   5 * time.Second,
	}
})

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return sharedHTTP()
}

// QueryStats returns counters matching any of patterns (substring match);
// with reset they are zeroed atomically as they are read.
func (c *Client) QueryStats(ctx context.Context, patterns []string, reset bool) ([]Stat, error) {
	var msg []byte
	for _, p := range patterns {
		msg = appendString(msg, 3, p)
	}
	if reset {
		msg = append(msg, 2<<3|0, 1)
	}
	resp, err := c.call(ctx, queryStatsPath, msg)
	if err != nil {
		return nil, err
	}
	return decodeQueryStatsResponse(resp)
}

const sysStatsPath = "/v2ray.core.app.stats.command.StatsService/GetSysStats"

// Uptime returns how long the stats service has been running. sing-box
// creates a new one on every start and reload, which is exactly when its
// counters go back to zero.
func (c *Client) Uptime(ctx context.Context) (time.Duration, error) {
	resp, err := c.call(ctx, sysStatsPath, nil)
	if err != nil {
		return 0, err
	}
	var up uint64
	found := false
	err = fields(resp, func(num, wire int, val uint64, _ []byte) error {
		if num == 10 && wire == 0 { // SysStatsResponse.Uptime
			up, found = val, true
		}
		return nil
	})
	if err == nil && !found && len(resp) > 0 {
		err = errors.New("no uptime in GetSysStats response")
	}
	return time.Duration(up) * time.Second, err
}

func (c *Client) call(ctx context.Context, path string, msg []byte) ([]byte, error) {
	frame := make([]byte, 5, 5+len(msg))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
	frame = append(frame, msg...)

	u := url.URL{Scheme: "http", Host: c.Addr, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	status := resp.Trailer.Get("Grpc-Status")
	if status == "" {
		status = resp.Header.Get("Grpc-Status") // trailers-only response
	}
	if status != "" && status != "0" {
		m, _ := url.PathUnescape(firstNonEmpty(resp.Trailer.Get("Grpc-Message"), resp.Header.Get("Grpc-Message")))
		return nil, fmt.Errorf("grpc status %s: %s", status, m)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("v2ray api: %s", resp.Status)
	}
	if len(body) < 5 {
		return nil, errors.New("v2ray api: short response")
	}
	if body[0] != 0 {
		return nil, errors.New("v2ray api: compressed responses are not supported")
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if int(n) > len(body)-5 {
		return nil, errors.New("v2ray api: truncated response")
	}
	return body[5 : 5+n], nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- minimal protobuf ---

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendString(b []byte, field int, s string) []byte {
	b = appendVarint(b, uint64(field<<3|2))
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("protobuf: bad varint")
}

// fields calls fn for each top-level field of a message. For length-
// delimited fields data is the payload; for varints val is the value.
func fields(b []byte, fn func(num int, wire int, val uint64, data []byte) error) error {
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return err
		}
		b = b[n:]
		num, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, n, err := readVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
			if err := fn(num, wire, v, nil); err != nil {
				return err
			}
		case 2:
			l, n, err := readVarint(b)
			if err != nil || uint64(len(b)-n) < l {
				return errors.New("protobuf: bad length")
			}
			if err := fn(num, wire, 0, b[n:n+int(l)]); err != nil {
				return err
			}
			b = b[n+int(l):]
		case 1:
			if len(b) < 8 {
				return errors.New("protobuf: short fixed64")
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return errors.New("protobuf: short fixed32")
			}
			b = b[4:]
		default:
			return fmt.Errorf("protobuf: unsupported wire type %d", wire)
		}
	}
	return nil
}

func decodeQueryStatsResponse(b []byte) ([]Stat, error) {
	var out []Stat
	err := fields(b, func(num, wire int, _ uint64, data []byte) error {
		if num != 1 || wire != 2 {
			return nil
		}
		var s Stat
		if err := fields(data, func(num, wire int, val uint64, data []byte) error {
			switch {
			case num == 1 && wire == 2:
				s.Name = string(data)
			case num == 2 && wire == 0:
				s.Value = int64(val)
			}
			return nil
		}); err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	return out, err
}

// Counter is a parsed stat name: kind is user, inbound or outbound;
// direction is uplink or downlink.
type Counter struct {
	Kind, Name, Direction string
}

// ParseName splits "user>>>alice>>>traffic>>>uplink".
func ParseName(s string) (Counter, bool) {
	p := strings.Split(s, ">>>")
	if len(p) != 4 || p[2] != "traffic" || (p[3] != "uplink" && p[3] != "downlink") {
		return Counter{}, false
	}
	return Counter{Kind: p[0], Name: p[1], Direction: p[3]}, true
}
