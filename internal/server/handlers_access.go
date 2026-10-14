package server

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/access"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/traffic"
)

const maxAccessDuration = 365 * 24 * time.Hour

func (s *Server) accessReady(w http.ResponseWriter) bool {
	if s.opts.Access == nil || s.opts.Access.Ctl == nil {
		writeError(w, http.StatusConflict, "unavailable", access.ErrUnavailable.Error())
		return false
	}
	return true
}

// myIP is the address the administrator's request came from.
func (s *Server) myIP(r *http.Request) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s.clientIP(r))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

type exposedSource struct {
	IP      string `json:"ip"`
	Inbound string `json:"inbound"`
	Conns   int    `json:"conns"`
}

// liveSources lists the client addresses of current connections to inbounds
// the firewall guards (the only ones whose real source sing-box sees).
func (s *Server) liveSources(ctx context.Context, targets []access.Target) []exposedSource {
	guarded := map[string]bool{}
	for _, t := range targets {
		if t.By == access.ByFirewall {
			guarded[t.Tag] = true
		}
	}
	c, err := traffic.ClashClient(s.opts.SingboxConfig)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	snap, err := c.Connections(ctx)
	if err != nil {
		return nil
	}
	byIP := map[string]*exposedSource{}
	for _, cn := range snap.Connections {
		tag := inboundTag(cn.Metadata.Type)
		a, err := netip.ParseAddr(cn.Metadata.SourceIP)
		if !guarded[tag] || err != nil || a.IsLoopback() {
			continue
		}
		key := a.Unmap().String()
		if byIP[key] == nil {
			byIP[key] = &exposedSource{IP: key, Inbound: tag}
		}
		byIP[key].Conns++
	}
	out := make([]exposedSource, 0, len(byIP))
	for _, v := range byIP {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Conns > out[j].Conns })
	return out
}

func (s *Server) handleGetAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m := s.opts.Access
	resp := map[string]any{"available": false}
	if ip, ok := s.myIP(r); ok {
		resp["my_ip"] = ip.String()
	}
	if m == nil || m.Ctl == nil {
		resp["error"] = access.ErrUnavailable.Error()
		resp["code"] = "no_ctl" // the UI explains this one in the user's language
		writeJSON(w, http.StatusOK, resp)
		return
	}
	info, err := m.Info(ctx)
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	rules, err := s.opts.Store.ListAccessRules(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	on, deadline, err := m.Allowlist(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var targets []access.Target
	if f, err := singbox.ReadFile(s.opts.SingboxConfig); err == nil {
		targets = access.Targets(f.Raw, info)
	}
	sources := s.liveSources(ctx, targets)
	// Connections the allowlist would cut (or cuts) if it were on.
	outside := []exposedSource{}
	for _, src := range sources {
		if a, err := netip.ParseAddr(src.IP); err == nil && !access.Contains(rules, access.Allow, a) {
			outside = append(outside, src)
		}
	}
	stats, _ := m.Ctl.AccessStats(ctx)
	resp["available"] = true
	resp["info"] = info
	resp["targets"] = targets
	resp["rules"] = rules
	resp["allowlist"] = map[string]any{"on": on, "confirm_by": deadline, "outside": outside}
	resp["sources"] = sources
	resp["stats"] = stats
	resp["last"] = m.Last()
	writeJSON(w, http.StatusOK, resp)
}

type accessRuleInput struct {
	List     string `json:"list"`
	IP       string `json:"ip"`
	Note     string `json:"note"`
	Duration int64  `json:"duration"` // seconds; 0 = permanent
}

func (s *Server) handleAddAccessRule(w http.ResponseWriter, r *http.Request) {
	if !s.accessReady(w) {
		return
	}
	var in accessRuleInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if in.List != access.Deny && in.List != access.Allow {
		writeError(w, http.StatusBadRequest, "bad_request", "list must be deny or allow")
		return
	}
	p, err := access.Normalize(in.IP)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_ip", err.Error())
		return
	}
	d := time.Duration(in.Duration) * time.Second
	if d < 0 || d > maxAccessDuration {
		writeError(w, http.StatusBadRequest, "bad_request", "duration must be 0 (permanent) to 365 days")
		return
	}
	if me, ok := s.myIP(r); ok && in.List == access.Deny && p.Contains(me) {
		writeError(w, http.StatusConflict, "self", "this would block your own address "+me.String())
		return
	}
	rule := store.AccessRule{List: in.List, CIDR: p.String(), Note: truncate(strings.TrimSpace(in.Note), 200), CreatedBy: s.actor(r)}
	if d > 0 {
		rule.ExpiresAt = time.Now().Add(d).UTC().Truncate(time.Second)
	}
	saved, err := s.opts.Store.PutAccessRule(r.Context(), rule)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "access.add", saved.CIDR, saved.List)
	s.applyAccess(w, r, map[string]any{"rule": saved})
}

