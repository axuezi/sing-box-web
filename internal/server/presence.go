package server

import (
	"context"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/presence"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/traffic"
)

// liveConns is the Clash API's connection list, attributed to users.
type liveConns struct {
	err   error
	conns []clashapi.Connection
	attr  presence.Attribution
}

func inboundTag(typ string) string {
	_, tag, _ := strings.Cut(typ, "/") // "vless/reality-in"
	return tag
}

// livePresence fetches live connections and matches them to users.
func (s *Server) livePresence(ctx context.Context, users []store.User) liveConns {
	c, err := traffic.ClashClient(s.opts.SingboxConfig)
	if err != nil {
		return liveConns{err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	snap, err := c.Connections(ctx)
	if err != nil {
		return liveConns{err: err}
	}
	return s.livePresenceFrom(snap, users)
}

// livePresenceFrom matches an already fetched connection list to users.
func (s *Server) livePresenceFrom(snap clashapi.Snapshot, users []store.User) liveConns {
	byTag := map[string][]string{}
	for _, u := range users {
		if u.Active() {
			for _, t := range u.Inbounds {
				byTag[t] = append(byTag[t], u.Name)
			}
		}
	}
	in := make([]presence.Conn, len(snap.Connections))
	for i, cn := range snap.Connections {
		in[i] = presence.Conn{Tag: inboundTag(cn.Metadata.Type), Source: joinHostPort(cn.Metadata.SourceIP, cn.Metadata.SourcePort)}
	}
	return liveConns{conns: snap.Connections, attr: s.opts.Presence.Attribute(in, byTag)}
}

// state returns a user's online state and matched connection count.
func (l liveConns) state(u store.User) (string, int) {
	if l.err != nil {
		return "unknown", 0
	}
	if n := l.attr.Count[u.Name]; n > 0 {
		return "online", n
	}
	if !u.Active() {
		return "offline", 0
	}
	for _, t := range u.Inbounds {
		if l.attr.Unknown[t] > 0 {
			return "unknown", 0 // some of those could be this user's
		}
	}
	return "offline", 0
}

type presenceSummary struct {
	// Available is false when live connections cannot be read at all.
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	// LogFollowed tells whether connections can be matched by the log.
	LogFollowed  bool `json:"log_followed"`
	Connections  int  `json:"connections"`
	Unattributed int  `json:"unattributed"`
}

func (l liveConns) summary(t *presence.Tracker) presenceSummary {
	p := presenceSummary{Available: l.err == nil, Connections: len(l.conns)}
	if l.err != nil {
		p.Error = l.err.Error()
	}
	if t != nil {
		p.LogFollowed, _ = t.Status()
	}
	for _, n := range l.attr.Unknown {
		p.Unattributed += n
	}
	return p
}
