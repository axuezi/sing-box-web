package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/traffic"
)

// Limits on what one analysis request may ask for.
const (
	maxAnalysisDays = 400 // traffic_daily retention
	maxHourlyDays   = 14  // 336 hourly buckets
	hourLayout      = "2006-01-02 15"
	monthLayout     = "2006-01"
)

// analysisRange is a run of buckets: hours, days or months.
type analysisRange struct {
	From    string   `json:"from"` // YYYY-MM-DD
	To      string   `json:"to"`
	Buckets []string `json:"buckets"`
}

// buckets lists the buckets of [from, from+days) at gran, leaving out any
// that start after now (no data can exist there yet).
func buckets(from time.Time, days int, gran string, now time.Time) []string {
	out := []string{}
	switch gran {
	case "hour":
		for t := from; t.Before(from.AddDate(0, 0, days)) && !t.After(now); t = t.Add(time.Hour) {
			out = append(out, t.Format(hourLayout))
		}
	case "day":
		for i := range days {
			d := from.AddDate(0, 0, i)
			if d.After(now) {
				break
			}
			out = append(out, d.Format(time.DateOnly))
		}
	case "month":
		end := from.AddDate(0, 0, days-1)
		for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location()); !m.After(end) && !m.After(now); m = m.AddDate(0, 1, 0) {
			out = append(out, m.Format(monthLayout))
		}
	}
	return out
}

type analysisSeries struct {
	Up     int64      `json:"up"`
	Down   int64      `json:"down"`
	Series [][2]int64 `json:"series"` // per bucket: [up, down]
}

type analysisItem struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Gone is true for a user that was deleted or an inbound/outbound no
	// longer in the config; its history still counts.
	Gone bool `json:"gone"`
	analysisSeries
	Cmp *analysisSeries `json:"cmp,omitempty"`
}

// fill sums rows into per-key series over rng's buckets.
func fill(rows []store.TrafficPoint, rng analysisRange, gran string) map[string]*analysisSeries {
	index := map[string]int{}
	for i, b := range rng.Buckets {
		index[b] = i
	}
	out := map[string]*analysisSeries{}
	for _, r := range rows {
		b := r.Bucket
		if gran == "month" {
			b = b[:7]
		}
		i, ok := index[b]
		if !ok {
			continue
		}
		s := out[r.Key]
		if s == nil {
			s = &analysisSeries{Series: make([][2]int64, len(rng.Buckets))}
			out[r.Key] = s
		}
		s.Series[i][0] += r.Up
		s.Series[i][1] += r.Down
		s.Up += r.Up
		s.Down += r.Down
	}
	return out
}

func (s *Server) loadRange(ctx context.Context, kind, gran string, rng analysisRange) (map[string]*analysisSeries, error) {
	if len(rng.Buckets) == 0 {
		return map[string]*analysisSeries{}, nil
	}
	hourly := gran == "hour"
	from, to := rng.From, rng.To
	if hourly {
		from, to = from+" 00", to+" 23"
	}
	rows, err := s.opts.Store.TrafficBetween(ctx, kind, hourly, from, to)
	if err != nil {
		return nil, err
	}
	return fill(rows, rng, gran), nil
}

