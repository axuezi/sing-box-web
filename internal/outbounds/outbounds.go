// Package outbounds maps the common sing-box outbound types to an editable
// form and back. Like package inbounds, writing a form only touches the
// fields the form models; everything else in the outbound object (multiplex,
// transport, tcp_fast_open, ...) is kept as it is.
package outbounds

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/axuezi/sing-box-web/internal/singbox"
)

// Editable lists the outbound types this package edits.
var Editable = []string{"direct", "socks", "http", "shadowsocks", "vless", "trojan", "hysteria2"}

// Security modes.
const (
	SecNone    = "none"
	SecTLS     = "tls"
	SecReality = "reality"

	FlowVision = "xtls-rprx-vision"
)

// Methods are the Shadowsocks ciphers offered by the form.
var Methods = []string{
	"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
	"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "none",
}

// Strategies are the domain resolution strategies sing-box accepts.
var Strategies = []string{"prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"}

// Form is the editable view of one outbound.
type Form struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`

	// Remote server: every type but direct.
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`

	Username string `json:"username"` // socks, http
	Password string `json:"password"` // socks, http, shadowsocks, trojan, hysteria2; masked when read
	Method   string `json:"method"`   // shadowsocks
	UUID     string `json:"uuid"`     // vless; masked when read
	Flow     string `json:"flow"`     // vless: "" or FlowVision

	TLS TLS `json:"tls"` // http, vless, trojan, hysteria2

	// Hysteria2.
	UpMbps       int    `json:"up_mbps"`
	DownMbps     int    `json:"down_mbps"`
	ObfsPassword string `json:"obfs_password"` // salamander; "" = off; masked when read

	// Dial fields, for every type.
	Detour           string `json:"detour"`             // outbound to connect through; "" = direct
	BindInterface    string `json:"bind_interface"`     // network interface to send from
	Inet4Bind        string `json:"inet4_bind_address"` // source IPv4
	Inet6Bind        string `json:"inet6_bind_address"` // source IPv6
	Resolver         string `json:"resolver"`           // DNS server tag resolving domain targets; "" = default
	ResolverStrategy string `json:"resolver_strategy"`  // "" or one of Strategies
}

// TLS holds the client-side security settings.
type TLS struct {
	Mode       string   `json:"mode"` // none | tls | reality
	ServerName string   `json:"server_name"`
	Insecure   bool     `json:"insecure"`
	ALPN       []string `json:"alpn"`
	UTLS       string   `json:"utls"` // uTLS fingerprint, e.g. chrome; "" = off

	PublicKey string `json:"public_key"` // reality
	ShortID   string `json:"short_id"`   // reality
}

var dialKeys = []string{"type", "tag", "detour", "bind_interface", "inet4_bind_address", "inet6_bind_address", "domain_resolver"}

// modelled are the top-level keys the form owns, per type.
var modelled = map[string][]string{
	"direct":      {},
	"socks":       {"server", "server_port", "username", "password"},
	"http":        {"server", "server_port", "username", "password", "tls"},
	"shadowsocks": {"server", "server_port", "method", "password"},
	"vless":       {"server", "server_port", "uuid", "flow", "tls"},
	"trojan":      {"server", "server_port", "password", "tls"},
	"hysteria2":   {"server", "server_port", "password", "tls", "up_mbps", "down_mbps", "obfs"},
}

var tlsModelled = []string{"enabled", "server_name", "insecure", "alpn", "utls", "reality"}

// hasTLS reports whether type t has a TLS block in the form.
func hasTLS(t string) bool { return slices.Contains(modelled[t], "tls") }

// secret masks a non-empty credential.
func secret(v string) string {
	if v == "" {
		return ""
	}
	return singbox.MaskedValue
}

