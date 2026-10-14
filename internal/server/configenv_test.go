package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/apply"
	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/tidwall/gjson"
)

const managedConfig = `{
  "log": {"level": "info"},
  "inbounds": [
    {"type": "hysteria2", "tag": "hy2-in", "listen": "::", "listen_port": 443,
     "users": [{"name": "owner", "password": "owner-hy2-pass"}], "tls": {"enabled": true}},
    {"type": "vless", "tag": "reality-in", "listen": "127.0.0.1", "listen_port": 8443,
     "users": [{"name": "owner", "uuid": "11111111-2222-4333-8444-555555555555", "flow": "xtls-rprx-vision"}],
     "tls": {"enabled": true, "server_name": "www.cloudflare.com",
             "reality": {"enabled": true, "private_key": "8BzFOZ8Y4u9I_JoYgTZWyvMY5y0UPu__HTSwwbHIRUU", "short_id": ["ab12"]}}}
  ],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`

type okChecker struct{}

func (okChecker) Check(_ context.Context, cfg []byte) (string, error) {
	if strings.Contains(string(cfg), "reject-me") {
		return "FATAL bad option", fmt.Errorf("exit status 1")
	}
	return "", nil
}

type runningCtl struct{ reloads int }

func (c *runningCtl) Name() string { return "test" }

func (c *runningCtl) Status(context.Context) (control.Status, error) {
	return control.Status{State: "running"}, nil
}

func (c *runningCtl) Reload(context.Context) error { c.reloads++; return nil }

func (c *runningCtl) Restart(context.Context) error { c.reloads++; return nil }

type configEnv struct {
	*env
	cfgPath string
	ctl     *runningCtl
	mgr     *apply.Manager
	cookie  *http.Cookie
	csrf    string
}

func newConfigEnv(t *testing.T) *configEnv {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("correct horse battery")
	st.SetAdmin(ctx, "admin", hash)
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte(managedConfig), 0o600)
	logPath := filepath.Join(dir, "sing-box.log")
	os.WriteFile(logPath, nil, 0o644)

	// Import existing users the way main does on first start.
	imported := protocol.Import([]byte(managedConfig))
	var ptrs []*store.User
	for i := range imported {
		ptrs = append(ptrs, &imported[i])
	}
	if err := st.SaveUsers(ctx, ptrs, nil); err != nil {
		t.Fatal(err)
	}

	ctl := &runningCtl{}
	mgr := &apply.Manager{ConfigPath: cfg, LogPath: logPath, Store: st, Controller: ctl, Checker: okChecker{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Settle: time.Millisecond, HealthTimeout: time.Second}
	e := &env{t: t, st: st, dir: dir, logPath: logPath}
	e.h = New(Options{Store: st, SingboxConfig: cfg, SingboxLog: logPath, Apply: mgr, Controller: ctl})
	m := &configEnv{env: e, cfgPath: cfg, ctl: ctl, mgr: mgr}
	m.cookie, m.csrf = e.login()
	return m
}

func (m *configEnv) call(method, path, body string) (int, gjson.Result) {
	m.t.Helper()
	rec := m.do(method, path, body, withCookie(m.cookie), withHeader(csrfHeader, m.csrf))
	return rec.Code, gjson.Parse(rec.Body.String())
}

func (m *configEnv) config() gjson.Result {
	b, _ := os.ReadFile(m.cfgPath)
	return gjson.ParseBytes(b)
}
