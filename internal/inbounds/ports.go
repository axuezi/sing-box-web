package inbounds

import (
	"bufio"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Socket is a bound local socket.
type Socket struct {
	Network string // tcp | udp
	Addr    netip.Addr
	Port    int
}

// ProcNet is where Bound reads socket tables. The panel runs in the host's
// network namespace, so these are the host's sockets.
var ProcNet = "/proc/net"

// Bound returns listening TCP sockets and bound UDP sockets. ok is false
// when the tables cannot be read (then nothing can be said).
func Bound() (socks []Socket, ok bool) {
	for _, f := range []struct{ name, network, state string }{
		{"tcp", "tcp", "0A"}, {"tcp6", "tcp", "0A"}, // LISTEN
		{"udp", "udp", "07"}, {"udp6", "udp", "07"}, // bound (CLOSE)
	} {
		file, err := os.Open(filepath.Join(ProcNet, f.name))
		if err != nil {
			continue
		}
		ok = true
		sc := bufio.NewScanner(file)
		sc.Scan() // header
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 || fields[3] != f.state {
				continue
			}
			if s, good := parseProcAddr(fields[1]); good {
				s.Network = f.network
				socks = append(socks, s)
			}
		}
		file.Close()
	}
	return socks, ok
}

// parseProcAddr parses "0100007F:1F90" (IPv4) or a 32-digit IPv6 address,
// each 32-bit word in host (little-endian) order.
func parseProcAddr(s string) (Socket, bool) {
	ipHex, portHex, found := strings.Cut(s, ":")
	if !found {
		return Socket{}, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return Socket{}, false
	}
	raw, err := hex.DecodeString(ipHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return Socket{}, false
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	addr, _ := netip.AddrFromSlice(raw)
	return Socket{Addr: addr.Unmap(), Port: int(port)}, true
}

// PortInUse reports a socket held by something other than sing-box's
// current inbounds that would stop f from binding, e.g. "tcp 127.0.0.1:9091".
func PortInUse(raw []byte, f Form, socks []Socket) string {
	nets := networks(gjson.Parse(`{"type":"` + f.Type + `"}`))
	// Ports the running sing-box already holds for configured inbounds
	// (including the one being edited) are expected to be bound.
	owned := func(s Socket) bool {
		hit := false
		gjson.GetBytes(raw, "inbounds").ForEach(func(_, v gjson.Result) bool {
			if int(v.Get("listen_port").Int()) == s.Port && slices.Contains(networks(v), s.Network) &&
				overlaps(v.Get("listen").String(), s.Addr.String()) {
				hit = true
			}
			return !hit
		})
		return hit
	}
	for _, s := range socks {
		if s.Port != f.ListenPort || !slices.Contains(nets, s.Network) || !overlaps(f.Listen, s.Addr.String()) || owned(s) {
			continue
		}
		return s.Network + " " + netip.AddrPortFrom(s.Addr, uint16(s.Port)).String()
	}
	return ""
}
