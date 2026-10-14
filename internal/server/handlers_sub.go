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
	var ctype, ext string
	switch format {
	case "mihomo", "clash":
		body, err = subscription.Mihomo(clients)
		ctype, ext = "text/yaml; charset=utf-8", "yaml"
	case "singbox", "sing-box":
		body, err = subscription.SingBox(clients)
		ctype, ext = "application/json; charset=utf-8", "json"
	case "links":
		body, ctype, ext = subscription.Links(clients), "text/plain; charset=utf-8", "txt"
	case "base64":
		body, ctype, ext = subscription.Base64Links(clients), "text/plain; charset=utf-8", "txt"
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
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", u.Name+"."+ext))
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
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "inbounds": inbounds})
}

func (s *Server) handlePutSubSettings(w http.ResponseWriter, r *http.Request) {
	var st subscription.Settings
	if err := decodeJSON(r, &st); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	st.PublicHost = strings.TrimSpace(st.PublicHost)
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
