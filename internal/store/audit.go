package store

import (
	"context"
	"time"
)

// AuditEntry is one append-only audit record.
type AuditEntry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	IP     string    `json:"ip"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
}

// Audit appends an entry. At is filled in when zero.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	if e.At.IsZero() {
		e.At = s.now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (at, actor, ip, action, target, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At.Unix(), e.Actor, e.IP, e.Action, e.Target, e.Detail)
	return err
}

// ListAudit returns up to limit entries older than beforeID (0 = newest),
// newest first.
func (s *Store) ListAudit(ctx context.Context, beforeID int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, at, actor, ip, action, target, detail FROM audit_log`
	args := []any{}
	if beforeID > 0 {
		q += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.IP, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
