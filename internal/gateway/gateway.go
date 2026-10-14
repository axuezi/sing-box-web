// Package gateway recovers client addresses hidden by a TCP proxy in front
// of sing-box.
//
// When HAProxy (or similar) forwards a connection to sing-box, sing-box only
// sees the proxy's own address, e.g. 127.0.0.1:41234, and sing-box no longer
// accepts the PROXY protocol. The proxy can however log every forwarded
// connection as it opens: the client address and the local address it used
// towards sing-box. The Resolver receives those lines as syslog datagrams
// and maps "proxy-side address" → client IP. With HAProxy:
//
//	log 127.0.0.1:5514 format raw local0
//	log-format "sbw-gw %ci %bi:%bp"
//	option logasap
//
// Lines without the "sbw-gw" marker are ignored.
package gateway

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Marker starts every line the Resolver understands.
const Marker = "sbw-gw"

const (
	entryTTL   = 24 * time.Hour // long-lived connections keep their mapping this long
	maxEntries = 100_000
)

type entry struct {
	client netip.Addr
	at     time.Time
}

// Resolver maps proxy-side source addresses back to client IPs. The zero
// value is usable (and resolves nothing until fed); a nil *Resolver too.
type Resolver struct {
	Listen string // UDP address to receive log lines on, e.g. 127.0.0.1:5514
	Logger *slog.Logger
	Now    func() time.Time

	mu     sync.Mutex
	byAddr map[netip.AddrPort]entry
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run receives log datagrams until ctx is done.
func (r *Resolver) Run(ctx context.Context) {
	if r.Listen == "" {
		return
	}
	pc, err := net.ListenPacket("udp", r.Listen)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Warn("gateway: cannot listen for proxy logs", "addr", r.Listen, "err", err)
		}
		return
	}
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	buf := make([]byte, 4096)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			r.Feed(line)
		}
	}
}

// Feed takes one log line: "<anything> sbw-gw <client ip> <proxy ip>:<proxy port>".
func (r *Resolver) Feed(line string) {
	_, rest, ok := strings.Cut(line, Marker+" ")
	if !ok {
		return
	}
	f := strings.Fields(rest)
	if len(f) < 2 {
		return
	}
	client, err := netip.ParseAddr(strings.Trim(f[0], "[]"))
	if err != nil {
		return
	}
	local, err := netip.ParseAddrPort(f[1])
	if err != nil {
		return // e.g. "-:-" for a connection refused before reaching sing-box
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byAddr == nil {
		r.byAddr = map[netip.AddrPort]entry{}
	}
	if len(r.byAddr) >= maxEntries {
		r.prune(now)
	}
	r.byAddr[unmap(local)] = entry{client.Unmap(), now}
}

func (r *Resolver) prune(now time.Time) {
	for k, e := range r.byAddr {
		if now.Sub(e.at) > entryTTL {
			delete(r.byAddr, k)
		}
	}
	if len(r.byAddr) >= maxEntries { // still full: start over rather than grow
		r.byAddr = map[netip.AddrPort]entry{}
	}
}

// Client returns the real client IP for a connection sing-box saw coming
// from ip:port, if the proxy reported it.
func (r *Resolver) Client(ip, port string) (string, bool) {
	if r == nil {
		return "", false
	}
	ap, err := netip.ParseAddrPort(net.JoinHostPort(ip, port))
	if err != nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byAddr[unmap(ap)]
	if !ok || r.now().Sub(e.at) > entryTTL {
		return "", false
	}
	return e.client.String(), true
}

func unmap(ap netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}
