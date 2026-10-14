package store

import (
	"context"
	"database/sql"
)

type transactionKey struct{}
type usageKey struct{}
type transaction struct {
	store *Store
	tx    *sql.Tx
}
type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) executor(ctx context.Context) executor {
	if t, ok := ctx.Value(transactionKey{}).(transaction); ok && t.store == s {
		return t.tx
	}
	return s.db
}

// Transaction makes user, setting, quota and revision operations in fn atomic.
// Those operations use executor(ctx); nested transactions join the outer one.
func (s *Store) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if t, ok := ctx.Value(transactionKey{}).(transaction); ok && t.store == s {
		return fn(ctx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ctx = context.WithValue(ctx, transactionKey{}, transaction{s, tx})
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// LockUsage serializes sampling with configuration changes and quota resets.
// Keep the returned context for nested calls; never acquire the apply lock while holding this lock.
func (s *Store) LockUsage(ctx context.Context) (context.Context, func()) {
	if owner, _ := ctx.Value(usageKey{}).(*Store); owner == s {
		return ctx, func() {}
	}
	s.usageMu.Lock()
	return context.WithValue(ctx, usageKey{}, s), s.usageMu.Unlock
}
