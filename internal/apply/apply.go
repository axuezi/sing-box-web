// Package apply changes sing-box's config.json safely: every change is
// validated with `sing-box check`, snapshotted, written atomically, reloaded,
// health-checked, and rolled back automatically if sing-box does not come up.
package apply

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
)

var (
	// ErrStale means the config changed since the caller read it.
	ErrStale = errors.New("config changed since it was loaded; reload and try again")
	// ErrRolledBack means applying or committing the change failed,
	// and the previous config and (when controlled) service were restored.
	ErrRolledBack = errors.New("configuration change failed; previous config restored")
)

// CheckError carries `sing-box check` output for a rejected config.
type CheckError struct{ Output string }

func (e *CheckError) Error() string { return "sing-box check failed: " + e.Output }

// Manager serialises all writes to one config file.
type Manager struct {
	ConfigPath string
	LogPath    string
	Store      *store.Store
	Controller control.Controller
	Checker    Checker
	Logger     *slog.Logger

	// Settle is how long sing-box gets after a reload before it is judged;
	// HealthTimeout bounds the whole health check.
	Settle        time.Duration
	HealthTimeout time.Duration
	KeepRevisions int

	// BeforeReload, if set, runs right before every reload or restart, e.g.
	// to save traffic counters that a reload would reset.
	BeforeReload func(context.Context)
	// BeforeChange samples pending usage before building a mutation under the usage lock.
	BeforeChange func(context.Context) error

	mu sync.Mutex
}

// Change describes who changes the config and why.
type Change struct {
	Actor   string
	Source  string // panel | rollback
	Summary string
	// BaseSHA256, if set, must match the file on disk (optimistic lock).
	BaseSHA256 string
	// RequireUsage prevents resetting quota against an unavailable counter baseline.
	RequireUsage bool
}

// Result reports what happened.
type Result struct {
	Changed  bool           `json:"changed"`
	Revision store.Revision `json:"revision"`
	Status   string         `json:"status"`
	Log      string         `json:"log"`
}

