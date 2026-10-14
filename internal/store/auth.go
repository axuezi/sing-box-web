package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Admin is the single administrator account.
type Admin struct {
	Username     string
	PasswordHash string
	UpdatedAt    time.Time
}

// GetAdmin returns the administrator, or ErrNotFound if none is set yet.
func (s *Store) GetAdmin(ctx context.Context) (Admin, error) {
	var a Admin
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT username, password_hash, updated_at FROM admin WHERE id = 1`).
		Scan(&a.Username, &a.PasswordHash, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrNotFound
	}
	a.UpdatedAt = time.Unix(updated, 0)
	return a, err
}

// SetAdmin creates or replaces the administrator credentials and, in the
// same transaction, signs out every existing session.
func (s *Store) SetAdmin(ctx context.Context, username, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin (id, username, password_hash, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET username = excluded.username,
			password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		username, passwordHash, s.now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameAdmin changes the administrator's login name. Sessions stay valid.
func (s *Store) RenameAdmin(ctx context.Context, username string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admin SET username = ?, updated_at = ? WHERE id = 1`, username, s.now().Unix())
	return err
}

// Session is a signed-in browser session. The raw token is only ever held by
// the browser; the database stores its hash.
type Session struct {
	TokenHash string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, csrf_token, created_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.CSRFToken, sess.CreatedAt.Unix(), sess.ExpiresAt.Unix(), sess.IP, sess.UserAgent)
	return err
}

// GetSession returns an unexpired session by token hash.
func (s *Store) GetSession(ctx context.Context, tokenHash string) (Session, error) {
	var sess Session
	var created, expires int64
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, csrf_token, created_at, expires_at, ip, user_agent
		FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, s.now().Unix()).
		Scan(&sess.TokenHash, &sess.CSRFToken, &created, &expires, &sess.IP, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.ExpiresAt = time.Unix(expires, 0)
	return sess, err
}

// DeleteSession signs out one session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// PruneSessions removes expired sessions.
func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, s.now().Unix())
	return err
}