// handleTrafficAnalysis returns per-user, per-inbound or per-outbound
// traffic over a date range, optionally beside a comparison range of the
// same length whose buckets line up by position.
func (s *Server) handleTrafficAnalysis(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, gran := q.Get("kind"), q.Get("gran")
	if !slices.Contains([]string{"user", "inbound", "outbound"}, kind) {
		writeError(w, http.StatusBadRequest, "bad_request", "kind must be user, inbound or outbound")
		return
	}
	if !slices.Contains([]string{"hour", "day", "month"}, gran) {
		writeError(w, http.StatusBadRequest, "bad_request", "gran must be hour, day or month")
		return
	}
	from, err1 := time.ParseInLocation(time.DateOnly, q.Get("from"), time.Local)
	to, err2 := time.ParseInLocation(time.DateOnly, q.Get("to"), time.Local)
	if err1 != nil || err2 != nil || to.Before(from) {
		writeError(w, http.StatusBadRequest, "bad_request", "from and to must be dates (YYYY-MM-DD), from <= to")
		return
	}
	days := int(to.Sub(from).Hours()/24+0.5) + 1
	if days > maxAnalysisDays || (gran == "hour" && days > maxHourlyDays) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"range too long: at most "+strconv.Itoa(maxHourlyDays)+" days by hour, "+strconv.Itoa(maxAnalysisDays)+" days otherwise")
		return
	}
	now := time.Now()
	cur := analysisRange{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Buckets: buckets(from, days, gran, now)}

	var cmp *analysisRange
	if c := q.Get("cmp_from"); c != "" {
		cf, err := time.ParseInLocation(time.DateOnly, c, time.Local)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "cmp_from must be a date (YYYY-MM-DD)")
			return
		}
		cmp = &analysisRange{From: cf.Format(time.DateOnly), To: cf.AddDate(0, 0, days-1).Format(time.DateOnly),
			Buckets: buckets(cf, days, gran, now)}
	}

	ctx := r.Context()
	curData, err := s.loadRange(ctx, kind, gran, cur)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cmpData := map[string]*analysisSeries{}
	partial := false
	if cmp != nil {
		if cmpData, err = s.loadRange(ctx, kind, gran, *cmp); err != nil {
			s.internalError(w, r, err)
			return
		}
		if partial, err = s.trimToday(ctx, kind, gran, cur, *cmp, cmpData, now); err != nil {
			s.internalError(w, r, err)
			return
		}
	}

	// Known entities in a stable order (users by id, tags by config order),
	// so the client can colour each one the same way on every view.
	type entity struct{ key, name string }
	var known []entity
	switch kind {
	case "user":
		users, err := s.opts.Store.ListUsers(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, u := range users {
			known = append(known, entity{strconv.FormatInt(u.ID, 10), u.Name})
		}
	default:
		if f, err := singbox.ReadFile(s.opts.SingboxConfig); err == nil {
			gjson.GetBytes(f.Raw, kind+"s").ForEach(func(_, v gjson.Result) bool {
				if tag := v.Get("tag").String(); tag != "" {
					known = append(known, entity{tag, tag})
				}
				return true
			})
		}
	}
	items := []analysisItem{}
	seen := map[string]bool{}
	add := func(key, name string, gone bool) {
		c, p := curData[key], cmpData[key]
		if seen[key] || (c == nil && p == nil) {
			return
		}
		seen[key] = true
		it := analysisItem{Key: key, Name: name, Gone: gone}
		if c != nil {
			it.analysisSeries = *c
		} else {
			it.Series = make([][2]int64, len(cur.Buckets))
		}
		if cmp != nil {
			if p == nil {
				p = &analysisSeries{Series: make([][2]int64, len(cmp.Buckets))}
			}
			it.Cmp = p
		}
		items = append(items, it)
	}
	for _, e := range known {
		add(e.key, e.name, false)
	}
	var rest []string
	for k := range curData {
		rest = append(rest, k)
	}
	for k := range cmpData {
		rest = append(rest, k)
	}
	slices.Sort(rest)
	for _, k := range rest {
		add(k, k, true)
	}

	firstDay, firstHour, err := s.opts.Store.TrafficSpan(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": kind, "gran": gran, "range": cur, "cmp": cmp, "items": items, "cmp_partial_today": partial,
		"first_day": firstDay, "first_hour": firstHour, "now": now.Format(hourLayout),
	})
}

// trimToday makes a daily comparison like for like when the current range
// ends today: today has only run until now, so the comparison day at the
// same position counts only up to the same hour (from hourly data). It
// reports whether it trimmed.
func (s *Server) trimToday(ctx context.Context, kind, gran string, cur, cmp analysisRange,
	data map[string]*analysisSeries, now time.Time) (bool, error) {
	i := len(cur.Buckets) - 1
	if gran != "day" || i < 0 || cur.Buckets[i] != now.Format(time.DateOnly) || i >= len(cmp.Buckets) {
		return false, nil
	}
	day := cmp.Buckets[i]
	rows, err := s.opts.Store.TrafficBetween(ctx, kind, true, day+" 00", day+now.Format(" 15"))
	if err != nil || len(rows) == 0 {
		return false, err // no hourly data for that day: leave the whole day
	}
	part := map[string][2]int64{}
	for _, r := range rows {
		p := part[r.Key]
		part[r.Key] = [2]int64{p[0] + r.Up, p[1] + r.Down}
	}
	for key, sr := range data {
		old, p := sr.Series[i], part[key]
		sr.Series[i] = p
		sr.Up += p[0] - old[0]
		sr.Down += p[1] - old[1]
	}
	return true, nil
}