// Update runs build on the current config and applies its output. commit,
// if non-nil, runs only after the new config is live and healthy (or written,
// when there is no controller); use it to persist the change that produced
// the new config so database and config never disagree.
func (m *Manager) Update(ctx context.Context, ch Change, build func(current []byte) ([]byte, error), commit func(context.Context) error) (Result, error) {
	ctx = context.WithoutCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, unlock := m.Store.LockUsage(ctx)
	defer unlock()
	if err := m.recoverPending(ctx); err != nil {
		return Result{}, err
	}
	if m.BeforeChange != nil {
		if err := m.BeforeChange(ctx); err != nil {
			if ch.RequireUsage {
				return Result{}, fmt.Errorf("%w: %v", ErrUsageUnavailable, err)
			}
			m.Logger.Warn("could not collect usage before change", "err", err)
		}
	}
	cur, err := singbox.ReadFile(m.ConfigPath)
	if err != nil {
		return Result{}, err
	}
	if err := m.recordExternal(ctx, cur); err != nil {
		return Result{}, err
	}
	if ch.BaseSHA256 != "" && ch.BaseSHA256 != cur.SHA256 {
		return Result{}, ErrStale
	}
	cand, err := build(cur.Raw)
	if err != nil {
		return Result{}, err
	}
	commitData := func(ctx context.Context) error {
		if err := protocol.SaveInboundFlows(ctx, m.Store, cur.Raw, cand); err != nil {
			return err
		}
		if commit != nil {
			return commit(ctx)
		}
		return nil
	}
	if sha(cand) == cur.SHA256 {
		if err := m.Store.Transaction(ctx, commitData); err != nil {
			return Result{}, err
		}
		return Result{Changed: false, Status: "unchanged"}, nil
	}
	if problems := singbox.Lint(cand); len(problems) > 0 {
		return Result{}, &CheckError{Output: strings.Join(problems, "\n")}
	}
	if out, err := m.Checker.Check(ctx, cand); err != nil {
		return Result{}, &CheckError{Output: strings.TrimSpace(out + "\n" + err.Error())}
	}
	rev := store.Revision{Actor: ch.Actor, Source: ch.Source, Summary: ch.Summary, SHA256: sha(cand), Content: cand, Status: store.RevPending}
	if err := m.Store.Transaction(ctx, func(ctx context.Context) error {
		if err := m.Store.AddRevision(ctx, &rev); err != nil {
			return err
		}
		return m.savePending(ctx, pendingApply{Revision: rev.ID, Path: m.ConfigPath, Previous: cur.Raw, Candidate: sha(cand)})
	}); err != nil {
		return Result{}, err
	}
	var log strings.Builder
	result := func(status string) Result {
		rev.Status, rev.ApplyLog = status, strings.TrimSpace(log.String())
		return Result{Changed: true, Revision: rev, Status: status, Log: rev.ApplyLog}
	}
	rollback := func(cause error) (Result, error) {
		fmt.Fprintf(&log, "change failed: %v\n", cause)
		if err := m.restore(ctx, cur.Raw, &log); err != nil {
			_ = m.Store.SetRevisionStatus(ctx, rev.ID, store.RevRecoveryFailed, log.String())
			return result(store.RevRecoveryFailed), fmt.Errorf("%w: %v", ErrRecoveryFailed, err)
		}
		if err := m.Store.Transaction(ctx, func(ctx context.Context) error {
			if err := m.Store.SetRevisionStatus(ctx, rev.ID, store.RevRolledBack, log.String()); err != nil {
				return err
			}
			return m.Store.SetSetting(ctx, pendingKey, "")
		}); err != nil {
			return result(store.RevRecoveryFailed), fmt.Errorf("%w: recording recovery: %v", ErrRecoveryFailed, err)
		}
		return result(store.RevRolledBack), fmt.Errorf("%w: %v", ErrRolledBack, cause)
	}
	if err := writeAtomic(m.ConfigPath, cand); err != nil {
		return rollback(err)
	}
	fmt.Fprintf(&log, "%s written (%d bytes)\n", stamp(), len(cand))
	status := store.RevWritten
	if _, none := m.Controller.(control.None); !none {
		offset := fileSize(m.LogPath)
		m.beforeReload(ctx)
		if err := m.Controller.Reload(ctx); err != nil {
			return rollback(fmt.Errorf("reload: %w", err))
		}
		fmt.Fprintf(&log, "%s reload requested via %s\n", stamp(), m.Controller.Name())
		if problem := m.waitHealthy(ctx, cand, offset); problem != "" {
			return rollback(errors.New(problem))
		}
		fmt.Fprintf(&log, "%s healthy\n", stamp())
		status = store.RevApplied
	} else {
		fmt.Fprintln(&log, "no controller configured: reload sing-box yourself to apply")
	}
	// Clearing the journal and saving the final revision share the business transaction.
	// A process crash before this commit leaves enough information to restore the old file.
	if err := m.Store.Transaction(ctx, func(ctx context.Context) error {
		if err := commitData(ctx); err != nil {
			return err
		}
		if err := m.Store.SetRevisionStatus(ctx, rev.ID, status, log.String()); err != nil {
			return err
		}
		return m.Store.SetSetting(ctx, pendingKey, "")
	}); err != nil {
		return rollback(fmt.Errorf("saving panel data: %w", err))
	}
	_ = m.Store.PruneRevisions(ctx, m.keep())
	return result(status), nil
}

// Reload asks sing-box to reload the current file and health-checks it,
// without changing the config.
func (m *Manager) Reload(ctx context.Context, restart bool) (string, error) {
	ctx = context.WithoutCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, unlock := m.Store.LockUsage(ctx)
	defer unlock()
	if err := m.recoverPending(ctx); err != nil {
		return "", err
	}
	cur, err := singbox.ReadFile(m.ConfigPath)
	if err != nil {
		return "", err
	}
	offset := fileSize(m.LogPath)
	m.beforeReload(ctx)
	if restart {
		err = m.Controller.Restart(ctx)
	} else {
		err = m.Controller.Reload(ctx)
	}
	if err != nil {
		return "", err
	}
	if problem := m.waitHealthy(ctx, cur.Raw, offset); problem != "" {
		return "", fmt.Errorf("sing-box unhealthy: %s", problem)
	}
	return "healthy", nil
}

