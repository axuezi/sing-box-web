package dnsconf

import (
	"slices"
	"testing"

	"github.com/tidwall/gjson"
)

const sample = `{
  "dns": {
    "servers": [
      { "type": "https", "tag": "doh", "server": "1.1.1.1", "server_port": 443, "path": "/dns-query",
        "tls": { "enabled": true, "server_name": "cloudflare-dns.com" } },
      { "type": "udp", "tag": "isp", "server": "192.0.2.53", "detour": "direct" },
      { "type": "tls", "tag": "dot", "server": "dns.example.net", "domain_resolver": { "server": "isp", "strategy": "ipv4_only" } },
      { "type": "local", "tag": "sys", "prefer_go": true },
      { "type": "fakeip", "tag": "fake", "inet4_range": "198.18.0.0/15" },
      { "tag": "legacy", "address": "tls://8.8.8.8" }
    ],
    "strategy": "prefer_ipv6",
    "rules": [
      { "domain_suffix": ["example.com", "example.org"], "action": "route", "server": "doh", "strategy": "ipv4_only" },
      { "type": "logical", "mode": "or", "rules": [{ "query_type": "AAAA" }, { "domain": "x.test" }], "action": "reject" },
      { "rule_set": "geosite-cn", "server": "dot", "invert": true },
      { "domain_suffix": ["example.net"], "query_type": ["AAAA"], "action": "predefined", "rcode": "NOERROR" }
    ]
  },
  "outbounds": [{ "type": "direct", "tag": "direct", "domain_resolver": "dot" }],
  "route": { "default_domain_resolver": { "server": "doh" } }
}`

func TestRoundTripUnchanged(t *testing.T) {
	raw := []byte(sample)
	gjson.GetBytes(raw, "dns.servers").ForEach(func(k, v gjson.Result) bool {
		if !slices.Contains(Editable, v.Get("type").String()) {
			return true
		}
		s, _ := Read(v)
		if err := Normalize(&s); err != nil {
			t.Fatalf("%s: %v", s.Tag, err)
		}
		out, err := Write(raw, int(k.Int()), s, v)
		if err != nil {
			t.Fatalf("%s: %v", s.Tag, err)
		}
		if string(out) != string(raw) {
			t.Errorf("%s: unchanged save changed the config:\n%s", s.Tag, gjson.GetBytes(out, "dns.servers."+k.String()).Raw)
		}
		return true
	})
	set := ReadSettings(raw)
	if err := NormalizeSettings(raw, &set); err != nil {
		t.Fatal(err)
	}
	if out, err := WriteSettings(raw, set); err != nil || string(out) != string(raw) {
		t.Errorf("unchanged settings save changed the config (err %v)", err)
	}
}

func TestReadExtras(t *testing.T) {
	_, v := Find([]byte(sample), "sys")
	if _, extra := Read(v); !slices.Equal(extra, []string{"prefer_go"}) {
		t.Errorf("local extra = %v", extra)
	}
	_, v = Find([]byte(sample), "dot")
	if s, _ := Read(v); s.Resolver != "isp" {
		t.Errorf("resolver = %q", s.Resolver)
	}
}

func TestEdit(t *testing.T) {
	raw := []byte(sample)
	idx, v := Find(raw, "dot")
	s, _ := Read(v)
	s.Resolver = "doh" // object form keeps its strategy
	s.Insecure = true
	if err := Normalize(&s); err != nil {
		t.Fatal(err)
	}
	out, err := Write(raw, idx, s, v)
	if err != nil {
		t.Fatal(err)
	}
	got := gjson.GetBytes(out, "dns.servers.2")
	if got.Get("domain_resolver.server").String() != "doh" || got.Get("domain_resolver.strategy").String() != "ipv4_only" ||
		!got.Get("tls.enabled").Bool() || !got.Get("tls.insecure").Bool() {
		t.Errorf("after edit: %s", got.Raw)
	}

	n := Server{Tag: "q", Type: "quic", Server: "dns.adguard-dns.com", Resolver: "isp"}
	if err := Normalize(&n); err != nil {
		t.Fatal(err)
	}
	if out, err = Write(raw, -1, n, gjson.Result{}); err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(out, "dns.servers.6").Raw; got != `{"type":"quic","tag":"q","server":"dns.adguard-dns.com","domain_resolver":"isp"}` {
		t.Errorf("created %s", got)
	}

	set := Settings{Strategy: "ipv4_only", Final: "doh", DisableCache: true}
	if err := NormalizeSettings(raw, &set); err != nil {
		t.Fatal(err)
	}
	if out, err = WriteSettings(raw, set); err != nil {
		t.Fatal(err)
	}
	if d := gjson.GetBytes(out, "dns"); d.Get("strategy").String() != "ipv4_only" || d.Get("final").String() != "doh" || !d.Get("disable_cache").Bool() {
		t.Errorf("settings: %s", d.Raw)
	}
	if err := NormalizeSettings(raw, &Settings{Final: "nope"}); err == nil {
		t.Error("unknown final accepted")
	}
}

func TestReferences(t *testing.T) {
	raw := []byte(sample)
	cases := map[string][]string{
		"doh":  {"dns.final (default: first server)", "dns.rules.0", "route.default_domain_resolver"},
		"isp":  {"dns.servers.2.domain_resolver"},
		"dot":  {"dns.rules.2", "outbounds.0.domain_resolver"},
		"sys":  nil,
		"fake": nil,
	}
	for tag, want := range cases {
		got := References(raw, tag)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("References(%s) = %v, want %v", tag, got, want)
		}
	}
	if !ResolverLoop(raw, "isp", "dot") {
		t.Error("isp -> dot -> isp not detected")
	}
}

func TestRules(t *testing.T) {
	rules := Rules([]byte(sample))
	if len(rules) != 4 {
		t.Fatalf("%d rules", len(rules))
	}
	// Action parameters are not match conditions.
	if r := rules[3]; r.Action != "predefined" || len(r.Conditions) != 2 || r.Conditions[1].Key != "query_type" {
		t.Errorf("rule 3: %+v", r)
	}
	if r := rules[0]; r.Action != "route" || r.Server != "doh" || !r.LegacyStrategy ||
		len(r.Conditions) != 1 || r.Conditions[0].Key != "domain_suffix" || len(r.Conditions[0].Values) != 2 {
		t.Errorf("rule 0: %+v", r)
	}
	if r := rules[1]; r.Action != "reject" || r.Conditions[0].Key != "logical" || r.Conditions[0].Values[1] != "2" {
		t.Errorf("rule 1: %+v", r)
	}
	if r := rules[2]; r.Action != "route" || !r.Invert || r.LegacyStrategy {
		t.Errorf("rule 2: %+v", r)
	}
}
