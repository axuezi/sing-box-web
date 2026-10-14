package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/limits"
	"github.com/axuezi/sing-box-web/internal/store"
)

func (m *configEnv) enforcer() *limits.Enforcer {
	return &limits.Enforcer{Store: m.st, Apply: m.mgr, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func (m *configEnv) inboundUsers() int64 {
	c := m.config()
	return c.Get("inbounds.0.users.#").Int() + c.Get("inbounds.1.users.#").Int()
}

func TestQuotaLimitAndUsageReset(t *testing.T) {
	m := newConfigEnv(t)
	ctx := context.Background()

	if code, b := m.call("PATCH", "/api/users/1", `{"quota_bytes":1000,"reset_day":5}`); code != 200 {
		t.Fatalf("set quota: %d %s", code, b.Raw)
	}
	if code, _ := m.call("PATCH", "/api/users/1", `{"reset_day":29}`); code != 400 {
		t.Fatalf("reset_day 29 accepted: %d", code)
	}
	if code, _ := m.call("PATCH", "/api/users/1", `{"quota_bytes":-1}`); code != 400 {
		t.Fatalf("negative quota accepted: %d", code)
	}

	day := time.Now().Format(time.DateOnly)
	if err := m.st.AddTraffic(ctx, []store.TrafficDelta{{Day: day, Kind: "user", Key: "1", Up: 400, Down: 300}}); err != nil {
		t.Fatal(err)
	}
	e := m.enforcer()
	if ch, err := e.Check(ctx); err != nil || len(ch) != 0 {
		t.Fatalf("under quota: %v %v", ch, err)
	}
	m.st.AddTraffic(ctx, []store.TrafficDelta{{Day: day, Kind: "user", Key: "1", Down: 300}})
	ch, err := e.Check(ctx)
	if err != nil || len(ch) != 1 || ch[0].To != store.LimitQuota {
		t.Fatalf("over quota: %v %v", ch, err)
	}
	if n := m.inboundUsers(); n != 0 {
		t.Fatalf("limited user still in config: %d entries", n)
	}
	_, body := m.call("GET", "/api/users", "")
	u := body.Get("users.0")
	if u.Get("limited").String() != "quota" || u.Get("cycle_up").Int() != 400 || u.Get("cycle_down").Int() != 600 ||
		u.Get("reset_day").Int() != 5 || !u.Get("enabled").Bool() {
		t.Fatalf("user view: %s", u.Raw)
	}
	if !strings.Contains(strings.Join(m.auditActions(), ","), "user.limited") {
		t.Fatalf("audit: %v", m.auditActions())
	}

	// A limited user keeps their subscription, which reports usage.
	_, body = m.call("GET", "/api/users/1/client", "")
	path := strings.TrimPrefix(body.Get("subscription.mihomo").String(), "http://example.com")
	rec := m.do("GET", path, "")
	if rec.Code != 200 || rec.Header().Get("Subscription-Userinfo") != "upload=400; download=600; total=1000" {
		t.Fatalf("subscription: %d %q", rec.Code, rec.Header().Get("Subscription-Userinfo"))
	}

	if code, b := m.call("POST", "/api/users/1/reset-usage", `{}`); code != 200 {
		t.Fatalf("reset usage: %d %s", code, b.Raw)
	}
	if n := m.inboundUsers(); n != 2 {
		t.Fatalf("user not restored after reset: %d entries", n)
	}
	got, _ := m.st.GetUser(ctx, 1)
	if got.Limited != "" || got.CycleUsed() != 0 || time.Since(got.CycleStart) > time.Minute {
		t.Fatalf("after reset: %+v", got)
	}
	if ch, err := e.Check(ctx); err != nil || len(ch) != 0 {
		t.Fatalf("stable after reset: %v %v", ch, err)
	}
}

func TestExpiryAndRenew(t *testing.T) {
	m := newConfigEnv(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Hour).Unix()
	if code, b := m.call("PATCH", "/api/users/1", fmt.Sprintf(`{"expires_at":%d}`, past)); code != 200 {
		t.Fatalf("set expiry: %d %s", code, b.Raw)
	}
	if n := m.inboundUsers(); n != 0 {
		t.Fatalf("expired user still in config: %d entries", n)
	}
	got, _ := m.st.GetUser(ctx, 1)
	if got.Limited != store.LimitExpired {
		t.Fatalf("limited = %q", got.Limited)
	}

	if code, _ := m.call("POST", "/api/users/1/renew", `{}`); code != 400 {
		t.Fatalf("empty renew accepted: %d", code)
	}
	// Renewing a lapsed user counts from now, not from the old expiry.
	if code, b := m.call("POST", "/api/users/1/renew", `{"months":1}`); code != 200 {
		t.Fatalf("renew: %d %s", code, b.Raw)
	}
	got, _ = m.st.GetUser(ctx, 1)
	want := time.Now().AddDate(0, 1, 0)
	if got.Limited != "" || got.ExpiresAt.Sub(want).Abs() > time.Minute {
		t.Fatalf("after renew: limited=%q expires=%v want≈%v", got.Limited, got.ExpiresAt, want)
	}
	if n := m.inboundUsers(); n != 2 {
		t.Fatalf("renewed user not in config: %d entries", n)
	}
	// Renewing a current user extends from the existing expiry.
	m.call("POST", "/api/users/1/renew", `{"days":10}`)
	again, _ := m.st.GetUser(ctx, 1)
	if d := again.ExpiresAt.Sub(got.ExpiresAt); d < 239*time.Hour || d > 241*time.Hour {
		t.Fatalf("extended by %v", d)
	}

	if code, _ := m.call("PATCH", "/api/users/1", `{"expires_at":0}`); code != 200 {
		t.Fatal("clear expiry")
	}
	got, _ = m.st.GetUser(ctx, 1)
	if !got.ExpiresAt.IsZero() {
		t.Fatalf("expiry not cleared: %v", got.ExpiresAt)
	}

	// Expiry reached while the panel runs: the enforcer takes the user out.
	m.call("PATCH", "/api/users/1", fmt.Sprintf(`{"expires_at":%d}`, time.Now().Add(time.Hour).Unix()))
	e := m.enforcer()
	e.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if ch, err := e.Check(ctx); err != nil || len(ch) != 1 || ch[0].To != store.LimitExpired {
		t.Fatalf("enforce expiry: %v %v", ch, err)
	}
	if n := m.inboundUsers(); n != 0 {
		t.Fatalf("expired user still in config: %d entries", n)
	}
}

func TestEnforcerBacksOffAfterFailedApply(t *testing.T) {
	m := newConfigEnv(t)
	ctx := context.Background()
	m.call("PATCH", "/api/users/1", `{"quota_bytes":10}`)
	m.st.AddTraffic(ctx, []store.TrafficDelta{{Day: time.Now().Format(time.DateOnly), Kind: "user", Key: "1", Up: 50}})

	// sing-box refuses the change: the enforcer must not retry every minute.
	e := m.enforcer()
	m.mgr.Checker = rejectAll{}
	if _, err := e.Check(ctx); err == nil {
		t.Fatal("expected apply failure")
	}
	m.mgr.Checker = okChecker{}
	if ch, err := e.Check(ctx); err != nil || len(ch) != 0 {
		t.Fatalf("retried during backoff: %v %v", ch, err)
	}
	e.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if ch, err := e.Check(ctx); err != nil || len(ch) != 1 {
		t.Fatalf("after backoff: %v %v", ch, err)
	}
}

type rejectAll struct{}

func (rejectAll) Check(context.Context, []byte) (string, error) {
	return "FATAL", fmt.Errorf("exit status 1")
}
