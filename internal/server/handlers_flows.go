package server

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/axuezi/sing-box-web/internal/flows"
)

// maxFlowRows bounds one response; past it the smallest sites fold into
// one row with an empty site.
const maxFlowRows = 5000

type flowRowView struct {
	User     string `json:"user"`
	IP       string `json:"ip"`
	Site     string `json:"site"`
	Outbound string `json:"outbound"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Conns    int64  `json:"conns"`
}

// handleFlows returns traffic per (user, client IP, site, outbound) summed
// over a date range.
func (s *Server) handleFlows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.ParseInLocation(time.DateOnly, q.Get("from"), time.Local)
	to, err2 := time.ParseInLocation(time.DateOnly, q.Get("to"), time.Local)
	if err1 != nil || err2 != nil || to.Before(from) {
		writeError(w, http.StatusBadRequest, "bad_request", "from and to must be dates (YYYY-MM-DD), from <= to")
		return
	}
	ctx := r.Context()
	rows, err := s.opts.Store.FlowsBetween(ctx, from.Format(time.DateOnly)+" 00", to.Format(time.DateOnly)+" 23")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	first, err := s.opts.Store.FlowsSpan(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	enabled, _ := s.opts.Store.GetSetting(ctx, flows.SettingKey)

	out := make([]flowRowView, len(rows))
	for i, x := range rows {
		out[i] = flowRowView{x.User, x.IP, x.Site, x.Outbound, x.Up, x.Down, x.Conns}
	}
	truncated := false
	if len(out) > maxFlowRows {
		out, truncated = foldSmallSites(out), true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": enabled == "1", "available": s.opts.Flows != nil,
		"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly),
		"first_hour": first, "keep_days": flows.KeepDays, "truncated": truncated, "rows": out,
	})
}

// foldSmallSites keeps the biggest sites' rows and merges the rest per
// (user, IP, outbound) under site "".
func foldSmallSites(rows []flowRowView) []flowRowView {
	bySite := map[string]int64{}
	count := map[string]int{}
	for _, x := range rows {
		bySite[x.Site] += x.Up + x.Down
		count[x.Site]++
	}
	sites := make([]string, 0, len(bySite))
	for k := range bySite {
		sites = append(sites, k)
	}
	slices.SortFunc(sites, func(a, b string) int { return cmp.Compare(bySite[b], bySite[a]) })
	keep := map[string]bool{}
	n := 0
	for _, site := range sites {
		c := count[site]
		if n+c > maxFlowRows/2 {
			break
		}
		keep[site], n = true, n+c
	}
	out := []flowRowView{}
	rest := map[[3]string]*flowRowView{}
	for _, x := range rows {
		if keep[x.Site] {
			out = append(out, x)
			continue
		}
		k := [3]string{x.User, x.IP, x.Outbound}
		if p := rest[k]; p != nil {
			p.Up, p.Down, p.Conns = p.Up+x.Up, p.Down+x.Down, p.Conns+x.Conns
		} else {
			x.Site = ""
			rest[k] = &x
		}
	}
	for _, p := range rest {
		out = append(out, *p)
	}
	return out
}

func (s *Server) handlePutFlowsSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v := "0"
	if body.Enabled {
		v = "1"
	}
	if err := s.opts.Store.SetSetting(r.Context(), flows.SettingKey, v); err != nil {
		s.internalError(w, r, err)
		return
	}
	if s.opts.Flows != nil {
		s.opts.Flows.Refresh(r.Context())
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "settings.update", "flows", "enabled="+strconv.FormatBool(body.Enabled))
	writeJSON(w, http.StatusOK, map[string]any{"enabled": body.Enabled})
}

func (s *Server) handleClearFlows(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Store.ClearFlows(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "flows.clear", "", "")
	w.WriteHeader(http.StatusNoContent)
}
