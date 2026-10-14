// Package dnsconf maps sing-box's DNS section to editable forms: the global
// settings and the common server types (the typed format of sing-box 1.12
// and later). DNS rules are only summarized, not edited. As elsewhere,
// writing a form only touches the fields it models.
package dnsconf

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/axuezi/sing-box-web/internal/outbounds"
)

// Editable lists the DNS server types this package edits.
var Editable = []string{"local", "udp", "tcp", "tls", "https", "quic", "h3"}

// DefaultPort is the port sing-box uses when server_port is left out.
var DefaultPort = map[string]int{"udp": 53, "tcp": 53, "tls": 853, "quic": 853, "https": 443, "h3": 443}

// Settings are the global DNS options the form edits.
type Settings struct {
	Strategy     string `json:"strategy"`      // "" or one of outbounds.Strategies
	Final        string `json:"final"`         // default server tag; "" = the first server
	DisableCache bool   `json:"disable_cache"` // query every time instead of caching answers
}

// ReadSettings returns the global DNS settings.
func ReadSettings(raw []byte) Settings {
	d := gjson.GetBytes(raw, "dns")
	return Settings{
		Strategy:     d.Get("strategy").String(),
		Final:        d.Get("final").String(),
		DisableCache: d.Get("disable_cache").Bool(),
	}
}

// NormalizeSettings checks s against the servers in raw.
func NormalizeSettings(raw []byte, s *Settings) error {
	s.Strategy, s.Final = strings.TrimSpace(s.Strategy), strings.TrimSpace(s.Final)
	if s.Strategy != "" && !slices.Contains(outbounds.Strategies, s.Strategy) {
		return errors.New("strategy must be one of " + strings.Join(outbounds.Strategies, ", "))
	}
	if s.Final != "" && !slices.Contains(Tags(raw), s.Final) {
		return errors.New("final must be the tag of a DNS server")
	}
	return nil
}

// WriteSettings applies s to the dns section.
func WriteSettings(raw []byte, s Settings) ([]byte, error) {
	var err error
	setOrDel := func(path string, v any, keep bool) {
		if err != nil {
			return
		}
		if keep {
			raw, err = sjson.SetBytes(raw, path, v)
		} else if gjson.GetBytes(raw, path).Exists() {
			raw, err = sjson.DeleteBytes(raw, path)
		}
	}
	setOrDel("dns.strategy", s.Strategy, s.Strategy != "")
	setOrDel("dns.final", s.Final, s.Final != "")
	setOrDel("dns.disable_cache", true, s.DisableCache)
	return raw, err
}

// Server is the editable view of one DNS server.
type Server struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"`
	Server     string `json:"server"`      // IP or host name; unused for local
	ServerPort int    `json:"server_port"` // 0 = the type's default
	Path       string `json:"path"`        // https, h3; "" = /dns-query
	ServerName string `json:"server_name"` // TLS name when it differs from server
	Insecure   bool   `json:"insecure"`    // skip certificate verification
	Detour     string `json:"detour"`      // outbound to query through; "" = direct
	Resolver   string `json:"resolver"`    // DNS server resolving a host-name server
}

func hasTLS(t string) bool  { return t == "tls" || t == "https" || t == "quic" || t == "h3" }
func hasPath(t string) bool { return t == "https" || t == "h3" }

var modelled = []string{"type", "tag", "server", "server_port", "path", "tls", "detour", "domain_resolver"}
var tlsModelled = []string{"enabled", "server_name", "insecure"}

// Read returns the form for a typed DNS server object and the keys the form
// does not model.
func Read(v gjson.Result) (Server, []string) {
	s := Server{
		Tag:        v.Get("tag").String(),
		Type:       v.Get("type").String(),
		Server:     v.Get("server").String(),
		ServerPort: int(v.Get("server_port").Int()),
		Path:       v.Get("path").String(),
		ServerName: v.Get("tls.server_name").String(),
		Insecure:   v.Get("tls.insecure").Bool(),
		Detour:     v.Get("detour").String(),
	}
	if r := v.Get("domain_resolver"); r.Type == gjson.String {
		s.Resolver = r.String()
	} else {
		s.Resolver = r.Get("server").String()
	}
	var extra []string
	v.ForEach(func(k, _ gjson.Result) bool {
		key := k.String()
		if !slices.Contains(modelled, key) || (s.Type == "local" && key != "type" && key != "tag") ||
			(key == "tls" && !hasTLS(s.Type)) || (key == "path" && !hasPath(s.Type)) {
			extra = append(extra, key)
		}
		return true
	})
	if hasTLS(s.Type) {
		v.Get("tls").ForEach(func(k, _ gjson.Result) bool {
			if !slices.Contains(tlsModelled, k.String()) {
				extra = append(extra, "tls."+k.String())
			}
			return true
		})
	}
	return s, extra
}

