package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Signal controls a sing-box process on the same host by PID: reload is
// SIGHUP, which sing-box handles by re-reading its config. It cannot restart
// a process; use a process manager for that.
type Signal struct {
	PIDFile string
}

func (s *Signal) Name() string { return "signal" }

func (s *Signal) pid() (int, error) {
	b, err := os.ReadFile(s.PIDFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("%s: invalid pid", s.PIDFile)
	}
	return pid, nil
}

func (s *Signal) Status(context.Context) (Status, error) {
	pid, err := s.pid()
	if err != nil {
		return Status{State: "unknown"}, err
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return Status{State: "exited"}, nil
	}
	st := Status{State: "running"}
	if fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		st.StartedAt = fi.ModTime().UTC()
	}
	return st, nil
}

func (s *Signal) Reload(context.Context) error {
	pid, err := s.pid()
	if err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

func (s *Signal) Restart(context.Context) error { return ErrUnsupported }

// Systemd controls a sing-box systemd unit with systemctl. The panel must
// run as a user allowed to manage the unit.
type Systemd struct {
	Unit string
}

func (s *Systemd) Name() string { return "systemd" }

func (s *Systemd) systemctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *Systemd) Status(ctx context.Context) (Status, error) {
	out, err := s.systemctl(ctx, "show", s.Unit, "--property=ActiveState,ExecMainStartTimestamp,NRestarts")
	if err != nil {
		return Status{State: "unknown"}, fmt.Errorf("%v: %s", err, out)
	}
	st := Status{State: "unknown"}
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "ActiveState":
			if v == "active" {
				st.State = "running"
			} else {
				st.State = v
			}
		case "ExecMainStartTimestamp":
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", v); err == nil {
				st.StartedAt = t.UTC()
			}
		case "NRestarts":
			st.RestartCount, _ = strconv.Atoi(v)
		}
	}
	return st, nil
}

func (s *Systemd) Reload(ctx context.Context) error {
	if out, err := s.systemctl(ctx, "reload", s.Unit); err != nil {
		return errors.New(out)
	}
	return nil
}

func (s *Systemd) Restart(ctx context.Context) error {
	if out, err := s.systemctl(ctx, "restart", s.Unit); err != nil {
		return errors.New(out)
	}
	return nil
}
