package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AccessRule is one entry of the proxy blocklist or allowlist.
type AccessRule struct {
	ID        int64     `json:"id"`
	List      string    `json:"list"` // deny | allow
	CIDR      string    `json:"cidr"`
	Note      string    `json:"note"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// ListAccessRules returns all rules, newest first.
func (s *Store) ListAccessRules(ctx context.Context) ([]AccessRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, list, cidr, note, expires_at, created_at, created_by FROM access_rules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessRule{}
	for rows.Next() {
		var r AccessRule
		var exp, created int64
		if err := rows.Scan(&r.ID, &r.List, &r.CIDR, &r.Note, &exp, &created, &r.CreatedBy); err != nil {
			return nil, err
		}
		if exp > 0 {
			r.ExpiresAt = time.Unix(exp, 0).UTC()
		}
		r.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutAccessRule adds a rule, or updates note and expiry of the same
// list+prefix. It returns the rule as stored.
func (s *Store) PutAccessRule(ctx context.Context, r AccessRule) (AccessRule, error) {
	now := s.now()
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO access_rules (list, cidr, note, expires_at, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (list, cidr) DO UPDATE SET note = excluded.note, expires_at = excluded.expires_at
		RETURNING id, created_at, created_by`,
		r.List, r.CIDR, r.Note, unixOrZero(r.ExpiresAt), now.Unix(), r.CreatedBy).Scan(&r.ID, new(int64), &r.CreatedBy)
	if err != nil {
		return AccessRule{}, err
	}
	rules, err := s.ListAccessRules(ctx)
	if err != nil {
		return AccessRule{}, err
	}
	for _, x := range rules {
		if x.ID == r.ID {
			return x, nil
		}
	}
	return r, nil
}

// DeleteAccessRule removes a rule; it reports whether one existed.
func (s *Store) DeleteAccessRule(ctx context.Context, id int64) (AccessRule, bool, error) {
	var r AccessRule
	var exp, created int64
	err := s.db.QueryRowContext(ctx, `
		DELETE FROM access_rules WHERE id = ? RETURNING id, list, cidr, note, expires_at, created_at, created_by`, id).
		Scan(&r.ID, &r.List, &r.CIDR, &r.Note, &exp, &created, &r.CreatedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccessRule{}, false, nil
		}
		return AccessRule{}, false, err
	}
	return r, true, nil
}

// PurgeExpiredAccessRules deletes rules whose expiry has passed and returns
// them.
func (s *Store) PurgeExpiredAccessRules(ctx context.Context, now time.Time) ([]AccessRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		DELETE FROM access_rules WHERE expires_at > 0 AND expires_at <= ? RETURNING id, list, cidr, note`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessRule
	for rows.Next() {
		var r AccessRule
		if err := rows.Scan(&r.ID, &r.List, &r.CIDR, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
