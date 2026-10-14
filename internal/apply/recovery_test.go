package apply

import (
	"context"
	"errors"
	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/store"
	"os"
	"strings"
	"testing"
)

func TestCommitFailureRestoresConfigAndBusinessData(t *testing.T) {
	for _, noController := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload", true: "write-only"}[noController], func(t *testing.T) {
			m, svc, cfg := setup(t)
			if noController {
				m.Controller = control.None{}
			}
			ctx := context.Background()
			old, _ := os.ReadFile(cfg)
			if err := m.Store.SetSetting(ctx, "subscription", "old"); err != nil {
				t.Fatal(err)
			}
			res, err := m.Update(ctx, Change{Source: "panel"}, set(`{"v":2}`), func(ctx context.Context) error {
				if err := m.Store.SaveUsers(ctx, []*store.User{{Name: "new", Password: "p", UUID: "u", SubToken: "t"}}, nil); err != nil {
					return err
				}
				if err := m.Store.SetSetting(ctx, "subscription", "new"); err != nil {
					return err
				}
				return errors.New("injected final save failure")
			})
			if !errors.Is(err, ErrRolledBack) || res.Status != store.RevRolledBack {
				t.Fatalf("%+v %v", res, err)
			}
			got, _ := os.ReadFile(cfg)
			if string(got) != string(old) {
				t.Fatal("file not restored")
			}
			users, _ := m.Store.ListUsers(ctx)
			if len(users) != 0 {
				t.Fatal("partial user commit")
			}
			setting, _ := m.Store.GetSetting(ctx, "subscription")
			if setting != "old" {
				t.Fatal("partial subscription commit")
			}
			pending, _ := m.Store.GetSetting(ctx, pendingKey)
			if pending != "" {
				t.Fatal("journal not cleared")
			}
			if !noController && svc.loaded != string(old) {
				t.Fatal("runtime not restored")
			}
		})
	}
}

type failingRestore struct {
	*fakeService
	failFile  bool
	unhealthy bool
}

func (c *failingRestore) Reload(ctx context.Context) error {
	if c.reloads == 0 {
		c.fakeService.Reload(ctx)
		if c.failFile {
			os.Remove(c.cfgPath)
			os.Mkdir(c.cfgPath, 0700)
		}
		return errors.New("first reload failed")
	}
	if c.unhealthy {
		c.reloads++
		f, _ := os.OpenFile(c.logPath, os.O_APPEND|os.O_WRONLY, 0600)
		f.WriteString("FATAL old config also failed\n")
		f.Close()
		return nil
	}
	return errors.New("restore reload failed")
}
func TestRecoveryFailureNeverClaimsRollback(t *testing.T) {
	for _, mode := range []string{"file", "reload", "health"} {
		t.Run(mode, func(t *testing.T) {
			m, svc, _ := setup(t)
			m.Controller = &failingRestore{fakeService: svc, failFile: mode == "file", unhealthy: mode == "health"}
			res, err := m.Update(context.Background(), Change{Source: "panel"}, set(`{"v":2}`), nil)
			if !errors.Is(err, ErrRecoveryFailed) || errors.Is(err, ErrRolledBack) || res.Status != store.RevRecoveryFailed {
				t.Fatalf("%+v %v", res, err)
			}
			journal, _ := m.Store.GetSetting(context.Background(), pendingKey)
			if journal == "" {
				t.Fatal("lost recovery journal")
			}
			if mode == "health" && !strings.Contains(res.Log, "old config also failed") {
				t.Fatalf("missed immediate restore error: %s", res.Log)
			}
		})
	}
}
func TestRecoverInterruptedApply(t *testing.T) {
	m, svc, cfg := setup(t)
	ctx := context.Background()
	old, _ := os.ReadFile(cfg)
	candidate := []byte(`{"v":2}`)
	rev := store.Revision{Source: "panel", Content: candidate, SHA256: sha(candidate), Status: store.RevPending}
	if err := m.Store.AddRevision(ctx, &rev); err != nil {
		t.Fatal(err)
	}
	if err := m.savePending(ctx, pendingApply{Revision: rev.ID, Path: cfg, Previous: old, Candidate: sha(candidate)}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(cfg, candidate, 0600)
	// Fresh manager simulates restarting the process with the persisted journal.
	next := &Manager{ConfigPath: cfg, LogPath: m.LogPath, Store: m.Store, Controller: svc, Checker: m.Checker, Logger: m.Logger, Settle: m.Settle, HealthTimeout: m.HealthTimeout}
	if err := next.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if string(got) != string(old) || svc.loaded != string(old) {
		t.Fatal("interrupted change not recovered")
	}
	r, _ := m.Store.GetRevision(ctx, rev.ID)
	if r.Status != store.RevRolledBack {
		t.Fatal(r.Status)
	}
	if err := next.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.reloads != 1 {
		t.Fatal("recovery not idempotent")
	}
}
func TestRecoveryDoesNotOverwriteExternalEdit(t *testing.T) {
	m, _, cfg := setup(t)
	ctx := context.Background()
	old, _ := os.ReadFile(cfg)
	if err := m.savePending(ctx, pendingApply{Revision: 1, Path: cfg, Previous: old, Candidate: sha([]byte(`{"v":2}`))}); err != nil {
		t.Fatal(err)
	}
	external := []byte(`{"v":"external"}`)
	os.WriteFile(cfg, external, 0600)
	if err := m.Recover(ctx); !errors.Is(err, ErrRecoveryFailed) {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if string(got) != string(external) {
		t.Fatal("external edit overwritten")
	}
}
func TestUnchangedCommitIsAtomic(t *testing.T) {
	m, _, cfg := setup(t)
	ctx := context.Background()
	old, _ := os.ReadFile(cfg)
	_, err := m.Update(ctx, Change{}, set(string(old)), func(ctx context.Context) error {
		if err := m.Store.SetSetting(ctx, "subscription", "partial"); err != nil {
			return err
		}
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	v, _ := m.Store.GetSetting(ctx, "subscription")
	if v != "" {
		t.Fatal("partial no-op commit")
	}
}

func TestUsageFailureBlocksResetButAllowsRepair(t *testing.T) {
	m, _, _ := setup(t)
	m.BeforeChange = func(context.Context) error { return errors.New("stats offline") }
	built := false
	_, err := m.Update(context.Background(), Change{RequireUsage: true}, func(cur []byte) ([]byte, error) { built = true; return cur, nil }, nil)
	if !errors.Is(err, ErrUsageUnavailable) || built {
		t.Fatalf("reset continued: built=%v err=%v", built, err)
	}
	if _, err := m.Update(context.Background(), Change{}, set(`{"v":2}`), nil); err != nil {
		t.Fatalf("repair blocked: %v", err)
	}
}
