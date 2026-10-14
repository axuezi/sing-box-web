package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCycleBoundary(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		now  string
		day  int
		want string
	}{
		{"2026-10-13 12:00", 1, "2026-10-01 00:00"},
		{"2026-10-13 12:00", 13, "2026-10-13 00:00"},
		{"2026-10-13 12:00", 20, "2026-09-20 00:00"},
		{"2026-01-05 08:00", 10, "2025-12-10 00:00"},
		{"2026-03-01 00:00", 31, "2026-02-28 00:00"}, // clamped to 28
	}
	for _, c := range cases {
		if got := CycleBoundary(at(c.now), c.day, loc); !got.Equal(at(c.want)) {
			t.Errorf("CycleBoundary(%s, %d) = %v, want %s", c.now, c.day, got, c.want)
		}
	}
	if got := NextCycle(at("2026-10-13 12:00"), 20, loc); !got.Equal(at("2026-10-20 00:00")) {
		t.Errorf("NextCycle = %v", got)
	}
}

func TestLimitReason(t *testing.T) {
	now := time.Now()
	cases := []struct {
		u    User
		want string
	}{
		{User{}, ""},
		{User{ExpiresAt: now.Add(time.Hour)}, ""},
		{User{ExpiresAt: now}, LimitExpired},
		{User{QuotaBytes: 100, CycleUp: 40, CycleDown: 59}, ""},
		{User{QuotaBytes: 100, CycleUp: 40, CycleDown: 60}, LimitQuota},
		{User{QuotaBytes: 100, CycleUp: 100, ExpiresAt: now.Add(-time.Second)}, LimitExpired},
	}
	for i, c := range cases {
		if got := c.u.LimitReason(now); got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
}

func TestCycleCountersAndRollover(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u := &User{Name: "a", Enabled: true, Password: "p", UUID: "u", SubToken: "t", ResetDay: 1}
	if err := s.SaveUsers(ctx, []*User{u}, nil); err != nil {
		t.Fatal(err)
	}
	s.AddTraffic(ctx, []TrafficDelta{{Day: "2026-10-13", Kind: "user", Key: "1", Up: 10, Down: 20}})
	s.AddTraffic(ctx, []TrafficDelta{{Day: "2026-10-13", Kind: "user", Key: "1", Up: 1, Down: 2}})

	// SaveUsers must not clobber counters the collector maintains.
	u.Note = "edited"
	s.SaveUsers(ctx, []*User{u}, nil)
	got, _ := s.GetUser(ctx, 1)
	if got.CycleUp != 11 || got.CycleDown != 22 {
		t.Fatalf("counters: %d/%d", got.CycleUp, got.CycleDown)
	}

	// Within the cycle nothing changes; a month later the cycle restarts.
	if err := s.RollCycles(ctx, got.CycleStart.Add(time.Hour), time.UTC); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetUser(ctx, 1); got.CycleUsed() != 33 {
		t.Fatalf("rolled too early: %d", got.CycleUsed())
	}
	later := got.CycleStart.AddDate(0, 1, 1)
	s.RollCycles(ctx, later, time.UTC)
	got, _ = s.GetUser(ctx, 1)
	if got.CycleUsed() != 0 || !got.CycleStart.Equal(CycleBoundary(later, 1, time.UTC)) {
		t.Fatalf("after rollover: used=%d start=%v", got.CycleUsed(), got.CycleStart)
	}

	// An upgraded database (cycle_start = 0) keeps its seeded usage.
	s.db.Exec(`UPDATE users SET cycle_start = 0, cycle_up = 5`)
	s.RollCycles(ctx, later, time.UTC)
	got, _ = s.GetUser(ctx, 1)
	if got.CycleUp != 5 || got.CycleStart.IsZero() {
		t.Fatalf("seeded cycle: %+v", got)
	}
}
