package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Fatalf("verify good password: ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong horse battery", h); ok {
		t.Fatal("wrong password verified")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestPasswordTooShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error for short password")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$AA$AA", "$argon2id$v=19$m=x$AA$AA"} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("%q: expected error", h)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewLimiter(3, time.Minute, 10*time.Minute)
	l.Now = func() time.Time { return now }

	for i := range 2 {
		if locked, _ := l.Fail("1.2.3.4"); locked {
			t.Fatalf("locked after %d failures", i+1)
		}
	}
	if locked, _ := l.Locked("1.2.3.4"); locked {
		t.Fatal("locked too early")
	}
	locked, until := l.Fail("1.2.3.4")
	if !locked || !until.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("third failure: locked=%v until=%v", locked, until)
	}
	if locked, _ := l.Locked("5.6.7.8"); locked {
		t.Fatal("other IP must not be locked")
	}

	now = now.Add(11 * time.Minute)
	if locked, _ := l.Locked("1.2.3.4"); locked {
		t.Fatal("lock should expire")
	}
}

func TestLimiterWindowExpires(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewLimiter(2, time.Minute, time.Hour)
	l.Now = func() time.Time { return now }
	l.Fail("ip")
	now = now.Add(2 * time.Minute)
	if locked, _ := l.Fail("ip"); locked {
		t.Fatal("old failure outside window must not count")
	}
	l.Succeed("ip")
	if locked, _ := l.Fail("ip"); locked {
		t.Fatal("success must reset failures")
	}
}

func TestTOTPRFC6238Vectors(t *testing.T) {
	// RFC 6238 appendix B, SHA1 seed "12345678901234567890", 8 digits
	// truncated to the last 6.
	key := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if got := totpCode(key, v.unix/30); got != v.want {
			t.Errorf("t=%d: got %s want %s", v.unix, got, v.want)
		}
	}
	secret := b32.EncodeToString(key)
	now := time.Unix(1234567890, 0)
	if step, ok := VerifyTOTP(secret, "005924", now); !ok || step != 1234567890/30 {
		t.Fatal("valid code rejected")
	}
	if _, ok := VerifyTOTP(secret, "005924", now.Add(31*time.Second)); !ok {
		t.Fatal("previous step should be accepted")
	}
	if _, ok := VerifyTOTP(secret, "005924", now.Add(2*time.Minute)); ok {
		t.Fatal("stale code accepted")
	}
	if !strings.HasPrefix(TOTPURI(NewTOTPSecret(), "admin", "sing-box-web"), "otpauth://totp/sing-box-web:admin?") {
		t.Fatal("uri")
	}
}
