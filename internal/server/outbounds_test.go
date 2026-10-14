package server

import (
	"encoding/json"
	"testing"

	"github.com/axuezi/sing-box-web/internal/singbox"
)

func TestOutboundAndDNSLifecycle(t *testing.T) {
	m := newConfigEnv(t)
	body := func(form map[string]any) string {
		b, _ := json.Marshal(map[string]any{"form": form})
		return string(b)
	}

	// The fixture has one direct outbound and no DNS section.
	code, b := m.call("GET", "/api/outbounds", "")
	if code != 200 || b.Get("outbounds.#").Int() != 1 || b.Get("route_final").String() != "direct" ||
		b.Get("outbounds.0.refs.0").String() != "route.final (default: first outbound)" {
		t.Fatalf("list: %d %s", code, b.Raw)
	}

	// DNS servers first, so outbounds can name a resolver.
	if code, b := m.call("POST", "/api/dns/servers", body(map[string]any{"tag": "doh", "type": "https", "server": "1.1.1.1"})); code != 200 ||
		b.Get("result.status").String() != "applied" {
		t.Fatalf("create dns server: %d %s", code, b.Raw)
	}
	if code, b := m.call("POST", "/api/dns/servers", body(map[string]any{"tag": "doh", "type": "udp", "server": "8.8.8.8"})); code != 409 {
		t.Fatalf("duplicate dns tag: %d %s", code, b.Raw)
	}
	if code, b := m.call("PUT", "/api/dns", `{"settings":{"strategy":"ipv4_only","final":"doh"}}`); code != 200 {
		t.Fatalf("dns settings: %d %s", code, b.Raw)
	}
	if d := m.config().Get("dns"); d.Get("final").String() != "doh" || d.Get("strategy").String() != "ipv4_only" {
		t.Fatalf("dns after settings: %s", d.Raw)
	}

	// An upstream proxy, then a direct outbound chained through it.
	socks := map[string]any{"tag": "up", "type": "socks", "server": "127.0.0.1", "server_port": 1080, "username": "u", "password": "secret"}
	if code, b := m.call("POST", "/api/outbounds", body(socks)); code != 200 {
		t.Fatalf("create socks: %d %s", code, b.Raw)
	}
	chained := map[string]any{"tag": "v4", "type": "direct", "inet4_bind_address": "192.0.2.10", "detour": "up", "resolver": "doh"}
	if code, b := m.call("POST", "/api/outbounds", body(chained)); code != 200 {
		t.Fatalf("create direct: %d %s", code, b.Raw)
	}
	if code, b := m.call("POST", "/api/outbounds", body(map[string]any{"tag": "x", "type": "direct", "detour": "nope"})); code != 400 {
		t.Fatalf("unknown detour: %d %s", code, b.Raw)
	}
	if code, b := m.call("POST", "/api/outbounds", body(map[string]any{"tag": "y", "type": "direct", "resolver": "nope"})); code != 400 {
		t.Fatalf("unknown resolver: %d %s", code, b.Raw)
	}

	// The password comes back masked; saving the mask keeps it.
	code, b = m.call("GET", "/api/outbounds", "")
	if code != 200 || b.Get(`outbounds.#(form.tag=="up").form.password`).String() != singbox.MaskedValue ||
		b.Get(`outbounds.#(form.tag=="up").refs.0`).String() != "outbounds.2.detour" {
		t.Fatalf("list after create: %s", b.Raw)
	}
	socks["password"] = singbox.MaskedValue
	socks["server_port"] = 1081
	if code, b := m.call("PUT", "/api/outbounds/up", body(socks)); code != 200 {
		t.Fatalf("update socks: %d %s", code, b.Raw)
	}
	up := m.config().Get(`outbounds.#(tag=="up")`)
	if up.Get("password").String() != "secret" || up.Get("server_port").Int() != 1081 {
		t.Fatalf("socks after update: %s", up.Raw)
	}

	// A loop through detours is refused.
	socks["detour"] = "v4"
	if code, b := m.call("PUT", "/api/outbounds/up", body(socks)); code != 400 {
		t.Fatalf("detour loop: %d %s", code, b.Raw)
	}

	// Things still in use cannot be deleted.
	if code, b := m.call("DELETE", "/api/outbounds/up", ""); code != 409 || b.Get("code").String() != "referenced" {
		t.Fatalf("delete referenced outbound: %d %s", code, b.Raw)
	}
	if code, b := m.call("DELETE", "/api/dns/servers/doh", ""); code != 409 {
		t.Fatalf("delete referenced dns server: %d %s", code, b.Raw)
	}
	if code, b := m.call("DELETE", "/api/outbounds/v4", ""); code != 200 {
		t.Fatalf("delete v4: %d %s", code, b.Raw)
	}
	if code, b := m.call("DELETE", "/api/outbounds/up", ""); code != 200 {
		t.Fatalf("delete up: %d %s", code, b.Raw)
	}
	if code, b := m.call("DELETE", "/api/outbounds/direct", ""); code != 409 {
		t.Fatalf("delete default outbound: %d %s", code, b.Raw)
	}

	code, b = m.call("GET", "/api/dns", "")
	if code != 200 || b.Get("servers.0.form.tag").String() != "doh" || b.Get("settings.final").String() != "doh" ||
		b.Get("outbounds.#").Int() != 1 {
		t.Fatalf("dns list: %d %s", code, b.Raw)
	}

	if code, b = m.call("GET", "/api/audit?limit=20", ""); code != 200 {
		t.Fatalf("audit: %d %s", code, b.Raw)
	}
	actions := map[string]bool{}
	for _, e := range b.Get("entries").Array() {
		actions[e.Get("action").String()+" "+e.Get("target").String()] = true
	}
	for _, want := range []string{"dns_server.create doh", "dns.update dns", "outbound.create up", "outbound.update up", "outbound.delete v4"} {
		if !actions[want] {
			t.Errorf("audit missing %q (have %v)", want, actions)
		}
	}
}
