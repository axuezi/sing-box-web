// Package flows records who connected to which site: per hour, the bytes of
// every (user, client IP, destination site, outbound).
//
// sing-box's V2Ray stats only count users, inbounds and outbounds apart, so
// this follows the Clash API connection list instead. Each poll adds the
// growth of every connection's counters since the previous poll. A
// connection that closes between two polls loses only its last interval;
// a connection seen first is counted from its start.
package flows

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/gateway"
	"github.com/axuezi/sing-box-web/internal/presence"
	"github.com/axuezi/sing-box-web/internal/store"
)

// KeepDays is how long hourly flow rows are kept.
const KeepDays = 31

// SettingKey turns recording on ("1") or off.
const SettingKey = "flows_enabled"

const (
	hourLayout   = "2006-01-02 15"
	settingEvery = 10 * time.Second // re-read SettingKey
	usersEvery   = 30 * time.Second // re-read users for attribution
	attrWait     = 5 * time.Second  // hold a new connection's bytes this long for its log line
	flushEvery   = time.Minute
)

type conn struct {
	first      time.Time
	up, down   int64 // counted so far
	seenUp     int64 // counters at the last poll
	seenDown   int64
	user       string
	ip, site   string
	outbound   string
	attributed bool // user is settled (found, or waited long enough)
	counted    bool // the connection itself was counted once
}

type key struct{ hour, user, ip, site, outbound string }

// Recorder turns connection snapshots into flow rows. Feed it every
// snapshot with Observe and call Run to write rows out.
type Recorder struct {
	Store    *store.Store
	Presence *presence.Tracker // may be nil: then only single-user inbounds are attributed
	Gateway  *gateway.Resolver // may be nil: then relayed connections keep the relay's address
	Logger   *slog.Logger

	mu        sync.Mutex
	enabled   bool
	checkedAt time.Time
	byTag     map[string][]string
	usersAt   time.Time
	primed    bool
	conns     map[string]*conn
	pending   map[key]*store.FlowRow
}

// Refresh re-reads the setting now (after it was changed).
func (r *Recorder) Refresh(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkedAt = time.Time{}
	r.refresh(ctx, time.Now())
}

func (r *Recorder) refresh(ctx context.Context, now time.Time) {
	if now.Sub(r.checkedAt) >= settingEvery {
		r.checkedAt = now
		v, err := r.Store.GetSetting(ctx, SettingKey)
		if err == nil {
			on := v == "1"
			if !on {
				// Off: forget everything, so turning it back on starts clean
				// rather than counting the gap as one burst.
				r.primed, r.conns, r.pending = false, nil, nil
			}
			r.enabled = on
		}
	}
	if r.enabled && now.Sub(r.usersAt) >= usersEvery {
		if users, err := r.Store.ListUsers(ctx); err == nil {
			r.usersAt = now
			r.byTag = map[string][]string{}
			for _, u := range users {
				if u.Active() {
					for _, t := range u.Inbounds {
						r.byTag[t] = append(r.byTag[t], u.Name)
					}
				}
			}
		}
	}
}

