package outbounds

import (
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/singbox"
)

// A config in the shapes people write by hand: short and long forms,
// options the form does not model, odd key order.
const sample = `{
  "outbounds": [
    {
      "type": "direct",
      "tag": "direct",
      "inet6_bind_address": "2001:db8::1",
      "domain_resolver": { "server": "doh", "strategy": "prefer_ipv6" }
    },
    { "type": "direct", "tag": "v4", "inet4_bind_address": "192.0.2.10", "domain_resolver": "doh" },
    {
      "tag": "relay", "type": "vless", "server": "relay.example.com", "server_port": 443,
      "uuid": "6f6a3a1e-6c8e-4c7e-9f1b-6a4f2b3c9d10", "flow": "xtls-rprx-vision",
      "tls": { "enabled": true, "server_name": "www.example.com", "alpn": "h2",
        "utls": { "enabled": true },
        "reality": { "enabled": true, "public_key": "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0", "short_id": "0123abcd" } },
      "multiplex": { "enabled": true, "protocol": "h2mux" }
    },
    { "type": "hysteria2", "tag": "hy", "server": "198.51.100.7", "server_port": 8443, "password": "pw",
      "up_mbps": 50, "obfs": { "type": "salamander", "password": "ob" }, "tls": { "enabled": true, "insecure": true } },
    { "type": "shadowsocks", "tag": "ss", "server": "198.51.100.8", "server_port": 8388,
      "method": "2022-blake3-aes-128-gcm", "password": "k", "udp_over_tcp": true, "detour": "v4" },
    { "type": "socks", "tag": "s5", "server": "127.0.0.1", "server_port": 1080, "version": "5", "username": "u", "password": "p" },
    { "type": "selector", "tag": "pick", "outbounds": ["relay", "hy"], "default": "relay" }
  ],
  "route": {
    "rules": [
      { "domain_suffix": ["example.org"], "outbound": "v4" },
      { "type": "logical", "mode": "and", "rules": [{ "port": 22 }, { "network": "tcp" }], "outbound": "ss" }
    ],
    "rule_set": [{ "tag": "geo", "type": "remote", "url": "https://example.test/geo.srs", "download_detour": "relay" }]
  },
  "dns": { "servers": [{ "type": "https", "tag": "doh", "server": "1.1.1.1", "detour": "hy" }] },
  "experimental": { "v2ray_api": { "stats": { "enabled": true, "outbounds": ["direct", "v4"] } } }
}`

func TestRoundTripUnchanged(t *testing.T) {
	raw := []byte(sample)
	gjson.GetBytes(raw, "outbounds").ForEach(func(k, o gjson.Result) bool {
		if !slices.Contains(Editable, o.Get("type").String()) {
			return true
		}
		f, _ := Read(o)
		if err := Normalize(&f); err != nil {
			t.Fatalf("%s: normalize: %v", f.Tag, err)
		}
		out, err := Write(raw, int(k.Int()), f, o)
		if err != nil {
			t.Fatalf("%s: write: %v", f.Tag, err)
		}
		if string(out) != string(raw) {
			t.Errorf("%s: saving an unchanged form changed the config:\n%s", f.Tag, gjson.GetBytes(out, "outbounds."+k.String()).Raw)
		}
		return true
	})
}

func TestReadMasksAndListsExtras(t *testing.T) {
	_, o := Find([]byte(sample), "relay")
	f, extra := Read(o)
	if f.UUID != singbox.MaskedValue {
		t.Errorf("uuid not masked: %q", f.UUID)
	}
	if f.TLS.Mode != SecReality || f.TLS.UTLS != "chrome" || !slices.Equal(f.TLS.ALPN, []string{"h2"}) {
		t.Errorf("tls read wrong: %+v", f.TLS)
	}
	if !slices.Equal(extra, []string{"multiplex"}) {
		t.Errorf("extra = %v", extra)
	}
	_, o = Find([]byte(sample), "direct")
	f, _ = Read(o)
	if f.Resolver != "doh" || f.ResolverStrategy != "prefer_ipv6" || f.Inet6Bind != "2001:db8::1" {
		t.Errorf("direct read wrong: %+v", f)
	}
}

func TestEditKeepsSecretsAndUnmodelled(t *testing.T) {
	raw := []byte(sample)
	idx, o := Find(raw, "hy")
	f, _ := Read(o)
	f.ServerPort = 9443
	f.ObfsPassword = "" // turn obfuscation off
	if err := Normalize(&f); err != nil {
		t.Fatal(err)
	}
	out, err := Write(raw, idx, f, o)
	if err != nil {
		t.Fatal(err)
	}
	got := gjson.GetBytes(out, "outbounds.3")
	if got.Get("password").String() != "pw" || got.Get("server_port").Int() != 9443 || got.Get("obfs").Exists() {
		t.Errorf("hy after edit: %s", got.Raw)
	}

	idx, o = Find(raw, "direct")
	f, _ = Read(o)
	f.ResolverStrategy = "" // object form: drop only the strategy
	if err := Normalize(&f); err != nil {
		t.Fatal(err)
	}
	if out, err = Write(raw, idx, f, o); err != nil {
		t.Fatal(err)
	}
	if r := gjson.GetBytes(out, "outbounds.0.domain_resolver"); r.Get("server").String() != "doh" || r.Get("strategy").Exists() {
		t.Errorf("resolver after edit: %s", r.Raw)
	}
}