// applyAccess pushes the rules and answers; a failed push is reported but
// the change stays saved and is retried every minute.
func (s *Server) applyAccess(w http.ResponseWriter, r *http.Request, body map[string]any) {
	err := s.opts.Access.Apply(context.WithoutCancel(r.Context()), true)
	body["applied"] = err == nil
	if err != nil {
		body["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleDeleteAccessRule(w http.ResponseWriter, r *http.Request) {
	if !s.accessReady(w) {
		return
	}
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	rules, err := s.opts.Store.ListAccessRules(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	i := slices.IndexFunc(rules, func(x store.AccessRule) bool { return x.ID == id })
	if i < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such rule")
		return
	}
	if rules[i].List == access.Allow {
		on, _, err := s.opts.Access.Allowlist(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		allows := 0
		for _, x := range rules {
			if x.List == access.Allow {
				allows++
			}
		}
		if on && allows == 1 {
			writeError(w, http.StatusConflict, "last_allow", "turn the allowlist off before removing its last entry")
			return
		}
	}
	if _, _, err := s.opts.Store.DeleteAccessRule(ctx, id); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(ctx, s.actor(r), s.clientIP(r), "access.delete", rules[i].CIDR, rules[i].List)
	s.applyAccess(w, r, map[string]any{})
}

func (s *Server) handleSetAllowlist(w http.ResponseWriter, r *http.Request) {
	if !s.accessReady(w) {
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx := context.WithoutCancel(r.Context())
	deadline, err := s.opts.Access.SetAllowlist(ctx, in.On)
	action := "access.allowlist_off"
	if in.On {
		action = "access.allowlist_on"
	}
	if err != nil && deadline.IsZero() && in.On {
		writeError(w, http.StatusConflict, "bad_request", err.Error())
		return
	}
	s.audit(ctx, s.actor(r), s.clientIP(r), action, "", "")
	body := map[string]any{"on": in.On, "confirm_by": deadline, "applied": err == nil}
	if err != nil {
		body["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleConfirmAllowlist(w http.ResponseWriter, r *http.Request) {
	if !s.accessReady(w) {
		return
	}
	if err := s.opts.Access.Confirm(r.Context()); err != nil {
		writeError(w, http.StatusConflict, "nothing_to_confirm", err.Error())
		return
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "access.allowlist_confirm", "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListBans(w http.ResponseWriter, r *http.Request) {
	if !s.accessReady(w) {
		return
	}
	info, err := s.opts.Access.Info(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "controller", err.Error())
		return
	}
	bans, err := s.opts.Access.Ctl.Bans(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "controller", err.Error())
		return
	}
	sort.SliceStable(bans, func(i, j int) bool { return bans[i].Since.After(bans[j].Since) })
	writeJSON(w, http.StatusOK, map[string]any{"jails": info.Jails, "bans": bans})
}

func (s *Server) handleBanAction(w http.ResponseWriter, r *http.Request) {
	if !s.accessReady(w) {
		return
	}
	var in struct {
		Action string `json:"action"` // ban | unban
		Jail   string `json:"jail"`
		IP     string `json:"ip"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx := r.Context()
	info, err := s.opts.Access.Info(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "controller", err.Error())
		return
	}
	if !slices.Contains(info.Jails, in.Jail) {
		writeError(w, http.StatusBadRequest, "bad_jail", "unknown jail")
		return
	}
	a, err := netip.ParseAddr(strings.TrimSpace(in.IP))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_ip", "fail2ban takes a single IP address")
		return
	}
	a = a.Unmap()
	switch in.Action {
	case "ban":
		if me, ok := s.myIP(r); ok && me == a {
			writeError(w, http.StatusConflict, "self", "this is your own address")
			return
		}
		err = s.opts.Access.Ctl.Ban(ctx, in.Jail, a.String())
	case "unban":
		err = s.opts.Access.Ctl.Unban(ctx, in.Jail, a.String())
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "action must be ban or unban")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "controller", err.Error())
		return
	}
	s.audit(ctx, s.actor(r), s.clientIP(r), "f2b."+in.Action, a.String(), in.Jail)
	w.WriteHeader(http.StatusNoContent)
}
