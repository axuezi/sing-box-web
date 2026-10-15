package server

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func codeFor(secret string, at time.Time) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

func TestTOTPLogin(t *testing.T) {
	m := newConfigEnv(t)
	_, body := m.call("POST", "/api/totp/setup", "")
	secret := body.Get("secret").String()
	if !strings.HasPrefix(body.Get("uri").String(), "otpauth://totp/") {
		t.Fatalf("setup: %s", body.Raw)
	}
	if code, _ := m.call("POST", "/api/totp/enable", `{"code":"000000"}`); code != http.StatusBadRequest {
		t.Fatalf("wrong code accepted: %d", code)
	}
	now := time.Now()
	if code, b := m.call("POST", "/api/totp/enable", fmt.Sprintf(`{"code":%q}`, codeFor(secret, now))); code != http.StatusNoContent {
		t.Fatalf("enable: %d %s", code, b.Raw)
	}

	login := func(code string) *string {
		body := fmt.Sprintf(`{"username":"admin","password":"correct horse battery","code":%q}`, code)
		rec := m.do("POST", "/api/login", body, fromAddr("198.51.100.20:1"))
		c := gjson.Get(rec.Body.String(), "code").String()
		if rec.Code == 200 {
			c = "ok"
		}
		return &c
	}
	if got := *login(""); got != "totp_required" {
		t.Fatalf("no code: %s", got)
	}
	if got := *login("123456"); got != "bad_credentials" {
		t.Fatalf("wrong code: %s", got)
	}
	// The code that confirmed setup is spent and cannot sign in.
	if got := *login(codeFor(secret, now)); got != "bad_credentials" {
		t.Fatalf("setup code reused for login: %s", got)
	}
	good := codeFor(secret, now.Add(30*time.Second))
	if got := *login(good); got != "ok" {
		t.Fatalf("good code: %s", got)
	}
	if got := *login(good); got != "bad_credentials" {
		t.Fatalf("replayed code accepted: %s", got)
	}
	// While on, the secret cannot be replaced with only a session.
	if code, b := m.call("POST", "/api/totp/setup", ""); code != http.StatusConflict || b.Get("code").String() != "totp_enabled" {
		t.Fatalf("setup while enabled: %d %s", code, b.Raw)
	}
	if code, _ := m.call("POST", "/api/totp/enable", fmt.Sprintf(`{"code":%q}`, good)); code != http.StatusConflict {
		t.Fatalf("enable while enabled: %d", code)
	}
	// Disabling needs password + a fresh code; the code used to log in is spent.
	if code, _ := m.call("POST", "/api/totp/disable", fmt.Sprintf(`{"password":"correct horse battery","code":%q}`, good)); code != http.StatusForbidden {
		t.Fatalf("disable with spent code: %d", code)
	}
}

func TestAccountRename(t *testing.T) {
	m := newConfigEnv(t)
	if code, b := m.call("PUT", "/api/account", `{"username":"axue","current":"wrong password"}`); code != http.StatusForbidden {
		t.Fatalf("wrong password: %d %s", code, b.Raw)
	}
	if code, _ := m.call("PUT", "/api/account", `{"username":"a b","current":"correct horse battery"}`); code != http.StatusBadRequest {
		t.Fatalf("bad name accepted: %d", code)
	}
	if code, b := m.call("PUT", "/api/account", `{"username":" axue ","current":"correct horse battery"}`); code != 200 || b.Get("username").String() != "axue" {
		t.Fatalf("rename: %d %s", code, b.Raw)
	}
	// The session survives; the old name no longer logs in, the new one does.
	if code, b := m.call("GET", "/api/me", ""); code != 200 || b.Get("username").String() != "axue" {
		t.Fatalf("me after rename: %d %s", code, b.Raw)
	}
	if rec := m.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old name still logs in: %d", rec.Code)
	}
	if rec := m.do("POST", "/api/login", `{"username":"axue","password":"correct horse battery"}`); rec.Code != 200 {
		t.Fatalf("new name: %d %s", rec.Code, rec.Body)
	}
	if acts := strings.Join(m.auditActions(), ","); !strings.Contains(acts, "account.rename") {
		t.Fatalf("audit: %s", acts)
	}
}
