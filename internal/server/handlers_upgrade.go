package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/release"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/upgrade"
	"github.com/axuezi/sing-box-web/internal/version"
)

// upgradeAuditedKey remembers the last finished upgrade written to the
// audit log, so each outcome is logged once.
const upgradeAuditedKey = "upgrade_audited"

type upgradeView struct {
	// Enabled: one-click upgrades are possible (the upgrade unit is set
	// up). Otherwise new versions are only shown, e.g. for Docker.
	Enabled bool `json:"enabled"`
	// Checking: the panel asks GitHub for new releases (-update-check).
	Checking bool `json:"checking"`
	Panel    struct {
		Current string          `json:"current"`
		Latest  *release.Latest `json:"latest,omitempty"`
		Newer   bool            `json:"newer"`
	} `json:"panel"`
	SingBox struct {
		// Managed: install.sh installed sing-box, so the panel may replace it.
		Managed bool   `json:"managed"`
		Current string `json:"current,omitempty"`
		Bundled string `json:"bundled,omitempty"` // in the latest sing-box-web release
		Newer   bool   `json:"newer"`
	} `json:"sing_box"`
	State *upgrade.State `json:"state,omitempty"`
}

func (s *Server) upgradeView(ctx context.Context) (upgradeView, error) {
	var v upgradeView
	v.Enabled = s.opts.Upgrade.Enabled()
	v.Checking = s.opts.PanelRelease != nil
	v.Panel.Current = version.Version
	v.SingBox.Managed = v.Enabled && s.opts.KernelManaged
	v.SingBox.Current = s.runningSingBox(ctx)
	if s.opts.PanelRelease != nil {
		if l := s.opts.PanelRelease.Latest(); l != nil {
			v.Panel.Latest = l
			v.Panel.Newer = release.Newer(l.Version, v.Panel.Current)
			v.SingBox.Bundled = l.SingBox
			v.SingBox.Newer = l.SingBox != "" && release.Newer(l.SingBox, v.SingBox.Current)
		}
	}
	st, err := s.opts.Upgrade.State()
	if err != nil {
		return v, err
	}
	v.State = st
	return v, nil
}

// runningSingBox asks the Clash API for the running version, e.g. "1.14.3"
// ("" if unknown).
func (s *Server) runningSingBox(ctx context.Context) string {
	file, err := singbox.ReadFile(s.opts.SingboxConfig)
	if err != nil {
		return ""
	}
	addr := singbox.Summarize(file.Raw).ClashAPI
	if addr == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ver, err := (&clashapi.Client{BaseURL: "http://" + addr, Secret: singbox.ClashAPISecret(file.Raw)}).Version(ctx)
	if err != nil {
		return ""
	}
	// The Clash API says "sing-box 1.14.3"; keep the number only.
	f := strings.Fields(ver.Version)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

func (s *Server) handleGetUpgrade(w http.ResponseWriter, r *http.Request) {
	v, err := s.upgradeView(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.auditUpgradeOutcome(r.Context(), v.State)
	writeJSON(w, http.StatusOK, v)
}

// auditUpgradeOutcome logs a finished upgrade once. The upgrade unit cannot
// write the audit log, and an upgraded panel only learns the outcome here.
func (s *Server) auditUpgradeOutcome(ctx context.Context, st *upgrade.State) {
	if st == nil || st.Finished.IsZero() || s.opts.Store == nil {
		return
	}
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	mark := strconv.FormatInt(st.Finished.Unix(), 10)
	if done, err := s.opts.Store.GetSetting(ctx, upgradeAuditedKey); err != nil || done == mark {
		return
	}
	action := "upgrade.done"
	if st.Status != upgrade.Done {
		action = "upgrade." + st.Status
	}
	s.audit(ctx, "system", "local", action, string(st.Target)+" "+st.To, st.Message)
	_ = s.opts.Store.SetSetting(context.WithoutCancel(ctx), upgradeAuditedKey, mark)
}

func (s *Server) handleStartUpgrade(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target upgrade.Target `json:"target"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !s.opts.Upgrade.Enabled() {
		writeError(w, http.StatusConflict, "upgrade_off", "one-click upgrades are not set up on this host")
		return
	}
	v, err := s.upgradeView(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if v.Panel.Latest == nil {
		writeError(w, http.StatusConflict, "no_release", "the latest release is not known yet")
		return
	}
	tag := "v" + v.Panel.Latest.Version
	var from, to string
	switch body.Target {
	case upgrade.Panel:
		if !v.Panel.Newer {
			writeError(w, http.StatusConflict, "up_to_date", "the panel is already up to date")
			return
		}
		from, to = v.Panel.Current, tag
	case upgrade.SingBox:
		if !v.SingBox.Managed {
			writeError(w, http.StatusConflict, "not_managed", "sing-box was not installed by install.sh; upgrade it yourself")
			return
		}
		if !v.SingBox.Newer {
			writeError(w, http.StatusConflict, "up_to_date", "sing-box is already up to date")
			return
		}
		from, to = v.SingBox.Current, v.SingBox.Bundled
	default:
		writeError(w, http.StatusBadRequest, "bad_target", "target must be panel or sing-box")
		return
	}
	if err := s.opts.Upgrade.Request(body.Target, tag, from, to); errors.Is(err, upgrade.ErrBusy) {
		writeError(w, http.StatusConflict, "busy", err.Error())
		return
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "upgrade.request", string(body.Target)+" "+to, "from "+from)
	st, _ := s.opts.Upgrade.State()
	writeJSON(w, http.StatusAccepted, map[string]any{"state": st})
}
