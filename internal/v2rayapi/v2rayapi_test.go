package v2rayapi

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func encodeStat(name string, v int64) []byte {
	var s []byte
	s = appendString(s, 1, name)
	s = appendVarint(s, 2<<3|0)
	s = appendVarint(s, uint64(v))
	var b []byte
	b = appendVarint(b, 1<<3|2)
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

func TestQueryStatsOverH2C(t *testing.T) {
	var gotPatterns []string
	var gotReset bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.URL.Path != queryStatsPath || r.Header.Get("Content-Type") != "application/grpc" {
			t.Errorf("bad request: proto %d path %s", r.ProtoMajor, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		fields(body[5:], func(num, wire int, val uint64, data []byte) error {
			if num == 3 {
				gotPatterns = append(gotPatterns, string(data))
			}
			if num == 2 {
				gotReset = val == 1
			}
			return nil
		})
		msg := append(encodeStat("user>>>alice>>>traffic>>>uplink", 1234), encodeStat("user>>>alice>>>traffic>>>downlink", 300000000000)...)
		frame := make([]byte, 5)
		binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write(append(frame, msg...))
		w.Header().Set("Grpc-Status", "0")
	})
	srv := httptest.NewUnstartedServer(h)
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	srv.Start()
	defer srv.Close()

	c := &Client{Addr: strings.TrimPrefix(srv.URL, "http://")}
	stats, err := c.QueryStats(context.Background(), []string{"user>>>", "inbound>>>"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotPatterns, ",") != "user>>>,inbound>>>" || !gotReset {
		t.Fatalf("request patterns=%v reset=%v", gotPatterns, gotReset)
	}
	if len(stats) != 2 || stats[0].Value != 1234 || stats[1].Value != 300000000000 {
		t.Fatalf("stats = %+v", stats)
	}
	c2, ok := ParseName(stats[1].Name)
	if !ok || c2 != (Counter{"user", "alice", "downlink"}) {
		t.Fatalf("parse = %+v", c2)
	}
}

func TestGRPCError(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "12")
		w.Header().Set("Grpc-Message", "unknown%20service")
	})
	srv := httptest.NewUnstartedServer(h)
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	srv.Start()
	defer srv.Close()
	_, err := (&Client{Addr: strings.TrimPrefix(srv.URL, "http://")}).QueryStats(context.Background(), []string{"x"}, false)
	if err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("err = %v", err)
	}
}

// TestClientsShareConnection guards against a connection leak: the
// collector builds a new Client every minute, and each used to open (and
// keep) its own HTTP/2 connection to sing-box.
func TestClientsShareConnection(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write(make([]byte, 5))
		w.Header().Set("Grpc-Status", "0")
	})
	srv := httptest.NewUnstartedServer(h)
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	var conns atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	for range 20 {
		if _, err := (&Client{Addr: addr}).QueryStats(context.Background(), []string{"user>>>"}, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("20 clients opened %d connections, want 1", n)
	}
}

// TestLive queries a real sing-box when SBW_TEST_V2RAY is set (host:port).
func TestLive(t *testing.T) {
	addr := os.Getenv("SBW_TEST_V2RAY")
	if addr == "" {
		t.Skip("SBW_TEST_V2RAY not set")
	}
	stats, err := (&Client{Addr: addr}).QueryStats(context.Background(), []string{"user>>>", "inbound>>>", "outbound>>>"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stats {
		t.Logf("%s = %d", s.Name, s.Value)
	}
}
