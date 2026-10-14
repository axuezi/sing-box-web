package store

import (
	"context"
	"time"
)

// Reasons the panel takes a user out of the config.
const (
	LimitExpired = "expired"
	LimitQuota   = "quota"
)

// Active reports whether the user belongs in the config: enabled by the
// administrator and not limited by the panel.
func (u User) Active() bool { return u.Enabled && u.Limited == "" }

// CycleUsed is the traffic counted against the quota.
func (u User) CycleUsed() int64 { return u.CycleUp + u.CycleDown }

// LimitReason returns why the user should be limited at now, or "".
func (u User) LimitReason(now time.Time) string {
	switch {
	case !u.ExpiresAt.IsZero() && !now.Before(u.ExpiresAt):
		return LimitExpired
	case u.QuotaBytes > 0 && u.CycleUsed() >= u.QuotaBytes:
		return LimitQuota
	}
	return ""
}

// ClampResetDay keeps a reset day within 1-28 so every month has it.
func ClampResetDay(d int) int {
	return min(max(d, 1), 28)
}

// CycleBoundary is the start of the monthly cycle that contains now: the
// most recent resetDay at 00:00 in loc.
func CycleBoundary(now time.Time, resetDay int, loc *time.Location) time.Time {
	now = now.In(loc)
	b := time.Date(now.Year(), now.Month(), ClampResetDay(resetDay), 0, 0, 0, 0, loc)
	if b.After(now) {
		b = b.AddDate(0, -1, 0)
	}
	return b
}

// NextCycle is when the cycle that contains now ends.
func NextCycle(now time.Time, resetDay int, loc *time.Location) time.Time {
	return CycleBoundary(now, resetDay, loc).AddDate(0, 1, 0)
}

// RollCycles starts a new cycle, with zeroed usage, for every user whose
// cycle began before the latest reset boundary. A user that has no cycle yet
// (upgraded database) gets the current boundary without losing the usage the
// migration seeded.
func (s *Store) RollCycles(ctx context.Context, now time.Time, loc *time.Location) error {
	ctx, unlock := s.LockUsage(ctx)
	defer unlock()
	return s.Transaction(ctx, func(ctx context.Context) error {
		users, err := s.ListUsers(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			b := CycleBoundary(now, u.ResetDay, loc).Unix()
			old := unixOrZero(u.CycleStart)
			// The cycle_start guard makes each update a compare-and-swap, so a
			// concurrent manual reset is never undone.
			switch {
			case old == 0:
				_, err = s.executor(ctx).ExecContext(ctx, `UPDATE users SET cycle_start = ? WHERE id = ? AND cycle_start = 0`, b, u.ID)
			case old < b:
				_, err = s.executor(ctx).ExecContext(ctx, `
				UPDATE users SET cycle_start = ?, cycle_up = 0, cycle_down = 0 WHERE id = ? AND cycle_start = ?`, b, u.ID, old)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ResetCycle zeroes a user's usage and starts their cycle at at.
func (s *Store) ResetCycle(ctx context.Context, id int64, at time.Time) error {
	_, err := s.executor(ctx).ExecContext(ctx, `UPDATE users SET cycle_start = ?, cycle_up = 0, cycle_down = 0 WHERE id = ?`, at.Unix(), id)
	return err
}
