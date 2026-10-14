// Package access keeps the host's IP rules for the proxy inbounds in step
// with the panel: a blocklist that always applies, and an allowlist that,
// once switched on, is the only way in.
//
// Rules are enforced outside sing-box, by the host controller: nftables for
// inbounds that listen on a public address, and HAProxy lists for inbounds
// that sit behind an SNI router (sing-box only sees 127.0.0.1 there).
package access

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/inbounds"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
)

// Lists.
const (
	Deny  = "deny"
	Allow = "allow"
)

// Enforcers.
const (
	ByFirewall = "firewall" // nftables on the host
	ByHAProxy  = "haproxy"  // HAProxy lists in front of the inbound
	ByNone     = "none"
)

// ConfirmWindow is how long an administrator has to confirm the allowlist
// before it switches itself off again.
const ConfirmWindow = 60 * time.Second

const (
	allowlistKey   = "access_allowlist"  // "on" | ""
	confirmByKey   = "access_confirm_by" // unix deadline while unconfirmed
	resyncInterval = 10 * time.Minute    // re-apply even if nothing changed
	checkInterval  = time.Minute         // expiry, deadline, config changes
)

// ErrUnavailable means the controller cannot enforce IP rules.
var ErrUnavailable = errors.New("the controller cannot manage IP rules; use the ctlfile controller (deploy/ctl)")

// Target is a proxy inbound and how its rules are enforced.
type Target struct {
	Tag      string   `json:"tag"`
	Type     string   `json:"type"`
	Listen   string   `json:"listen"`
	Port     int      `json:"port"`
	Networks []string `json:"networks"`
	By       string   `json:"by"`
	Reason   string   `json:"reason,omitempty"` // why By is none
}

// Targets works out who enforces rules for each proxy inbound.
func Targets(raw []byte, info control.AccessInfo) []Target {
	var out []Target
	for _, in := range protocol.Inbounds(raw) {
		t := Target{Tag: in.Tag, Type: in.Type, Listen: in.Raw.Get("listen").String(),
			Port: int(in.Raw.Get("listen_port").Int()), Networks: inbounds.Networks(in.Raw)}
		addr, err := netip.ParseAddr(t.Listen)
		local := err == nil && addr.IsLoopback()
		switch {
		case local && info.HAProxy:
			t.By = ByHAProxy
		case local:
			t.By, t.Reason = ByNone, "listens on a loopback address behind a front proxy that has no access lists configured"
		case info.NFT:
			t.By = ByFirewall
		default:
			t.By, t.Reason = ByNone, "nftables is not available on the host"
		}
		out = append(out, t)
	}
	return out
}

// Normalize turns an IP or CIDR into a canonical prefix (host bits cleared,
// IPv4-mapped addresses unmapped). Very wide prefixes are refused.
func Normalize(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an IP address or network", s)
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an IP address or network", s)
		}
		if a.Zone() != "" {
			return netip.Prefix{}, errors.New("zoned addresses are not supported")
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	p = p.Masked()
	if (p.Addr().Is4() && p.Bits() < 8) || (p.Addr().Is6() && p.Bits() < 16) {
		return netip.Prefix{}, fmt.Errorf("%s is too wide (at most /8 for IPv4, /16 for IPv6)", p)
	}
	return p, nil
}

// Spec renders the rule set in the controller's line format.
func Spec(targets []Target, rules []store.AccessRule, allowlist bool) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, t := range targets {
		if t.By != ByFirewall {
			continue
		}
		for _, n := range t.Networks {
			line := "port " + n + " " + strconv.Itoa(t.Port)
			if !seen[line] {
				seen[line] = true
				b.WriteString(line + "\n")
			}
		}
	}
	for _, r := range rules {
		b.WriteString(r.List + " " + r.CIDR + "\n")
	}
	if allowlist {
		b.WriteString("allowlist on\n")
	} else {
		b.WriteString("allowlist off\n")
	}
	return b.String()
}

