package apply

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/store"
)

type fakeChecker struct{ reject string }

func (f fakeChecker) Check(_ context.Context, cfg []byte) (string, error) {
	if f.reject != "" && strings.Contains(string(cfg), f.reject) {
		return "FATAL decode config: bad", errors.New("exit status 1")
	}
	return "", nil
}

// fakeService "loads" the config on reload and writes a FATAL line to the
// log when the config contains "breaks-at-runtime".
type fakeService struct {
	cfgPath, logPath string
	reloads          int
	loaded           string
}

func (f *fakeService) Name() string { return "fake" }
func (f *fakeService) Status(context.Context) (control.Status, error) {
	return control.Status{State: "running"}, nil
}
func (f *fakeService) Reload(context.Context) error {
	f.reloads++
	b, _ := os.ReadFile(f.cfgPath)
	f.loaded = string(b)
	if strings.Contains(f.loaded, "breaks-at-runtime") {
		lf, _ := os.OpenFile(f.logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		lf.WriteString("+0000 2026-10-13 00:00:00 ERROR reload service: start inbound: address already in use\n")
		lf.Close()
	}
	return nil
}
func (f *fakeService) Restart(ctx context.Context) error { return f.Reload(ctx) }

func setup(t *testing.T) (*Manager, *fakeService, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte("{\n  \"v\": 1\n}\n"), 0o600)
	logPath := filepath.Join(dir, "sing-box.log")
	os.WriteFile(logPath, nil, 0o644)
	svc := &fakeService{cfgPath: cfg, logPath: logPath}
	m := &Manager{
		ConfigPath: cfg, LogPath: logPath, Store: st, Controller: svc,
		Checker: fakeChecker{reject: "invalid"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settle: 10 * time.Millisecond, HealthTimeout: 500 * time.Millisecond,
	}
	return m, svc, cfg
}

func set(content string) func([]byte) ([]byte, error) {
	return func([]byte) ([]byte, error) { return []byte(content), nil }
}

func statuses(t *testing.T, st *store.Store) string {
	revs, _ := st.ListRevisions(context.Background(), 10)
	var s []string
	for i := len(revs) - 1; i >= 0; i-- {
		s = append(s, revs[i].Source+":"+revs[i].Status)
	}
	return strings.Join(s, ",")
}

func TestApplySuccess(t *testing.T) {
	m, svc, cfg := setup(t)
	committed := false
	res, err := m.Update(context.Background(), Change{Actor: "admin", Source: "panel"}, set("{\"v\":2}"),
		func(context.Context) error { committed = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Status != store.RevApplied || !committed || svc.reloads != 1 {
		t.Fatalf("res = %+v committed=%v reloads=%d", res, committed, svc.reloads)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "{\"v\":2}" {
		t.Fatalf("file = %s", b)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode())
	}
	if got := statuses(t, m.Store); got != "import:observed,panel:applied" {
		t.Fatalf("revisions = %s", got)
	}
}

func TestCheckFailureLeavesFileAlone(t *testing.T) {
	m, svc, cfg := setup(t)
	committed := false
	_, err := m.Update(context.Background(), Change{Source: "panel"}, set(`{"invalid":true}`),
		func(context.Context) error { committed = true; return nil })
	var ce *CheckError
	if !errors.As(err, &ce) || !strings.Contains(ce.Output, "FATAL") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "{\n  \"v\": 1\n}\n" || committed || svc.reloads != 0 {
		t.Fatal("rejected config must not be written, committed or reloaded")
	}
}

func TestUnhealthyRollsBack(t *testing.T) {
	m, svc, cfg := setup(t)
	committed := false
	res, err := m.Update(context.Background(), Change{Source: "panel"}, set(`{"breaks-at-runtime":true}`),
		func(context.Context) error { committed = true; return nil })
	if !errors.Is(err, ErrRolledBack) || res.Status != store.RevRolledBack || committed {
		t.Fatalf("err=%v res=%+v committed=%v", err, res, committed)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "{\n  \"v\": 1\n}\n" {
		t.Fatalf("file not restored: %s", b)
	}
	if svc.reloads != 2 || !strings.Contains(svc.loaded, `"v": 1`) {
		t.Fatalf("service did not reload the previous config: %d %q", svc.reloads, svc.loaded)
	}
	if !strings.Contains(res.Log, "address already in use") || !strings.Contains(res.Log, "previous config healthy again") {
		t.Fatalf("log = %s", res.Log)
	}
}

func TestStaleBaseAndExternalEdit(t *testing.T) {
	m, _, cfg := setup(t)
	ctx := context.Background()
	if err := m.SyncExternal(ctx); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(cfg, []byte(`{"v":"hand-edited"}`), 0o600)
	_, err := m.Update(ctx, Change{Source: "panel", BaseSHA256: "deadbeef"}, set(`{"v":3}`), nil)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v", err)
	}
	if got := statuses(t, m.Store); got != "import:observed,external:observed" {
		t.Fatalf("revisions = %s", got)
	}
}

func TestNoChangeStillCommits(t *testing.T) {
	m, svc, _ := setup(t)
	committed := false
	res, err := m.Update(context.Background(), Change{Source: "panel"}, func(cur []byte) ([]byte, error) { return cur, nil },
		func(context.Context) error { committed = true; return nil })
	if err != nil || res.Changed || !committed || svc.reloads != 0 {
		t.Fatalf("res=%+v err=%v committed=%v reloads=%d", res, err, committed, svc.reloads)
	}
}

func TestWrittenWithoutController(t *testing.T) {
	m, _, _ := setup(t)
	m.Controller = control.None{}
	res, err := m.Update(context.Background(), Change{Source: "panel"}, set(`{"v":9}`), nil)
	if err != nil || res.Status != store.RevWritten {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestCommandChecker(t *testing.T) {
	c := &CommandChecker{Command: []string{"grep", "-q", "ok", "{config}"}, TempDir: t.TempDir()}
	if _, err := c.Check(context.Background(), []byte(`{"ok":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Check(context.Background(), []byte(`{"no":1}`)); err == nil {
		t.Fatal("expected failure")
	}
	if left, _ := os.ReadDir(c.TempDir); len(left) != 0 {
		t.Fatal("temp file left behind")
	}
	if _, err := ParseCommand("sing-box check"); err == nil {
		t.Fatal("expected placeholder error")
	}
}

// The admin's own request often travels through the sing-box being
// reloaded, so it is cancelled mid-apply. The change must still complete.
func TestCallerDisconnectDoesNotRollBack(t *testing.T) {
	m, svc, cfg := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.Controller = &cancelOnReload{fakeService: svc, cancel: cancel}
	committed := false
	res, err := m.Update(ctx, Change{Source: "panel"}, set(`{"v":42}`), func(context.Context) error { committed = true; return nil })
	if err != nil || res.Status != store.RevApplied || !committed {
		t.Fatalf("err=%v res=%+v committed=%v", err, res, committed)
	}
	if b, _ := os.ReadFile(cfg); string(b) != `{"v":42}` {
		t.Fatalf("file = %s", b)
	}
}

type cancelOnReload struct {
	*fakeService
	cancel func()
}

func (c *cancelOnReload) Reload(ctx context.Context) error {
	c.cancel() // the reload drops the caller's connection
	return c.fakeService.Reload(ctx)
}
