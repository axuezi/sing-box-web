package server

import (
	"context"
	"net/http"
	"time"

	"github.com/axuezi/sing-box-web/internal/auth"
)

func (s *Server) setting(ctx context.Context, key string) string {
	v, _ := s.opts.Store.GetSetting(ctx, key)
	return v
}

const totpPendingKey = "totp_pending"

func (s *Server) handleTOTPStatus(w http.ResponseWriter, r *http.Request) {
	t, err := s.opts.Store.GetTOTP(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": t.Secret != ""})
}

// refuseIfTOTPOn stops setup and enable while TOTP is on: replacing the
// secret would let a stolen session swap out the second factor. Turning it
// off first needs the password and a current code.
func (s *Server) refuseIfTOTPOn(w http.ResponseWriter, r *http.Request) bool {
	t, err := s.opts.Store.GetTOTP(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return true
	}
	if t.Secret != "" {
		writeError(w, http.StatusConflict, "totp_enabled", "two-factor authentication is already on; turn it off first")
		return true
	}
	return false
}

// handleTOTPSetup creates a new secret to be confirmed with a code.
func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfTOTPOn(w, r) {
		return
	}
	secret := auth.NewTOTPSecret()
	if err := s.opts.Store.SetSetting(r.Context(), totpPendingKey, secret); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"uri":    auth.TOTPURI(secret, s.actor(r), "sing-box-web"),
	})
}

type totpInput struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var in totpInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx := r.Context()
	if s.refuseIfTOTPOn(w, r) {
		return
	}
	pending := s.setting(ctx, totpPendingKey)
	if pending == "" {
		writeError(w, http.StatusBadRequest, "no_pending", "start setup first")
		return
	}
	step, ok := auth.VerifyTOTP(pending, in.Code, time.Now())
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_code", "code does not match")
		return
	}
	if err := s.opts.Store.SetTOTP(ctx, pending); err != nil {
		s.internalError(w, r, err)
		return
	}
	// The confirming code is spent: it must not also sign someone in.
	if _, err := s.opts.Store.UseTOTPStep(ctx, step); err != nil {
		s.internalError(w, r, err)
		return
	}
	_ = s.opts.Store.SetSetting(ctx, totpPendingKey, "")
	s.audit(ctx, s.actor(r), s.clientIP(r), "totp.enable", "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var in totpInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx := r.Context()
	admin, err := s.opts.Store.GetAdmin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.hashSlots <- struct{}{}
	okPass, err := auth.VerifyPassword(in.Password, admin.PasswordHash)
	<-s.hashSlots
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	t, err := s.opts.Store.GetTOTP(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	step, okCode := auth.VerifyTOTP(t.Secret, in.Code, time.Now())
	if okPass && okCode {
		// Same replay rule as login: a code works once.
		if okCode, err = s.opts.Store.UseTOTPStep(ctx, step); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if !okPass || !okCode {
		writeError(w, http.StatusForbidden, "bad_credentials", "password or code is wrong")
		return
	}
	if err := s.opts.Store.SetTOTP(ctx, ""); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(ctx, admin.Username, s.clientIP(r), "totp.disable", "", "")
	w.WriteHeader(http.StatusNoContent)
}

// checkTOTP verifies a login code when TOTP is enabled. It returns
// (required, ok): required=false means TOTP is off.
func (s *Server) checkTOTP(ctx context.Context, code string) (bool, bool, error) {
	t, err := s.opts.Store.GetTOTP(ctx)
	if err != nil || t.Secret == "" {
		return false, true, err
	}
	step, ok := auth.VerifyTOTP(t.Secret, code, time.Now())
	if !ok {
		return true, false, nil
	}
	fresh, err := s.opts.Store.UseTOTPStep(ctx, step)
	return true, fresh, err
}