// Normalize trims and checks a submitted server on its own.
func Normalize(s *Server) error {
	for _, p := range []*string{&s.Tag, &s.Server, &s.Path, &s.ServerName, &s.Detour, &s.Resolver} {
		*p = strings.TrimSpace(*p)
	}
	if !slices.Contains(Editable, s.Type) {
		return fmt.Errorf("type must be one of %s", strings.Join(Editable, ", "))
	}
	if !outbounds.ValidTag(s.Tag) {
		return errors.New("tag must be 1-64 letters, digits, . _ -, starting with a letter or digit")
	}
	if s.Type == "local" {
		*s = Server{Tag: s.Tag, Type: s.Type}
		return nil
	}
	if !outbounds.ValidHost(s.Server) {
		return errors.New("server must be an IP address or a host name")
	}
	if s.ServerPort < 0 || s.ServerPort > 65535 {
		return errors.New("server_port must be 0-65535")
	}
	if s.Resolver == s.Tag {
		return errors.New("a DNS server cannot resolve its own name")
	}
	if !hasPath(s.Type) {
		s.Path = ""
	} else if s.Path != "" && !strings.HasPrefix(s.Path, "/") {
		return errors.New("path must start with /")
	}
	if !hasTLS(s.Type) {
		s.ServerName, s.Insecure = "", false
	} else if s.ServerName != "" && !outbounds.ValidHost(s.ServerName) {
		return errors.New("invalid server name")
	}
	return nil
}

// Write applies server s onto the DNS server at index in raw (index = -1
// appends a new one). orig is the server before the edit.
func Write(raw []byte, index int, s Server, orig gjson.Result) ([]byte, error) {
	obj := []byte(`{}`)
	if index >= 0 {
		obj = []byte(orig.Raw)
	}
	var err error
	set := func(path string, v any) {
		if err == nil {
			obj, err = sjson.SetBytes(obj, path, v)
		}
	}
	setOrDel := func(path string, v any, keep bool) {
		if keep {
			set(path, v)
		} else if err == nil && gjson.GetBytes(obj, path).Exists() {
			obj, err = sjson.DeleteBytes(obj, path)
		}
	}
	if index < 0 {
		set("type", s.Type)
		set("tag", s.Tag)
	}
	if s.Type != "local" {
		set("server", s.Server)
		setOrDel("server_port", s.ServerPort, s.ServerPort != 0)
		if hasPath(s.Type) {
			setOrDel("path", s.Path, s.Path != "")
		}
		if hasTLS(s.Type) {
			if s.ServerName != "" || s.Insecure {
				if !gjson.GetBytes(obj, "tls").Exists() {
					set("tls.enabled", true)
				}
			}
			setOrDel("tls.server_name", s.ServerName, s.ServerName != "")
			setOrDel("tls.insecure", true, s.Insecure)
		}
		setOrDel("detour", s.Detour, s.Detour != "")
		if err == nil {
			// Keep a strategy set on an object-form resolver; the form does
			// not edit it.
			obj, err = outbounds.WriteResolver(obj, s.Resolver, gjson.GetBytes(obj, "domain_resolver.strategy").String())
		}
	}
	if err != nil {
		return nil, err
	}
	out := raw
	if index < 0 {
		if !gjson.GetBytes(raw, "dns.servers").Exists() {
			if out, err = sjson.SetRawBytes(out, "dns.servers", []byte("[]")); err != nil {
				return nil, err
			}
		}
		return sjson.SetRawBytes(out, "dns.servers.-1", obj)
	}
	return sjson.SetRawBytes(out, "dns.servers."+strconv.Itoa(index), obj)
}

// Find returns the index and object of the DNS server with tag, or -1.
func Find(raw []byte, tag string) (int, gjson.Result) {
	idx, res := -1, gjson.Result{}
	gjson.GetBytes(raw, "dns.servers").ForEach(func(k, v gjson.Result) bool {
		if v.Get("tag").String() == tag {
			idx, res = int(k.Int()), v
			return false
		}
		return true
	})
	return idx, res
}

// Tags lists the tags of all DNS servers.
func Tags(raw []byte) []string {
	var tags []string
	gjson.GetBytes(raw, "dns.servers").ForEach(func(_, v gjson.Result) bool {
		if t := v.Get("tag").String(); t != "" {
			tags = append(tags, t)
		}
		return true
	})
	return tags
}

