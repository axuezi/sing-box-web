package singbox

import (
	"fmt"

	"github.com/tidwall/gjson"
)

// Lint finds reference mistakes that `sing-box check` accepts but that make
// sing-box fail at startup, such as a route pointing at an outbound tag that
// does not exist. It returns one message per problem.
func Lint(raw []byte) []string {
	root := gjson.ParseBytes(raw)
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	tags := func(list, kind string) map[string]bool {
		seen := map[string]bool{}
		root.Get(list).ForEach(func(k, v gjson.Result) bool {
			tag := v.Get("tag").String()
			if tag == "" {
				return true
			}
			if seen[tag] {
				add("%s tag %q is used more than once", kind, tag)
			}
			seen[tag] = true
			return true
		})
		return seen
	}
	inbounds := tags("inbounds", "inbound")
	outbounds := tags("outbounds", "outbound")
	for tag := range tags("endpoints", "endpoint") {
		outbounds[tag] = true // endpoints can be used as outbounds
	}
	dnsServers := tags("dns.servers", "dns server")
	ruleSets := tags("route.rule_set", "rule set")

	needOut := func(path, tag string) {
		if tag != "" && !outbounds[tag] {
			add("%s refers to unknown outbound %q", path, tag)
		}
	}
	needDNS := func(path, tag string) {
		if tag != "" && !dnsServers[tag] {
			add("%s refers to unknown DNS server %q", path, tag)
		}
	}
	needIn := func(path string, v gjson.Result) {
		forEachString(v, func(tag string) {
			if !inbounds[tag] {
				add("%s refers to unknown inbound %q", path, tag)
			}
		})
	}
	needRuleSet := func(path string, v gjson.Result) {
		forEachString(v, func(tag string) {
			if !ruleSets[tag] {
				add("%s refers to unknown rule set %q", path, tag)
			}
		})
	}

	needOut("route.final", root.Get("route.final").String())
	root.Get("route.rules").ForEach(func(k, r gjson.Result) bool {
		p := "route.rules[" + k.String() + "]"
		needOut(p+".outbound", r.Get("outbound").String())
		needIn(p+".inbound", r.Get("inbound"))
		needRuleSet(p+".rule_set", r.Get("rule_set"))
		return true
	})
	needDNS("route.default_domain_resolver", resolverServer(root.Get("route.default_domain_resolver")))

	root.Get("outbounds").ForEach(func(k, o gjson.Result) bool {
		p := "outbounds[" + k.String() + "]"
		forEachString(o.Get("outbounds"), func(tag string) { needOut(p+".outbounds", tag) })
		needOut(p+".default", o.Get("default").String())
		needOut(p+".detour", o.Get("detour").String())
		needDNS(p+".domain_resolver", resolverServer(o.Get("domain_resolver")))
		return true
	})

	needDNS("dns.final", root.Get("dns.final").String())
	root.Get("dns.rules").ForEach(func(k, r gjson.Result) bool {
		p := "dns.rules[" + k.String() + "]"
		needDNS(p+".server", r.Get("server").String())
		needRuleSet(p+".rule_set", r.Get("rule_set"))
		return true
	})
	root.Get("dns.servers").ForEach(func(k, s gjson.Result) bool {
		p := "dns.servers[" + k.String() + "]"
		needOut(p+".detour", s.Get("detour").String())
		needDNS(p+".domain_resolver", resolverServer(s.Get("domain_resolver")))
		return true
	})
	return problems
}

// resolverServer reads a domain_resolver, which is either a server tag or
// an object with a "server" field.
func resolverServer(v gjson.Result) string {
	if v.IsObject() {
		return v.Get("server").String()
	}
	return v.String()
}

func forEachString(v gjson.Result, fn func(string)) {
	if v.IsArray() {
		v.ForEach(func(_, s gjson.Result) bool {
			fn(s.String())
			return true
		})
	} else if v.Type == gjson.String {
		fn(v.String())
	}
}
