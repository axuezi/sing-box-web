package protocol

import (
	"net/url"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/store"
)

// Reality key pair from `sing-box generate reality-keypair`.
const (
	testPriv = "8BzFOZ8Y4u9I_JoYgTZWyvMY5y0UPu__HTSwwbHIRUU"
	testPub  = "Byv2lVaQvdAS_PSp28BIt1JLPDtVcb4pS7HEdAuYHiA"
)

var cfg = []byte(`{
  "log": {"level": "info"},
  "inbounds": [
    {
      "type": "hysteria2",
      "tag": "hy2-in",
      "listen_port": 443,
      "users": [
        {
          "password": "hy2-pass"
        }
      ],
      "tls": {"enabled": true}
    },
    {
      "type": "vless",
      "tag": "reality-in",
      "listen": "127.0.0.1",
      "listen_port": 8443,
      "users": [
        {
          "uuid": "11111111-2222-4333-8444-555555555555",
          "flow": "xtls-rprx-vision"
        }
      ],
      "tls": {
        "enabled": true,
        "server_name": "www.cloudflare.com",
        "reality": {"enabled": true, "private_key": "` + testPriv + `", "short_id": ["", "ab12"]}
      }
    },
    {"type": "mixed", "tag": "local", "listen_port": 2080}
  ],
  "experimental": {"v2ray_api": {"listen": "127.0.0.1:10085", "stats": {"enabled": true, "users": ["old"]}}}
}`)

func TestRealityPublicKey(t *testing.T) {
	pub, err := RealityPublicKey(testPriv)
	if err != nil || pub != testPub {
		t.Fatalf("pub = %q err = %v", pub, err)
	}
	if _, err := RealityPublicKey("short"); err == nil {
		t.Fatal("expected error")
	}
}

func TestImportMergesByPosition(t *testing.T) {
	users := Import(cfg)
	if len(users) != 1 {
		t.Fatalf("users = %+v", users)
	}
	u := users[0]
	if u.Name != "user-1" || u.Password != "hy2-pass" || u.UUID != "11111111-2222-4333-8444-555555555555" ||
		strings.Join(u.Inbounds, ",") != "hy2-in,reality-in" || u.SubToken == "" || !u.Enabled {
		t.Fatalf("user = %+v", u)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	users := Import(cfg)
	users[0].Name = "owner"
	users = append(users, store.User{ID: 2, Name: "alice", Enabled: true, Inbounds: []string{"reality-in"},
		Password: "alice-pw", UUID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"})
	users = append(users, store.User{ID: 3, Name: "bob", Enabled: false, Inbounds: []string{"hy2-in", "reality-in"}})

	out, err := Render(cfg, users)
	if err != nil {
		t.Fatal(err)
	}
	g := gjson.ParseBytes(out)
	if got := g.Get("inbounds.0.users").Raw; !strings.Contains(got, `"name": "owner"`) || strings.Contains(got, "alice") {
		t.Fatalf("hy2 users = %s", got)
	}
	if n := g.Get("inbounds.1.users.#").Int(); n != 2 {
		t.Fatalf("vless users = %d", n)
	}
	// flow is copied from the existing entry onto the new user.
	if g.Get("inbounds.1.users.1.flow").String() != "xtls-rprx-vision" || g.Get("inbounds.1.users.1.uuid").String() != users[1].UUID {
		t.Fatalf("alice entry = %s", g.Get("inbounds.1.users.1").Raw)
	}
	if strings.Contains(string(out), "bob") {
		t.Fatal("disabled user rendered")
	}
	if got := g.Get("experimental.v2ray_api.stats.users").Raw; strings.Join(strings.Fields(got), "") != `["owner","alice"]` {
		t.Fatalf("stats users = %s", got)
	}
	// Unmanaged inbound untouched; key order kept.
	if g.Get("inbounds.2.users").Exists() || !strings.HasPrefix(string(out), "{\n  \"log\"") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	// Re-rendering is stable.
	again, _ := Render(out, users)
	if string(again) != string(out) {
		t.Fatal("render not idempotent")
	}
}

func TestFormatLikeTabs(t *testing.T) {
	out := FormatLike([]byte("{\n\t\"a\": 1\n}\n"), []byte(`{"a":1,"b":[1,2]}`))
	if !strings.Contains(string(out), "\n\t\"b\": [\n\t\t1,") {
		t.Fatalf("got %q", out)
	}
}

func TestClients(t *testing.T) {
	users := Import(cfg)
	ins := Inbounds(cfg)
	hy := Client{Name: "us-hy2", Inbound: ins[0], Endpoint: Endpoint{Host: "203.0.113.5", Port: 443, Insecure: true}, User: users[0]}
	vl := Client{Name: "us-reality", Inbound: ins[1], Endpoint: Endpoint{Host: "203.0.113.5", Port: 443}, User: users[0]}

	p, _ := Get("hysteria2")
	m, _ := p.Mihomo(hy)
	if m["skip-cert-verify"] != true || m["sni"] != "203.0.113.5" || m["password"] != "hy2-pass" {
		t.Fatalf("hy2 mihomo = %v", m)
	}
	link, _ := p.Link(hy)
	if link != "hysteria2://hy2-pass@203.0.113.5:443/?insecure=1&sni=203.0.113.5#us-hy2" {
		t.Fatalf("hy2 link = %s", link)
	}

	p, _ = Get("vless")
	m, err := p.Mihomo(vl)
	if err != nil {
		t.Fatal(err)
	}
	ro := m["reality-opts"].(map[string]any)
	if ro["public-key"] != testPub || ro["short-id"] != "ab12" || m["flow"] != "xtls-rprx-vision" || m["servername"] != "www.cloudflare.com" {
		t.Fatalf("vless mihomo = %v", m)
	}
	link, _ = p.Link(vl)
	u, _ := url.Parse(link)
	q := u.Query()
	if u.Scheme != "vless" || u.User.Username() != users[0].UUID || u.Host != "203.0.113.5:443" ||
		q.Get("pbk") != testPub || q.Get("sid") != "ab12" || q.Get("security") != "reality" || u.Fragment != "us-reality" {
		t.Fatalf("vless link = %s", link)
	}
	sb, _ := p.SingBox(vl)
	if sb["tls"].(map[string]any)["reality"].(map[string]any)["public_key"] != testPub {
		t.Fatalf("vless sing-box = %v", sb)
	}

	// Unsupported transport is reported, not silently mis-exported.
	ws := []byte(`{"inbounds":[{"type":"vless","tag":"ws","users":[{"uuid":"x"}],"transport":{"type":"ws"}}]}`)
	if _, err := p.Mihomo(Client{Inbound: Inbounds(ws)[0], User: users[0]}); err == nil {
		t.Fatal("expected unsupported transport error")
	}
}