type dayTotal struct {
	Day  string `json:"day"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

type seriesView struct {
	Kind  string     `json:"kind"`
	Key   string     `json:"key"`
	Name  string     `json:"name"`
	Daily []dayTotal `json:"daily"`
	Today dayTotal   `json:"today"`
	Month dayTotal   `json:"month"`
	Total dayTotal   `json:"total"` // over the requested range
}

// trafficSeries returns per-user/inbound/outbound daily series for the
// last `days` days (dense, zero-filled), with today and this-month totals.
func (s *Server) trafficSeries(ctx context.Context, days int) ([]string, []seriesView, error) {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	from := now.AddDate(0, 0, -(days - 1))
	if monthStart.Before(from) {
		from = monthStart
	}
	rows, err := s.opts.Store.Traffic(ctx, from.Format(time.DateOnly))
	if err != nil {
		return nil, nil, err
	}
	axis := make([]string, days)
	for i := range days {
		axis[i] = now.AddDate(0, 0, i-(days-1)).Format(time.DateOnly)
	}
	axisStart := axis[0]
	today, month := now.Format(time.DateOnly), monthStart.Format(time.DateOnly)

	users, err := s.opts.Store.ListUsers(ctx)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for _, u := range users {
		names["user/"+strconv.FormatInt(u.ID, 10)] = u.Name
	}

	byKey := map[string]*seriesView{}
	var order []string
	get := func(kind, key string) *seriesView {
		k := kind + "/" + key
		if v, ok := byKey[k]; ok {
			return v
		}
		name := key
		if kind == "user" {
			name = names[k]
		}
		v := &seriesView{Kind: kind, Key: key, Name: name, Daily: make([]dayTotal, days)}
		for i, d := range axis {
			v.Daily[i].Day = d
		}
		byKey[k] = v
		order = append(order, k)
		return v
	}
	// Every user appears, even with no traffic yet.
	for _, u := range users {
		get("user", strconv.FormatInt(u.ID, 10))
	}
	for _, r := range rows {
		if r.Kind == "user" && names["user/"+r.Key] == "" {
			continue // deleted user
		}
		v := get(r.Kind, r.Key)
		if r.Day >= axisStart {
			for i := range v.Daily {
				if v.Daily[i].Day == r.Day {
					v.Daily[i].Up += r.Up
					v.Daily[i].Down += r.Down
				}
			}
			v.Total.Up += r.Up
			v.Total.Down += r.Down
		}
		if r.Day >= month {
			v.Month.Up += r.Up
			v.Month.Down += r.Down
		}
		if r.Day == today {
			v.Today.Up += r.Up
			v.Today.Down += r.Down
		}
	}
	out := make([]seriesView, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return axis, out, nil
}

func (s *Server) handleTrafficStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 366 {
		days = 30
	}
	if r.URL.Query().Get("flush") == "1" && s.opts.Collector != nil {
		_ = s.opts.Collector.Flush(r.Context())
	}
	axis, series, err := s.trafficSeries(r.Context(), days)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	resp := map[string]any{"days": axis, "series": series}
	if s.opts.Collector != nil {
		last, cerr := s.opts.Collector.Status()
		col := map[string]any{"last_run": last}
		if cerr != nil {
			col["error"] = cerr.Error()
			if errors.Is(cerr, traffic.ErrNoStatsAPI) {
				col["code"] = "no_v2ray_api"
			}
		}
		resp["collector"] = col
	}
	writeJSON(w, http.StatusOK, resp)
}
