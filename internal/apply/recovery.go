package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/axuezi/sing-box-web/internal/store"
	"os"
	"strings"
)

var ErrUsageUnavailable = errors.New("usage collection unavailable; retry the quota reset after statistics recover")

var ErrRecoveryFailed = errors.New("configuration recovery incomplete; administrator action required")

const pendingKey = "config_apply_pending"

type pendingApply struct {
	Revision  int64
	Path      string
	Previous  []byte
	Candidate string
}

func (m *Manager) savePending(ctx context.Context, p pendingApply) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return m.Store.SetSetting(ctx, pendingKey, string(b))
}

// Recover must run before importing users or starting background workers.
func (m *Manager) Recover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, unlock := m.Store.LockUsage(ctx)
	defer unlock()
	return m.recoverPending(ctx)
}
func (m *Manager) recoverPending(ctx context.Context) error {
	raw, err := m.Store.GetSetting(ctx, pendingKey)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	var p pendingApply
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("%w: invalid recovery journal", ErrRecoveryFailed)
	}
	if p.Revision <= 0 || len(p.Candidate) != 64 || !json.Valid(p.Previous) {
		return fmt.Errorf("%w: invalid recovery journal", ErrRecoveryFailed)
	}
	if p.Path != m.ConfigPath {
		return fmt.Errorf("%w: recovery journal belongs to another config", ErrRecoveryFailed)
	}
	cur, err := os.ReadFile(m.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("%w: %v", ErrRecoveryFailed, err)
	}
	if err == nil && sha(cur) != p.Candidate && sha(cur) != sha(p.Previous) {
		return fmt.Errorf("%w: config changed externally; refusing to overwrite it", ErrRecoveryFailed)
	}
	var log strings.Builder
	fmt.Fprintln(&log, "recovering interrupted configuration change")
	if err := m.restore(ctx, p.Previous, &log); err != nil {
		_ = m.Store.SetRevisionStatus(ctx, p.Revision, store.RevRecoveryFailed, log.String())
		return fmt.Errorf("%w: %v", ErrRecoveryFailed, err)
	}
	return m.Store.Transaction(ctx, func(ctx context.Context) error {
		if err := m.Store.SetRevisionStatus(ctx, p.Revision, store.RevRolledBack, log.String()); err != nil {
			return err
		}
		return m.Store.SetSetting(ctx, pendingKey, "")
	})
}
