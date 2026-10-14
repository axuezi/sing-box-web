package server

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestSubscriptionAccessLog(t *testing.T) {
	m := newConfigEnv(t)
	m.call("PUT", "/api/settings/subscription", `{"public_host":"203.0.113.5"}`)
	_, body := m.call("GET", "/api/users/1/client", "")
	path := strings.TrimPrefix(body.Get("subscription.mihomo").String(), "http://example.com")
	rec := m.do("GET", path, "", withHeader("User-Agent", "clash-verge/v2.0"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "proxies:") || !strings.Contains(rec.Body.String(), "hy2-in") {
		t.Fatalf("mihomo subscription:\n%s", rec.Body)
	}
	rec = m.do("GET", strings.Replace(path, "mihomo", "singbox", 1), "")
	if out := gjson.Parse(rec.Body.String()); out.Get(`outbounds.#(type=="hysteria2").server`).String() != "203.0.113.5" {
		t.Fatalf("sing-box subscription:\n%s", rec.Body)
	}
	_, body = m.call("GET", "/api/users/1/access", "")
	if body.Get("access.#").Int() != 2 || body.Get("access.1.user_agent").String() != "clash-verge/v2.0" ||
		body.Get("access.1.format").String() != "mihomo" {
		t.Fatalf("access log: %s", body.Raw)
	}
}
