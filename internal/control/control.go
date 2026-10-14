// Package control reloads, restarts and inspects the sing-box service.
package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/auth"
)

// ErrUnsupported is returned by controllers that cannot perform an action.
var ErrUnsupported = errors.New("not supported by this controller")

// Status is what the controller knows about the running service.
type Status struct {
	State        string    `json:"state"` // e.g. running, exited, unknown
	StartedAt    time.Time `json:"started_at,omitzero"`
	RestartCount int       `json:"restart_count"`
}

// Controller acts on the sing-box service.
type Controller interface {
	Name() string
	Status(ctx context.Context) (Status, error)
	Reload(ctx context.Context) error
	Restart(ctx context.Context) error
}

// None is used when no controller is configured: status is unknown and
// actions are refused.
type None struct{}

func (None) Name() string                           { return "none" }
func (None) Status(context.Context) (Status, error) { return Status{State: "unknown"}, nil }
func (None) Reload(context.Context) error           { return ErrUnsupported }
func (None) Restart(context.Context) error          { return ErrUnsupported }

// CtlFile talks to the host-side sing-box-web-ctl unit through files: it
// drops <id> into Dir/requests and waits for Dir/results/<id>.
type CtlFile struct {
	Dir     string
	Timeout time.Duration
	Poll    time.Duration
}

func (c *CtlFile) Name() string { return "ctlfile" }

func (c *CtlFile) Status(ctx context.Context) (Status, error) {
	out, err := c.do(ctx, "status")
	if err != nil {
		return Status{State: "unknown"}, err
	}
	// "<state> <startedAt RFC3339> <restartCount>"
	f := strings.Fields(out)
	if len(f) != 3 {
		return Status{State: "unknown"}, fmt.Errorf("unexpected status output %q", out)
	}
	st := Status{State: f[0]}
	st.StartedAt, _ = time.Parse(time.RFC3339Nano, f[1])
	st.RestartCount, _ = strconv.Atoi(f[2])
	return st, nil
}

func (c *CtlFile) Reload(ctx context.Context) error {
	_, err := c.do(ctx, "reload")
	return err
}

func (c *CtlFile) Restart(ctx context.Context) error {
	_, err := c.do(ctx, "restart")
	return err
}

func (c *CtlFile) do(ctx context.Context, action string) (string, error) {
	timeout, poll := c.Timeout, c.Poll
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if poll == 0 {
		poll = 100 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id := "r" + strings.NewReplacer("_", "x").Replace(auth.RandomToken(12))
	reqDir := filepath.Join(c.Dir, "requests")
	resPath := filepath.Join(c.Dir, "results", id)

	// Dotfiles are ignored by the host script, so write then rename.
	tmp := filepath.Join(reqDir, "."+id)
	if err := os.WriteFile(tmp, []byte(action+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(reqDir, id)); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("submit request: %w", err)
	}

	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%s: no answer from controller (is sing-box-web-ctl.path running?)", action)
		case <-t.C:
		}
		data, err := os.ReadFile(resPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		os.Remove(resPath)
		// line 1: exit code, line 2: time, rest: output
		parts := strings.SplitN(string(data), "\n", 3)
		if len(parts) < 3 {
			return "", fmt.Errorf("malformed controller result")
		}
		out := strings.TrimSpace(parts[2])
		if rc, _ := strconv.Atoi(parts[0]); rc != 0 {
			return out, fmt.Errorf("%s failed (exit %d): %s", action, rc, out)
		}
		return out, nil
	}
}
