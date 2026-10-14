package server

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/store"
)

const testConfig = `{
  "log": {"level": "info"},
  "inbounds": [{"type": "hysteria2", "tag": "hy2-in", "listen_port": 443,
                "users": [{"name": "owner", "password": "TOPSECRET"}]}],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`

type env struct {
	t       *testing.T
	h       http.Handler
	st      *store.Store
	dir     string
	logPath string
	authLog string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdmin(context.Background(), "admin", hash); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte(testConfig), 0o600)
	logPath := filepath.Join(dir, "sing-box.log")
	os.WriteFile(logPath, []byte(
		"+0000 2026-10-13 03:24:53 INFO [1 0ms] inbound/hysteria2[hy2-in]: inbound connection to example.com:443\n"+
			"+0000 2026-10-13 03:24:54 ERROR [2 0ms] connection: dial tcp: i/o timeout\n"), 0o644)
	e := &env{t: t, st: st, dir: dir, logPath: logPath, authLog: filepath.Join(dir, "auth.log")}
	e.h = New(Options{
		Static:         fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/app.js": {Data: []byte("x")}},
		Store:          st,
		SingboxConfig:  cfg,
		SingboxLog:     logPath,
		AuthLog:        e.authLog,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Limiter:        auth.NewLimiter(3, time.Minute, time.Minute),
	})
	return e
}

type reqOpt func(*http.Request)

func withCookie(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }
func withHeader(k, v string) reqOpt    { return func(r *http.Request) { r.Header.Set(k, v) } }
func fromAddr(a string) reqOpt         { return func(r *http.Request) { r.RemoteAddr = a } }

func (e *env) do(method, target, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) login() (*http.Cookie, string) {
	e.t.Helper()
	rec := e.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var me meResponse
	json.Unmarshal(rec.Body.Bytes(), &me)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				e.t.Fatalf("cookie flags: %+v", c)
			}
			return c, me.CSRFToken
		}
	}
	e.t.Fatal("no session cookie")
	return nil, ""
}

func (e *env) auditActions() []string {
	e.t.Helper()
	entries, err := e.st.ListAudit(context.Background(), 0, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		out = append(out, entries[i].Action)
	}
	return out
}

func TestHealthIsPublic(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("GET", "/api/health", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestProtectedEndpointsNeedSession(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/status", "/api/inbounds", "/api/audit", "/api/logs/stream", "/api/me"} {
		if rec := e.do("GET", p, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", p, rec.Code)
		}
	}
}

func TestLoginLogoutFlow(t *testing.T) {
	e := newEnv(t)
	cookie, csrf := e.login()

	if rec := e.do("GET", "/api/me", "", withCookie(cookie)); rec.Code != http.StatusOK {
		t.Fatalf("me: %d", rec.Code)
	}
	// State change without CSRF token is refused.
	if rec := e.do("POST", "/api/logout", "", withCookie(cookie)); rec.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/logout", "", withCookie(cookie), withHeader(csrfHeader, csrf)); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/me", "", withCookie(cookie)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", rec.Code)
	}
	got := strings.Join(e.auditActions(), ",")
	if got != "login.success,logout" {
		t.Fatalf("audit = %s", got)
	}
}

func TestLoginLockoutAndFail2banLog(t *testing.T) {
	e := newEnv(t)
	bad := `{"username":"admin","password":"wrong password!"}`
	for i := range 3 {
		if rec := e.do("POST", "/api/login", bad); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, rec.Code)
		}
	}
	// Now locked, even with the right password.
	rec := e.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("locked login: %d", rec.Code)
	}
	// A different client is unaffected.
	if rec := e.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`, fromAddr("198.51.100.1:1")); rec.Code != http.StatusOK {
		t.Fatalf("other client: %d", rec.Code)
	}

	data, _ := os.ReadFile(e.authLog)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || !strings.Contains(lines[0], "authentication failure from 203.0.113.7") ||
		!strings.Contains(lines[3], "login locked for 203.0.113.7") {
		t.Fatalf("auth log:\n%s", data)
	}
	got := strings.Join(e.auditActions(), ",")
	if got != "login.failure,login.failure,login.failure,login.locked,login.success" {
		t.Fatalf("audit = %s", got)
	}
}

func TestClientIPBehindTrustedProxy(t *testing.T) {
	e := newEnv(t)
	bad := `{"username":"admin","password":"wrong password!"}`
	viaProxy := []reqOpt{fromAddr("127.0.0.1:40000"), withHeader("X-Forwarded-For", "6.6.6.6, 198.51.100.9")}
	e.do("POST", "/api/login", bad, viaProxy...)
	// Untrusted peer cannot spoof X-Forwarded-For.
	e.do("POST", "/api/login", bad, withHeader("X-Forwarded-For", "1.1.1.1"))

	entries, _ := e.st.ListAudit(context.Background(), 0, 10)
	if entries[1].IP != "198.51.100.9" || entries[0].IP != "203.0.113.7" {
		t.Fatalf("ips = %s, %s", entries[1].IP, entries[0].IP)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`,
		withHeader("Origin", "https://evil.example"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	cookie, _ := e.login()
	rec := e.do("GET", "/api/status", "", withCookie(cookie))
	var st statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Config.Summary == nil || len(st.Config.Summary.Inbounds) != 1 || st.Service.Controller != "none" ||
		st.Runtime.Available || st.Log.Size == 0 {
		t.Fatalf("status = %s", rec.Body)
	}
}

