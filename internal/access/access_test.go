package access

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/store"
)

const cfg = `{"inbounds":[
 {"type":"hysteria2","tag":"hy2-in","listen":"::","listen_port":443,"users":[]},
 {"type":"vless","tag":"reality-in","listen":"127.0.0.1","listen_port":8443,"users":[]},
 {"type":"vless","tag":"vless-direct","listen":"0.0.0.0","listen_port":2443,"users":[]},
 {"type":"mixed","tag":"mixed-in","listen":"127.0.0.1","listen_port":1080}]}`

type fakeCtl struct {
	info  control.AccessInfo
	specs []string
	fail  bool
}

func (f *fakeCtl) AccessInfo(context.Context) (control.AccessInfo, error) { return f.info, nil }
func (f *fakeCtl) ApplyAccess(_ context.Context, spec string) (string, error) {
	f.specs = append(f.specs, spec)
	if f.fail {
		return "boom", io.ErrUnexpectedEOF
	}
	return "ok", nil
}
func (f *fakeCtl) AccessStats(context.Context) (map[string]int64, error) { return nil, nil }
func (f *fakeCtl) Bans(context.Context) ([]control.Ban, error)           { return nil, nil }
func (f *fakeCtl) Ban(context.Context, string, string) error             { return nil }
func (f *fakeCtl) Unban(context.Context, string, string) error           { return nil }

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":        "203.0.113.7/32",
		" 203.0.113.7/24 ":   "203.0.113.0/24",
		"2001:db8::1":        "2001:db8::1/128",
		"2001:db8:1::/48":    "2001:db8:1::/48",
		"::ffff:203.0.113.7": "203.0.113.7/32",
		"10.0.0.0/8":         "10.0.0.0/8",
	} {
		if p, err := Normalize(in); err != nil || p.String() != want {
			t.Errorf("Normalize(%q) = %v, %v; want %s", in, p, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.2.3.4/33", "0.0.0.0/0", "10.0.0.0/7", "::/0", "fe80::1%eth0", "1.2.3.4; flush"} {
		if p, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) accepted: %v", bad, p)
		}
	}
}

func TestTargetsAndSpec(t *testing.T) {
	ts := Targets([]byte(cfg), control.AccessInfo{NFT: true, HAProxy: true})
	by := map[string]string{}
	for _, x := range ts {
		by[x.Tag] = x.By
	}
	if len(ts) != 3 || by["hy2-in"] != ByFirewall || by["reality-in"] != ByHAProxy || by["vless-direct"] != ByFirewall {
		t.Fatalf("targets: %+v", ts)
	}
	if ts := Targets([]byte(cfg), control.AccessInfo{NFT: true}); ts[1].By != ByNone || ts[1].Reason == "" {
		t.Fatalf("without haproxy: %+v", ts[1])
	}
	spec := Spec(ts, []store.AccessRule{{List: Deny, CIDR: "203.0.113.7/32"}, {List: Allow, CIDR: "198.51.100.0/24"}}, true)
	want := "port udp 443\nport tcp 2443\ndeny 203.0.113.7/32\nallow 198.51.100.0/24\nallowlist on\n"
	if spec != want {
		t.Fatalf("spec:\n%s\nwant:\n%s", spec, want)
	}
	rules := []store.AccessRule{{List: Allow, CIDR: "198.51.100.0/24"}}
	if !Contains(rules, Allow, netip.MustParseAddr("198.51.100.9")) || Contains(rules, Deny, netip.MustParseAddr("198.51.100.9")) ||
		Contains(rules, Allow, netip.MustParseAddr("198.51.101.9")) {
		t.Fatal("Contains")
	}
}

func TestManagerAllowlistAndExpiry(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(cfg), 0o600)
	now := time.Now()
	ctl := &fakeCtl{info: control.AccessInfo{NFT: true}}
	m := &Manager{Store: st, Ctl: ctl, ConfigPath: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now }}

	if _, err := m.SetAllowlist(ctx, true); err == nil {
		t.Fatal("allowlist turned on with no entries")
	}
	st.PutAccessRule(ctx, store.AccessRule{List: Allow, CIDR: "198.51.100.0/24"})
	st.PutAccessRule(ctx, store.AccessRule{List: Deny, CIDR: "203.0.113.7/32", ExpiresAt: now.Add(time.Hour)})
	deadline, err := m.SetAllowlist(ctx, true)
	if err != nil || deadline.Sub(now) > ConfirmWindow || !strings.Contains(ctl.specs[len(ctl.specs)-1], "allowlist on") {
		t.Fatalf("switch on: %v %v %q", deadline, err, ctl.specs)
	}

	// Unchanged rules are not pushed again within the resync interval.
	n := len(ctl.specs)
	if err := m.Check(ctx); err != nil || len(ctl.specs) != n {
		t.Fatalf("idle check pushed again: %v %d", err, len(ctl.specs))
	}

	// Not confirmed in time: switched off again.
	now = now.Add(ConfirmWindow + time.Second)
	m.Check(ctx)
	if on, _, _ := m.Allowlist(ctx); on || !strings.Contains(ctl.specs[len(ctl.specs)-1], "allowlist off") {
		t.Fatalf("not reverted: on=%v %q", on, ctl.specs[len(ctl.specs)-1])
	}

	// Confirmed in time: stays on.
	m.SetAllowlist(ctx, true)
	if err := m.Confirm(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * ConfirmWindow)
	m.Check(ctx)
	if on, d, _ := m.Allowlist(ctx); !on || !d.IsZero() {
		t.Fatalf("confirmed allowlist changed: %v %v", on, d)
	}

	// Expired blocklist entries disappear and the change is pushed.
	now = now.Add(2 * time.Hour)
	m.Check(ctx)
	if strings.Contains(ctl.specs[len(ctl.specs)-1], "203.0.113.7") {
		t.Fatalf("expired rule still pushed: %q", ctl.specs[len(ctl.specs)-1])
	}
	rules, _ := st.ListAccessRules(ctx)
	if len(rules) != 1 {
		t.Fatalf("rules after expiry: %+v", rules)
	}

	// A failed push is retried on the next check even if nothing changed.
	ctl.fail = true
	m.Apply(ctx, true)
	ctl.fail = false
	n = len(ctl.specs)
	m.Check(ctx)
	if len(ctl.specs) != n+1 || !m.Last().OK {
		t.Fatalf("no retry after failure: %d %+v", len(ctl.specs)-n, m.Last())
	}
}
