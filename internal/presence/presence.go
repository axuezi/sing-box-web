// Package presence attributes live connections to users.
//
// The Clash API lists connections with their inbound and source address but
// not the user. sing-box logs both, under one connection id:
//
//	[4040533735 0ms] inbound/vless[reality-in]: inbound connection from 127.0.0.1:57390
//	[4040533735 180ms] inbound/vless[reality-in]: [alice] inbound connection to example.com:443
//
// so following the log gives (inbound tag, source address) → user. This needs
// log level info or lower and named users; anything else stays unattributed.
package presence

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/logtail"
)

var lineRE = regexp.MustCompile(
	`^\[(\d+) [^\]]*\] inbound/[^\[\]]+\[([^\]]+)\]: (?:\[([^\]]+)\] )?inbound (?:packet )?connection (from|to) (\S+)$`)

const (
	pendingTTL = time.Minute     // "from" lines waiting for their "to" line
	entryTTL   = 24 * time.Hour  // address → user mappings
	maxEntries = 50_000          // bound memory if the log is very busy
	backlog    = 20_000          // lines read at start for older connections
	pollEvery  = 1 * time.Second // log follow interval
)

type pending struct {
	tag, src string
	at       time.Time
}

type entry struct {
	user string
	at   time.Time
}

// Tracker follows the sing-box log. The zero value is usable but empty;
// call Run to start following.
type Tracker struct {
	LogPath string
	Logger  *slog.Logger
	Now     func() time.Time

	mu       sync.Mutex
	pending  map[string]pending // connection id → source
	byAddr   map[string]entry   // tag + "|" + source → user
	lastSeen map[string]time.Time
	running  bool
	err      error
}

// Run reads recent log lines, then follows the log until ctx is done.
func (t *Tracker) Run(ctx context.Context) {
	if t.LogPath == "" {
		t.setState(false, errNoLog)
		return
	}
	for {
		lines, off, err := logtail.Backlog(t.LogPath, backlog)
		if err == nil {
			for _, l := range lines {
				t.Feed(l)
			}
			t.setState(true, nil)
			err = logtail.Follow(ctx, t.LogPath, off, pollEvery, func(l string) { t.Feed(l) })
		}
		t.setState(false, err)
		if err != nil && t.Logger != nil {
			t.Logger.Debug("presence: cannot follow sing-box log", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

var errNoLog = errors.New("sing-box log file unknown")

func (t *Tracker) setState(running bool, err error) {
	t.mu.Lock()
	t.running, t.err = running, err
	t.mu.Unlock()
}

// Status reports whether the log is being followed.
func (t *Tracker) Status() (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running, t.err
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Feed processes one raw log line.
func (t *Tracker) Feed(raw string) {
	l := logtail.Parse(raw)
	if !strings.Contains(l.Text, "inbound") {
		return
	}
	m := lineRE.FindStringSubmatch(l.Text)
	if m == nil {
		return
	}
	id, tag, user, dir, addr := m[1], m[2], m[3], m[4], m[5]
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		t.pending = map[string]pending{}
		t.byAddr = map[string]entry{}
		t.lastSeen = map[string]time.Time{}
	}
	switch dir {
	case "from":
		t.pending[id] = pending{tag: tag, src: normalize(addr), at: now}
		if len(t.pending) > 1000 {
			for k, p := range t.pending {
				if now.Sub(p.at) > pendingTTL {
					delete(t.pending, k)
				}
			}
		}
	case "to":
		p, ok := t.pending[id]
		delete(t.pending, id)
		if user == "" {
			return
		}
		t.lastSeen[user] = now
		if ok && p.tag == tag {
			t.byAddr[tag+"|"+p.src] = entry{user: user, at: now}
			if len(t.byAddr) > maxEntries {
				t.prune(now)
			}
		}
	}
}

func (t *Tracker) prune(now time.Time) {
	for k, e := range t.byAddr {
		if now.Sub(e.at) > entryTTL {
			delete(t.byAddr, k)
		}
	}
	// Still too many: drop the oldest half.
	if len(t.byAddr) > maxEntries {
		cut := now.Add(-entryTTL / 2)
		for k, e := range t.byAddr {
			if e.at.Before(cut) {
				delete(t.byAddr, k)
			}
		}
	}
}

// User returns the user whose connection on inbound tag came from source
// ("ip:port"), if the log said so.
func (t *Tracker) User(tag, source string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.byAddr[tag+"|"+normalize(source)]
	return e.user, ok
}

// LastSeen returns when each user last opened a connection, as far as the
// log tells.
func (t *Tracker) LastSeen() map[string]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]time.Time, len(t.lastSeen))
	for k, v := range t.lastSeen {
		out[k] = v
	}
	return out
}

// normalize makes "::ffff:1.2.3.4:5" and "1.2.3.4:5" compare equal.
func normalize(addr string) string {
	if ap, err := netip.ParseAddrPort(addr); err == nil {
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String()
	}
	return addr
}

// Conn is a live connection as the Clash API reports it.
type Conn struct {
	Tag    string // inbound tag
	Source string // "ip:port"
}

// Attribution says whose each connection is.
type Attribution struct {
	Users   []string       // per connection; "" = unknown
	Count   map[string]int // user → connections
	Unknown map[string]int // inbound tag → connections nobody could be matched to
}

// Attribute matches connections to users: first by the log, then — for an
// inbound that only one user can use — by elimination. usersByTag lists the
// users currently in the config for each inbound. t may be nil.
func (t *Tracker) Attribute(conns []Conn, usersByTag map[string][]string) Attribution {
	a := Attribution{Users: make([]string, len(conns)), Count: map[string]int{}, Unknown: map[string]int{}}
	for i, c := range conns {
		user := ""
		if t != nil {
			user, _ = t.User(c.Tag, c.Source)
		}
		if user == "" && len(usersByTag[c.Tag]) == 1 {
			user = usersByTag[c.Tag][0]
		}
		a.Users[i] = user
		if user == "" {
			a.Unknown[c.Tag]++
		} else {
			a.Count[user]++
		}
	}
	return a
}