// Read returns the form for an outbound object, with credentials masked, and
// the keys the form does not model (they are kept as they are on save).
func Read(o gjson.Result) (Form, []string) {
	typ := o.Get("type").String()
	f := Form{
		Tag:           o.Get("tag").String(),
		Type:          typ,
		Server:        o.Get("server").String(),
		ServerPort:    int(o.Get("server_port").Int()),
		Username:      o.Get("username").String(),
		Password:      secret(o.Get("password").String()),
		Method:        o.Get("method").String(),
		UUID:          secret(o.Get("uuid").String()),
		Flow:          o.Get("flow").String(),
		UpMbps:        int(o.Get("up_mbps").Int()),
		DownMbps:      int(o.Get("down_mbps").Int()),
		Detour:        o.Get("detour").String(),
		BindInterface: o.Get("bind_interface").String(),
		Inet4Bind:     o.Get("inet4_bind_address").String(),
		Inet6Bind:     o.Get("inet6_bind_address").String(),
	}
	if r := o.Get("domain_resolver"); r.Type == gjson.String {
		f.Resolver = r.String()
	} else {
		f.Resolver = r.Get("server").String()
		f.ResolverStrategy = r.Get("strategy").String()
	}
	if o.Get("obfs.type").String() == "salamander" {
		f.ObfsPassword = secret(o.Get("obfs.password").String())
	}
	t := o.Get("tls")
	switch {
	case !t.Get("enabled").Bool():
		f.TLS.Mode = SecNone
	case t.Get("reality.enabled").Bool():
		f.TLS.Mode = SecReality
	default:
		f.TLS.Mode = SecTLS
	}
	f.TLS.ServerName = t.Get("server_name").String()
	f.TLS.Insecure = t.Get("insecure").Bool()
	t.Get("alpn").ForEach(func(_, v gjson.Result) bool {
		f.TLS.ALPN = append(f.TLS.ALPN, v.String())
		return true
	})
	if t.Get("alpn").Type == gjson.String {
		f.TLS.ALPN = []string{t.Get("alpn").String()}
	}
	if t.Get("utls.enabled").Bool() {
		f.TLS.UTLS = t.Get("utls.fingerprint").String()
		if f.TLS.UTLS == "" {
			f.TLS.UTLS = "chrome" // sing-box's default fingerprint
		}
	}
	f.TLS.PublicKey = t.Get("reality.public_key").String()
	f.TLS.ShortID = t.Get("reality.short_id").String()

	var extra []string
	o.ForEach(func(k, v gjson.Result) bool {
		key := k.String()
		switch {
		case key == "obfs" && o.Get("obfs.type").String() != "salamander",
			key == "tls" && !hasTLS(typ):
			extra = append(extra, key) // a shape the form cannot show: kept as is
		case !slices.Contains(dialKeys, key) && !slices.Contains(modelled[typ], key):
			extra = append(extra, key)
		}
		return true
	})
	if hasTLS(typ) {
		t.ForEach(func(k, _ gjson.Result) bool {
			if !slices.Contains(tlsModelled, k.String()) {
				extra = append(extra, "tls."+k.String())
			}
			return true
		})
	}
	return f, extra
}

