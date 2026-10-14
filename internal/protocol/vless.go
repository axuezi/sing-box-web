package protocol

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/tidwall/gjson"
	"golang.org/x/crypto/curve25519"

	"github.com/axuezi/sing-box-web/internal/store"
)

func init() { register(vless{}) }

type vless struct{}

func (vless) Type() string             { return "vless" }
func (vless) CredentialKeys() []string { return []string{"uuid"} }

func (vless) UserFields(u store.User) []KV { return []KV{{"uuid", u.UUID}} }

// DefaultUserFields gives new users XTLS Vision on plain-TCP TLS/Reality
// inbounds, the configuration sing-box documents for Reality.
func (vless) DefaultUserFields(in Inbound) []KV {
	if in.Raw.Get("tls.enabled").Bool() && !in.Raw.Get("transport").Exists() {
		return []KV{{"flow", "xtls-rprx-vision"}}
	}
	return nil
}

func (vless) Import(e gjson.Result) (string, string) { return "", e.Get("uuid").String() }

type vlessParams struct {
	flow, sni, publicKey, shortID string
	reality, tls                  bool
}

func (vless) params(c Client) (vlessParams, error) {
	in := c.Inbound.Raw
	if t := in.Get("transport.type").String(); t != "" {
		return vlessParams{}, fmt.Errorf("%w: vless transport %q", ErrUnsupported, t)
	}
	p := vlessParams{
		tls:     in.Get("tls.enabled").Bool(),
		reality: in.Get("tls.reality.enabled").Bool(),
		sni:     in.Get("tls.server_name").String(),
	}
	// The user's flow lives on the config user entry; all entries of an
	// inbound share it, so the first one is representative.
	p.flow = in.Get("users.0.flow").String()
	if c.Flow != nil {
		p.flow = *c.Flow
	}
	if p.reality {
		pub, err := RealityPublicKey(in.Get("tls.reality.private_key").String())
		if err != nil {
			return vlessParams{}, err
		}
		p.publicKey = pub
		// short_id may be a string or a list; take the first non-empty one.
		in.Get("tls.reality.short_id").ForEach(func(_, v gjson.Result) bool {
			p.shortID = v.String()
			return p.shortID == ""
		})
	}
	if p.sni == "" && p.tls && !p.reality {
		p.sni = c.Endpoint.Host
	}
	return p, nil
}

// RealityPublicKey derives the client-side public key from a Reality
// private key (base64url, as produced by `sing-box generate reality-keypair`).
func RealityPublicKey(private string) (string, error) {
	priv, err := base64.RawURLEncoding.DecodeString(private)
	if err != nil || len(priv) != curve25519.ScalarSize {
		return "", fmt.Errorf("invalid reality private_key")
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pub), nil
}

func (v vless) Mihomo(c Client) (map[string]any, error) {
	p, err := v.params(c)
	if err != nil {
		return nil, err
	}
	m := map[string]any{
		"name":    c.Name,
		"type":    "vless",
		"server":  c.Endpoint.Host,
		"port":    c.Endpoint.Port,
		"uuid":    c.User.UUID,
		"network": "tcp",
		"udp":     true,
		"tls":     p.tls,
	}
	if p.flow != "" {
		m["flow"] = p.flow
		m["packet-encoding"] = "xudp"
	}
	if p.tls {
		m["servername"] = p.sni
		m["client-fingerprint"] = "chrome"
	}
	if p.reality {
		m["reality-opts"] = map[string]any{"public-key": p.publicKey, "short-id": p.shortID}
	} else if p.tls && c.Endpoint.Insecure {
		m["skip-cert-verify"] = true
	}
	return m, nil
}

func (v vless) SingBox(c Client) (map[string]any, error) {
	p, err := v.params(c)
	if err != nil {
		return nil, err
	}
	o := map[string]any{
		"type":        "vless",
		"tag":         c.Name,
		"server":      c.Endpoint.Host,
		"server_port": c.Endpoint.Port,
		"uuid":        c.User.UUID,
	}
	if p.flow != "" {
		o["flow"] = p.flow
	}
	if p.tls {
		tls := map[string]any{
			"enabled":     true,
			"server_name": p.sni,
			"utls":        map[string]any{"enabled": true, "fingerprint": "chrome"},
		}
		if p.reality {
			tls["reality"] = map[string]any{"enabled": true, "public_key": p.publicKey, "short_id": p.shortID}
		} else if c.Endpoint.Insecure {
			tls["insecure"] = true
		}
		o["tls"] = tls
	}
	return o, nil
}

func (v vless) Link(c Client) (string, error) {
	p, err := v.params(c)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("type", "tcp")
	if p.flow != "" {
		q.Set("flow", p.flow)
	}
	switch {
	case p.reality:
		q.Set("security", "reality")
		q.Set("sni", p.sni)
		q.Set("fp", "chrome")
		q.Set("pbk", p.publicKey)
		q.Set("sid", p.shortID)
	case p.tls:
		q.Set("security", "tls")
		q.Set("sni", p.sni)
		q.Set("fp", "chrome")
		if c.Endpoint.Insecure {
			q.Set("allowInsecure", "1")
		}
	default:
		q.Set("security", "none")
	}
	u := url.URL{
		Scheme:   "vless",
		User:     url.User(c.User.UUID),
		Host:     net.JoinHostPort(c.Endpoint.Host, strconv.Itoa(c.Endpoint.Port)),
		RawQuery: q.Encode(),
		Fragment: c.Name,
	}
	return u.String(), nil
}
