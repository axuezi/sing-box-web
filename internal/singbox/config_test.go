package singbox

import (
	"strings"
	"testing"
)

const sample = `{
  "log": {"level": "info", "output": "/var/log/sing-box/sing-box.log"},
  "inbounds": [
    {"type": "hysteria2", "tag": "hy2-in", "listen": "::", "listen_port": 443,
     "users": [{"name": "owner", "password": "hunter2-secret"}],
     "tls": {"enabled": true, "key_path": "/etc/sing-box/cert/private.key"}},
    {"type": "vless", "tag": "reality-in", "listen": "127.0.0.1", "listen_port": 8443,
     "users": [{"uuid": "6f1c-uuid", "flow": "xtls-rprx-vision"}],
     "tls": {"enabled": true, "server_name": "www.cloudflare.com",
             "reality": {"enabled": true, "private_key": "PRIV", "short_id": ["ab12", ""]}}}
  ],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"rules": [{"action": "sniff"}, {"port": 53, "action": "hijack-dns"}]},
  "experimental": {"clash_api": {"external_controller": "127.0.0.1:9090", "secret": "s3cr3t"},
                   "weird.key": {"password": "dotted"}}
}`

func TestSummarize(t *testing.T) {
	sum := Summarize([]byte(sample))
	if len(sum.Inbounds) != 2 || sum.Inbounds[0].Users != 1 || sum.Inbounds[0].ListenPort != 443 {
		t.Fatalf("inbounds = %+v", sum.Inbounds)
	}
	if !sum.Inbounds[1].Reality || sum.Inbounds[1].ServerName != "www.cloudflare.com" {
		t.Fatalf("reality inbound = %+v", sum.Inbounds[1])
	}
	if sum.RouteRules != 2 || sum.ClashAPI != "127.0.0.1:9090" || sum.V2RayAPI != "" {
		t.Fatalf("summary = %+v", sum)
	}
	if ClashAPISecret([]byte(sample)) != "s3cr3t" {
		t.Fatal("clash secret")
	}
}

func TestLint(t *testing.T) {
	ok := []byte(`{
	  "inbounds": [{"tag": "in"}],
	  "outbounds": [{"type": "direct", "tag": "direct", "domain_resolver": {"server": "doh"}},
	                {"type": "selector", "tag": "sel", "outbounds": ["direct"], "default": "direct"}],
	  "endpoints": [{"type": "wireguard", "tag": "wg"}],
	  "dns": {"servers": [{"tag": "doh", "type": "https", "server": "1.1.1.1"}], "final": "doh",
	          "rules": [{"domain_suffix": ["x"], "server": "doh"}]},
	  "route": {"final": "sel", "rules": [{"inbound": ["in"], "outbound": "wg"}, {"rule_set": "cn", "action": "reject"}],
	            "rule_set": [{"tag": "cn", "type": "remote"}], "default_domain_resolver": "doh"}
	}`)
	if p := Lint(ok); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
	bad := []byte(`{
	  "inbounds": [{"tag": "in"}, {"tag": "in"}],
	  "outbounds": [{"type": "direct", "tag": "direct", "domain_resolver": "nope-dns"}],
	  "dns": {"servers": [], "final": "gone"},
	  "route": {"final": "no-such-outbound", "rules": [{"inbound": "missing-in", "outbound": "direct"}, {"rule_set": ["x"]}]}
	}`)
	got := strings.Join(Lint(bad), "\n")
	for _, want := range []string{`inbound tag "in" is used more than once`, `route.final refers to unknown outbound "no-such-outbound"`,
		`unknown inbound "missing-in"`, `unknown rule set "x"`, `dns.final refers to unknown DNS server "gone"`,
		`outbounds[0].domain_resolver refers to unknown DNS server "nope-dns"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
