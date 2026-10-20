// Package upgrade hands one-click upgrades to the host-side upgrade unit
// (deploy/upgrade/sing-box-web-upgrade, installed by install.sh).
//
// The panel never replaces binaries itself: it may not even be able to
// write them. It drops a request naming the target and the sing-box-web
// release into Dir/requests, the same way the ctlfile controller does; a
// systemd path unit runs the upgrade script as root, which checks the
// request, downloads the release assets, verifies them against the
// release's checksums.txt, swaps them in, restarts and health-checks the
// service, and rolls back on failure. Upgrading the panel restarts it, so
// there is no answer to wait for: the script records its progress in
// Dir/status/state, which the panel reads after it comes back.
package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/auth"
)

// Target is what gets upgraded.
type Target string

const (
	// Panel is sing-box-web itself.
	Panel Target = "panel"
	// SingBox is the sing-box build attached to a sing-box-web release.
	// Only offered when install.sh installed sing-box (KernelMark).
	SingBox Target = "sing-box"
)

// KernelMark is the file in the panel's data directory that install.sh
// creates when it installed sing-box, so the panel may replace it.
const KernelMark = ".installed-sing-box"

// Statuses written to the state file.
const (
	Requested  = "requested"   // by the panel; the unit has not picked it up yet
	Running    = "running"     // the unit is working on it
	Done       = "done"        // upgraded and healthy
	Failed     = "failed"      // nothing changed (download, checksum, check failed)
	RolledBack = "rolled_back" // the new version was unhealthy; the old one is back
)

const (
	// pickupTimeout is how long a request may wait for the unit. The
	// script drops requests older than a minute.
	pickupTimeout = 90 * time.Second
	// runTimeout bounds a running upgrade (the unit's TimeoutStartSec is
	// 15 minutes); after it the state is treated as interrupted.
	runTimeout = 20 * time.Minute
)

// ErrBusy means another upgrade is still requested or running.
var ErrBusy = errors.New("another upgrade is in progress")

var versionRE = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// State is the last upgrade's progress or outcome.
type State struct {
	Target   Target    `json:"target"`
	From     string    `json:"from,omitempty"`
	To       string    `json:"to,omitempty"`
	Status   string    `json:"status"`
	Message  string    `json:"message,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
}

// Busy reports whether the upgrade is still under way at now.
func (s *State) Busy(now time.Time) bool {
	switch s.Status {
	case Requested:
		return now.Sub(s.Started) < pickupTimeout
	case Running:
		return now.Sub(s.Started) < runTimeout
	}
	return false
}

// Manager submits upgrade requests and reads their state. A nil Manager or
// an empty Dir means one-click upgrades are off.
type Manager struct {
	Dir string
	Now func() time.Time
}

// Enabled reports whether requests can be made.
func (m *Manager) Enabled() bool { return m != nil && m.Dir != "" }

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) statePath() string { return filepath.Join(m.Dir, "status", "state") }

// State returns the last upgrade's state, nil when there was none. A
// request the unit never picked up, or a run that never finished, is
// reported as failed.
func (m *Manager) State() (*State, error) {
	if !m.Enabled() {
		return nil, nil
	}
	data, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := parseState(string(data))
	now := m.now()
	switch {
	case st.Status == Requested && !st.Busy(now):
		st.Status, st.Message = Failed, "the upgrade unit did not pick up the request (is sing-box-web-upgrade.path enabled?)"
	case st.Status == Running && !st.Busy(now):
		st.Status, st.Message = Failed, "the upgrade did not finish (interrupted?)"
	}
	return &st, nil
}

// parseState reads "key=value" lines; times are unix seconds.
func parseState(s string) State {
	var st State
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "target":
			st.Target = Target(v)
		case "from":
			st.From = v
		case "to":
			st.To = v
		case "status":
			st.Status = v
		case "message":
			st.Message = v
		case "started", "finished":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				if k == "started" {
					st.Started = time.Unix(n, 0).UTC()
				} else {
					st.Finished = time.Unix(n, 0).UTC()
				}
			}
		}
	}
	return st
}

// Request asks the unit to upgrade target from the sing-box-web release
// tag (e.g. "v1.1.0"). from and to are only shown to the administrator.
func (m *Manager) Request(target Target, tag, from, to string) error {
	if !m.Enabled() {
		return errors.New("one-click upgrades are off")
	}
	if target != Panel && target != SingBox {
		return fmt.Errorf("unknown upgrade target %q", target)
	}
	if !versionRE.MatchString(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	if st, err := m.State(); err != nil {
		return err
	} else if st != nil && st.Busy(m.now()) {
		return ErrBusy
	}
	reqDir := filepath.Join(m.Dir, "requests")
	for _, d := range []string{reqDir, filepath.Join(m.Dir, "status")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	now := m.now()
	state := fmt.Sprintf("target=%s\nfrom=%s\nto=%s\nstatus=%s\nstarted=%d\n", target, oneLine(from), oneLine(to), Requested, now.Unix())
	if err := writeAtomic(m.statePath(), state); err != nil {
		return err
	}
	// Dotfiles are ignored by the unit, so write then rename.
	id := "u" + strings.NewReplacer("_", "x", "-", "x").Replace(auth.RandomToken(12))
	tmp := filepath.Join(reqDir, "."+id)
	if err := os.WriteFile(tmp, []byte(string(target)+" "+tag+"\n"), 0o644); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(reqDir, id)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("submit request: %w", err)
	}
	return nil
}

func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

func writeAtomic(path, data string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
