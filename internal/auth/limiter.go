package auth

import (
	"sync"
	"time"
)

// Limiter locks out an IP after too many failed logins within a window.
type Limiter struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
	Now         func() time.Time

	mu   sync.Mutex
	byIP map[string]*ipState
}

type ipState struct {
	failures    []time.Time
	lockedUntil time.Time
}

// NewLimiter returns a limiter: maxFailures within window locks for lockout.
func NewLimiter(maxFailures int, window, lockout time.Duration) *Limiter {
	return &Limiter{MaxFailures: maxFailures, Window: window, Lockout: lockout, Now: time.Now, byIP: map[string]*ipState{}}
}

// Locked reports whether ip is locked out and until when.
func (l *Limiter) Locked(ip string) (bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.byIP[ip]
	if st == nil {
		return false, time.Time{}
	}
	if l.Now().Before(st.lockedUntil) {
		return true, st.lockedUntil
	}
	return false, time.Time{}
}

// Fail records a failed attempt and reports whether this failure caused a
// new lockout.
func (l *Limiter) Fail(ip string) (lockedNow bool, until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	st := l.byIP[ip]
	if st == nil {
		st = &ipState{}
		l.byIP[ip] = st
	}
	cutoff := now.Add(-l.Window)
	kept := st.failures[:0]
	for _, t := range st.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	st.failures = append(kept, now)
	if len(st.failures) >= l.MaxFailures {
		st.failures = nil
		st.lockedUntil = now.Add(l.Lockout)
		return true, st.lockedUntil
	}
	return false, time.Time{}
}

// Succeed clears the failure history for ip.
func (l *Limiter) Succeed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byIP, ip)
}

// Prune drops state that no longer matters; call periodically.
func (l *Limiter) Prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	for ip, st := range l.byIP {
		recent := len(st.failures) > 0 && st.failures[len(st.failures)-1].After(now.Add(-l.Window))
		if !recent && now.After(st.lockedUntil) {
			delete(l.byIP, ip)
		}
	}
}
