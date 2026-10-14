// Package subscription builds client configs for one user: a Mihomo
// (Clash.Meta) profile, a sing-box client config, and share links.
package subscription

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/store"
)

// Settings say how clients reach the server. Stored as JSON.
type Settings struct {
	// PublicHost is the address clients connect to (IP or domain).
	PublicHost string `json:"public_host"`
	// Inbounds holds per-inbound overrides keyed by tag.
	Inbounds map[string]InboundSettings `json:"inbounds"`
}

// InboundSettings override how one inbound is exported.
type InboundSettings struct {
	Name     string `json:"name,omitempty"` // proxy name in clients (default: tag)
	Host     string `json:"host,omitempty"` // default: PublicHost
	Port     int    `json:"port,omitempty"` // default: listen_port
	Insecure bool   `json:"insecure"`       // skip TLS verification
	Hidden   bool   `json:"hidden"`         // leave out of subscriptions
}

// Clients returns one entry per inbound the user can use. Inbounds that
// cannot be exported are returned in skipped with the reason.
func Clients(raw []byte, u store.User, s Settings, flows ...map[string]string) (clients []protocol.Client, skipped map[string]string) {
	skipped = map[string]string{}
	for _, in := range protocol.Inbounds(raw) {
		if !u.HasInbound(in.Tag) {
			continue
		}
		o := s.Inbounds[in.Tag]
		if o.Hidden {
			continue
		}
		ep := protocol.Endpoint{Host: o.Host, Port: o.Port, Insecure: o.Insecure}
		if ep.Host == "" {
			ep.Host = s.PublicHost
		}
		if ep.Port == 0 {
			ep.Port = int(in.Raw.Get("listen_port").Int())
		}
		if ep.Host == "" || ep.Port == 0 {
			skipped[in.Tag] = "no public host or port configured"
			continue
		}
		name := o.Name
		if name == "" {
			name = in.Tag
		}
		c := protocol.Client{Name: name, Inbound: in, Endpoint: ep, User: u}
		if in.Type == "vless" && len(flows) > 0 {
			if flow, ok := flows[0][in.Tag]; ok {
				c.Flow = &flow
			}
		}
		clients = append(clients, c)
	}
	return clients, skipped
}

// mihomoTemplate is the Mihomo profile; {{proxies}} and {{proxy_names}}
// are filled in per user.
//
//go:embed mihomo.yaml
var mihomoTemplate string

// Mihomo renders a complete Mihomo profile for the user's proxies.
// Proxies are written as JSON flow mappings, which every YAML parser
// accepts and which need no YAML-specific escaping.
func Mihomo(clients []protocol.Client) ([]byte, error) {
	var proxies, names strings.Builder
	for _, c := range clients {
		p, _ := protocol.Get(c.Inbound.Type)
		m, err := p.Mihomo(c)
		if err != nil {
			continue
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&proxies, "  - %s\n", b)
		n, _ := json.Marshal(c.Name)
		names.WriteString(string(n) + ", ")
	}
	if proxies.Len() == 0 {
		proxies.WriteString("  []\n")
	}
	out := strings.ReplaceAll(mihomoTemplate, "{{proxies}}", strings.TrimRight(proxies.String(), "\n"))
	out = strings.ReplaceAll(out, "{{proxy_names}}", names.String())
	return []byte(out), nil
}

// singBoxBase is the sing-box client config (1.12+ syntax) every user
// gets; SingBox fills its "proxy" selector with the user's proxies. It is
// built fresh on each call because SingBox modifies it.
func singBoxBase() map[string]any {
	return map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
				map[string]any{"type": "https", "tag": "local", "server": "223.5.5.5"},
			},
			"rules": []any{
				map[string]any{"rule_set": []string{"geosite-cn"}, "server": "local"},
			},
			"final":    "remote",
			"strategy": "ipv4_only",
		},
		"inbounds": []any{
			map[string]any{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30"}, "auto_route": true, "strict_route": true},
			map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		},
		"outbounds": []any{
			map[string]any{"type": "selector", "tag": "proxy", "outbounds": []any{"direct"}},
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": map[string]any{
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{"protocol": "dns", "action": "hijack-dns"},
				map[string]any{"ip_is_private": true, "outbound": "direct"},
				map[string]any{"rule_set": []string{"geosite-cn", "geoip-cn"}, "outbound": "direct"},
			},
			"rule_set": []any{
				remoteRuleSet("geosite-cn", "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs"),
				remoteRuleSet("geoip-cn", "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"),
			},
			"final":                   "proxy",
			"auto_detect_interface":   true,
			"default_domain_resolver": "local",
		},
	}
}

// SingBox renders a sing-box client config for the user's proxies: they
// are inserted after the "proxy" selector, which lists them first, followed
// by whatever the base config's selector already listed.
func SingBox(clients []protocol.Client) ([]byte, error) {
	cfg := singBoxBase()
	var proxies []any
	var tags []string
	for _, c := range clients {
		p, _ := protocol.Get(c.Inbound.Type)
		o, err := p.SingBox(c)
		if err != nil {
			continue
		}
		proxies = append(proxies, o)
		tags = append(tags, c.Name)
	}
	var outs []any
	for _, o := range cfg["outbounds"].([]any) {
		m, ok := o.(map[string]any)
		if ok && m["tag"] == "proxy" && m["type"] == "selector" {
			list := append([]any{}, toAny(tags)...)
			if extra, ok := m["outbounds"].([]any); ok {
				list = append(list, extra...)
			}
			m["outbounds"] = list
			if len(tags) > 0 {
				m["default"] = tags[0]
			}
			outs = append(outs, m)
			outs = append(outs, proxies...)
			continue
		}
		outs = append(outs, o)
	}
	cfg["outbounds"] = outs
	return encodeIndent(cfg)
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func encodeIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func remoteRuleSet(tag, url string) map[string]any {
	return map[string]any{"type": "remote", "tag": tag, "format": "binary", "url": url, "download_detour": "proxy"}
}

// Links returns one share link per line.
func Links(clients []protocol.Client) []byte {
	var b strings.Builder
	for _, c := range clients {
		p, _ := protocol.Get(c.Inbound.Type)
		if l, err := p.Link(c); err == nil {
			b.WriteString(l + "\n")
		}
	}
	return []byte(b.String())
}

// Base64Links is Links in the base64 form v2rayN-style clients expect.
func Base64Links(clients []protocol.Client) []byte {
	return []byte(base64.StdEncoding.EncodeToString(Links(clients)))
}
