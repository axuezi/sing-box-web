package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/axuezi/sing-box-web/internal/release"
	"github.com/axuezi/sing-box-web/internal/upgrade"
	"github.com/axuezi/sing-box-web/internal/version"
)

// newUpgradeEnv serves a fake sing-box-web release (v9.9.9 bundling
// sing-box 1.99.0) and a Clash API reporting sing-box 1.14.3.
func newUpgradeEnv(t *testing.T, managed bool) (*configEnv, *upgrade.Manager) {
	t.Helper()
	m := newConfigEnv(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"tag_name":"v9.9.9","html_url":"https://example.test/r","body":"notes\n<!-- sing-box-v2api: v1.99.0 -->"}`)
	}))
	t.Cleanup(gh.Close)
	clash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"version":"sing-box 1.14.3"}`)
	}))
	t.Cleanup(clash.Close)
	cfg := strings.Replace(managedConfig, `"log": {"level": "info"},`,
		`"log": {"level": "info"}, "experimental": {"clash_api": {"external_controller": "`+strings.TrimPrefix(clash.URL, "http://")+`"}},`, 1)
	os.WriteFile(m.cfgPath, []byte(cfg), 0o600)

	checker := &release.Checker{URL: release.LatestURL(gh.URL, "o/r"), Logger: slog.New(slog.DiscardHandler)}
	if err := checker.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	mgr := &upgrade.Manager{Dir: filepath.Join(m.dir, "upgrade")}
	m.h = New(Options{Store: m.st, SingboxConfig: m.cfgPath, SingboxLog: m.logPath, Apply: m.mgr, Controller: m.ctl,
		PanelRelease: checker, Upgrade: mgr, KernelManaged: managed})
	m.cookie, m.csrf = m.login()
	return m, mgr
}

func TestUpgradeStatus(t *testing.T) {
	old := version.Version
	version.Version = "v1.0.1"
	defer func() { version.Version = old }()

	m, _ := newUpgradeEnv(t, true)
	code, v := m.call("GET", "/api/upgrade", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/upgrade: %d %s", code, v.Raw)
	}
	if !v.Get("enabled").Bool() || !v.Get("panel.newer").Bool() || v.Get("panel.latest.version").String() != "9.9.9" ||
		v.Get("sing_box.current").String() != "1.14.3" || v.Get("sing_box.bundled").String() != "1.99.0" ||
		!v.Get("sing_box.newer").Bool() || !v.Get("sing_box.managed").Bool() || v.Get("state").Exists() {
		t.Fatalf("upgrade view = %s", v.Raw)
	}
}

func TestStartUpgrade(t *testing.T) {
	old := version.Version
	version.Version = "v1.0.1"
	defer func() { version.Version = old }()

	m, mgr := newUpgradeEnv(t, true)
	if code, body := m.call("POST", "/api/upgrade", `{"target":"panel"}`); code != http.StatusAccepted ||
		body.Get("state.status").String() != upgrade.Requested {
		t.Fatalf("start panel upgrade: %d %s", code, body.Raw)
	}
	reqs, _ := os.ReadDir(filepath.Join(mgr.Dir, "requests"))
	if len(reqs) != 1 {
		t.Fatalf("requests = %v", reqs)
	}
	// The version comes from the release check, never from the browser.
	if b, _ := os.ReadFile(filepath.Join(mgr.Dir, "requests", reqs[0].Name())); string(b) != "panel v9.9.9\n" {
		t.Fatalf("request = %q", b)
	}
	if code, body := m.call("POST", "/api/upgrade", `{"target":"sing-box"}`); code != http.StatusConflict || body.Get("code").String() != "busy" {
		t.Fatalf("second upgrade while busy: %d %s", code, body.Raw)
	}
	if code, _ := m.call("POST", "/api/upgrade", `{"target":"../../etc"}`); code != http.StatusBadRequest {
		t.Fatalf("bad target: %d", code)
	}
	if !slices.Contains(m.auditActions(), "upgrade.request") {
		t.Fatalf("audit = %v", m.auditActions())
	}

	// The unit reports back; the outcome is audited once.
	os.WriteFile(filepath.Join(mgr.Dir, "status", "state"),
		[]byte("target=panel\nfrom=v1.0.1\nto=v9.9.9\nstatus=rolled_back\nmessage=health check failed\nstarted=1\nfinished=2\n"), 0o644)
	for range 2 {
		if code, v := m.call("GET", "/api/upgrade", ""); code != http.StatusOK || v.Get("state.status").String() != upgrade.RolledBack {
			t.Fatalf("state: %d %s", code, v.Raw)
		}
	}
	n := 0
	for _, a := range m.auditActions() {
		if a == "upgrade.rolled_back" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("rolled_back audited %d times: %v", n, m.auditActions())
	}
}

func TestStartUpgradeRefused(t *testing.T) {
	old := version.Version
	version.Version = "v9.9.9"
	defer func() { version.Version = old }()

	m, _ := newUpgradeEnv(t, false)
	if code, body := m.call("POST", "/api/upgrade", `{"target":"panel"}`); code != http.StatusConflict || body.Get("code").String() != "up_to_date" {
		t.Fatalf("panel already current: %d %s", code, body.Raw)
	}
	if code, body := m.call("POST", "/api/upgrade", `{"target":"sing-box"}`); code != http.StatusConflict || body.Get("code").String() != "not_managed" {
		t.Fatalf("sing-box not installed by install.sh: %d %s", code, body.Raw)
	}

	// Without the upgrade unit (e.g. Docker) new versions are only shown.
	m.h = New(Options{Store: m.st, SingboxConfig: m.cfgPath, Apply: m.mgr, Controller: m.ctl})
	m.cookie, m.csrf = m.login()
	if code, v := m.call("GET", "/api/upgrade", ""); code != http.StatusOK || v.Get("enabled").Bool() || v.Get("checking").Bool() {
		t.Fatalf("upgrade view without unit: %d %s", code, v.Raw)
	}
	if code, body := m.call("POST", "/api/upgrade", `{"target":"panel"}`); code != http.StatusConflict || body.Get("code").String() != "upgrade_off" {
		t.Fatalf("upgrade without unit: %d %s", code, body.Raw)
	}
}