// ResolverLoop reports whether letting tag resolve its name through
// resolver would make a chain that comes back to tag.
func ResolverLoop(raw []byte, tag, resolver string) bool {
	next := map[string]string{}
	gjson.GetBytes(raw, "dns.servers").ForEach(func(_, v gjson.Result) bool {
		r := v.Get("domain_resolver")
		if !r.IsObject() {
			next[v.Get("tag").String()] = r.String()
		} else {
			next[v.Get("tag").String()] = r.Get("server").String()
		}
		return true
	})
	next[tag] = resolver
	seen := map[string]bool{}
	for cur := tag; cur != ""; cur = next[cur] {
		if seen[cur] {
			return true
		}
		seen[cur] = true
	}
	return false
}

// References lists where the config points at DNS server tag. With no
// dns.final the first server is the default, which counts as a reference.
func References(raw []byte, tag string) []string {
	var refs []string
	resolverHit := func(v gjson.Result) bool {
		if v.IsObject() {
			return v.Get("server").String() == tag
		}
		return v.Exists() && v.String() == tag
	}
	final := gjson.GetBytes(raw, "dns.final")
	if final.String() == tag {
		refs = append(refs, "dns.final")
	} else if !final.Exists() && gjson.GetBytes(raw, "dns.servers.0.tag").String() == tag {
		refs = append(refs, "dns.final (default: first server)")
	}
	var walk func(path string, rules gjson.Result)
	walk = func(path string, rules gjson.Result) {
		rules.ForEach(func(k, r gjson.Result) bool {
			p := path + "." + k.String()
			if r.Get("server").String() == tag {
				refs = append(refs, p)
			}
			walk(p+".rules", r.Get("rules"))
			return true
		})
	}
	walk("dns.rules", gjson.GetBytes(raw, "dns.rules"))
	for _, section := range []string{"dns.servers", "outbounds", "endpoints"} {
		gjson.GetBytes(raw, section).ForEach(func(k, v gjson.Result) bool {
			if resolverHit(v.Get("domain_resolver")) {
				refs = append(refs, section+"."+k.String()+".domain_resolver")
			}
			return true
		})
	}
	if resolverHit(gjson.GetBytes(raw, "route.default_domain_resolver")) {
		refs = append(refs, "route.default_domain_resolver")
	}
	return refs
}

// Remove deletes the DNS server at index.
func Remove(raw []byte, index int) ([]byte, error) {
	return sjson.DeleteBytes(raw, "dns.servers."+strconv.Itoa(index))
}

// Condition is one match field of a DNS rule.
type Condition struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// Rule summarizes a DNS rule for display.
type Rule struct {
	Action     string      `json:"action"` // route, reject, predefined, ...
	Server     string      `json:"server,omitempty"`
	Strategy   string      `json:"strategy,omitempty"`
	Invert     bool        `json:"invert,omitempty"`
	Conditions []Condition `json:"conditions"`
	// LegacyStrategy marks a route action with "strategy", deprecated in
	// sing-box 1.14 and removed in 1.16.
	LegacyStrategy bool `json:"legacy_strategy,omitempty"`
}

// options are rule keys that say what to do rather than what to match:
// the action and its parameters (route / route-options, reject,
// predefined), and the parts of a logical rule.
var options = []string{
	"action", "server", "strategy", "disable_cache", "rewrite_ttl", "client_subnet",
	"method", "no_drop",
	"rcode", "answer", "ns", "extra",
	"invert", "type", "mode", "rules",
}

// Rules summarizes dns.rules.
func Rules(raw []byte) []Rule {
	rules := []Rule{}
	gjson.GetBytes(raw, "dns.rules").ForEach(func(_, r gjson.Result) bool {
		rule := Rule{
			Action:     r.Get("action").String(),
			Server:     r.Get("server").String(),
			Strategy:   r.Get("strategy").String(),
			Invert:     r.Get("invert").Bool(),
			Conditions: []Condition{},
		}
		if rule.Action == "" {
			rule.Action = "route"
		}
		rule.LegacyStrategy = rule.Action == "route" && rule.Strategy != ""
		if r.Get("type").String() == "logical" {
			rule.Conditions = append(rule.Conditions, Condition{Key: "logical",
				Values: []string{r.Get("mode").String(), strconv.Itoa(int(r.Get("rules.#").Int()))}})
		}
		r.ForEach(func(k, v gjson.Result) bool {
			if slices.Contains(options, k.String()) {
				return true
			}
			c := Condition{Key: k.String(), Values: []string{}}
			if v.IsArray() {
				v.ForEach(func(_, x gjson.Result) bool {
					c.Values = append(c.Values, x.String())
					return true
				})
			} else {
				c.Values = append(c.Values, v.String())
			}
			rule.Conditions = append(rule.Conditions, c)
			return true
		})
		rules = append(rules, rule)
		return true
	})
	return rules
}
