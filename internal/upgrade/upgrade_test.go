package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequest(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := &Manager{Dir: t.TempDir(), Now: func() time.Time { return now }}
	if st, err := m.State(); err != nil || st != nil {
		t.Fatalf("State before any upgrade = %+v, %v", st, err)
	}
	if err := m.Request(Panel, "v1.1.0", "v1.0.1", "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	reqs, _ := os.ReadDir(filepath.Join(m.Dir, "requests"))
	if len(reqs) != 1 || strings.HasPrefix(reqs[0].Name(), ".") {
		t.Fatalf("requests = %v", reqs)
	}
	body, _ := os.ReadFile(filepath.Join(m.Dir, "requests", reqs[0].Name()))
	if string(body) != "panel v1.1.0\n" {
		t.Fatalf("request = %q", body)
	}
	st, _ := m.State()
	if st.Status != Requested || st.Target != Panel || st.From != "v1.0.1" || st.To != "v1.1.0" || !st.Started.Equal(now) {
		t.Fatalf("state = %+v", st)
	}

	// One at a time.
	if err := m.Request(SingBox, "v1.1.0", "1.14.3", "1.14.4"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request: %v", err)
	}
	// Never picked up: reported as failed, and a new request is allowed.
	now = now.Add(2 * time.Minute)
	if st, _ := m.State(); st.Status != Failed || !strings.Contains(st.Message, "did not pick up") {
		t.Fatalf("stale request state = %+v", st)
	}
	if err := m.Request(SingBox, "v1.1.0", "1.14.3", "1.14.4"); err != nil {
		t.Fatal(err)
	}
}

func TestRequestValidates(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	for _, c := range []struct {
		target Target
		tag    string
	}{
		{"kernel", "v1.1.0"},
		{Panel, "1.1.0"},
		{Panel, "v1.1.0-beta.1"},
		{Panel, "v1.1.0 ; rm -rf /"},
		{Panel, "v1.1.0\nsing-box v1.0.0"},
	} {
		if err := m.Request(c.target, c.tag, "", ""); err == nil {
			t.Errorf("Request(%q, %q) accepted", c.target, c.tag)
		}
	}
	if (&Manager{}).Enabled() || (*Manager)(nil).Enabled() {
		t.Error("a manager without Dir should be off")
	}
}

func TestStateFromUnit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := &Manager{Dir: t.TempDir(), Now: func() time.Time { return now }}
	os.MkdirAll(filepath.Join(m.Dir, "status"), 0o755)
	write := func(s string) { os.WriteFile(filepath.Join(m.Dir, "status", "state"), []byte(s), 0o644) }

	write("target=sing-box\nfrom=1.14.3\nto=1.14.4\nstatus=rolled_back\nmessage=sing-box did not stay up; restored 1.14.3\nstarted=1799999000\nfinished=1799999060\n")
	st, err := m.State()
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != RolledBack || st.Target != SingBox || st.Message != "sing-box did not stay up; restored 1.14.3" ||
		st.Finished.Unix() != 1799999060 || st.Busy(now) {
		t.Fatalf("state = %+v", st)
	}

	// Running for too long: interrupted.
	write("target=panel\nstatus=running\nstarted=1799990000\n")
	if st, _ := m.State(); st.Status != Failed || !strings.Contains(st.Message, "did not finish") {
		t.Fatalf("state = %+v", st)
	}
	write("target=panel\nstatus=running\nstarted=1799999990\n")
	if st, _ := m.State(); st.Status != Running || !st.Busy(now) {
		t.Fatalf("state = %+v", st)
	}
}
