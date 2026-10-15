package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Access is implemented by controllers that can enforce IP rules on the
// host and manage fail2ban (sing-box-web-ctl does, see deploy/ctl).
type Access interface {
	AccessInfo(ctx context.Context) (AccessInfo, error)
	// ApplyAccess hands over a complete rule set (see the ctl script for the
	// line format) and returns the controller's summary.
	ApplyAccess(ctx context.Context, spec string) (string, error)
	// AccessStats returns packets dropped per rule kind ("deny", "allow").
	AccessStats(ctx context.Context) (map[string]int64, error)
	Bans(ctx context.Context) ([]Ban, error)
	Ban(ctx context.Context, jail, ip string) error
	Unban(ctx context.Context, jail, ip string) error
}

// AccessInfo says what the host can enforce.
type AccessInfo struct {
	NFT     bool     `json:"nft"`     // nftables for directly exposed inbounds
	HAProxy bool     `json:"haproxy"` // lists for inbounds behind HAProxy
	Jails   []string `json:"jails"`   // fail2ban jails that may be managed
}

// Ban is one fail2ban ban.
type Ban struct {
	Jail  string    `json:"jail"`
	IP    string    `json:"ip"`
	Since time.Time `json:"since,omitzero"`
	Until time.Time `json:"until,omitzero"`
}

func (c *CtlFile) AccessInfo(ctx context.Context) (AccessInfo, error) {
	out, err := c.do(ctx, "access-info")
	if err != nil {
		return AccessInfo{}, err
	}
	var info AccessInfo
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "nft":
			info.NFT = f[1] == "yes"
		case "haproxy":
			info.HAProxy = f[1] == "yes"
		case "fail2ban":
			if f[1] != "no" {
				info.Jails = f[1:]
			}
		}
	}
	return info, nil
}

func (c *CtlFile) ApplyAccess(ctx context.Context, spec string) (string, error) {
	dir := filepath.Join(c.Dir, "access")
	// A unique dotfile per call (the host script ignores dotfiles), renamed
	// over spec in one step.
	f, err := os.CreateTemp(dir, ".spec-*")
	if err != nil {
		return "", fmt.Errorf("write access spec: %w", err)
	}
	tmp := f.Name()
	_, werr := f.WriteString(spec)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp, filepath.Join(dir, "spec"))
	}
	if werr != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write access spec: %w", werr)
	}
	return c.do(ctx, "access-apply")
}

func (c *CtlFile) AccessStats(ctx context.Context) (map[string]int64, error) {
	out, err := c.do(ctx, "access-stats")
	if err != nil {
		return nil, err
	}
	stats := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		// "<kind> <packets> <bytes>"
		if f := strings.Fields(line); len(f) == 3 {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			stats[f[0]] += n
		}
	}
	return stats, nil
}

func (c *CtlFile) Bans(ctx context.Context) ([]Ban, error) {
	out, err := c.do(ctx, "f2b-status")
	if err != nil {
		return nil, err
	}
	return parseBans(out), nil
}

// parseBans reads "jail<TAB>ip<TAB>since<TAB>until" lines (unix seconds).
func parseBans(out string) []Ban {
	bans := []Ban{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || strings.TrimSpace(f[0]) == "" || strings.TrimSpace(f[1]) == "" {
			continue
		}
		b := Ban{Jail: strings.TrimSpace(f[0]), IP: strings.TrimSpace(f[1])}
		if len(f) >= 4 {
			if v, err := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64); err == nil && v > 0 {
				b.Since = time.Unix(v, 0).UTC()
			}
			if v, err := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64); err == nil && v > 0 {
				b.Until = time.Unix(v, 0).UTC()
			}
		}
		bans = append(bans, b)
	}
	return bans
}

func (c *CtlFile) Ban(ctx context.Context, jail, ip string) error {
	_, err := c.do(ctx, "f2b-ban "+jail+" "+ip)
	return err
}

func (c *CtlFile) Unban(ctx context.Context, jail, ip string) error {
	_, err := c.do(ctx, "f2b-unban "+jail+" "+ip)
	return err
}

var _ Access = (*CtlFile)(nil)
