package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/axuezi/sing-box-web/internal/presence"
	"github.com/axuezi/sing-box-web/internal/store"
)

// fakeClash serves /connections with whatever conns holds.
type fakeClash struct {
	mu    sync.Mutex
	conns []map[string]any
	down  bool
}

func (f *fakeClash) set(conns ...[3]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conns = nil
	for i, c := range conns { // type, source ip, source port
		f.conns = append(f.conns, map[string]any{
			"id": string(rune('a' + i)), "start": time.Now(), "chains": []string{"direct"},
			"metadata": map[string]string{"type": c[0], "sourceIP": c[1], "sourcePort": c[2], "network": "tcp", "host": "example.com"},
		})
	}
}

func (f *fakeClash) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"connections": f.conns, "uploadTotal": 0, "downloadTotal": 0})
}

func TestUserPresence(t *testing.T) {
	m := newConfigEnv(t)
	ctx := context.Background()
	fc := &fakeClash{}
	srv := httptest.NewServer(fc)
	defer srv.Close()
	raw, _ := os.ReadFile(m.cfgPath)
	raw, _ = sjson.SetBytes(raw, "experimental.clash_api.external_controller", strings.TrimPrefix(srv.URL, "http://"))
	os.WriteFile(m.cfgPath, raw, 0o600)

	tr := &presence.Tracker{}
	m.h = New(Options{Store: m.st, SingboxConfig: m.cfgPath, SingboxLog: m.logPath, Apply: m.mgr, Controller: m.ctl, Presence: tr})

	// alice shares hy2-in with owner; reality-in stays owner-only.
	if code, b := m.call("POST", "/api/users", `{"name":"alice","inbounds":["hy2-in"]}`); code != 200 {
		t.Fatalf("create alice: %d %s", code, b.Raw)
	}
	tr.Feed("+0000 2026-10-13 08:00:00 INFO [1 0ms] inbound/hysteria2[hy2-in]: inbound packet connection from 203.0.113.9:4000")
	tr.Feed("+0000 2026-10-13 08:00:00 INFO [1 0ms] inbound/hysteria2[hy2-in]: [alice] inbound packet connection to example.com:443")

	state := func() map[string]gjson.Result {
		_, body := m.call("GET", "/api/users", "")
		out := map[string]gjson.Result{"": body.Get("presence")}
		for _, u := range body.Get("users").Array() {
			out[u.Get("name").String()] = u
		}
		return out
	}
	check := func(name string, s map[string]gjson.Result, user, online string, conns int64) {
		t.Helper()
		if u := s[user]; u.Get("online").String() != online || u.Get("conns").Int() != conns {
			t.Fatalf("%s: %s = %s/%d, want %s/%d", name, user, u.Get("online"), u.Get("conns").Int(), online, conns)
		}
	}

	fc.set([3]string{"hysteria2/hy2-in", "203.0.113.9", "4000"}, [3]string{"vless/reality-in", "127.0.0.1", "5555"},
		[3]string{"vless/reality-in", "127.0.0.1", "5556"})
	s := state()
	check("matched", s, "alice", "online", 1)
	check("matched", s, "owner", "online", 2) // reality-in has one user
	if s[""].Get("unattributed").Int() != 0 || !s[""].Get("available").Bool() || s["alice"].Get("last_active_at").String() == "" {
		t.Fatalf("summary: %s alice: %s", s[""].Raw, s["alice"].Raw)
	}

	// An hy2 connection nobody can be matched to: alice and owner might own it.
	fc.set([3]string{"hysteria2/hy2-in", "198.51.100.7", "1"})
	s = state()
	check("unmatched", s, "alice", "unknown", 0)
	check("unmatched", s, "owner", "unknown", 0)
	if s[""].Get("unattributed").Int() != 1 {
		t.Fatalf("summary: %s", s[""].Raw)
	}
	_, body := m.call("GET", "/api/connections", "")
	if body.Get("connections.0.user").String() != "" {
		t.Fatalf("unmatched connection has a user: %s", body.Raw)
	}

	fc.set()
	s = state()
	check("idle", s, "alice", "offline", 0)
	check("idle", s, "owner", "offline", 0)

	fc.set([3]string{"hysteria2/hy2-in", "203.0.113.9", "4000"})
	_, body = m.call("GET", "/api/connections", "")
	if body.Get("connections.0.user").String() != "alice" {
		t.Fatalf("connections: %s", body.Raw)
	}

	fc.down = true
	s = state()
	check("clash down", s, "alice", "unknown", 0)
	if s[""].Get("available").Bool() {
		t.Fatalf("summary: %s", s[""].Raw)
	}

	// Traffic seen by the collector counts as activity and is persisted.
	m.st.AddTraffic(ctx, []store.TrafficDelta{{Day: time.Now().Format(time.DateOnly), Kind: "user", Key: "1", Down: 10}})
	if u, _ := m.st.GetUser(ctx, 1); time.Since(u.LastActiveAt) > time.Minute {
		t.Fatalf("last active: %v", u.LastActiveAt)
	}
}