// Contains reports whether any rule of list covers ip.
func Contains(rules []store.AccessRule, list string, ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, r := range rules {
		if r.List != list {
			continue
		}
		if p, err := netip.ParsePrefix(r.CIDR); err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

// Result is the outcome of the last time rules were handed to the host.
type Result struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Output string    `json:"output"`
}

// Manager applies rules and runs the background checks.
type Manager struct {
	Store      *store.Store
	Ctl        control.Access // nil = unavailable
	ConfigPath string
	Logger     *slog.Logger
	Now        func() time.Time

	mu       sync.Mutex
	last     Result
	lastSpec [32]byte
	lastAt   time.Time
	info     *control.AccessInfo
	infoAt   time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Info returns what the host can enforce, cached for a minute.
func (m *Manager) Info(ctx context.Context) (control.AccessInfo, error) {
	if m.Ctl == nil {
		return control.AccessInfo{}, ErrUnavailable
	}
	m.mu.Lock()
	if m.info != nil && m.now().Sub(m.infoAt) < time.Minute {
		info := *m.info
		m.mu.Unlock()
		return info, nil
	}
	m.mu.Unlock()
	info, err := m.Ctl.AccessInfo(ctx)
	if err != nil {
		return control.AccessInfo{}, err
	}
	m.mu.Lock()
	m.info, m.infoAt = &info, m.now()
	m.mu.Unlock()
	return info, nil
}

// Allowlist returns whether the allowlist is on and, while it awaits
// confirmation, the deadline.
func (m *Manager) Allowlist(ctx context.Context) (bool, time.Time, error) {
	on, err := m.Store.GetSetting(ctx, allowlistKey)
	if err != nil {
		return false, time.Time{}, err
	}
	by, err := m.Store.GetSetting(ctx, confirmByKey)
	if err != nil {
		return false, time.Time{}, err
	}
	var deadline time.Time
	if v, _ := strconv.ParseInt(by, 10, 64); v > 0 {
		deadline = time.Unix(v, 0).UTC()
	}
	return on == "on", deadline, nil
}

// SetAllowlist switches the allowlist. Switching it on starts the confirm
// window; Confirm must follow before the deadline.
func (m *Manager) SetAllowlist(ctx context.Context, on bool) (time.Time, error) {
	var deadline time.Time
	value, by := "", ""
	if on {
		rules, err := m.Store.ListAccessRules(ctx)
		if err != nil {
			return time.Time{}, err
		}
		if !slices.ContainsFunc(rules, func(r store.AccessRule) bool { return r.List == Allow }) {
			return time.Time{}, errors.New("add at least one allowlist entry first")
		}
		deadline = m.now().Add(ConfirmWindow).UTC().Truncate(time.Second)
		value, by = "on", strconv.FormatInt(deadline.Unix(), 10)
	}
	if err := m.Store.SetSetting(ctx, allowlistKey, value); err != nil {
		return time.Time{}, err
	}
	if err := m.Store.SetSetting(ctx, confirmByKey, by); err != nil {
		return time.Time{}, err
	}
	return deadline, m.Apply(ctx, true)
}

// Confirm keeps the allowlist on.
func (m *Manager) Confirm(ctx context.Context) error {
	on, deadline, err := m.Allowlist(ctx)
	if err != nil {
		return err
	}
	if !on || deadline.IsZero() {
		return errors.New("nothing to confirm")
	}
	return m.Store.SetSetting(ctx, confirmByKey, "")
}

// Apply hands the current rule set to the host. Unless force is set, it
// skips the call when the set is unchanged and was applied recently.
func (m *Manager) Apply(ctx context.Context, force bool) error {
	if m.Ctl == nil {
		return ErrUnavailable
	}
	info, err := m.Info(ctx)
	if err != nil {
		return err
	}
	file, err := singbox.ReadFile(m.ConfigPath)
	if err != nil {
		return err
	}
	rules, err := m.Store.ListAccessRules(ctx)
	if err != nil {
		return err
	}
	on, _, err := m.Allowlist(ctx)
	if err != nil {
		return err
	}
	spec := Spec(Targets(file.Raw, info), rules, on)
	sum := sha256.Sum256([]byte(spec))
	m.mu.Lock()
	fresh := sum == m.lastSpec && m.last.OK && m.now().Sub(m.lastAt) < resyncInterval
	m.mu.Unlock()
	if fresh && !force {
		return nil
	}
	out, err := m.Ctl.ApplyAccess(ctx, spec)
	m.mu.Lock()
	m.last = Result{At: m.now().UTC(), OK: err == nil, Output: strings.TrimSpace(out)}
	if err == nil {
		m.lastSpec, m.lastAt = sum, m.now()
	} else {
		m.lastSpec = [32]byte{}
	}
	m.mu.Unlock()
	return err
}

// Last returns the outcome of the last Apply.
func (m *Manager) Last() Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// Check expires rules, reverts an unconfirmed allowlist and re-applies when
// anything changed (including inbound ports in the sing-box config).
func (m *Manager) Check(ctx context.Context) error {
	now := m.now()
	expired, err := m.Store.PurgeExpiredAccessRules(ctx, now)
	if err != nil {
		return err
	}
	for _, r := range expired {
		m.audit(ctx, "access.expire", r.CIDR, r.List)
	}
	if on, deadline, err := m.Allowlist(ctx); err != nil {
		return err
	} else if on && !deadline.IsZero() && !now.Before(deadline) {
		if err := m.Store.SetSetting(ctx, allowlistKey, ""); err != nil {
			return err
		}
		if err := m.Store.SetSetting(ctx, confirmByKey, ""); err != nil {
			return err
		}
		m.audit(ctx, "access.allowlist_revert", "", "not confirmed in time")
	}
	return m.Apply(ctx, false)
}

// Run checks every minute until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	if m.Ctl == nil {
		return
	}
	t := time.NewTicker(checkInterval)
	defer t.Stop()
	for {
		if err := m.Check(ctx); err != nil && m.Logger != nil {
			m.Logger.Warn("applying IP rules failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) audit(ctx context.Context, action, target, detail string) {
	if err := m.Store.Audit(context.WithoutCancel(ctx), store.AuditEntry{Actor: "system", Action: action, Target: target, Detail: detail}); err != nil && m.Logger != nil {
		m.Logger.Error("audit write failed", "action", action, "err", err)
	}
}
