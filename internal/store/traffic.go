package store

import (
	"context"
	"math/bits"
	"time"
)

// TrafficDelta is bytes to add to one day's counter.
type TrafficDelta struct {
	From, To time.Time // sampling interval; zero for legacy/manual adjustments
	Location *time.Location
	Day      string // YYYY-MM-DD
	Hour     string // YYYY-MM-DD HH; "" = daily only
	Kind     string // user | inbound | outbound
	Key      string
	Up       int64
	Down     int64
}

// AddTraffic adds deltas in one transaction.
func (s *Store) AddTraffic(ctx context.Context, deltas []TrafficDelta) error {
	return s.AddTrafficAndSet(ctx, deltas, "", "")
}

// AddTrafficAndSet adds deltas and, if key is not empty, stores a setting
// in the same transaction (the collector's counter baseline, so the two
// never disagree).
func (s *Store) AddTrafficAndSet(ctx context.Context, deltas []TrafficDelta, key, value string) error {
	if len(deltas) == 0 && key == "" {
		return nil
	}
	ctx, unlock := s.LockUsage(ctx)
	defer unlock()
	return s.Transaction(ctx, func(ctx context.Context) error {
		tx := s.executor(ctx)
		now := s.now().Unix()
		for _, d := range deltas {
			if !d.To.IsZero() {
				loc := d.Location
				if loc == nil {
					loc = time.Local
				}
				if err := s.RollCycles(ctx, d.To, loc); err != nil {
					return err
				}
				break
			}
		}
		for _, d := range deltas {
			if _, err := tx.ExecContext(ctx, `
			INSERT INTO traffic_daily (day, kind, key, up, down) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (day, kind, key) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
				d.Day, d.Kind, d.Key, d.Up, d.Down); err != nil {
				return err
			}
			if d.Hour != "" {
				if _, err := tx.ExecContext(ctx, `
				INSERT INTO traffic_hourly (hour, kind, key, up, down) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (hour, kind, key) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
					d.Hour, d.Kind, d.Key, d.Up, d.Down); err != nil {
					return err
				}
			}
			if d.Kind == "user" {
				cycleUp, cycleDown := d.Up, d.Down
				if !d.To.IsZero() {
					var start int64
					if err := tx.QueryRowContext(ctx, "SELECT cycle_start FROM users WHERE id = CAST(? AS INTEGER)", d.Key).Scan(&start); err != nil {
						return err
					}
					cycleUp = intervalShare(d.Up, d.From, d.To, time.Unix(start, 0))
					cycleDown = intervalShare(d.Down, d.From, d.To, time.Unix(start, 0))
				}
				if _, err := tx.ExecContext(ctx, `
				UPDATE users SET cycle_up = cycle_up + ?, cycle_down = cycle_down + ?,
					last_active_at = CASE WHEN ? > 0 THEN ? ELSE last_active_at END
				WHERE id = CAST(? AS INTEGER)`,
					cycleUp, cycleDown, d.Up+d.Down, now, d.Key); err != nil {
					return err
				}
			}
		}
		if key != "" {
			if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
				return err
			}
		}
		return nil
	})
}

