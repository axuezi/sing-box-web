package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/store"
)

func TestTrafficAnalysis(t *testing.T) {
	m := newConfigEnv(t)
	ctx := context.Background()
	now := time.Now()
	day := func(offset int) string { return now.AddDate(0, 0, offset).Format(time.DateOnly) }
	hour := func(offset, h int) string { return day(offset) + fmt.Sprintf(" %02d", h) }

	m.call("POST", "/api/users", `{"name":"alice"}`) // id 2
	m.call("POST", "/api/users", `{"name":"gone"}`)  // id 3, deleted below
	var deltas []store.TrafficDelta
	for _, d := range []struct {
		off       int
		h         int
		kind, key string
		up, down  int64
	}{
		{0, 0, "user", "1", 10, 100}, {0, 0, "user", "2", 1, 5},
		{-1, 0, "user", "1", 20, 200}, {-1, 5, "user", "1", 1, 1},
		{-7, 0, "user", "1", 7, 70}, {-7, 0, "user", "3", 3, 30}, {-7, 23, "user", "1", 1000, 1000},
		{-8, 0, "user", "2", 2, 2},
		{0, 0, "inbound", "hy2-in", 5, 50}, {0, 0, "inbound", "old-in", 1, 1},
	} {
		deltas = append(deltas, store.TrafficDelta{Day: day(d.off), Hour: hour(d.off, d.h), Kind: d.kind, Key: d.key, Up: d.up, Down: d.down})
	}
	if err := m.st.AddTraffic(ctx, deltas); err != nil {
		t.Fatal(err)
	}
	m.call("DELETE", "/api/users/3", "")

	// Last 7 days vs the 7 before. The 23:00 row on the comparison day only
	// counts when the test runs in the 23rd hour.
	wantCmp, wantCmpDay := int64(70), "[7,70]"
	if now.Hour() == 23 {
		wantCmp, wantCmpDay = 1070, "[1007,1070]"
	}
	code, b := m.call("GET", fmt.Sprintf("/api/stats/analysis?kind=user&gran=day&from=%s&to=%s&cmp_from=%s", day(-6), day(0), day(-13)), "")
	if code != 200 || b.Get("range.buckets.#").Int() != 7 || b.Get("cmp.to").String() != day(-7) {
		t.Fatalf("day analysis: %d %s", code, b.Raw)
	}
	owner := b.Get(`items.#(name=="owner")`)
	if owner.Get("down").Int() != 301 || owner.Get("series.6").Raw != "[10,100]" || owner.Get("series.5").Raw != "[21,201]" ||
		owner.Get("cmp.down").Int() != wantCmp || owner.Get("cmp.series.6").Raw != wantCmpDay {
		t.Fatalf("owner: %s", owner.Raw)
	}
	// Deleting a user removes its history too.
	if g := b.Get(`items.#(key=="3")`); g.Exists() {
		t.Fatalf("deleted user still has history: %s", g.Raw)
	}
	if a := b.Get(`items.#(name=="alice")`); a.Get("up").Int() != 1 || a.Get("cmp.up").Int() != 2 {
		t.Fatalf("alice: %s", a.Raw)
	}
	// Today has run only until now: the comparison day counts up to the same hour.
	if !b.Get("cmp_partial_today").Bool() {
		t.Fatalf("today not trimmed: %s", b.Raw)
	}
	if b.Get("items.0.name").String() != "owner" || b.Get("first_day").String() != day(-8) {
		t.Fatalf("order / span: %s", b.Raw)
	}

	// Today by hour vs yesterday by hour; future hours are left out.
	_, b = m.call("GET", fmt.Sprintf("/api/stats/analysis?kind=user&gran=hour&from=%s&to=%s&cmp_from=%s", day(0), day(0), day(-1)), "")
	if n := b.Get("range.buckets.#").Int(); n != int64(now.Hour()+1) || b.Get("cmp.buckets.#").Int() != 24 {
		t.Fatalf("hour buckets: %d / %d", n, b.Get("cmp.buckets.#").Int())
	}
	owner = b.Get(`items.#(name=="owner")`)
	if owner.Get("series.0").Raw != "[10,100]" || owner.Get("cmp.series.5").Raw != "[1,1]" {
		t.Fatalf("hourly owner: %s", owner.Raw)
	}

	// Months, and inbounds no longer in the config.
	_, b = m.call("GET", fmt.Sprintf("/api/stats/analysis?kind=inbound&gran=month&from=%s&to=%s", day(-40), day(0)), "")
	if b.Get("range.buckets.#").Int() < 2 || b.Get(`items.#(key=="hy2-in").gone`).Bool() || !b.Get(`items.#(key=="old-in").gone`).Bool() ||
		b.Get(`items.#(key=="hy2-in").down`).Int() != 50 {
		t.Fatalf("month inbound: %s", b.Raw)
	}

	for _, bad := range []string{
		"kind=x&gran=day&from=2026-01-01&to=2026-01-02",
		"kind=user&gran=week&from=2026-01-01&to=2026-01-02",
		"kind=user&gran=day&from=2026-01-03&to=2026-01-02",
		"kind=user&gran=hour&from=2026-01-01&to=2026-01-20",
		"kind=user&gran=day&from=2024-01-01&to=2026-01-20",
		"kind=user&gran=day&from=2026-01-01&to=2026-01-02&cmp_from=yesterday",
	} {
		if code, _ := m.call("GET", "/api/stats/analysis?"+bad, ""); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

func TestTrafficSeries(t *testing.T) {
	m := newConfigEnv(t)
	today := time.Now().Format(time.DateOnly)
	yesterday := time.Now().AddDate(0, 0, -1).Format(time.DateOnly)
	m.st.AddTraffic(context.Background(), []store.TrafficDelta{
		{Day: today, Kind: "user", Key: "1", Up: 100, Down: 1000},
		{Day: yesterday, Kind: "user", Key: "1", Up: 5, Down: 50},
		{Day: today, Kind: "user", Key: "99", Up: 1, Down: 1}, // deleted user: hidden
		{Day: today, Kind: "inbound", Key: "hy2-in", Up: 7, Down: 70},
	})
	code, body := m.call("GET", "/api/stats/traffic?days=7", "")
	if code != 200 || body.Get("days.#").Int() != 7 || body.Get("days.6").String() != today {
		t.Fatalf("stats: %d %s", code, body.Raw)
	}
	owner := body.Get(`series.#(kind=="user")`)
	if owner.Get("name").String() != "owner" || owner.Get("today.down").Int() != 1000 || owner.Get("total.down").Int() != 1050 ||
		owner.Get("daily.6.up").Int() != 100 || owner.Get("daily.5.down").Int() != 50 {
		t.Fatalf("owner series: %s", owner.Raw)
	}
	if strings.Contains(body.Raw, `"key":"99"`) {
		t.Fatal("deleted user shown")
	}
	_, body = m.call("GET", "/api/users", "")
	if body.Get("users.0.month_down").Int() < 1000 {
		t.Fatalf("users month traffic: %s", body.Raw)
	}
}
