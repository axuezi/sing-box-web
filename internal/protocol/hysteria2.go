package protocol

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/store"
)

func init() { register(hysteria2{}) }

type hysteria2 struct{}

func (hysteria2) Type() string             { return "hysteria2" }
func (hysteria2) CredentialKeys() []string { return []string{"password"} }

func (hysteria2) UserFields(u store.User) []KV { return []KV{{"password", u.Password}} }

func (hysteria2) DefaultUserFields(Inbound) []KV { return nil }

func (hysteria2) Import(e gjson.Result) (string, string) { return e.Get("password").String(), "" }

// sni is the TLS server name a client should send.
func (hysteria2) sni(c Client) string {
	if s := c.Inbound.Raw.Get("tls.server_name").String(); s != "" {
		return s
	}
	return c.Endpoint.Host
}

func (h hysteria2) obfs(c Client) (typ, password string) {
	o := c.Inbound.Raw.Get("obfs")
	return o.Get("type").String(), o.Get("password").String()
}

func (h hysteria2) Mihomo(c Client) (map[string]any, error) {
	m := map[string]any{
		"name":     c.Name,
		"type":     "hysteria2",
		"server":   c.Endpoint.Host,
		"port":     c.Endpoint.Port,
		"password": c.User.Password,
		"sni":      h.sni(c),
	}
	if c.Endpoint.Insecure {
		m["skip-cert-verify"] = true
	}
	if t, p := h.obfs(c); t != "" {
		m["obfs"] = t
		m["obfs-password"] = p
	}
	return m, nil
}

func (h hysteria2) SingBox(c Client) (map[string]any, error) {
	tls := map[string]any{"enabled": true, "server_name": h.sni(c)}
	if c.Endpoint.Insecure {
		tls["insecure"] = true
	}
	o := map[string]any{
		"type":        "hysteria2",
		"tag":         c.Name,
		"server":      c.Endpoint.Host,
		"server_port": c.Endpoint.Port,
		"password":    c.User.Password,
		"tls":         tls,
	}
	if t, p := h.obfs(c); t != "" {
		o["obfs"] = map[string]any{"type": t, "password": p}
	}
	return o, nil
}

func (h hysteria2) Link(c Client) (string, error) {
	q := url.Values{}
	q.Set("sni", h.sni(c))
	if c.Endpoint.Insecure {
		q.Set("insecure", "1")
	}
	if t, p := h.obfs(c); t != "" {
		q.Set("obfs", t)
		q.Set("obfs-password", p)
	}
	u := url.URL{
		Scheme:   "hysteria2",
		User:     url.User(c.User.Password),
		Host:     net.JoinHostPort(c.Endpoint.Host, strconv.Itoa(c.Endpoint.Port)),
		Path:     "/",
		RawQuery: q.Encode(),
		Fragment: c.Name,
	}
	if c.User.Password == "" {
		return "", fmt.Errorf("user %s has no password", c.User.Name)
	}
	return u.String(), nil
}
