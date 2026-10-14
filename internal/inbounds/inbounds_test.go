package inbounds

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
)

const cfg = `{
  "inbounds": [
    {"type": "hysteria2", "tag": "hy2-in", "listen": "::", "listen_port": 443,
     "users": [{"name": "owner", "password": "p"}],
     "tls": {"enabled": true, "certificate_path": "/etc/sing-box/cert/cert.pem", "key_path": "/etc/sing-box/cert/private.key"},
     "masquerade": {"type": "proxy", "url": "https://example.com"}},
    {"type": "vless", "tag": "reality-in", "listen": "127.0.0.1", "listen_port": 8443, "sniff": true,
     "users": [{"name": "owner", "uuid": "11111111-2222-4333-8444-555555555555", "flow": "xtls-rprx-vision"}],
     "tls": {"enabled": true, "server_name": "www.cloudflare.com",
             "reality": {"enabled": true, "handshake": {"server": "www.cloudflare.com", "server_port": 443},
                         "private_key": "8BzFOZ8Y4u9I_JoYgTZWyvMY5y0UPu__HTSwwbHIRUU", "short_id": "ab12"}}},
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 1080}
  ],
  "route": {"rules": [{"inbound": ["mixed-in"], "outbound": "direct"},
                      {"type": "logical", "mode": "and", "rules": [{"inbound": "reality-in"}, {"port": 80}], "outbound": "direct"}]},
  "experimental": {"v2ray_api": {"stats": {"enabled": true, "inbounds": ["hy2-in", "reality-in"]}}}
}`

func inbound(raw []byte, tag string) gjson.Result {
	_, v := Find(raw, tag)
	return v
}

func sameJSON(t *testing.T, a, b string) {
	t.Helper()
	var x, y any
	json.Unmarshal([]byte(a), &x)
	json.Unmarshal([]byte(b), &y)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("JSON differs:\n%s\n%s", a, b)
	}
}

func TestReadMasksAndListsExtras(t *testing.T) {
	f, extra := Read(inbound([]byte(cfg), "reality-in"))
	pub, _ := protocol.RealityPublicKey("8BzFOZ8Y4u9I_JoYgTZWyvMY5y0UPu__HTSwwbHIRUU")
	if f.TLS.Mode != SecReality || f.TLS.PrivateKey != singbox.MaskedValue || f.TLS.PublicKey != pub ||
		!reflect.DeepEqual(f.TLS.ShortIDs, []string{"ab12"}) || f.TLS.HandshakePort != 443 || f.Flow != FlowVision {
		t.Fatalf("reality form: %+v", f)
	}
	if !reflect.DeepEqual(extra, []string{"sniff"}) {
		t.Fatalf("extra: %v", extra)
	}
	h, extra := Read(inbound([]byte(cfg), "hy2-in"))
	if h.TLS.Mode != SecTLS || h.TLS.CertSource != CertFile || h.Masquerade != "" || !reflect.DeepEqual(extra, []string{"masquerade"}) {
		t.Fatalf("hy2 form: %+v extra %v", h, extra)
	}
}

// Saving an unchanged form must leave the inbound exactly as it was.
func TestRoundTripIsLossless(t *testing.T) {
	raw := []byte(cfg)
	for _, tag := range []string{"hy2-in", "reality-in"} {
		idx, orig := Find(raw, tag)
		f, _ := Read(orig)
		if err := Normalize(&f); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		out, err := Write(raw, idx, f, orig)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		sameJSON(t, orig.Raw, inbound(out, tag).Raw)
	}
}