// TrafficRow is one day's counter.
type TrafficRow struct {
	Day  string `json:"day"`
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// Traffic returns rows with day >= since, oldest first.
func (s *Store) Traffic(ctx context.Context, since string) ([]TrafficRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, kind, key, up, down FROM traffic_daily WHERE day >= ? ORDER BY day, kind, key`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficRow{}
	for rows.Next() {
		var r TrafficRow
		if err := rows.Scan(&r.Day, &r.Kind, &r.Key, &r.Up, &r.Down); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneTraffic drops days before cutoff and hours before hourCutoff.
func (s *Store) PruneTraffic(ctx context.Context, cutoff, hourCutoff string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM traffic_daily WHERE day < ?`, cutoff); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM traffic_hourly WHERE hour < ?`, hourCutoff)
	return err
}

// TrafficPoint is one bucket of one key's traffic.
type TrafficPoint struct {
	Bucket string // a day (YYYY-MM-DD) or an hour (YYYY-MM-DD HH)
	Key    string
	Up     int64
	Down   int64
}

// TrafficBetween returns one kind's daily (hourly = false) or hourly rows
// with from <= bucket <= to, compared as strings.
func (s *Store) TrafficBetween(ctx context.Context, kind string, hourly bool, from, to string) ([]TrafficPoint, error) {
	q := `SELECT day, key, up, down FROM traffic_daily WHERE kind = ? AND day >= ? AND day <= ?`
	if hourly {
		q = `SELECT hour, key, up, down FROM traffic_hourly WHERE kind = ? AND hour >= ? AND hour <= ?`
	}
	rows, err := s.db.QueryContext(ctx, q, kind, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficPoint{}
	for rows.Next() {
		var p TrafficPoint
		if err := rows.Scan(&p.Bucket, &p.Key, &p.Up, &p.Down); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TrafficSpan returns the first day with daily data and the first hour with
// hourly data ("" when there is none).
func (s *Store) TrafficSpan(ctx context.Context) (firstDay, firstHour string, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(day), '') FROM traffic_daily`).Scan(&firstDay); err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(hour), '') FROM traffic_hourly`).Scan(&firstHour)
	return
}

// SubAccess is one subscription fetch.
type SubAccess struct {
	At        time.Time `json:"at"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Format    string    `json:"format"`
}

// LogSubAccess records a subscription fetch and keeps the newest 50 per user.
func (s *Store) LogSubAccess(ctx context.Context, userID int64, a SubAccess) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sub_access (user_id, at, ip, user_agent, format) VALUES (?, ?, ?, ?, ?)`,
		userID, s.now().Unix(), a.IP, a.UserAgent, a.Format); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM sub_access WHERE user_id = ? AND id NOT IN
			(SELECT id FROM sub_access WHERE user_id = ? ORDER BY id DESC LIMIT 50)`, userID, userID)
	return err
}

// ListSubAccess returns a user's recent subscription fetches, newest first.
func (s *Store) ListSubAccess(ctx context.Context, userID int64) ([]SubAccess, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT at, ip, user_agent, format FROM sub_access WHERE user_id = ? ORDER BY id DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubAccess{}
	for rows.Next() {
		var a SubAccess
		var at int64
		if err := rows.Scan(&at, &a.IP, &a.UserAgent, &a.Format); err != nil {
			return nil, err
		}
		a.At = time.Unix(at, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// TOTP state of the administrator.
type TOTP struct {
	Secret   string // base32; empty = disabled
	LastStep int64  // last accepted time step, to refuse replays
}

// GetTOTP returns the administrator's TOTP state.
func (s *Store) GetTOTP(ctx context.Context) (TOTP, error) {
	var t TOTP
	err := s.db.QueryRowContext(ctx, `SELECT totp_secret, totp_last_step FROM admin WHERE id = 1`).Scan(&t.Secret, &t.LastStep)
	return t, err
}

// SetTOTP enables (secret != "") or disables TOTP.
func (s *Store) SetTOTP(ctx context.Context, secret string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admin SET totp_secret = ?, totp_last_step = 0 WHERE id = 1`, secret)
	return err
}

// UseTOTPStep records step as used if it is newer than the last one; it
// reports false for a replayed code.
func (s *Store) UseTOTPStep(ctx context.Context, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE admin SET totp_last_step = ? WHERE id = 1 AND totp_last_step < ?`, step, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Counter samples cannot identify exact byte times. Apportion a crossing
// interval by elapsed time, preserving full historical totals outside quota usage.
func intervalShare(value int64, from, to, boundary time.Time) int64 {
	if value <= 0 || !boundary.After(from) || from.IsZero() {
		return value
	}
	if !boundary.Before(to) {
		return 0
	}
	hi, lo := bits.Mul64(uint64(value), uint64(to.Sub(boundary)))
	q, _ := bits.Div64(hi, lo, uint64(to.Sub(from)))
	return int64(q)
}
