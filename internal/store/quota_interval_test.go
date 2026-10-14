package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestQuotaCrossingIntervalAndRolloverOrder(t *testing.T) {
	for _, rollFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "sample-first", true: "enforcer-first"}[rollFirst], func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			boundary := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			s.now = func() time.Time { return boundary.Add(-time.Hour) }
			u := &User{Name: "a", SubToken: "t", ResetDay: 1}
			if err := s.SaveUsers(ctx, []*User{u}, nil); err != nil {
				t.Fatal(err)
			}
			s.AddTraffic(ctx, []TrafficDelta{{Day: "2026-10-31", Kind: "user", Key: "1", Down: 1000}})
			if rollFirst {
				if err := s.RollCycles(ctx, boundary.Add(30*time.Second), time.UTC); err != nil {
					t.Fatal(err)
				}
			}
			d := TrafficDelta{Day: "2026-11-01", Kind: "user", Key: "1", Down: 600, From: boundary.Add(-30 * time.Second), To: boundary.Add(30 * time.Second), Location: time.UTC}
			if err := s.AddTrafficAndSet(ctx, []TrafficDelta{d}, "baseline", "next"); err != nil {
				t.Fatal(err)
			}
			if err := s.RollCycles(ctx, boundary.Add(time.Minute), time.UTC); err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetUser(ctx, 1)
			if got.CycleDown != 300 {
				t.Fatalf("new cycle=%d, want 300", got.CycleDown)
			}
			rows, _ := s.Traffic(ctx, "2026-10-01")
			var total int64
			for _, r := range rows {
				total += r.Down
			}
			if total != 1600 {
				t.Fatalf("history=%d", total)
			}
		})
	}
}
func TestManualResetKeepsOnlyPostResetShare(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at.Add(-time.Hour) }
	if err := s.SaveUsers(ctx, []*User{{Name: "a", SubToken: "t", ResetDay: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetCycle(ctx, 1, at); err != nil {
		t.Fatal(err)
	}
	d := TrafficDelta{Day: "2026-10-13", Kind: "user", Key: "1", Up: 120, From: at.Add(-time.Minute), To: at.Add(time.Minute), Location: time.UTC}
	if err := s.AddTraffic(ctx, []TrafficDelta{d}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser(ctx, 1)
	if got.CycleUp != 60 {
		t.Fatalf("usage=%d", got.CycleUp)
	}
	// A failed multi-step reset rolls back both the reset and other changes.
	err = s.Transaction(ctx, func(ctx context.Context) error {
		if err := s.ResetCycle(ctx, 1, at.Add(time.Hour)); err != nil {
			return err
		}
		if err := s.SetSetting(ctx, "subscription", "new"); err != nil {
			return err
		}
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	got, _ = s.GetUser(ctx, 1)
	if got.CycleUp != 60 {
		t.Fatal("reset escaped transaction")
	}
}
func TestBaselineRollsBackWithTraffic(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetSetting(ctx, "baseline", "old")
	// Fail quota attribution after the history INSERT, before advancing baseline.
	err = s.AddTrafficAndSet(ctx, []TrafficDelta{{Day: "2026-10-13", Kind: "user", Key: "999", Down: 42, From: time.Now().Add(-time.Minute), To: time.Now()}}, "baseline", "new")
	if err == nil {
		t.Fatal("expected missing-user error")
	}
	v, _ := s.GetSetting(ctx, "baseline")
	rows, _ := s.Traffic(ctx, "2000-01-01")
	if v != "old" || len(rows) != 0 {
		t.Fatal("partial sampling transaction")
	}
}

func TestSamplingWaitsUntilUsageMutationFinishes(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return at.Add(-time.Hour) }
	if err := s.SaveUsers(ctx, []*User{{Name: "a", SubToken: "t", ResetDay: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	locked, unlock := s.LockUsage(ctx)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- s.AddTrafficAndSet(ctx, []TrafficDelta{{Day: "2026-10-13", Kind: "user", Key: "1", Down: 100, From: at, To: at.Add(time.Minute), Location: time.UTC}}, "baseline", "new")
	}()
	<-started
	select {
	case err := <-done:
		unlock()
		t.Fatalf("sampling escaped usage lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	err = s.Transaction(locked, func(ctx context.Context) error { return s.ResetCycle(ctx, 1, at) })
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sampling deadlocked")
	}
	got, _ := s.GetUser(ctx, 1)
	if got.CycleDown != 100 {
		t.Fatalf("post-reset sampling lost: %d", got.CycleDown)
	}
}
