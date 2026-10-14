package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/axuezi/sing-box-web/internal/access"
	"github.com/axuezi/sing-box-web/internal/control"
)

type fakeAccessCtl struct {
	specs  []string
	banned []string
}

func (f *fakeAccessCtl) AccessInfo(context.Context) (control.AccessInfo, error) {
	return control.AccessInfo{NFT: true, HAProxy: true, Jails: []string{"sshd", "sing-box-web"}}, nil
}
func (f *fakeAccessCtl) ApplyAccess(_ context.Context, spec string) (string, error) {
	f.specs = append(f.specs, spec)
	return "applied", nil
}
func (f *fakeAccessCtl) AccessStats(context.Context) (map[string]int64, error) {
	return map[string]int64{"deny": 7}, nil
}
func (f *fakeAccessCtl) Bans(context.Context) ([]control.Ban, error) {
	return []control.Ban{{Jail: "sshd", IP: "203.0.113.50"}}, nil
}
func (f *fakeAccessCtl) Ban(_ context.Context, jail, ip string) error {
	f.banned = append(f.banned, jail+" "+ip)
	return nil
}
func (f *fakeAccessCtl) Unban(_ context.Context, jail, ip string) error {
	f.banned = append(f.banned, "-"+jail+" "+ip)
	return nil
}

func TestAccessAPI(t *testing.T) {
	m := newConfigEnv(t)
	ctl := &fakeAccessCtl{}
	mgr := &access.Manager{Store: m.st, Ctl: ctl, ConfigPath: m.cfgPath}
	m.h = New(Options{Store: m.st, SingboxConfig: m.cfgPath, SingboxLog: m.logPath, Apply: m.mgr, Controller: m.ctl, Access: mgr})
	last := func() string { return ctl.specs[len(ctl.specs)-1] }

	code, body := m.call("GET", "/api/access", "")
	if code != 200 || !body.Get("available").Bool() || body.Get("targets.#").Int() != 2 ||
		body.Get(`targets.#(tag=="hy2-in").by`).String() != "firewall" || body.Get(`targets.#(tag=="reality-in").by`).String() != "haproxy" ||
		body.Get("stats.deny").Int() != 7 {
		t.Fatalf("get: %d %s", code, body.Raw)
	}
	me := body.Get("my_ip").String()

	// Blocking yourself is refused, also through a covering network.
	if code, b := m.call("POST", "/api/access/rules", fmt.Sprintf(`{"list":"deny","ip":%q}`, me)); code != 409 || b.Get("code").String() != "self" {
		t.Fatalf("self block: %d %s", code, b.Raw)
	}
	if code, _ := m.call("POST", "/api/access/rules", `{"list":"deny","ip":"0.0.0.0/0"}`); code != 400 {
		t.Fatal("whole internet accepted")
	}
	if code, b := m.call("POST", "/api/access/rules", `{"list":"deny","ip":"203.0.113.9","note":"scanner","duration":3600}`); code != 200 ||
		!b.Get("applied").Bool() || b.Get("rule.cidr").String() != "203.0.113.9/32" || b.Get("rule.expires_at").String() == "" {
		t.Fatalf("add deny: %d %s", code, b.Raw)
	}
	if !strings.Contains(last(), "deny 203.0.113.9/32") || !strings.Contains(last(), "port udp 443") || strings.Contains(last(), "8443") {
		t.Fatalf("spec: %q", last())
	}

	// Allowlist: needs an entry; then on, awaiting confirmation; the last
	// entry cannot be removed while it is on.
	if code, _ := m.call("PUT", "/api/access/allowlist", `{"on":true}`); code != 409 {
		t.Fatal("empty allowlist switched on")
	}
	_, b := m.call("POST", "/api/access/rules", `{"list":"allow","ip":"198.51.100.0/24","note":"home"}`)
	allowID := b.Get("rule.id").Int()
	if code, b := m.call("PUT", "/api/access/allowlist", `{"on":true}`); code != 200 || b.Get("confirm_by").String() == "" || !strings.Contains(last(), "allowlist on") {
		t.Fatalf("allowlist on: %d %s", code, b.Raw)
	}
	if code, _ := m.call("DELETE", fmt.Sprintf("/api/access/rules/%d", allowID), ""); code != 409 {
		t.Fatal("last allow entry removed while on")
	}
	if code, _ := m.call("POST", "/api/access/allowlist/confirm", ""); code != 204 {
		t.Fatal("confirm")
	}
	if code, _ := m.call("POST", "/api/access/allowlist/confirm", ""); code != 409 {
		t.Fatal("double confirm")
	}
	m.call("PUT", "/api/access/allowlist", `{"on":false}`)
	if code, _ := m.call("DELETE", fmt.Sprintf("/api/access/rules/%d", allowID), ""); code != 200 || strings.Contains(last(), "198.51.100") {
		t.Fatalf("delete allow: %d %q", code, last())
	}

	// fail2ban
	_, b = m.call("GET", "/api/access/bans", "")
	if b.Get("bans.0.ip").String() != "203.0.113.50" || b.Get("jails.#").Int() != 2 {
		t.Fatalf("bans: %s", b.Raw)
	}
	if code, _ := m.call("POST", "/api/access/bans", fmt.Sprintf(`{"action":"ban","jail":"sshd","ip":%q}`, me)); code != 409 {
		t.Fatal("banned own address")
	}
	if code, _ := m.call("POST", "/api/access/bans", `{"action":"ban","jail":"nginx","ip":"203.0.113.1"}`); code != 400 {
		t.Fatal("unknown jail accepted")
	}
	if code, _ := m.call("POST", "/api/access/bans", `{"action":"ban","jail":"sshd","ip":"203.0.113.0/24"}`); code != 400 {
		t.Fatal("network accepted by fail2ban")
	}
	m.call("POST", "/api/access/bans", `{"action":"ban","jail":"sshd","ip":"203.0.113.1"}`)
	m.call("POST", "/api/access/bans", `{"action":"unban","jail":"sshd","ip":"203.0.113.50"}`)
	if strings.Join(ctl.banned, ",") != "sshd 203.0.113.1,-sshd 203.0.113.50" {
		t.Fatalf("ban calls: %v", ctl.banned)
	}
	acts := strings.Join(m.auditActions(), ",")
	for _, a := range []string{"access.add", "access.delete", "access.allowlist_on", "access.allowlist_confirm", "access.allowlist_off", "f2b.ban", "f2b.unban"} {
		if !strings.Contains(acts, a) {
			t.Errorf("audit missing %s: %s", a, acts)
		}
	}
}

func TestAccessUnavailable(t *testing.T) {
	m := newConfigEnv(t) // test controller has no access support
	code, b := m.call("GET", "/api/access", "")
	if code != 200 || b.Get("available").Bool() || b.Get("error").String() == "" {
		t.Fatalf("%d %s", code, b.Raw)
	}
	if code, _ := m.call("POST", "/api/access/rules", `{"list":"deny","ip":"203.0.113.9"}`); code != 409 {
		t.Fatalf("write without controller: %d", code)
	}
}
