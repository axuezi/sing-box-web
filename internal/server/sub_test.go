package server

import (
	"encoding/base64"
	"net/url"
	"strconv"
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

// clashVergeName replays how Clash Verge derives a profile name from
// Content-Disposition: it takes the header's Rust debug form (quotes
// escaped), trims the outer quotes, prefers filename* (percent-decoded,
// after the last ”) and otherwise takes filename up to ';' and trims '"'.
func clashVergeName(header string) string {
	dbg := strings.Trim(strconv.Quote(header), `"`)
	param := func(key string) (string, bool) {
		i := strings.Index(dbg, key+"=")
		if i < 0 {
			return "", false
		}
		v := dbg[i+len(key)+1:]
		if j := strings.IndexByte(v, ';'); j >= 0 {
			v = v[:j]
		}
		return strings.TrimSpace(v), true
	}
	if v, ok := param("filename*"); ok {
		dec, _ := url.PathUnescape(v)
		parts := strings.Split(dec, "''")
		return parts[len(parts)-1]
	}
	v, _ := param("filename")
	return strings.Trim(v, `"`)
}

func TestSubscriptionProfileName(t *testing.T) {
	m := newConfigEnv(t)
	_, body := m.call("GET", "/api/users/1/client", "")
	const user = "owner" // the first user in newConfigEnv
	path := strings.TrimPrefix(body.Get("subscription.mihomo").String(), "http://example.com")
	rec := m.do("GET", path, "")
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(rec.Header().Get("Profile-Title"), "base64:"))
	cd := rec.Header().Get("Content-Disposition")
	// The user name, unquoted and without an extension, which Clash Verge
	// shows as is.
	if string(raw) != user || cd != "attachment; filename="+user || clashVergeName(cd) != user {
		t.Fatalf("Profile-Title %q, Content-Disposition %q -> Clash Verge %q", raw, cd, clashVergeName(cd))
	}
}

// User names with characters outside an RFC 7230 token still reach Clash
// Verge intact through filename*.
func TestContentDispositionNonToken(t *testing.T) {
	for _, name := range []string{"张三", "a b", `x"y;z`} {
		cd := contentDisposition(name)
		if strings.Contains(cd, `"`) {
			t.Errorf("%q: quoted header %q", name, cd)
		}
		if got := clashVergeName(cd); got != name {
			t.Errorf("%q: Clash Verge would show %q (header %q)", name, got, cd)
		}
	}
}

func TestSubscriptionMihomoTemplate(t *testing.T) {
	m := newConfigEnv(t)
	_, body := m.call("GET", "/api/settings/subscription", "")
	if !strings.Contains(body.Get("mihomo_default_template").String(), "{{proxies}}") {
		t.Fatalf("default template missing: %s", body.Raw)
	}
	_, body = m.call("GET", "/api/users/1/client", "")
	path := strings.TrimPrefix(body.Get("subscription.mihomo").String(), "http://example.com")

	if code, b := m.call("PUT", "/api/settings/subscription", `{"mihomo_template":"mode: rule\n"}`); code != 400 || b.Get("code").String() != "bad_mihomo_template" {
		t.Fatalf("template without {{proxies}}: %d %s", code, b.Raw)
	}
	if code, _ := m.call("PUT", "/api/settings/subscription", `{"public_host":"203.0.113.5","mihomo_template":"mixed-port: 7890\nproxies:\n{{proxies}}\n"}`); code != 200 {
		t.Fatalf("save template: %d", code)
	}
	if got := m.do("GET", path, "").Body.String(); !strings.HasPrefix(got, "mixed-port: 7890\nproxies:\n  - {") {
		t.Fatalf("custom template not served:\n%s", got)
	}
	// Clearing it brings back the built-in profile.
	m.call("PUT", "/api/settings/subscription", `{"public_host":"203.0.113.5","mihomo_template":""}`)
	if got := m.do("GET", path, "").Body.String(); !strings.Contains(got, "mixed-port: 7897") {
		t.Fatalf("built-in template not restored:\n%s", got)
	}
}