// recordExternal snapshots the file if it differs from the newest revision
// we know reached disk (first run, or edited by hand).
func (m *Manager) recordExternal(ctx context.Context, cur singbox.File) error {
	latest, err := m.Store.LatestRevision(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err == nil && latest.SHA256 == cur.SHA256 {
		return nil
	}
	source, summary := "external", "changed outside sing-box-web"
	if errors.Is(err, store.ErrNotFound) {
		source, summary = "import", "initial config"
	}
	return m.Store.AddRevision(ctx, &store.Revision{
		Actor: "system", Source: source, Summary: summary, SHA256: cur.SHA256,
		Content: cur.Raw, Status: store.RevObserved, CreatedAt: cur.ModTime,
	})
}

// SyncExternal records an out-of-band edit, if any. Safe to call any time.
func (m *Manager) SyncExternal(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, err := singbox.ReadFile(m.ConfigPath)
	if err != nil {
		return err
	}
	return m.recordExternal(ctx, cur)
}

func (m *Manager) restore(ctx context.Context, prev []byte, log *strings.Builder) error {
	if err := writeAtomic(m.ConfigPath, prev); err != nil {
		fmt.Fprintf(log, "previous config file restore failed: %v\n", err)
		return fmt.Errorf("file restore failed: %w", err)
	}
	fmt.Fprintf(log, "%s previous config restored\n", stamp())
	if _, none := m.Controller.(control.None); none {
		return nil
	}
	// Capture before the reload so immediate errors are not skipped.
	offset := fileSize(m.LogPath)
	st, _ := m.Controller.Status(ctx)
	var err error
	if st.State == "running" {
		err = m.Controller.Reload(ctx)
	} else {
		err = m.Controller.Restart(ctx)
	}
	if err != nil {
		fmt.Fprintf(log, "previous config service reload failed: %v\n", err)
		return fmt.Errorf("service restore failed: %w", err)
	}
	if problem := m.waitHealthy(ctx, prev, offset); problem != "" {
		fmt.Fprintf(log, "previous config is unhealthy: %s\n", problem)
		return fmt.Errorf("restored service unhealthy: %s", problem)
	}
	fmt.Fprintf(log, "%s previous config healthy again\n", stamp())
	return nil
}

// waitHealthy returns "" once sing-box looks healthy, or a description of
// the problem. Healthy = the controller reports running, the Clash API (if
// the config enables it) answers, and no fatal or reload error was logged
// since logOffset.
func (m *Manager) waitHealthy(ctx context.Context, cfg []byte, logOffset int64) string {
	settle, timeout := m.Settle, m.HealthTimeout
	if settle == 0 {
		settle = 2 * time.Second
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var clash *clashapi.Client
	if sum := singbox.Summarize(cfg); sum.ClashAPI != "" {
		clash = &clashapi.Client{BaseURL: "http://" + sum.ClashAPI, Secret: singbox.ClashAPISecret(cfg)}
	}

	select {
	case <-time.After(settle):
	case <-ctx.Done():
		return "timed out"
	}
	last := "not checked"
	for {
		if bad := scanLog(m.LogPath, logOffset); bad != "" {
			return "log: " + bad
		}
		st, err := m.Controller.Status(ctx)
		switch {
		case err != nil:
			last = "status: " + err.Error()
		case st.State == "exited" || st.State == "dead" || st.State == "failed":
			return "service is " + st.State // will not recover by waiting
		case st.State != "running":
			last = "service is " + st.State
		case clash != nil:
			if _, err := clash.Version(ctx); err != nil {
				last = "clash api: " + err.Error()
			} else {
				return ""
			}
		default:
			return ""
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return last
		}
	}
}

// scanLog looks for a fatal error or a failed reload logged after offset.
func scanLog(path string, offset int64) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() < offset {
		offset = 0 // rotated
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(f, 4<<20))
	for _, line := range bytes.Split(data, []byte("\n")) {
		s := string(line)
		if strings.Contains(s, "FATAL") || strings.Contains(s, "reload service") ||
			(strings.Contains(s, "ERROR") && strings.Contains(s, "start service")) {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// writeAtomic replaces path with data, keeping its permissions and owner
// (sing-box may run as its own user and must still read the file). The temp
// file has no .json suffix so `sing-box run -C <dir>` never picks it up.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o600)
	uid, gid := -1, -1
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(sys.Uid), int(sys.Gid)
		}
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".sbw-tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if uid != os.Getuid() || gid != os.Getgid() {
		// Best effort: only root may give the file away.
		_ = f.Chown(uid, gid)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) beforeReload(ctx context.Context) {
	if m.BeforeReload != nil {
		m.BeforeReload(context.WithoutCancel(ctx))
	}
}

func (m *Manager) keep() int {
	if m.KeepRevisions > 0 {
		return m.KeepRevisions
	}
	return 100
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func stamp() string { return time.Now().UTC().Format("15:04:05") }