var (
	validTag     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	validHost    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	validIface   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,14}$`)
	validUUID    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	validShortID = regexp.MustCompile(`^[0-9a-f]{0,16}$`)
	validPubKey  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// ValidTag reports whether s is an acceptable tag.
func ValidTag(s string) bool { return validTag.MatchString(s) }

// ValidHost accepts an IP address or a host name.
func ValidHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	return validHost.MatchString(s) && len(s) <= 253
}

// Normalize trims and defaults a submitted form and checks it on its own.
// Masked credentials are accepted here; Write restores them.
func Normalize(f *Form) error {
	trim := func(p *string) { *p = strings.TrimSpace(*p) }
	for _, p := range []*string{&f.Tag, &f.Server, &f.Username, &f.Method, &f.UUID, &f.Detour, &f.BindInterface,
		&f.Inet4Bind, &f.Inet6Bind, &f.Resolver, &f.ResolverStrategy, &f.TLS.ServerName, &f.TLS.UTLS,
		&f.TLS.PublicKey, &f.TLS.ShortID} {
		trim(p)
	}
	f.TLS.ShortID = strings.ToLower(f.TLS.ShortID)
	if !slices.Contains(Editable, f.Type) {
		return fmt.Errorf("type must be one of %s", strings.Join(Editable, ", "))
	}
	if !validTag.MatchString(f.Tag) {
		return errors.New("tag must be 1-64 letters, digits, . _ -, starting with a letter or digit")
	}
	if f.Detour == f.Tag {
		return errors.New("an outbound cannot connect through itself")
	}
	if f.BindInterface != "" && !validIface.MatchString(f.BindInterface) {
		return errors.New("invalid interface name")
	}
	if f.Inet4Bind != "" {
		if a, err := netip.ParseAddr(f.Inet4Bind); err != nil || !a.Is4() {
			return errors.New("inet4_bind_address must be an IPv4 address")
		}
	}
	if f.Inet6Bind != "" {
		if a, err := netip.ParseAddr(f.Inet6Bind); err != nil || !a.Is6() || a.Is4In6() {
			return errors.New("inet6_bind_address must be an IPv6 address")
		}
	}
	if f.ResolverStrategy != "" && !slices.Contains(Strategies, f.ResolverStrategy) {
		return errors.New("resolver strategy must be one of " + strings.Join(Strategies, ", "))
	}
	if f.ResolverStrategy != "" && f.Resolver == "" {
		return errors.New("choose the DNS server the strategy applies to")
	}

	if f.Type == "direct" {
		return nil
	}
	if !ValidHost(f.Server) {
		return errors.New("server must be an IP address or a host name")
	}
	if f.ServerPort < 1 || f.ServerPort > 65535 {
		return errors.New("server_port must be 1-65535")
	}
	switch f.Type {
	case "socks":
		f.TLS.Mode = SecNone
		if f.Password != "" && f.Username == "" {
			return errors.New("a password needs a username")
		}
	case "http":
		if f.Password != "" && f.Username == "" {
			return errors.New("a password needs a username")
		}
		if f.TLS.Mode != SecNone && f.TLS.Mode != SecTLS {
			return errors.New("tls mode must be none or tls")
		}
	case "shadowsocks":
		f.TLS.Mode = SecNone
		if !slices.Contains(Methods, f.Method) {
			return errors.New("method must be one of " + strings.Join(Methods, ", "))
		}
		if f.Password == "" && f.Method != "none" {
			return errors.New("password is required")
		}
	case "vless":
		if f.UUID != singbox.MaskedValue && !validUUID.MatchString(f.UUID) {
			return errors.New("uuid must look like 8-4-4-4-12 hex digits")
		}
		if f.Flow != "" && f.Flow != FlowVision {
			return errors.New("flow must be empty or " + FlowVision)
		}
		if f.Flow != "" && f.TLS.Mode == SecNone {
			return errors.New("XTLS Vision needs TLS or Reality")
		}
	case "trojan", "hysteria2":
		f.TLS.Mode = SecTLS // both always run over TLS
		if f.Password == "" {
			return errors.New("password is required")
		}
	}
	if f.Type == "hysteria2" && (f.UpMbps < 0 || f.DownMbps < 0) {
		return errors.New("bandwidth must not be negative")
	}

	alpn := []string{}
	for _, a := range f.TLS.ALPN {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(alpn, a) {
			alpn = append(alpn, a)
		}
	}
	f.TLS.ALPN = alpn
	if f.TLS.ServerName != "" && !validHost.MatchString(f.TLS.ServerName) {
		return errors.New("invalid server name")
	}
	switch f.TLS.Mode {
	case SecNone, SecTLS:
	case SecReality:
		if f.Type != "vless" {
			return errors.New("reality is only for vless")
		}
		if f.TLS.ServerName == "" {
			return errors.New("reality needs the server name the server impersonates")
		}
		if !validPubKey.MatchString(f.TLS.PublicKey) {
			return errors.New("reality public key must be 43 base64url characters")
		}
		if !validShortID.MatchString(f.TLS.ShortID) || len(f.TLS.ShortID)%2 != 0 {
			return errors.New("short id must be 0-16 hex digits, an even count")
		}
		if f.TLS.UTLS == "" {
			f.TLS.UTLS = "chrome" // sing-box refuses a reality client without uTLS
		}
	default:
		return errors.New("tls mode must be none, tls or reality")
	}
	return nil
}

// Write applies form f onto the outbound at index in raw (index = -1
// appends a new outbound). orig is the outbound before the edit, used to
// restore masked credentials; it is empty for a new outbound.
func Write(raw []byte, index int, f Form, orig gjson.Result) ([]byte, error) {
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
	del := func(path string) {
		if err == nil && gjson.GetBytes(obj, path).Exists() {
			obj, err = sjson.DeleteBytes(obj, path)
		}
	}
	setOrDel := func(path string, v any, keep bool) {
		if keep {
			set(path, v)
		} else {
			del(path)
		}
	}
	// keepSecret writes a credential, or keeps the stored one when the form
	// sent the mask back.
	keepSecret := func(path, v string) {
		switch {
		case v != singbox.MaskedValue:
			setOrDel(path, v, v != "")
		case orig.Get(path).String() == "":
			if err == nil {
				err = fmt.Errorf("enter the %s", path[strings.LastIndexByte(path, '.')+1:])
			}
		}
	}

	if index < 0 {
		set("type", f.Type)
		set("tag", f.Tag)
	}
	if f.Type != "direct" {
		set("server", f.Server)
		set("server_port", f.ServerPort)
	}
	switch f.Type {
	case "socks", "http":
		setOrDel("username", f.Username, f.Username != "")
		keepSecret("password", f.Password)
	case "shadowsocks":
		set("method", f.Method)
		keepSecret("password", f.Password)
	case "vless":
		keepSecret("uuid", f.UUID)
		setOrDel("flow", f.Flow, f.Flow != "")
	case "trojan":
		keepSecret("password", f.Password)
	case "hysteria2":
		keepSecret("password", f.Password)
		setOrDel("up_mbps", f.UpMbps, f.UpMbps > 0)
		setOrDel("down_mbps", f.DownMbps, f.DownMbps > 0)
		if f.ObfsPassword == "" {
			if gjson.GetBytes(obj, "obfs.type").String() == "salamander" {
				del("obfs")
			}
		} else {
			set("obfs.type", "salamander")
			keepSecret("obfs.password", f.ObfsPassword)
		}
	}

	if hasTLS(f.Type) {
		if f.TLS.Mode == SecNone {
			del("tls")
		} else {
			set("tls.enabled", true)
			setOrDel("tls.server_name", f.TLS.ServerName, f.TLS.ServerName != "")
			setOrDel("tls.insecure", true, f.TLS.Insecure && f.TLS.Mode == SecTLS)
			// Leave the short forms sing-box also accepts alone when unchanged:
			// "alpn": "h2", and uTLS without a fingerprint (= chrome).
			if a := gjson.GetBytes(obj, "tls.alpn"); !(a.Type == gjson.String && slices.Equal(f.TLS.ALPN, []string{a.String()})) {
				setOrDel("tls.alpn", f.TLS.ALPN, len(f.TLS.ALPN) > 0)
			}
			if f.TLS.UTLS != "" {
				set("tls.utls.enabled", true)
				if fp := gjson.GetBytes(obj, "tls.utls.fingerprint"); fp.Exists() || f.TLS.UTLS != "chrome" {
					set("tls.utls.fingerprint", f.TLS.UTLS)
				}
			} else {
				del("tls.utls")
			}
			if f.TLS.Mode == SecReality {
				set("tls.reality.enabled", true)
				set("tls.reality.public_key", f.TLS.PublicKey)
				setOrDel("tls.reality.short_id", f.TLS.ShortID, f.TLS.ShortID != "")
			} else {
				del("tls.reality")
			}
		}
	}

	setOrDel("detour", f.Detour, f.Detour != "")
	setOrDel("bind_interface", f.BindInterface, f.BindInterface != "")
	setOrDel("inet4_bind_address", f.Inet4Bind, f.Inet4Bind != "")
	setOrDel("inet6_bind_address", f.Inet6Bind, f.Inet6Bind != "")
	if err == nil {
		obj, err = WriteResolver(obj, f.Resolver, f.ResolverStrategy)
	}
	if err != nil {
		return nil, err
	}

	out := raw
	if index < 0 {
		if !gjson.GetBytes(raw, "outbounds").Exists() {
			if out, err = sjson.SetRawBytes(out, "outbounds", []byte("[]")); err != nil {
				return nil, err
			}
		}
		return sjson.SetRawBytes(out, "outbounds.-1", obj)
	}
	return sjson.SetRawBytes(out, "outbounds."+strconv.Itoa(index), obj)
}

// WriteResolver sets obj's domain_resolver, keeping the shape already
// there: the short string form while no strategy is needed, otherwise an
// object whose other options (e.g. rewrite_ttl) are left alone. An empty
// server removes it.
func WriteResolver(obj []byte, server, strategy string) ([]byte, error) {
	cur := gjson.GetBytes(obj, "domain_resolver")
	var err error
	switch {
	case server == "":
		if cur.Exists() {
			obj, err = sjson.DeleteBytes(obj, "domain_resolver")
		}
	case cur.IsObject():
		obj, err = sjson.SetBytes(obj, "domain_resolver.server", server)
		if err == nil && strategy != "" {
			obj, err = sjson.SetBytes(obj, "domain_resolver.strategy", strategy)
		} else if err == nil && cur.Get("strategy").Exists() {
			obj, err = sjson.DeleteBytes(obj, "domain_resolver.strategy")
		}
	case strategy == "":
		obj, err = sjson.SetBytes(obj, "domain_resolver", server)
	default:
		obj, err = sjson.SetBytes(obj, "domain_resolver", map[string]string{"server": server, "strategy": strategy})
	}
	return obj, err
}

// Find returns the index and object of the outbound with tag, or -1.
func Find(raw []byte, tag string) (int, gjson.Result) {
	idx, res := -1, gjson.Result{}
	gjson.GetBytes(raw, "outbounds").ForEach(func(k, v gjson.Result) bool {
		if v.Get("tag").String() == tag {
			idx, res = int(k.Int()), v
			return false
		}
		return true
	})
	return idx, res
}

// Tags lists the tags of every outbound and endpoint, the things a route or
// a detour can point at.
func Tags(raw []byte) []string {
	var tags []string
	for _, section := range []string{"outbounds", "endpoints"} {
		gjson.GetBytes(raw, section).ForEach(func(_, v gjson.Result) bool {
			if t := v.Get("tag").String(); t != "" {
				tags = append(tags, t)
			}
			return true
		})
	}
	return tags
}

// DetourLoop reports whether letting tag connect through detour would make
// a chain that comes back to tag.
func DetourLoop(raw []byte, tag, detour string) bool {
	next := map[string]string{}
	for _, section := range []string{"outbounds", "endpoints"} {
		gjson.GetBytes(raw, section).ForEach(func(_, v gjson.Result) bool {
			next[v.Get("tag").String()] = v.Get("detour").String()
			return true
		})
	}
	next[tag] = detour
	seen := map[string]bool{}
	for cur := tag; cur != ""; cur = next[cur] {
		if seen[cur] {
			return true
		}
		seen[cur] = true
	}
	return false
}

// References lists where the config points at outbound tag, so it can't be
// deleted blindly. With no route.final the first outbound is the default
// route, which counts as a reference too.
func References(raw []byte, tag string) []string {
	var refs []string
	hit := func(v gjson.Result) bool {
		if v.IsArray() {
			found := false
			v.ForEach(func(_, x gjson.Result) bool {
				found = x.String() == tag
				return !found
			})
			return found
		}
		return v.Exists() && v.String() == tag
	}
	var walkRules func(path string, rules gjson.Result, key string)
	walkRules = func(path string, rules gjson.Result, key string) {
		rules.ForEach(func(k, r gjson.Result) bool {
			p := path + "." + k.String()
			if hit(r.Get(key)) {
				refs = append(refs, p)
			}
			walkRules(p+".rules", r.Get("rules"), key) // logical rules
			return true
		})
	}
	walkRules("route.rules", gjson.GetBytes(raw, "route.rules"), "outbound")
	walkRules("dns.rules", gjson.GetBytes(raw, "dns.rules"), "outbound")
	final := gjson.GetBytes(raw, "route.final")
	if final.String() == tag {
		refs = append(refs, "route.final")
	} else if !final.Exists() && gjson.GetBytes(raw, "outbounds.0.tag").String() == tag {
		refs = append(refs, "route.final (default: first outbound)")
	}
	for _, section := range []string{"outbounds", "endpoints", "dns.servers"} {
		gjson.GetBytes(raw, section).ForEach(func(k, v gjson.Result) bool {
			p := section + "." + k.String()
			for _, key := range []string{"detour", "outbounds", "default"} {
				if hit(v.Get(key)) {
					refs = append(refs, p+"."+key)
				}
			}
			return true
		})
	}
	gjson.GetBytes(raw, "route.rule_set").ForEach(func(k, v gjson.Result) bool {
		if hit(v.Get("download_detour")) {
			refs = append(refs, "route.rule_set."+k.String()+".download_detour")
		}
		return true
	})
	if hit(gjson.GetBytes(raw, "experimental.clash_api.external_ui_download_detour")) {
		refs = append(refs, "experimental.clash_api.external_ui_download_detour")
	}
	return refs
}

// Remove deletes the outbound at index.
func Remove(raw []byte, index int) ([]byte, error) {
	return sjson.DeleteBytes(raw, "outbounds."+strconv.Itoa(index))
}

// SyncStats adds or removes tag from experimental.v2ray_api.stats.outbounds
// when that list exists, so traffic stays counted per outbound.
func SyncStats(raw []byte, tag string, present bool) ([]byte, error) {
	list := gjson.GetBytes(raw, "experimental.v2ray_api.stats.outbounds")
	if !list.IsArray() {
		return raw, nil
	}
	tags := []string{}
	list.ForEach(func(_, v gjson.Result) bool {
		if v.String() != tag {
			tags = append(tags, v.String())
		}
		return true
	})
	if present {
		tags = append(tags, tag)
	}
	return sjson.SetBytes(raw, "experimental.v2ray_api.stats.outbounds", tags)
}
