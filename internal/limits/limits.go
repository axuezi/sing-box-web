// Package limits takes users out of the config when they expire or use up
// their monthly quota, and puts them back once renewed or reset.
package limits

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/apply"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/store"
)

// Actor is the audit actor for changes the enforcer makes.
const Actor = "system"

// Enforcer periodically starts new quota cycles and applies limit changes.
type Enforcer struct {
	Store    *store.Store
	Apply    *apply.Manager
	Location *time.Location
	Logger   *slog.Logger
	Interval time.Duration
	// Backoff is how long to wait after a failed apply before trying again,
	// so a config sing-box refuses does not cause a reload every minute.
	Backoff time.Duration
	Now     func() time.Time

	mu        sync.Mutex
	failUntil time.Time
	kickOnce  sync.Once
	kick      chan struct{}
}

func (e *Enforcer) kicks() chan struct{} {
	e.kickOnce.Do(func() { e.kick = make(chan struct{}, 1) })
	return e.kick
}

// Kick asks Run to check now instead of at the next tick, e.g. right after
// fresh usage was recorded, so an exhausted quota takes effect within one
// collection. It never blocks; kicks while a check is pending coalesce.
func (e *Enforcer) Kick() {
	select {
	case e.kicks() <- struct{}{}:
	default:
	}
}

// Run checks every Interval, and whenever kicked, until ctx is done.
func (e *Enforcer) Run(ctx context.Context) {
	iv := e.Interval
	if iv == 0 {
		iv = time.Minute
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if _, err := e.Check(ctx); err != nil {
			e.Logger.Warn("enforcing user limits failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.kicks():
		}
	}
}

func (e *Enforcer) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Enforcer) loc() *time.Location {
	if e.Location != nil {
		return e.Location
	}
	return time.Local
}

// Change is one user whose limit state changed.
type Change struct {
	ID   int64
	Name string
	From string // previous reason ("" = was not limited)
	To   string // new reason ("" = no longer limited)
}

// Check rolls quota cycles over and, if any user's limit state changed,
// re-renders and applies the config. It returns the changes it applied.
func (e *Enforcer) Check(ctx context.Context) ([]Change, error) {
	now := e.now()
	if err := e.Store.RollCycles(ctx, now, e.loc()); err != nil {
		return nil, err
	}
	users, err := e.Store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	if len(diff(users, now)) == 0 {
		return nil, nil
	}
	e.mu.Lock()
	wait := now.Before(e.failUntil)
	e.mu.Unlock()
	if wait {
		return nil, nil
	}

	var changes []Change
	var upsert []*store.User
	build := func(cur []byte) ([]byte, error) {
		// Re-read under the apply lock: a panel change may have landed since.
		users, err := e.Store.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		changes, upsert = nil, nil
		for _, c := range diff(users, now) {
			for i := range users {
				if users[i].ID == c.ID {
					u := users[i]
					u.Limited = c.To
					upsert = append(upsert, &u)
				}
			}
			changes = append(changes, c)
		}
		return protocol.RenderManaged(ctx, e.Store, cur, withUpdates(users, upsert))
	}
	commit := func(ctx context.Context) error { return e.Store.SaveUsers(ctx, upsert, nil) }

	res, err := e.Apply.Update(ctx, apply.Change{Actor: Actor, Source: "panel", Summary: summary(changes)}, build, commit)
	if err != nil {
		backoff := e.Backoff
		if backoff == 0 {
			backoff = 15 * time.Minute
		}
		e.mu.Lock()
		e.failUntil = now.Add(backoff)
		e.mu.Unlock()
		return nil, fmt.Errorf("apply: %w", err)
	}
	for _, c := range changes {
		action := "user.limited"
		if c.To == "" {
			action = "user.unlimited"
		}
		detail := c.To
		if c.To == "" {
			detail = c.From
		}
		if err := e.Store.Audit(context.WithoutCancel(ctx), store.AuditEntry{
			Actor: Actor, Action: action, Target: c.Name, Detail: detail + " (" + res.Status + ")",
		}); err != nil {
			e.Logger.Error("audit write failed", "action", action, "err", err)
		}
		e.Logger.Info("user limit changed", "user", c.Name, "from", c.From, "to", c.To, "status", res.Status)
	}
	return changes, nil
}

// diff lists users whose stored limit state no longer matches now.
func diff(users []store.User, now time.Time) []Change {
	var out []Change
	for _, u := range users {
		if want := u.LimitReason(now); want != u.Limited {
			out = append(out, Change{ID: u.ID, Name: u.Name, From: u.Limited, To: want})
		}
	}
	return out
}

// withUpdates returns users with the updated copies swapped in.
func withUpdates(users []store.User, upsert []*store.User) []store.User {
	out := make([]store.User, len(users))
	copy(out, users)
	for _, p := range upsert {
		for i := range out {
			if out[i].ID == p.ID {
				out[i] = *p
			}
		}
	}
	return out
}

func summary(changes []Change) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		if c.To == "" {
			parts = append(parts, c.Name+" restored")
		} else {
			parts = append(parts, c.Name+" "+c.To)
		}
	}
	return "user.limits " + strings.Join(parts, ", ")
}