func TestLogStreamBacklogFiltered(t *testing.T) {
	e := newEnv(t)
	cookie, _ := e.login()
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/logs/stream?level=error", nil)
	req.AddCookie(cookie)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)
	var data []string
	for sc.Scan() {
		line := sc.Text()
		if line == "event: ready" {
			break
		}
		if d, ok := strings.CutPrefix(line, "data: "); ok {
			data = append(data, d)
		}
	}
	if len(data) != 1 || !strings.Contains(data[0], `"level":"ERROR"`) {
		t.Fatalf("backlog = %q", data)
	}

	// New lines arrive after the backlog.
	f, _ := os.OpenFile(e.logPath, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("+0000 2026-10-13 03:25:00 ERROR [3 0ms] fresh failure\n")
	f.Close()
	for sc.Scan() {
		if strings.Contains(sc.Text(), "fresh failure") {
			return
		}
	}
	t.Fatal("did not receive followed line")
}

func TestPasswordChangeSignsOut(t *testing.T) {
	e := newEnv(t)
	cookie, csrf := e.login()
	rec := e.do("POST", "/api/password", `{"current":"correct horse battery","new":"a much better passphrase"}`,
		withCookie(cookie), withHeader(csrfHeader, csrf))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/me", "", withCookie(cookie)); rec.Code != http.StatusUnauthorized {
		t.Fatal("old session survived password change")
	}
	if rec := e.do("POST", "/api/login", `{"username":"admin","password":"a much better passphrase"}`); rec.Code != http.StatusOK {
		t.Fatalf("login with new password: %d", rec.Code)
	}
}

func TestSPA(t *testing.T) {
	e := newEnv(t)
	cases := []struct{ target, wantBody, wantCache string }{
		{"/", "<html>app</html>", "no-cache"},
		{"/users/42", "<html>app</html>", "no-cache"},
		{"/assets/app.js", "x", "public, max-age=31536000, immutable"},
	}
	for _, c := range cases {
		rec := e.do("GET", c.target, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s: %d %q", c.target, rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.wantCache {
			t.Errorf("%s: cache-control = %q", c.target, got)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing security headers", c.target)
		}
	}
	if rec := e.do("GET", "/api/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown api: %d", rec.Code)
	}
}

func TestStaticGzip(t *testing.T) {
	big := strings.Repeat("console.log('sing-box-web');\n", 200)
	h := spaHandler(fstest.MapFS{
		"index.html":      {Data: []byte("<html>" + strings.Repeat("app ", 400) + "</html>")},
		"assets/app.js":   {Data: []byte(big)},
		"assets/tiny.css": {Data: []byte("a{}")},
		"assets/logo.png": {Data: []byte(strings.Repeat("\x89PNG", 400))},
	})
	get := func(target, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	rec := get("/assets/app.js", "gzip, deflate, br")
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") ||
		rec.Header().Get("Vary") != "Accept-Encoding" || rec.Body.Len() >= len(big)/5 {
		t.Fatalf("gzip js: %v len %d", rec.Header(), rec.Body.Len())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != big {
		t.Fatal("decompressed body differs")
	}
	if rec := get("/assets/app.js", ""); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != big {
		t.Fatal("plain js without Accept-Encoding")
	}
	if rec := get("/assets/app.js", "gzip;q=0"); rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 must be honoured")
	}
	if rec := get("/assets/tiny.css", "gzip"); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "a{}" {
		t.Fatal("tiny files stay plain")
	}
	if rec := get("/assets/logo.png", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("images stay plain")
	}
	if rec := get("/users/7", "gzip"); rec.Header().Get("Content-Encoding") != "gzip" ||
		rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index fallback: %v", rec.Header())
	}
}
