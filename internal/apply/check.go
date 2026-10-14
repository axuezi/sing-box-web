package apply

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Checker validates a candidate config.
type Checker interface {
	Check(ctx context.Context, config []byte) (output string, err error)
}

// CommandChecker runs a command such as `sing-box check -c {config}`. The
// candidate is written to a temp file in TempDir and {config} in Command is
// replaced by its path.
type CommandChecker struct {
	Command []string
	TempDir string
	Timeout time.Duration
}

// ParseCommand splits a check command line on spaces.
func ParseCommand(s string) ([]string, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil, errors.New("empty check command")
	}
	found := false
	for _, a := range f {
		if strings.Contains(a, "{config}") {
			found = true
		}
	}
	if !found {
		return nil, errors.New("check command must contain {config}")
	}
	return f, nil
}

func (c *CommandChecker) Check(ctx context.Context, config []byte) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	f, err := os.CreateTemp(c.TempDir, "candidate-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return "", err
	}
	if _, err := f.Write(config); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	args := make([]string, len(c.Command))
	for i, a := range c.Command {
		args[i] = strings.ReplaceAll(a, "{config}", f.Name())
	}
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	return stripANSI(string(out)), err
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
