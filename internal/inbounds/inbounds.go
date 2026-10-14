// Package inbounds maps Hysteria2 and VLESS inbounds to an editable form and
// back. Writing a form only touches the fields the form models; everything
// else in the inbound object (transport, multiplex, sniffing, ...) is kept.
package inbounds

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

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
)

// Security modes.
const (
	SecNone    = "none"
	SecTLS     = "tls"
	SecReality = "reality"

	CertFile   = "file"
	CertInline = "inline"

	FlowVision = "xtls-rprx-vision"
)

// Editable lists the inbound types this package edits.
var Editable = []string{"hysteria2", "vless"}

// Form is the editable view of one inbound.
type Form struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
	TLS        TLS    `json:"tls"`

	// Hysteria2.
	UpMbps       int    `json:"up_mbps"`
	DownMbps     int    `json:"down_mbps"`
	ObfsPassword string `json:"obfs_password"` // salamander; "" = off
	Masquerade   string `json:"masquerade"`    // URL; "" = off

	// VLESS: flow written onto every user entry ("" or FlowVision).
	Flow string `json:"flow"`
}

// TLS holds the security settings.
type TLS struct {
	Mode       string   `json:"mode"` // none | tls | reality (hysteria2: always tls)
	ServerName string   `json:"server_name"`
	ALPN       []string `json:"alpn"`

	CertSource      string `json:"cert_source"` // file | inline
	CertificatePath string `json:"certificate_path"`
	KeyPath         string `json:"key_path"`
	Certificate     string `json:"certificate"` // PEM
	Key             string `json:"key"`         // PEM; masked when read

	HandshakeServer string   `json:"handshake_server"`
	HandshakePort   int      `json:"handshake_port"`
	PrivateKey      string   `json:"private_key"` // masked when read
	PublicKey       string   `json:"public_key"`  // derived, read-only
	ShortIDs        []string `json:"short_ids"`
}

// modelled are the top-level keys the form owns, per type.
var modelled = map[string][]string{
	"hysteria2": {"type", "tag", "listen", "listen_port", "users", "tls", "up_mbps", "down_mbps", "obfs", "masquerade"},
	"vless":     {"type", "tag", "listen", "listen_port", "users", "tls"},
}

var tlsModelled = []string{"enabled", "server_name", "alpn", "certificate_path", "key_path", "certificate", "key", "reality"}

// Read returns the form for an inbound object, with secrets masked, and the
// keys the form does not model (they are kept as they are on save).
func Read(in gjson.Result) (Form, []string) {
	typ := in.Get("type").String()
	f := Form{
		Tag:        in.Get("tag").String(),
		Type:       typ,
		Listen:     in.Get("listen").String(),
		ListenPort: int(in.Get("listen_port").Int()),
	}
	t := in.Get("tls")
	f.TLS.ServerName = t.Get("server_name").String()
	t.Get("alpn").ForEach(func(_, v gjson.Result) bool {
		f.TLS.ALPN = append(f.TLS.ALPN, v.String())
		return true
	})
	switch {
	case !t.Get("enabled").Bool():
		f.TLS.Mode = SecNone
	case t.Get("reality.enabled").Bool():
		f.TLS.Mode = SecReality
	default:
		f.TLS.Mode = SecTLS
	}
	f.TLS.CertificatePath = t.Get("certificate_path").String()
	f.TLS.KeyPath = t.Get("key_path").String()
	f.TLS.Certificate = joinLines(t.Get("certificate"))
	if joinLines(t.Get("key")) != "" {
		f.TLS.Key = singbox.MaskedValue
	}
	f.TLS.CertSource = CertFile
	if f.TLS.Certificate != "" && f.TLS.CertificatePath == "" {
		f.TLS.CertSource = CertInline
	}
	r := t.Get("reality")
	f.TLS.HandshakeServer = r.Get("handshake.server").String()
	f.TLS.HandshakePort = int(r.Get("handshake.server_port").Int())
	if pk := r.Get("private_key").String(); pk != "" {
		f.TLS.PrivateKey = singbox.MaskedValue
		f.TLS.PublicKey, _ = protocol.RealityPublicKey(pk)
	}
	r.Get("short_id").ForEach(func(_, v gjson.Result) bool {
		f.TLS.ShortIDs = append(f.TLS.ShortIDs, v.String())
		return true
	})
	if s := r.Get("short_id"); s.Type == gjson.String {
		f.TLS.ShortIDs = []string{s.String()}
	}

	switch typ {
	case "hysteria2":
		f.UpMbps = int(in.Get("up_mbps").Int())
		f.DownMbps = int(in.Get("down_mbps").Int())
		if in.Get("obfs.type").String() == "salamander" {
			f.ObfsPassword = in.Get("obfs.password").String()
		}
		if m := in.Get("masquerade"); m.Type == gjson.String {
			f.Masquerade = m.String()
		}
	case "vless":
		f.Flow = in.Get("users.0.flow").String()
	}

	var extra []string
	in.ForEach(func(k, v gjson.Result) bool {
		key := k.String()
		switch {
		case key == "masquerade" && v.Type != gjson.String,
			key == "obfs" && in.Get("obfs.type").String() != "salamander":
			extra = append(extra, key) // a form it cannot show: kept as is
		case !slices.Contains(modelled[typ], key):
			extra = append(extra, key)
		}
		return true
	})
	t.ForEach(func(k, _ gjson.Result) bool {
		if !slices.Contains(tlsModelled, k.String()) {
			extra = append(extra, "tls."+k.String())
		}
		return true
	})
	return f, extra
}