// Observe takes one connection snapshot taken at now.
func (r *Recorder) Observe(snap clashapi.Snapshot, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r.refresh(ctx, now)
	if !r.enabled {
		return
	}
	if r.conns == nil {
		r.conns = map[string]*conn{}
	}
	if r.pending == nil {
		r.pending = map[key]*store.FlowRow{}
	}
	hour := now.Format(hourLayout)
	seen := make(map[string]bool, len(snap.Connections))
	for _, c := range snap.Connections {
		seen[c.ID] = true
		st := r.conns[c.ID]
		if st == nil {
			st = &conn{first: now, ip: clientIP(c.Metadata.SourceIP), site: Site(c.Metadata.Host, c.Metadata.DestinationIP)}
			if len(c.Chains) > 0 {
				st.outbound = c.Chains[0] // the real outbound; the rest are the groups that chose it
			}
			r.conns[c.ID] = st
			if !r.primed {
				// Already open when recording started: count only what
				// comes from now on.
				st.up, st.down, st.counted = c.Upload, c.Download, true
			}
		}
		st.seenUp, st.seenDown = c.Upload, c.Download
		if !st.attributed {
			// The relay's log line usually arrives first, but look again
			// while the connection waits for its user anyway.
			if ip, ok := r.Gateway.Client(c.Metadata.SourceIP, c.Metadata.SourcePort); ok {
				st.ip = ip
			}
			tag := inboundTag(c.Metadata.Type)
			a := r.Presence.Attribute([]presence.Conn{{Tag: tag, Source: joinHostPort(c.Metadata.SourceIP, c.Metadata.SourcePort)}}, r.byTag)
			st.user = a.Users[0]
			// The log line naming the user can trail the connection by a
			// moment; hold its bytes until then rather than file them
			// under nobody.
			if st.user == "" && now.Sub(st.first) < attrWait && r.primed {
				continue
			}
			st.attributed = true
		}
		r.count(hour, st)
	}
	for id, st := range r.conns {
		if !seen[id] {
			if !st.attributed { // closed while waiting for its user: count it as unknown
				r.count(hour, st)
			}
			delete(r.conns, id)
		}
	}
	r.primed = true
}

// count adds a connection's growth since it was last counted.
func (r *Recorder) count(hour string, st *conn) {
	up, down := st.seenUp-st.up, st.seenDown-st.down
	if up < 0 || down < 0 { // counters reset: count from zero
		up, down = st.seenUp, st.seenDown
	}
	st.up, st.down = st.seenUp, st.seenDown
	var n int64
	if !st.counted {
		st.counted, n = true, 1
	}
	if up == 0 && down == 0 && n == 0 {
		return
	}
	k := key{hour, st.user, st.ip, st.site, st.outbound}
	row := r.pending[k]
	if row == nil {
		row = &store.FlowRow{Hour: hour, User: st.user, IP: st.ip, Site: st.site, Outbound: st.outbound}
		r.pending[k] = row
	}
	row.Up += up
	row.Down += down
	row.Conns += n
}

// Run writes pending rows once a minute until ctx is done, then once more.
func (r *Recorder) Run(ctx context.Context) {
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Flush(context.WithoutCancel(ctx))
			return
		case <-t.C:
			r.Flush(ctx)
		}
	}
}

// Flush writes pending rows. On failure they are kept for the next try.
func (r *Recorder) Flush(ctx context.Context) {
	r.mu.Lock()
	rows := make([]store.FlowRow, 0, len(r.pending))
	for _, row := range r.pending {
		rows = append(rows, *row)
	}
	r.pending = nil
	r.mu.Unlock()
	if err := r.Store.AddFlows(ctx, rows); err != nil {
		if r.Logger != nil {
			r.Logger.Warn("flows: cannot save", "err", err)
		}
		r.mu.Lock()
		if r.enabled {
			if r.pending == nil {
				r.pending = map[key]*store.FlowRow{}
			}
			for _, row := range rows {
				k := key{row.Hour, row.User, row.IP, row.Site, row.Outbound}
				if p := r.pending[k]; p != nil {
					p.Up, p.Down, p.Conns = p.Up+row.Up, p.Down+row.Down, p.Conns+row.Conns
				} else {
					row := row
					r.pending[k] = &row
				}
			}
		}
		r.mu.Unlock()
	}
}

// Site names a destination: the registrable domain of host (so
// "r3---sn-x.googlevideo.com" counts as "googlevideo.com"), or the
// destination IP when no domain is known.
func Site(host, ip string) string {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "" {
		return clientIP(ip)
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Unmap().String()
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(h); err == nil {
		return d
	}
	return h
}

func clientIP(s string) string {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	return s
}

func inboundTag(typ string) string {
	_, tag, _ := strings.Cut(typ, "/") // "vless/reality-in"
	return tag
}

func joinHostPort(host, port string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}
