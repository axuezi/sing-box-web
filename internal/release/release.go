// Package release learns the newest stable sing-box release from GitHub, so
// the panel can say an upgrade exists. It only informs: nothing is
// downloaded or installed, since sing-box is usually a custom build (for
// example with the V2Ray API) that the administrator rebuilds themselves.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultURL is GitHub's "latest release" endpoint for sing-box; it skips
// drafts and pre-releases.
const DefaultURL = "https://api.github.com/repos/SagerNet/sing-box/releases/latest"

// Latest is the newest stable release as last seen.
type Latest struct {
	Version   string    `json:"version"` // e.g. "1.14.4", without the leading v
	URL       string    `json:"url"`     // release notes page
	CheckedAt time.Time `json:"checked_at"`
}

// Checker polls for the latest release every Interval.
type Checker struct {
	URL      string // default DefaultURL
	Interval time.Duration
	Client   *http.Client
	Logger   *slog.Logger

	mu     sync.Mutex
	latest *Latest
}

// Run checks shortly after start and then every Interval until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	iv := c.Interval
	if iv == 0 {
		iv = 6 * time.Hour
	}
	// Let startup settle (and sing-box come up) before the first request.
	wait := time.NewTimer(30 * time.Second)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if err := c.Check(ctx); err != nil {
			c.Logger.Debug("sing-box release check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check fetches the latest release once and remembers it.
func (c *Checker) Check(ctx context.Context) error {
	url := c.URL
	if url == "" {
		url = DefaultURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sing-box-web")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release check: %s", resp.Status)
	}
	var body struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	v := strings.TrimPrefix(body.TagName, "v")
	if _, ok := parse(v); !ok || body.Prerelease || body.Draft {
		return errors.New("release check: unexpected release " + body.TagName)
	}
	c.mu.Lock()
	c.latest = &Latest{Version: v, URL: body.HTMLURL, CheckedAt: time.Now()}
	c.mu.Unlock()
	return nil
}

// Latest returns the last release seen, or nil before the first success.
func (c *Checker) Latest() *Latest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		return nil
	}
	l := *c.latest
	return &l
}

// Newer reports whether latest is a later version than running. running
// may carry a prefix such as "sing-box 1.14.3"; a version it cannot parse
// is never reported as outdated.
func Newer(latest, running string) bool {
	if f := strings.Fields(running); len(f) > 0 {
		running = f[len(f)-1]
	}
	l, ok1 := parse(strings.TrimPrefix(latest, "v"))
	r, ok2 := parse(strings.TrimPrefix(running, "v"))
	if !ok1 || !ok2 {
		return false
	}
	for i := range l.core {
		if l.core[i] != r.core[i] {
			return l.core[i] > r.core[i]
		}
	}
	// Same numbers: a running pre-release (1.15.0-beta.2) is older than the
	// stable 1.15.0.
	return r.pre && !l.pre
}

type version struct {
	core [3]int
	pre  bool
}

// parse reads "1.14.3", "1.15.0-beta.2" or "1.14.3+build".
func parse(s string) (version, bool) {
	var v version
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, v.pre = s[:i], true
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.core[i] = n
	}
	return v, true
}
