package subscription

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/store"
)

const config = `{"inbounds": [
  {"type": "hysteria2", "tag": "hy2-in", "listen_port": 443, "users": [{"name": "alice", "password": "pw"}],
   "tls": {"enabled": true, "server_name": "vpn.example.com"}},
  {"type": "vless", "tag": "vless-in", "listen_port": 8443, "users": [{"name": "alice", "uuid": "6f6a3a1e-6c8e-4c7e-9f1b-6a4f2b3c9d10"}],
   "tls": {"enabled": true, "server_name": "vpn.example.com"}}
]}`

func clients(t *testing.T) []protocol.Client {
	t.Helper()
	u := store.User{Name: "alice", Password: "pw", UUID: "6f6a3a1e-6c8e-4c7e-9f1b-6a4f2b3c9d10", Inbounds: []string{"hy2-in", "vless-in"}}
	cs, skipped := Clients([]byte(config), u, Settings{PublicHost: "vpn.example.com"})
	if len(cs) != 2 || len(skipped) != 0 {
		t.Fatalf("clients %d, skipped %v", len(cs), skipped)
	}
	return cs
}

func TestSingBox(t *testing.T) {
	out, err := SingBox(clients(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg := gjson.ParseBytes(out)
	// The selector lists the user's proxies first, then what the base had.
	sel := cfg.Get(`outbounds.#(tag=="proxy")`)
	var members []string
	sel.Get("outbounds").ForEach(func(_, v gjson.Result) bool {
		members = append(members, v.String())
		return true
	})
	if strings.Join(members, ",") != "hy2-in,vless-in,direct" {
		t.Errorf("selector outbounds = %v", members)
	}
	if sel.Get("default").String() != "hy2-in" {
		t.Errorf("selector default = %s", sel.Get("default"))
	}
	// Proxies come right after the selector, before direct.
	var tags []string
	cfg.Get("outbounds.#.tag").ForEach(func(_, v gjson.Result) bool {
		tags = append(tags, v.String())
		return true
	})
	if strings.Join(tags, ",") != "proxy,hy2-in,vless-in,direct" {
		t.Errorf("outbound order = %v", tags)
	}
	// Each call starts from a fresh base.
	again, _ := SingBox(clients(t))
	if string(again) != string(out) {
		t.Error("second render differs: the base config was modified")
	}
}

func TestMihomo(t *testing.T) {
	out, err := Mihomo(clients(t))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "{{proxies}}") || strings.Contains(s, "{{proxy_names}}") {
		t.Fatal("placeholders left in the profile")
	}
	if !strings.Contains(s, `"name":"hy2-in"`) || !strings.Contains(s, `"hy2-in", "vless-in", `) {
		t.Errorf("proxies missing from profile:\n%s", s)
	}
}
