package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHost answers requests the way the host script does.
func fakeHost(t *testing.T, dir string, answer func(action string) (rc, out string)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			entries, _ := os.ReadDir(filepath.Join(dir, "requests"))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				p := filepath.Join(dir, "requests", e.Name())
				b, _ := os.ReadFile(p)
				os.Remove(p)
				rc, out := answer(strings.TrimSpace(string(b)))
				os.WriteFile(filepath.Join(dir, "results", e.Name()), []byte(rc+"\n2026-10-13T00:00:00Z\n"+out+"\n"), 0o644)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func newDir(t *testing.T) string {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "requests"), 0o755)
	os.Mkdir(filepath.Join(dir, "results"), 0o755)
	return dir
}

func TestCtlFileStatus(t *testing.T) {
	dir := newDir(t)
	fakeHost(t, dir, func(a string) (string, string) {
		if a != "status" {
			return "2", "unknown action"
		}
		return "0", "running 2026-10-13T03:13:48.199251333Z 1"
	})
	c := &CtlFile{Dir: dir, Poll: 5 * time.Millisecond}
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "running" || st.RestartCount != 1 || st.StartedAt.Year() != 2026 {
		t.Fatalf("status = %+v", st)
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "results")); len(left) != 0 {
		t.Fatal("result file not cleaned up")
	}
}

func TestCtlFileFailure(t *testing.T) {
	dir := newDir(t)
	fakeHost(t, dir, func(string) (string, string) { return "1", "Error: No such container: sing-box" })
	c := &CtlFile{Dir: dir, Poll: 5 * time.Millisecond}
	err := c.Reload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "No such container") {
		t.Fatalf("err = %v", err)
	}
}

func TestCtlFileTimeout(t *testing.T) {
	c := &CtlFile{Dir: newDir(t), Timeout: 50 * time.Millisecond, Poll: 5 * time.Millisecond}
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("expected timeout")
	}
}
