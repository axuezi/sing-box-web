package server

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"slices"
	"testing"

	"github.com/tidwall/sjson"

	"github.com/axuezi/sing-box-web/internal/inbounds"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/subscription"
)

func TestInboundLifecycle(t *testing.T) {
	m := newConfigEnv(t)
	ctx := context.Background()
	// Another program on the host holds TCP 127.0.0.1:3443.
	held := []inbounds.Socket{{Network: "tcp", Addr: netip.MustParseAddr("127.0.0.1"), Port: 3443}}
	m.h = New(Options{Store: m.st, SingboxConfig: m.cfgPath, SingboxLog: m.logPath, Apply: m.mgr, Controller: m.ctl,
		Sockets: func() ([]inbounds.Socket, bool) { return held, true }})

	code, body := m.call("GET", "/api/inbounds", "")
	if code != 200 || body.Get("inbounds.#").Int() != 2 || body.Get("inbounds.1.form.tls.mode").String() != "reality" ||
		body.Get("inbounds.1.form.tls.private_key").String() != singbox.MaskedValue || body.Get("inbounds.1.form.tls.public_key").String() == "" {
		t.Fatalf("list: %d %s", code, body.Raw)
	}

	_, keys := m.call("POST", "/api/inbounds/generate", `{"kind":"reality_keypair"}`)
	_, sid := m.call("POST", "/api/inbounds/generate", `{"kind":"short_id"}`)
	form := map[string]any{
		"tag": "vless-2", "type": "vless", "listen_port": 2443, "flow": "",
		"tls": map[string]any{"mode": "reality", "server_name": "www.example.com", "handshake_server": "www.example.com",
			"private_key": keys.Get("private_key").String(), "short_ids": []string{sid.Get("short_id").String()}},
	}
	req, _ := json.Marshal(map[string]any{"form": form, "assign_users": true, "export": map[string]any{"port": 443}})
	if code, b := m.call("POST", "/api/inbounds", string(req)); code != 200 || b.Get("result.status").String() != "applied" {
		t.Fatalf("create: %d %s", code, b.Raw)
	}
	in := m.config().Get(`inbounds.#(tag=="vless-2")`)
	if in.Get("users.0.name").String() != "owner" || in.Get("users.0.uuid").String() == "" || in.Get("users.0.flow").Exists() {
		t.Fatalf("new inbound users: %s", in.Raw)
	}
	u, _ := m.st.GetUser(ctx, 1)
	if !slices.Contains(u.Inbounds, "vless-2") {
		t.Fatalf("user inbounds: %v", u.Inbounds)
	}
	if st, _ := m.subscriptionSettingsForTest(); st.Inbounds["vless-2"].Port != 443 {
		t.Fatalf("export not saved: %+v", st.Inbounds)
	}

	if code, b := m.call("POST", "/api/inbounds", string(req)); code != 409 || b.Get("code").String() != "tag_taken" {
		t.Fatalf("duplicate tag: %d %s", code, b.Raw)
	}
	form["tag"] = "vless-3"
	req, _ = json.Marshal(map[string]any{"form": form})
	if code, b := m.call("POST", "/api/inbounds", string(req)); code != 409 || b.Get("inbound").String() != "vless-2" {
		t.Fatalf("port conflict: %d %s", code, b.Raw)
	}
	form["listen_port"] = 3443
	req, _ = json.Marshal(map[string]any{"form": form})
	before, _ := os.ReadFile(m.cfgPath)
	if code, b := m.call("POST", "/api/inbounds", string(req)); code != 409 || b.Get("code").String() != "port_in_use" {
		t.Fatalf("host port in use: %d %s", code, b.Raw)
	}
	if after, _ := os.ReadFile(m.cfgPath); string(after) != string(before) {
		t.Fatal("config changed by a refused inbound")
	}

	// Edit: masked key kept, port moved, flow turned on for existing users.
	_, body = m.call("GET", "/api/inbounds", "")
	edit := body.Get(`inbounds.#(form.tag=="vless-2").form`).Value().(map[string]any)
	edit["listen_port"], edit["flow"] = 2444, "xtls-rprx-vision"
	req, _ = json.Marshal(map[string]any{"form": edit})
	if code, b := m.call("PUT", "/api/inbounds/vless-2", string(req)); code != 200 {
		t.Fatalf("update: %d %s", code, b.Raw)
	}
	in = m.config().Get(`inbounds.#(tag=="vless-2")`)
	if in.Get("listen_port").Int() != 2444 || in.Get("tls.reality.private_key").String() != keys.Get("private_key").String() ||
		in.Get("users.0.flow").String() != "xtls-rprx-vision" {
		t.Fatalf("updated inbound: %s", in.Raw)
	}

	// A rule using the inbound blocks deletion.
	raw, _ := os.ReadFile(m.cfgPath)
	withRule, _ := sjson.SetBytes(raw, "route.rules", []any{map[string]any{"inbound": []string{"vless-2"}, "outbound": "direct"}})
	os.WriteFile(m.cfgPath, withRule, 0o600)
	if code, b := m.call("DELETE", "/api/inbounds/vless-2", ""); code != 409 || b.Get("references.0").String() != "route.rules.0" {
		t.Fatalf("referenced delete: %d %s", code, b.Raw)
	}
	os.WriteFile(m.cfgPath, raw, 0o600)
	if code, b := m.call("DELETE", "/api/inbounds/vless-2", ""); code != 200 {
		t.Fatalf("delete: %d %s", code, b.Raw)
	}
	if m.config().Get(`inbounds.#(tag=="vless-2")`).Exists() {
		t.Fatal("inbound still in config")
	}
	u, _ = m.st.GetUser(ctx, 1)
	if slices.Contains(u.Inbounds, "vless-2") || len(u.Inbounds) != 2 {
		t.Fatalf("user inbounds after delete: %v", u.Inbounds)
	}
	if st, _ := m.subscriptionSettingsForTest(); len(st.Inbounds) != 0 {
		t.Fatalf("export not removed: %+v", st.Inbounds)
	}
	if code, _ := m.call("PUT", "/api/inbounds/nope", string(req)); code != 404 {
		t.Fatalf("missing inbound: %d", code)
	}
	if code, _ := m.call("POST", "/api/inbounds/generate", `{"kind":"self_signed","server_name":"vpn.example.com"}`); code != 200 {
		t.Fatal("self-signed")
	}
}

func (m *configEnv) subscriptionSettingsForTest() (subscription.Settings, error) {
	return (&Server{opts: Options{Store: m.st}}).subscriptionSettings(context.Background())
}