func joinLines(v gjson.Result) string {
	if v.IsArray() {
		var lines []string
		v.ForEach(func(_, l gjson.Result) bool {
			lines = append(lines, l.String())
			return true
		})
		return strings.Join(lines, "\n")
	}
	return v.String()
}

var (
	validTag     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	validShortID = regexp.MustCompile(`^[0-9a-f]{0,16}$`)
	validHost    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

// Normalize trims and defaults a submitted form and checks it on its own.
func Normalize(f *Form) error {
	f.Tag = strings.TrimSpace(f.Tag)
	f.Listen = strings.TrimSpace(f.Listen)
	f.TLS.ServerName = strings.TrimSpace(f.TLS.ServerName)
	f.TLS.HandshakeServer = strings.TrimSpace(f.TLS.HandshakeServer)
	f.Masquerade = strings.TrimSpace(f.Masquerade)
	if f.Listen == "" {
		f.Listen = "::"
	}
	if !slices.Contains(Editable, f.Type) {
		return fmt.Errorf("type must be one of %s", strings.Join(Editable, ", "))
	}
	if !validTag.MatchString(f.Tag) {
		return errors.New("tag must be 1-64 letters, digits, . _ -, starting with a letter or digit")
	}
	if _, err := netip.ParseAddr(f.Listen); err != nil {
		return errors.New("listen must be an IP address, e.g. :: or 127.0.0.1")
	}
	if f.ListenPort < 1 || f.ListenPort > 65535 {
		return errors.New("listen_port must be 1-65535")
	}
	if f.TLS.ServerName != "" && !validHost.MatchString(f.TLS.ServerName) {
		return errors.New("invalid server name")
	}
	if f.Type == "hysteria2" {
		f.TLS.Mode = SecTLS // Hysteria2 is QUIC: TLS is mandatory and Reality is unsupported
		if f.UpMbps < 0 || f.DownMbps < 0 {
			return errors.New("bandwidth must not be negative")
		}
		if f.Masquerade != "" && !strings.HasPrefix(f.Masquerade, "http://") && !strings.HasPrefix(f.Masquerade, "https://") &&
			!strings.HasPrefix(f.Masquerade, "file://") {
			return errors.New("masquerade must be an http(s):// or file:// URL")
		}
	}
	if f.Type == "vless" {
		if f.Flow != "" && f.Flow != FlowVision {
			return errors.New("flow must be empty or " + FlowVision)
		}
		if f.Flow != "" && f.TLS.Mode == SecNone {
			return errors.New("XTLS Vision needs TLS or Reality")
		}
	}
	alpn := []string{}
	for _, a := range f.TLS.ALPN {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(alpn, a) {
			alpn = append(alpn, a)
		}
	}
	f.TLS.ALPN = alpn
	switch f.TLS.Mode {
	case SecNone:
	case SecTLS:
		switch f.TLS.CertSource {
		case CertFile:
			if strings.TrimSpace(f.TLS.CertificatePath) == "" || strings.TrimSpace(f.TLS.KeyPath) == "" {
				return errors.New("certificate and key paths are required")
			}
		case CertInline:
			if !strings.Contains(f.TLS.Certificate, "BEGIN CERTIFICATE") ||
				(f.TLS.Key != singbox.MaskedValue && !strings.Contains(f.TLS.Key, "PRIVATE KEY")) {
				return errors.New("paste a PEM certificate and private key, or generate a self-signed one")
			}
		default:
			return errors.New("cert_source must be file or inline")
		}
	case SecReality:
		if f.Type != "vless" {
			return errors.New("reality is only for vless")
		}
		if f.TLS.ServerName == "" || f.TLS.HandshakeServer == "" {
			return errors.New("reality needs a server name and a handshake server")
		}
		if f.TLS.HandshakePort == 0 {
			f.TLS.HandshakePort = 443
		}
		if f.TLS.HandshakePort < 1 || f.TLS.HandshakePort > 65535 {
			return errors.New("handshake port must be 1-65535")
		}
		if f.TLS.PrivateKey != singbox.MaskedValue {
			if _, err := protocol.RealityPublicKey(f.TLS.PrivateKey); err != nil {
				return errors.New("invalid reality private key; generate a key pair")
			}
		}
		ids := []string{}
		for _, id := range f.TLS.ShortIDs {
			id = strings.ToLower(strings.TrimSpace(id))
			if !validShortID.MatchString(id) || len(id)%2 != 0 {
				return fmt.Errorf("short id %q must be 0-16 hex digits, an even count", id)
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return errors.New("reality needs at least one short id")
		}
		f.TLS.ShortIDs = ids
	default:
		return errors.New("tls mode must be none, tls or reality")
	}
	return nil
}

// Write applies form f onto the inbound at index in raw (index = -1 appends
// a new inbound). orig is the inbound before the edit, used to restore
// masked secrets; it is empty for a new inbound.
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
		if err == nil {
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

	if index < 0 {
		set("type", f.Type)
		set("tag", f.Tag)
	}
	set("listen", f.Listen)
	set("listen_port", f.ListenPort)
	if !gjson.GetBytes(obj, "users").Exists() {
		set("users", []any{})
	}

	if f.TLS.Mode == SecNone {
		del("tls")
	} else {
		set("tls.enabled", true)
		setOrDel("tls.server_name", f.TLS.ServerName, f.TLS.ServerName != "")
		setOrDel("tls.alpn", f.TLS.ALPN, len(f.TLS.ALPN) > 0)
	}
	switch f.TLS.Mode {
	case SecTLS:
		del("tls.reality")
		if f.TLS.CertSource == CertFile {
			set("tls.certificate_path", strings.TrimSpace(f.TLS.CertificatePath))
			set("tls.key_path", strings.TrimSpace(f.TLS.KeyPath))
			del("tls.certificate")
			del("tls.key")
		} else {
			set("tls.certificate", strings.TrimSpace(f.TLS.Certificate))
			if f.TLS.Key != singbox.MaskedValue {
				set("tls.key", strings.TrimSpace(f.TLS.Key))
			} else if !orig.Get("tls.key").Exists() {
				return nil, errors.New("enter the private key")
			}
			del("tls.certificate_path")
			del("tls.key_path")
		}
	case SecReality:
		for _, k := range []string{"certificate_path", "key_path", "certificate", "key", "acme"} {
			del("tls." + k)
		}
		set("tls.reality.enabled", true)
		set("tls.reality.handshake.server", f.TLS.HandshakeServer)
		set("tls.reality.handshake.server_port", f.TLS.HandshakePort)
		if f.TLS.PrivateKey != singbox.MaskedValue {
			set("tls.reality.private_key", f.TLS.PrivateKey)
		} else if orig.Get("tls.reality.private_key").String() == "" {
			return nil, errors.New("generate a reality key pair")
		}
		// Keep the original string form for a single id; sing-box takes both.
		if len(f.TLS.ShortIDs) == 1 && orig.Get("tls.reality.short_id").Type != gjson.JSON {
			set("tls.reality.short_id", f.TLS.ShortIDs[0])
		} else {
			set("tls.reality.short_id", f.TLS.ShortIDs)
		}
	}

	switch f.Type {
	case "hysteria2":
		setOrDel("up_mbps", f.UpMbps, f.UpMbps > 0)
		setOrDel("down_mbps", f.DownMbps, f.DownMbps > 0)
		if f.ObfsPassword != "" {
			set("obfs", map[string]string{"type": "salamander", "password": f.ObfsPassword})
		} else if gjson.GetBytes(obj, "obfs.type").String() == "salamander" {
			del("obfs")
		}
		if f.Masquerade != "" {
			set("masquerade", f.Masquerade)
		} else if gjson.GetBytes(obj, "masquerade").Type == gjson.String {
			del("masquerade")
		}
	case "vless":
		n := int(gjson.GetBytes(obj, "users.#").Int())
		for i := range n {
			setOrDel("users."+strconv.Itoa(i)+".flow", f.Flow, f.Flow != "")
		}
	}
	if err != nil {
		return nil, err
	}

	out := raw
	if index < 0 {
		if !gjson.GetBytes(raw, "inbounds").Exists() {
			out, err = sjson.SetRawBytes(out, "inbounds", []byte("[]"))
			if err != nil {
				return nil, err
			}
		}
		out, err = sjson.SetRawBytes(out, "inbounds.-1", obj)
	} else {
		out, err = sjson.SetRawBytes(out, "inbounds."+strconv.Itoa(index), obj)
	}
	return out, err
}

// Find returns the index and object of the inbound with tag, or -1.
func Find(raw []byte, tag string) (int, gjson.Result) {
	idx, res := -1, gjson.Result{}
	gjson.GetBytes(raw, "inbounds").ForEach(func(k, v gjson.Result) bool {
		if v.Get("tag").String() == tag {
			idx, res = int(k.Int()), v
			return false
		}
		return true
	})
	return idx, res
}

// Networks returns the transport protocols (tcp, udp) an inbound listens on.
func Networks(in gjson.Result) []string { return networks(in) }

// networks returns the transport protocols an inbound listens on.
func networks(in gjson.Result) []string {
	switch in.Get("type").String() {
	case "hysteria2", "hysteria", "tuic":
		return []string{"udp"}
	case "vless", "vmess", "trojan", "naive", "shadowtls", "http", "socks", "anytls":
		if in.Get("transport.type").String() == "quic" {
			return []string{"udp"}
		}
		return []string{"tcp"}
	case "shadowsocks", "mixed", "direct":
		if n := in.Get("network").String(); n == "tcp" || n == "udp" {
			return []string{n}
		}
		return []string{"tcp", "udp"}
	}
	return []string{"tcp", "udp"}
}

func overlaps(a, b string) bool {
	wild := func(s string) bool { return s == "" || s == "::" || s == "0.0.0.0" }
	if wild(a) || wild(b) {
		return true
	}
	pa, ea := netip.ParseAddr(a)
	pb, eb := netip.ParseAddr(b)
	if ea != nil || eb != nil {
		return a == b
	}
	return pa.Unmap() == pb.Unmap()
}

// Conflict returns the tag of another inbound already using f's port.
func Conflict(raw []byte, f Form) string {
	var mine gjson.Result
	if b, err := sjson.SetBytes([]byte(`{}`), "type", f.Type); err == nil {
		mine = gjson.ParseBytes(b)
	}
	nets := networks(mine)
	found := ""
	gjson.GetBytes(raw, "inbounds").ForEach(func(_, v gjson.Result) bool {
		if v.Get("tag").String() == f.Tag || int(v.Get("listen_port").Int()) != f.ListenPort {
			return true
		}
		if !overlaps(v.Get("listen").String(), f.Listen) {
			return true
		}
		for _, n := range networks(v) {
			if slices.Contains(nets, n) {
				found = v.Get("tag").String()
				return false
			}
		}
		return true
	})
	return found
}

// References lists where the config refers to an inbound tag (route and DNS
// rules), so it can't be deleted blindly.
func References(raw []byte, tag string) []string {
	var refs []string
	check := func(section string) {
		var walk func(path string, rules gjson.Result)
		walk = func(path string, rules gjson.Result) {
			rules.ForEach(func(k, r gjson.Result) bool {
				p := path + "." + k.String()
				hit := false
				r.Get("inbound").ForEach(func(_, v gjson.Result) bool {
					hit = hit || v.String() == tag
					return !hit
				})
				if r.Get("inbound").String() == tag {
					hit = true
				}
				if hit {
					refs = append(refs, p)
				}
				walk(p+".rules", r.Get("rules")) // logical rules
				return true
			})
		}
		walk(section, gjson.GetBytes(raw, section))
	}
	check("route.rules")
	check("dns.rules")
	return refs
}

// Remove deletes the inbound at index.
func Remove(raw []byte, index int) ([]byte, error) {
	return sjson.DeleteBytes(raw, "inbounds."+strconv.Itoa(index))
}

// SyncStatsInbounds adds or removes tag from experimental.v2ray_api.stats.
// inbounds when that list exists, so traffic stays counted per inbound.
func SyncStatsInbounds(raw []byte, tag string, present bool) ([]byte, error) {
	list := gjson.GetBytes(raw, "experimental.v2ray_api.stats.inbounds")
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
	return sjson.SetBytes(raw, "experimental.v2ray_api.stats.inbounds", tags)
}

// SetFlow writes the VLESS flow onto every user entry of inbound tag. Use it
// after rendering users into a new inbound, whose entries get the default.
func SetFlow(raw []byte, tag, flow string) ([]byte, error) {
	idx, in := Find(raw, tag)
	if idx < 0 || in.Get("type").String() != "vless" {
		return raw, nil
	}
	var err error
	for i := range int(in.Get("users.#").Int()) {
		path := "inbounds." + strconv.Itoa(idx) + ".users." + strconv.Itoa(i) + ".flow"
		if flow != "" {
			raw, err = sjson.SetBytes(raw, path, flow)
		} else {
			raw, err = sjson.DeleteBytes(raw, path)
		}
		if err != nil {
			return nil, err
		}
	}
	return raw, nil
}
