package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrConflict is returned when a unique constraint is violated.
var ErrConflict = errors.New("already exists")

// User is a proxy user. Credentials are shared across the inbounds the user
// is enabled on: Password for password-based protocols, UUID for UUID-based.
type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	Enabled   bool      `json:"enabled"`
	Inbounds  []string  `json:"inbounds"`
	Password  string    `json:"password"`
	UUID      string    `json:"uuid"`
	SubToken  string    `json:"sub_token"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastSubAt time.Time `json:"last_sub_at,omitzero"`

	// ExpiresAt (zero = never) and QuotaBytes (up + down per cycle, 0 =
	// unlimited) limit the user; the cycle restarts monthly on ResetDay.
	ExpiresAt  time.Time `json:"expires_at,omitzero"`
	QuotaBytes int64     `json:"quota_bytes"`
	ResetDay   int       `json:"reset_day"`
	// CycleStart, CycleUp and CycleDown are maintained by the store; SaveUsers
	// never writes them.
	CycleStart time.Time `json:"cycle_start,omitzero"`
	CycleUp    int64     `json:"cycle_up"`
	CycleDown  int64     `json:"cycle_down"`
	// Limited is why the panel took the user out of the config ("" | LimitExpired
	// | LimitQuota). It is separate from Enabled, the administrator's switch.
	Limited string `json:"limited"`
	// LastActiveAt is when traffic was last counted for the user.
	LastActiveAt time.Time `json:"last_active_at,omitzero"`
}

// HasInbound reports whether the user is enabled on tag.
func (u User) HasInbound(tag string) bool {
	for _, t := range u.Inbounds {
		if t == tag {
			return true
		}
	}
	return false
}

const userCols = `id, name, note, enabled, inbounds, password, uuid, sub_token, created_at, updated_at, last_sub_at,
	expires_at, quota_bytes, reset_day, cycle_start, cycle_up, cycle_down, limited, last_active_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var enabled int
	var inbounds string
	var created, updated, lastSub, expires, cycleStart, lastActive int64
	if err := sc.Scan(&u.ID, &u.Name, &u.Note, &enabled, &inbounds, &u.Password, &u.UUID, &u.SubToken,
		&created, &updated, &lastSub,
		&expires, &u.QuotaBytes, &u.ResetDay, &cycleStart, &u.CycleUp, &u.CycleDown, &u.Limited, &lastActive); err != nil {
		return User{}, err
	}
	u.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(inbounds), &u.Inbounds); err != nil || u.Inbounds == nil {
		u.Inbounds = []string{}
	}
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	if lastSub > 0 {
		u.LastSubAt = time.Unix(lastSub, 0).UTC()
	}
	if expires > 0 {
		u.ExpiresAt = time.Unix(expires, 0).UTC()
	}
	if cycleStart > 0 {
		u.CycleStart = time.Unix(cycleStart, 0).UTC()
	}
	if lastActive > 0 {
		u.LastActiveAt = time.Unix(lastActive, 0).UTC()
	}
	return u, nil
}

// ListUsers returns all users ordered by id.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.executor(ctx).QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUser returns a user by id.
func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.executor(ctx).QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// UserBySubToken returns the user owning a subscription token.
func (s *Store) UserBySubToken(ctx context.Context, token string) (User, error) {
	u, err := scanUser(s.executor(ctx).QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE sub_token = ?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// SaveUsers applies a set of user changes in one transaction: users with
// ID 0 are inserted (their IDs are filled in), others are updated, and ids
// in deleted are removed.
func (s *Store) SaveUsers(ctx context.Context, upsert []*User, deleted []int64) error {
	return s.Transaction(ctx, func(ctx context.Context) error {
		tx := s.executor(ctx)
		now := s.now().Unix()
		for _, id := range deleted {
			if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
				return err
			}
		}
		for _, u := range upsert {
			inbounds, _ := json.Marshal(u.Inbounds)
			u.ResetDay = ClampResetDay(u.ResetDay)
			if u.ID == 0 {
				res, err := tx.ExecContext(ctx, `
				INSERT INTO users (name, note, enabled, inbounds, password, uuid, sub_token, created_at, updated_at,
					expires_at, quota_bytes, reset_day, cycle_start, limited)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					u.Name, u.Note, boolInt(u.Enabled), string(inbounds), u.Password, u.UUID, u.SubToken, now, now,
					unixOrZero(u.ExpiresAt), u.QuotaBytes, u.ResetDay, now, u.Limited)
				if err != nil {
					return mapConflict(err)
				}
				u.ID, _ = res.LastInsertId()
				u.CreatedAt = time.Unix(now, 0).UTC()
				u.CycleStart = u.CreatedAt
			} else if _, err := tx.ExecContext(ctx, `
				UPDATE users SET name = ?, note = ?, enabled = ?, inbounds = ?, password = ?, uuid = ?,
					sub_token = ?, updated_at = ?, expires_at = ?, quota_bytes = ?, reset_day = ?, limited = ?
				WHERE id = ?`,
				u.Name, u.Note, boolInt(u.Enabled), string(inbounds), u.Password, u.UUID, u.SubToken, now,
				unixOrZero(u.ExpiresAt), u.QuotaBytes, u.ResetDay, u.Limited, u.ID); err != nil {
				return mapConflict(err)
			}
			u.UpdatedAt = time.Unix(now, 0).UTC()
		}
		return nil
	})
}

// TouchSubscription records that a user's subscription was fetched.
func (s *Store) TouchSubscription(ctx context.Context, id int64) error {
	_, err := s.executor(ctx).ExecContext(ctx, `UPDATE users SET last_sub_at = ? WHERE id = ?`, s.now().Unix(), id)
	return err
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mapConflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

// GetSetting returns a setting value, or "" when unset.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.executor(ctx).QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a setting value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.executor(ctx).ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