func TestCreate(t *testing.T) {
	raw := []byte(sample)
	f := Form{Tag: "warp", Type: "socks", Server: "127.0.0.1", ServerPort: 40000}
	if err := Normalize(&f); err != nil {
		t.Fatal(err)
	}
	out, err := Write(raw, -1, f, gjson.Result{})
	if err != nil {
		t.Fatal(err)
	}
	if out, err = SyncStats(out, "warp", true); err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(out, "outbounds.#(tag==\"warp\")").Raw; got != `{"type":"socks","tag":"warp","server":"127.0.0.1","server_port":40000}` {
		t.Errorf("created %s", got)
	}
	if got := gjson.GetBytes(out, "experimental.v2ray_api.stats.outbounds").Raw; got != `["direct","v4","warp"]` {
		t.Errorf("stats outbounds %s", got)
	}

	// A new outbound cannot keep a secret it never had.
	f = Form{Tag: "t", Type: "trojan", Server: "example.com", ServerPort: 443, Password: singbox.MaskedValue}
	if err := Normalize(&f); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(raw, -1, f, gjson.Result{}); err == nil {
		t.Error("masked password accepted for a new outbound")
	}
}

func TestNormalizeRejects(t *testing.T) {
	bad := []Form{
		{Tag: "x", Type: "wireguard"},
		{Tag: "-x", Type: "direct"},
		{Tag: "x", Type: "direct", Detour: "x"},
		{Tag: "x", Type: "direct", Inet4Bind: "2001:db8::1"},
		{Tag: "x", Type: "direct", ResolverStrategy: "prefer_ipv4"},
		{Tag: "x", Type: "socks", Server: "bad host", ServerPort: 1080},
		{Tag: "x", Type: "socks", Server: "h", ServerPort: 0},
		{Tag: "x", Type: "shadowsocks", Server: "h", ServerPort: 1, Method: "rc4", Password: "p"},
		{Tag: "x", Type: "vless", Server: "h", ServerPort: 1, UUID: "nope"},
		{Tag: "x", Type: "vless", Server: "h", ServerPort: 1, UUID: "6f6a3a1e-6c8e-4c7e-9f1b-6a4f2b3c9d10", Flow: FlowVision, TLS: TLS{Mode: SecNone}},
		{Tag: "x", Type: "vless", Server: "h", ServerPort: 1, UUID: "6f6a3a1e-6c8e-4c7e-9f1b-6a4f2b3c9d10", TLS: TLS{Mode: SecReality, ServerName: "a.b", PublicKey: "short"}},
		{Tag: "x", Type: "trojan", Server: "h", ServerPort: 1},
	}
	for _, f := range bad {
		if err := Normalize(&f); err == nil {
			t.Errorf("accepted %+v", f)
		}
	}
	ok := Form{Tag: "x", Type: "vless", Server: "h", ServerPort: 1, UUID: "6f6a3a1e-6c8e-4c7e-9f1b-6a4f2b3c9d10",
		TLS: TLS{Mode: SecReality, ServerName: "a.b", PublicKey: "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0"}}
	if err := Normalize(&ok); err != nil || ok.TLS.UTLS != "chrome" {
		t.Errorf("reality client: err %v, utls %q (sing-box needs uTLS for reality)", err, ok.TLS.UTLS)
	}
}

func TestReferences(t *testing.T) {
	raw := []byte(sample)
	cases := map[string][]string{
		"direct": {"route.final (default: first outbound)"},
		"v4":     {"route.rules.0", "outbounds.4.detour"},
		"ss":     {"route.rules.1"},
		"relay":  {"outbounds.6.outbounds", "outbounds.6.default", "route.rule_set.0.download_detour"},
		"hy":     {"outbounds.6.outbounds", "dns.servers.0.detour"},
		"s5":     nil,
	}
	for tag, want := range cases {
		got := References(raw, tag)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("References(%s) = %v, want %v", tag, got, want)
		}
	}
}

func TestDetourLoop(t *testing.T) {
	raw := []byte(sample) // ss -> v4
	if !DetourLoop(raw, "v4", "ss") {
		t.Error("v4 -> ss -> v4 not detected")
	}
	if DetourLoop(raw, "relay", "ss") {
		t.Error("relay -> ss -> v4 is no loop")
	}
	if got := strings.Join(Tags(raw), ","); got != "direct,v4,relay,hy,ss,s5,pick" {
		t.Errorf("Tags = %s", got)
	}
}
