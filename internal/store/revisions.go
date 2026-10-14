package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Revision statuses.
const (
	RevRecoveryFailed = "recovery_failed"
	RevPending        = "pending"     // stored, not yet applied
	RevApplied        = "applied"     // written, reloaded and healthy
	RevWritten        = "written"     // written; no controller to reload it
	RevRolledBack     = "rolled_back" // applied, unhealthy, previous version restored
	RevObserved       = "observed"    // found on disk (external edit or first import)
)

// Revision is one stored version of config.json.
type Revision struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Actor     string    `json:"actor"`
	Source    string    `json:"source"`
	Summary   string    `json:"summary"`
	SHA256    string    `json:"sha256"`
	Content   []byte    `json:"-"`
	Size      int       `json:"size"`
	Status    string    `json:"status"`
	ApplyLog  string    `json:"apply_log"`
}

// AddRevision stores a revision and fills in its ID.
func (s *Store) AddRevision(ctx context.Context, r *Revision) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = s.now()
	}
	res, err := s.executor(ctx).ExecContext(ctx, `
		INSERT INTO revisions (created_at, actor, source, summary, sha256, content, status, apply_log)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.CreatedAt.Unix(), r.Actor, r.Source, r.Summary, r.SHA256, r.Content, r.Status, r.ApplyLog)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	r.Size = len(r.Content)
	return err
}

// SetRevisionStatus updates the outcome of applying a revision.
func (s *Store) SetRevisionStatus(ctx context.Context, id int64, status, log string) error {
	_, err := s.executor(ctx).ExecContext(ctx, `UPDATE revisions SET status = ?, apply_log = ? WHERE id = ?`, status, log, id)
	return err
}

// LatestRevision returns the newest revision whose content reached disk
// (anything but pending/rolled_back), or ErrNotFound.
func (s *Store) LatestRevision(ctx context.Context) (Revision, error) {
	return s.oneRevision(ctx, `WHERE status IN ('applied','written','observed') ORDER BY id DESC LIMIT 1`)
}

// GetRevision returns one revision with its content.
func (s *Store) GetRevision(ctx context.Context, id int64) (Revision, error) {
	return s.oneRevision(ctx, `WHERE id = ?`, id)
}

func (s *Store) oneRevision(ctx context.Context, where string, args ...any) (Revision, error) {
	var r Revision
	var at int64
	err := s.executor(ctx).QueryRowContext(ctx, `
		SELECT id, created_at, actor, source, summary, sha256, content, status, apply_log
		FROM revisions `+where, args...).
		Scan(&r.ID, &at, &r.Actor, &r.Source, &r.Summary, &r.SHA256, &r.Content, &r.Status, &r.ApplyLog)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	r.CreatedAt = time.Unix(at, 0).UTC()
	r.Size = len(r.Content)
	return r, err
}

// ListRevisions returns revision metadata (no content), newest first.
func (s *Store) ListRevisions(ctx context.Context, limit int) ([]Revision, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.executor(ctx).QueryContext(ctx, `
		SELECT id, created_at, actor, source, summary, sha256, length(content), status, apply_log
		FROM revisions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		var at int64
		if err := rows.Scan(&r.ID, &at, &r.Actor, &r.Source, &r.Summary, &r.SHA256, &r.Size, &r.Status, &r.ApplyLog); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneRevisions keeps the newest keep revisions.
func (s *Store) PruneRevisions(ctx context.Context, keep int) error {
	_, err := s.executor(ctx).ExecContext(ctx, `
		DELETE FROM revisions WHERE id NOT IN (SELECT id FROM revisions ORDER BY id DESC LIMIT ?)`, keep)
	return err
}
