package release

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, running string
		want            bool
	}{
		{"1.14.4", "sing-box 1.14.3", true},
		{"1.15.0", "1.14.9", true},
		{"2.0.0", "1.99.99", true},
		{"1.14.3", "sing-box 1.14.3", false},
		{"1.14.3", "1.14.4", false},
		{"1.15.0", "1.15.0-beta.3", true},
		{"1.15.0", "1.15.1-alpha.1", false},
		{"1.14.3", "sing-box unknown", false},
		{"v1.14.4", "v1.14.3", true},
		{"1.14.4", "1.14.3+custom", true},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.running); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.running, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	body := `{"tag_name":"v1.14.4","html_url":"https://example.test/r/v1.14.4","prerelease":false,"draft":false}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent; GitHub rejects requests without one")
		}
		io.WriteString(w, body)
	}))
	defer srv.Close()
	c := &Checker{URL: srv.URL, Logger: slog.New(slog.DiscardHandler)}
	if c.Latest() != nil {
		t.Fatal("Latest before any check should be nil")
	}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	l := c.Latest()
	if l == nil || l.Version != "1.14.4" || l.URL != "https://example.test/r/v1.14.4" || l.CheckedAt.IsZero() {
		t.Fatalf("Latest = %+v", l)
	}

	// A pre-release or a failure keeps the last good answer.
	body = `{"tag_name":"v1.15.0-beta.1","prerelease":true}`
	if err := c.Check(context.Background()); err == nil {
		t.Error("pre-release accepted")
	}
	if c.Latest().Version != "1.14.4" {
		t.Errorf("Latest changed to %+v", c.Latest())
	}
}
