// Package singbox reads and inspects sing-box configuration files.
package singbox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/tidwall/gjson"
)

// MaskedValue stands in for a secret the API does not send back; sending it
// back unchanged keeps the stored value.
const MaskedValue = "••••••••"

// File is a snapshot of the config file on disk.
type File struct {
	Path    string
	Raw     []byte
	SHA256  string
	ModTime time.Time
}

// ReadFile reads the config and checks it is well-formed JSON.
func ReadFile(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	if !gjson.ValidBytes(raw) {
		return File{}, fmt.Errorf("%s: not valid JSON", path)
	}
	st, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	sum := sha256.Sum256(raw)
	return File{Path: path, Raw: raw, SHA256: hex.EncodeToString(sum[:]), ModTime: st.ModTime()}, nil
}

// Inbound summarises one inbound for display.
type Inbound struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
	Users      int    `json:"users"`
	TLS        bool   `json:"tls"`
	Reality    bool   `json:"reality"`
	ServerName string `json:"server_name,omitempty"`
}

// Outbound summarises one outbound for display.
type Outbound struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
}

// Summary is a structural overview of a config, with no secrets.
type Summary struct {
	Inbounds   []Inbound  `json:"inbounds"`
	Outbounds  []Outbound `json:"outbounds"`
	RouteRules int        `json:"route_rules"`
	DNSServers int        `json:"dns_servers"`
	LogLevel   string     `json:"log_level"`
	LogOutput  string     `json:"log_output"`
	ClashAPI   string     `json:"clash_api,omitempty"`
	V2RayAPI   string     `json:"v2ray_api,omitempty"`
}

// Summarize extracts a Summary from raw config JSON.
func Summarize(raw []byte) Summary {
	root := gjson.ParseBytes(raw)
	s := Summary{
		Inbounds:   []Inbound{},
		Outbounds:  []Outbound{},
		RouteRules: int(root.Get("route.rules.#").Int()),
		DNSServers: int(root.Get("dns.servers.#").Int()),
		LogLevel:   root.Get("log.level").String(),
		LogOutput:  root.Get("log.output").String(),
		ClashAPI:   root.Get("experimental.clash_api.external_controller").String(),
		V2RayAPI:   root.Get("experimental.v2ray_api.listen").String(),
	}
	root.Get("inbounds").ForEach(func(_, ib gjson.Result) bool {
		s.Inbounds = append(s.Inbounds, Inbound{
			Tag:        ib.Get("tag").String(),
			Type:       ib.Get("type").String(),
			Listen:     ib.Get("listen").String(),
			ListenPort: int(ib.Get("listen_port").Int()),
			Users:      int(ib.Get("users.#").Int()),
			TLS:        ib.Get("tls.enabled").Bool(),
			Reality:    ib.Get("tls.reality.enabled").Bool(),
			ServerName: ib.Get("tls.server_name").String(),
		})
		return true
	})
	root.Get("outbounds").ForEach(func(_, ob gjson.Result) bool {
		s.Outbounds = append(s.Outbounds, Outbound{Tag: ob.Get("tag").String(), Type: ob.Get("type").String()})
		return true
	})
	return s
}

// ClashAPISecret returns experimental.clash_api.secret, if configured.
func ClashAPISecret(raw []byte) string {
	return gjson.GetBytes(raw, "experimental.clash_api.secret").String()
}
