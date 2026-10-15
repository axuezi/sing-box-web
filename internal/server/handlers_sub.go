package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/subscription"
)

// handleSubscription serves a user's client config by token. The format
// comes from ?format= or, failing that, the client's User-Agent.
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := s.clientIP(r)
	if locked, _ := s.opts.SubLimiter.Locked(ip); locked {
		writeError(w, http.StatusTooManyRequests, "locked", "too many invalid tokens")
		return
	}
	token := r.PathValue("token")
	u, err := s.opts.Store.UserBySubToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !u.Enabled) {
		s.opts.SubLimiter.Fail(ip)
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	file, err := singbox.ReadFile(s.opts.SingboxConfig)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	st, err := s.subscriptionSettings(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	clients, _, err := s.subscriptionClients(ctx, file.Raw, u, st)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = formatFromUA(r.UserAgent())
	}
	var body []byte
	var ctype string
	switch format {
	case "mihomo", "clash":
		body, err = subscription.Mihomo(clients, st.MihomoTemplate)
		ctype = "text/yaml; charset=utf-8"
	case "singbox", "sing-box":
		body, err = subscription.SingBox(clients)
		ctype = "application/json; charset=utf-8"
	case "links":
		body, ctype = subscription.Links(clients), "text/plain; charset=utf-8"
	case "base64":
		body, ctype = subscription.Base64Links(clients), "text/plain; charset=utf-8"
	default:
		writeError(w, http.StatusBadRequest, "bad_format", "format must be mihomo, singbox, links or base64")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	_ = s.opts.Store.TouchSubscription(ctx, u.ID)
	_ = s.opts.Store.LogSubAccess(ctx, u.ID, store.SubAccess{IP: ip, UserAgent: truncate(r.UserAgent(), 200), Format: format})

	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Disposition", contentDisposition(u.Name))
	h.Set("Profile-Update-Interval", "24")
	h.Set("Profile-Title", "base64:"+b64(u.Name))
	h.Set("Subscription-Userinfo", subscriptionUserinfo(u))
	_, _ = w.Write(body)
}

// subscriptionUserinfo is the de-facto header clients (Clash, Mihomo,
// sing-box GUIs) read to show usage, quota and expiry.
func subscriptionUserinfo(u store.User) string {
	v := fmt.Sprintf("upload=%d; download=%d; total=%d", u.CycleUp, u.CycleDown, u.QuotaBytes)
	if !u.ExpiresAt.IsZero() {
		v += fmt.Sprintf("; expire=%d", u.ExpiresAt.Unix())
	}
	return v
}

func formatFromUA(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "sing-box"), strings.HasPrefix(ua, "sfa/"), strings.HasPrefix(ua, "sfi/"), strings.HasPrefix(ua, "sfm/"):
		return "singbox"
	case strings.Contains(ua, "clash"), strings.Contains(ua, "mihomo"), strings.Contains(ua, "stash"):
		return "mihomo"
	default:
		return "base64"
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// contentDisposition names the download. Clients that ignore Profile-Title
// (Clash Verge) show this as the profile name, so it carries no file
// extension. Clash Verge also parses the header naively: it cuts the value
// out of the header's escaped debug form, so a quoted filename shows up as
// \"name\. The fallback filename is therefore always an unquoted token
// (other characters become _), and the real name goes in filename*
// (RFC 5987), which Clash Verge and browsers prefer.
func contentDisposition(name string) string {
	token := []byte(name)
	for i, c := range token {
		if !isTokenChar(c) {
			token[i] = '_'
		}
	}
	v := "attachment; filename=" + string(token)
	if string(token) != name {
		v += "; filename*=UTF-8''" + rfc5987(name)
	}
	return v
}

// isTokenChar reports whether c may appear in an RFC 7230 token, which
// needs no quoting.
func isTokenChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// rfc5987 percent-encodes everything but RFC 5987 attr-chars.
func rfc5987(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

func (s *Server) handleSubAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	list, err := s.opts.Store.ListSubAccess(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": list})
}

const subscriptionSettingsKey = "subscription"

func (s *Server) subscriptionSettings(ctx context.Context) (subscription.Settings, error) {
	var st subscription.Settings
	raw, err := s.opts.Store.GetSetting(ctx, subscriptionSettingsKey)
	if err != nil || raw == "" {
		return st, err
	}
	err = json.Unmarshal([]byte(raw), &st)
	return st, err
}

func (s *Server) handleGetSubSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.subscriptionSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if st.Inbounds == nil {
		st.Inbounds = map[string]subscription.InboundSettings{}
	}
	type inboundInfo struct {
		Tag        string `json:"tag"`
		Type       string `json:"type"`
		ListenPort int    `json:"listen_port"`
	}
	inbounds := []inboundInfo{}
	if f, err := singbox.ReadFile(s.opts.SingboxConfig); err == nil {
		for _, in := range protocol.Inbounds(f.Raw) {
			inbounds = append(inbounds, inboundInfo{in.Tag, in.Type, int(in.Raw.Get("listen_port").Int())})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "inbounds": inbounds,
		"mihomo_default_template": subscription.DefaultMihomoTemplate()})
}

func (s *Server) handlePutSubSettings(w http.ResponseWriter, r *http.Request) {
	var st subscription.Settings
	if err := decodeJSON(r, &st); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	st.PublicHost = strings.TrimSpace(st.PublicHost)
	tmpl, err := subscription.NormalizeMihomoTemplate(st.MihomoTemplate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_mihomo_template", err.Error())
		return
	}
	st.MihomoTemplate = tmpl
	for tag, o := range st.Inbounds {
		if o.Port < 0 || o.Port > 65535 {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid port for "+tag)
			return
		}
		o.Host, o.Name = strings.TrimSpace(o.Host), strings.TrimSpace(o.Name)
		st.Inbounds[tag] = o
	}
	b, _ := json.Marshal(st)
	if err := s.opts.Store.SetSetting(r.Context(), subscriptionSettingsKey, string(b)); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "settings.update", "subscription", "")
	writeJSON(w, http.StatusOK, map[string]any{"settings": st})
}

func (s *Server) subscriptionClients(ctx context.Context, raw []byte, u store.User, st subscription.Settings) ([]protocol.Client, map[string]string, error) {
	flows, err := protocol.StoredFlows(ctx, s.opts.Store)
	if err != nil {
		return nil, nil, err
	}
	clients, skipped := subscription.Clients(raw, u, st, protocol.EffectiveFlows(raw, flows))
	return clients, skipped, nil
}
