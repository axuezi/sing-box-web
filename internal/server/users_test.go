package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/axuezi/sing-box-web/internal/store"
)

func TestUserLifecycle(t *testing.T) {
	m := newConfigEnv(t)

	code, body := m.call("GET", "/api/users", "")
	if code != 200 || body.Get("users.#").Int() != 1 || body.Get("users.0.name").String() != "owner" ||
		body.Get("users.0.password").Exists() || body.Get("inbounds.#").Int() != 2 {
		t.Fatalf("list: %d %s", code, body.Raw)
	}

	// Create on one inbound only.
	code, body = m.call("POST", "/api/users", `{"name":"alice","inbounds":["reality-in"],"note":"phone"}`)
	if code != 200 || body.Get("result.status").String() != store.RevApplied {
		t.Fatalf("create: %d %s", code, body.Raw)
	}
	aliceID := body.Get("users.0.id").Int()
	cfg := m.config()
	if cfg.Get("inbounds.1.users.#").Int() != 2 || cfg.Get("inbounds.0.users.#").Int() != 1 ||
		cfg.Get("inbounds.1.users.1.flow").String() != "xtls-rprx-vision" {
		t.Fatalf("config after create:\n%s", cfg.Raw)
	}
	if m.ctl.reloads != 1 {
		t.Fatalf("reloads = %d", m.ctl.reloads)
	}

	// Duplicate name is refused and nothing changes.
	if code, _ := m.call("POST", "/api/users", `{"name":"alice"}`); code != http.StatusConflict {
		t.Fatalf("duplicate: %d", code)
	}
	if code, _ := m.call("POST", "/api/users", `{"name":"bad name!"}`); code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", code)
	}

	// Disable removes alice from the config but keeps her in the database.
	code, _ = m.call("PATCH", fmt.Sprintf("/api/users/%d", aliceID), `{"enabled":false}`)
	if code != 200 || m.config().Get("inbounds.1.users.#").Int() != 1 {
		t.Fatalf("disable: %d %s", code, m.config().Get("inbounds.1.users").Raw)
	}

	// Client info has links with the derived Reality public key.
	m.call("PUT", "/api/settings/subscription", `{"public_host":"203.0.113.5","inbounds":{"reality-in":{"port":443},"hy2-in":{"insecure":true}}}`)
	code, body = m.call("GET", "/api/users/1/client", "")
	if code != 200 || body.Get("links.#").Int() != 2 ||
		!strings.Contains(body.Get("links.1.url").String(), "pbk=Byv2lVaQvdAS_PSp28BIt1JLPDtVcb4pS7HEdAuYHiA") ||
		!strings.Contains(body.Get("links.1.url").String(), "@203.0.113.5:443") {
		t.Fatalf("client: %d %s", code, body.Raw)
	}
	subURL := body.Get("subscription.mihomo").String()

	// Subscription by token, no session needed.
	path := strings.TrimPrefix(subURL, "http://example.com")
	rec := m.do("GET", path, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"type":"hysteria2"`) ||
		!strings.Contains(rec.Body.String(), `"skip-cert-verify":true`) ||
		!strings.Contains(rec.Body.String(), `proxies: ["hy2-in", "reality-in", DIRECT]`) {
		t.Fatalf("mihomo sub: %d\n%s", rec.Code, rec.Body)
	}
	rec = m.do("GET", strings.Replace(path, "format=mihomo", "format=base64", 1), "")
	links, _ := base64.StdEncoding.DecodeString(rec.Body.String())
	if !strings.HasPrefix(string(links), "hysteria2://owner-hy2-pass@203.0.113.5:443/") {
		t.Fatalf("base64 sub: %s", links)
	}
	rec = m.do("GET", strings.Split(path, "?")[0], "", withHeader("User-Agent", "SFA/1.12.0"))
	if !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), `"type": "selector"`) {
		t.Fatalf("sing-box sub by UA: %s", rec.Body)
	}
	if rec := m.do("GET", "/sub/not-a-token", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bad token: %d", rec.Code)
	}

	// Reset the token: the old URL stops working.
	m.call("POST", "/api/users/1/reset", `{"token":true}`)
	if rec := m.do("GET", path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("old token still works: %d", rec.Code)
	}

	// Delete alice.
	if code, _ := m.call("DELETE", fmt.Sprintf("/api/users/%d", aliceID), ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := m.call("DELETE", "/api/users/999", ""); code != http.StatusNotFound {
		t.Fatalf("delete missing: %d", code)
	}

	actions := strings.Join(m.auditActions(), ",")
	for _, want := range []string{"user.create", "user.update", "user.reset", "user.delete", "settings.update"} {
		if !strings.Contains(actions, want) {
			t.Errorf("audit missing %s: %s", want, actions)
		}
	}
}

func TestRejectedChangeKeepsDatabase(t *testing.T) {
	m := newConfigEnv(t)
	// The name ends up in the config, where the fake checker rejects it.
	code, body := m.call("POST", "/api/users", `{"name":"reject-me"}`)
	if code != http.StatusUnprocessableEntity || body.Get("code").String() != "check_failed" {
		t.Fatalf("%d %s", code, body.Raw)
	}
	users, _ := m.st.ListUsers(context.Background())
	if len(users) != 1 {
		t.Fatalf("rejected user was saved: %+v", users)
	}
}