func TestWriteChanges(t *testing.T) {
	raw := []byte(cfg)

	// Reality → TLS with an inline self-signed certificate, no flow.
	idx, orig := Find(raw, "reality-in")
	f, _ := Read(orig)
	cert, key, err := SelfSigned("vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	f.ListenPort, f.Flow = 9443, ""
	f.TLS = TLS{Mode: SecTLS, ServerName: "vpn.example.com", ALPN: []string{"h2", " ", "h2"}, CertSource: CertInline, Certificate: cert, Key: key}
	if err := Normalize(&f); err != nil {
		t.Fatal(err)
	}
	out, err := Write(raw, idx, f, orig)
	if err != nil {
		t.Fatal(err)
	}
	v := inbound(out, "reality-in")
	if v.Get("listen_port").Int() != 9443 || v.Get("tls.reality").Exists() || !strings.Contains(v.Get("tls.key").String(), "PRIVATE KEY") ||
		v.Get("tls.alpn").Raw != `["h2"]` || v.Get("users.0.flow").Exists() || !v.Get("sniff").Bool() {
		t.Fatalf("tls inbound: %s", v.Raw)
	}

	// Keeping the masked key on a later edit keeps the stored key.
	f2, _ := Read(v)
	f2.ListenPort = 9444
	Normalize(&f2)
	out2, err := Write(out, idx, f2, v)
	if err != nil || inbound(out2, "reality-in").Get("tls.key").String() != v.Get("tls.key").String() {
		t.Fatalf("masked key not kept: %v", err)
	}

	// Hysteria2: obfs on, bandwidth, masquerade object left alone.
	idx, orig = Find(raw, "hy2-in")
	h, _ := Read(orig)
	h.ObfsPassword, h.UpMbps = "secret", 100
	Normalize(&h)
	out, _ = Write(raw, idx, h, orig)
	v = inbound(out, "hy2-in")
	if v.Get("obfs.type").String() != "salamander" || v.Get("up_mbps").Int() != 100 || v.Get("down_mbps").Exists() ||
		v.Get("masquerade.type").String() != "proxy" {
		t.Fatalf("hy2: %s", v.Raw)
	}
	h.ObfsPassword = ""
	out, _ = Write(out, idx, h, v)
	if inbound(out, "hy2-in").Get("obfs").Exists() {
		t.Fatal("obfs not removed")
	}
}

func TestNewInbound(t *testing.T) {
	priv, pub, err := RealityKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := protocol.RealityPublicKey(priv); p != pub {
		t.Fatal("public key mismatch")
	}
	f := Form{Tag: "vless-new", Type: "vless", ListenPort: 2443, Flow: FlowVision,
		TLS: TLS{Mode: SecReality, ServerName: "www.example.com", HandshakeServer: "www.example.com", PrivateKey: priv, ShortIDs: []string{"AB12", "ab12", ""}}}
	if err := Normalize(&f); err != nil {
		t.Fatal(err)
	}
	if f.Listen != "::" || f.TLS.HandshakePort != 443 || !reflect.DeepEqual(f.TLS.ShortIDs, []string{"ab12", ""}) {
		t.Fatalf("normalized: %+v", f)
	}
	out, err := Write([]byte(cfg), -1, f, gjson.Result{})
	if err != nil {
		t.Fatal(err)
	}
	v := inbound(out, "vless-new")
	if v.Get("type").String() != "vless" || v.Get("users").Raw != "[]" || v.Get("tls.reality.short_id").Raw != `["ab12",""]` {
		t.Fatalf("new inbound: %s", v.Raw)
	}
	out, _ = SyncStatsInbounds(out, "vless-new", true)
	if gjson.GetBytes(out, "experimental.v2ray_api.stats.inbounds").Raw != `["hy2-in","reality-in","vless-new"]` {
		t.Fatalf("stats: %s", gjson.GetBytes(out, "experimental.v2ray_api.stats").Raw)
	}
}

func TestNormalizeRejects(t *testing.T) {
	base := func() Form {
		return Form{Tag: "x", Type: "vless", ListenPort: 443, TLS: TLS{Mode: SecTLS, CertSource: CertFile, CertificatePath: "/a", KeyPath: "/b"}}
	}
	cases := map[string]func(*Form){
		"bad type":      func(f *Form) { f.Type = "trojan" },
		"bad tag":       func(f *Form) { f.Tag = "-x" },
		"bad listen":    func(f *Form) { f.Listen = "localhost" },
		"bad port":      func(f *Form) { f.ListenPort = 70000 },
		"no cert path":  func(f *Form) { f.TLS.KeyPath = "" },
		"vision no tls": func(f *Form) { f.TLS.Mode, f.Flow = SecNone, FlowVision },
		"bad flow":      func(f *Form) { f.Flow = "xtls-rprx-direct" },
		"reality no key": func(f *Form) {
			f.TLS = TLS{Mode: SecReality, ServerName: "a.com", HandshakeServer: "a.com", ShortIDs: []string{"ab"}}
		},
		"odd short id": func(f *Form) {
			p, _, _ := RealityKeyPair()
			f.TLS = TLS{Mode: SecReality, ServerName: "a.com", HandshakeServer: "a.com", PrivateKey: p, ShortIDs: []string{"abc"}}
		},
		"inline no pem":  func(f *Form) { f.TLS.CertSource, f.TLS.Certificate, f.TLS.Key = CertInline, "x", "y" },
		"hy2 masquerade": func(f *Form) { f.Type, f.Masquerade = "hysteria2", "example.com" },
	}
	for name, mod := range cases {
		f := base()
		mod(&f)
		if err := Normalize(&f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	h := base()
	h.Type = "hysteria2"
	h.TLS.Mode = SecNone
	if err := Normalize(&h); err != nil || h.TLS.Mode != SecTLS {
		t.Errorf("hysteria2 must force TLS: %v %v", err, h.TLS.Mode)
	}
}

func TestConflictAndReferences(t *testing.T) {
	raw := []byte(cfg)
	// UDP 443 (hy2) and TCP 443 can coexist.
	if c := Conflict(raw, Form{Tag: "v", Type: "vless", Listen: "::", ListenPort: 443}); c != "" {
		t.Fatalf("tcp vs udp: %q", c)
	}
	if c := Conflict(raw, Form{Tag: "h", Type: "hysteria2", Listen: "0.0.0.0", ListenPort: 443}); c != "hy2-in" {
		t.Fatalf("udp 443: %q", c)
	}
	if c := Conflict(raw, Form{Tag: "v", Type: "vless", Listen: "::", ListenPort: 8443}); c != "reality-in" {
		t.Fatalf("wildcard vs loopback: %q", c)
	}
	if c := Conflict(raw, Form{Tag: "v", Type: "vless", Listen: "10.0.0.1", ListenPort: 8443}); c != "" {
		t.Fatalf("different address: %q", c)
	}
	if c := Conflict(raw, Form{Tag: "reality-in", Type: "vless", Listen: "::", ListenPort: 8443}); c != "" {
		t.Fatalf("itself: %q", c)
	}
	if c := Conflict(raw, Form{Tag: "v", Type: "vless", Listen: "127.0.0.1", ListenPort: 1080}); c != "mixed-in" {
		t.Fatalf("mixed: %q", c)
	}

	if r := References(raw, "reality-in"); !reflect.DeepEqual(r, []string{"route.rules.1.rules.0"}) {
		t.Fatalf("refs: %v", r)
	}
	if r := References(raw, "mixed-in"); !reflect.DeepEqual(r, []string{"route.rules.0"}) {
		t.Fatalf("refs: %v", r)
	}
	if r := References(raw, "hy2-in"); len(r) != 0 {
		t.Fatalf("refs: %v", r)
	}
}

func TestSelfSigned(t *testing.T) {
	for _, name := range []string{"vpn.example.com", "203.0.113.5"} {
		c, k, err := SelfSigned(name)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := pem.Decode([]byte(c))
		cert, err := x509.ParseCertificate(b.Bytes)
		if err != nil || cert.VerifyHostname(name) != nil || !strings.Contains(k, "EC PRIVATE KEY") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if id, _ := ShortID(); len(id) != 16 {
		t.Fatalf("short id %q", id)
	}
}
