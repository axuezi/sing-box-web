package presence

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

const logLines = `+0000 2026-10-13 08:51:50 INFO [4040533735 0ms] inbound/vless[reality-in]: inbound connection from 127.0.0.1:57390
+0000 2026-10-13 08:51:50 INFO [1935713263 0ms] inbound/vless[reality-in]: inbound connection from 127.0.0.1:57394
+0000 2026-10-13 08:51:50 INFO [4040533735 180ms] inbound/vless[reality-in]: [alice] inbound connection to example.com:443
+0000 2026-10-13 08:51:51 INFO [1935713263 230ms] inbound/vless[reality-in]: [bob] inbound connection to example.org:443
+0000 2026-10-13 08:27:14 INFO [1149583941 0ms] inbound/hysteria2[hy2-in]: inbound packet connection from 203.0.113.9:59320
+0000 2026-10-13 08:27:14 INFO [1149583941 0ms] inbound/hysteria2[hy2-in]: [bob] inbound packet connection to 198.51.100.7:123
+0000 2026-10-13 08:27:15 INFO [222 0ms] inbound/hysteria2[hy2-in]: inbound connection from [2001:db8::1]:4000
+0000 2026-10-13 08:27:15 INFO [222 0ms] inbound/hysteria2[hy2-in]: inbound connection to example.net:443
+0000 2026-10-13 08:27:16 INFO [333 0ms] outbound/direct[direct]: outbound connection to example.com:443`

func feed(t *Tracker, s string) {
	for _, l := range strings.Split(s, "\n") {
		t.Feed(l)
	}
}

func TestFeedAndLookup(t *testing.T) {
	tr := &Tracker{}
	feed(tr, logLines)
	cases := []struct{ tag, src, want string }{
		{"reality-in", "127.0.0.1:57390", "alice"},
		{"reality-in", "127.0.0.1:57394", "bob"},
		{"hy2-in", "203.0.113.9:59320", "bob"},
		{"hy2-in", "::ffff:203.0.113.9:59320", ""}, // not a valid AddrPort without brackets
		{"hy2-in", "[::ffff:203.0.113.9]:59320", "bob"},
		{"hy2-in", "127.0.0.1:57390", ""},    // same address, other inbound
		{"hy2-in", "[2001:db8::1]:4000", ""}, // unnamed user
		{"reality-in", "127.0.0.1:1", ""},
	}
	for _, c := range cases {
		if got, _ := tr.User(c.tag, c.src); got != c.want {
			t.Errorf("User(%s, %s) = %q, want %q", c.tag, c.src, got, c.want)
		}
	}
	seen := tr.LastSeen()
	if seen["alice"].IsZero() || seen["bob"].IsZero() || len(seen) != 2 {
		t.Fatalf("last seen: %v", seen)
	}

	// A reused source port belongs to whoever connected last.
	feed(tr, `+0000 2026-10-13 09:00:00 INFO [9 0ms] inbound/vless[reality-in]: inbound connection from 127.0.0.1:57390
+0000 2026-10-13 09:00:00 INFO [9 1ms] inbound/vless[reality-in]: [bob] inbound connection to example.com:443`)
	if got, _ := tr.User("reality-in", "127.0.0.1:57390"); got != "bob" {
		t.Fatalf("reused port: %q", got)
	}
}

func TestColouredLog(t *testing.T) {
	tr := &Tracker{}
	tr.Feed("+0000 2026-10-13 08:51:50 \x1b[36mINFO\x1b[0m [1 0ms] inbound/vless[in]: inbound connection from 10.0.0.1:1")
	tr.Feed("+0000 2026-10-13 08:51:50 \x1b[36mINFO\x1b[0m [1 0ms] inbound/vless[in]: [carol] inbound connection to x:1")
	if got, _ := tr.User("in", "10.0.0.1:1"); got != "carol" {
		t.Fatalf("got %q", got)
	}
}

func TestAttribute(t *testing.T) {
	tr := &Tracker{}
	feed(tr, logLines)
	conns := []Conn{
		{"reality-in", "127.0.0.1:57390"}, // alice by log
		{"hy2-in", "203.0.113.9:59320"},   // bob by log
		{"hy2-in", "198.51.100.1:1"},      // unknown: two users on hy2-in
		{"solo-in", "198.51.100.2:2"},     // only dave can use solo-in
	}
	byTag := map[string][]string{"reality-in": {"alice", "bob"}, "hy2-in": {"alice", "bob"}, "solo-in": {"dave"}}
	a := tr.Attribute(conns, byTag)
	if a.Users[0] != "alice" || a.Users[1] != "bob" || a.Users[2] != "" || a.Users[3] != "dave" {
		t.Fatalf("users: %q", a.Users)
	}
	if a.Count["alice"] != 1 || a.Count["bob"] != 1 || a.Count["dave"] != 1 || a.Unknown["hy2-in"] != 1 || len(a.Unknown) != 1 {
		t.Fatalf("counts: %v unknown: %v", a.Count, a.Unknown)
	}
	// Without a tracker, only elimination works.
	var none *Tracker
	if a := none.Attribute(conns, byTag); a.Count["dave"] != 1 || a.Unknown["hy2-in"] != 2 || a.Unknown["reality-in"] != 1 {
		t.Fatalf("nil tracker: %v %v", a.Count, a.Unknown)
	}
}

func TestPendingExpires(t *testing.T) {
	now := time.Now()
	tr := &Tracker{Now: func() time.Time { return now }}
	for i := range 1001 {
		tr.Feed("+0000 2026-10-13 08:51:50 INFO [" + strconv.Itoa(i) + " 0ms] inbound/vless[in]: inbound connection from 10.0.0.1:1")
	}
	now = now.Add(2 * time.Minute)
	tr.Feed("+0000 2026-10-13 08:51:50 INFO [99999 0ms] inbound/vless[in]: inbound connection from 10.0.0.1:2")
	if n := len(tr.pending); n > 2 {
		t.Fatalf("pending not pruned: %d", n)
	}
}
