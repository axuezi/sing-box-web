package server

import (
	"crypto/subtle"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/version"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version.Version,
		"commit":  version.Commit,
	})
}

type meResponse struct {
	Username  string    `json:"username"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "server has no database")
		return
	}
	admin, err := s.opts.Store.GetAdmin(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "setup_required", "no administrator yet: run `sing-box-web passwd`")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sess, ok := s.lookupSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{Username: admin.Username, CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"` // TOTP, when enabled
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := s.clientIP(r)

	if locked, until := s.opts.Limiter.Locked(ip); locked {
		retry := int(math.Ceil(time.Until(until).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeError(w, http.StatusTooManyRequests, "locked", "too many failed attempts; try again later")
		return
	}

	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if s.opts.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "server has no database")
		return
	}
	admin, err := s.opts.Store.GetAdmin(ctx)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusServiceUnavailable, "setup_required", "no administrator yet: run `sing-box-web passwd`")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	select {
	case s.hashSlots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	okPass, verr := auth.VerifyPassword(req.Password, admin.PasswordHash)
	<-s.hashSlots
	if verr != nil {
		s.internalError(w, r, verr)
		return
	}
	okUser := subtle.ConstantTimeCompare([]byte(req.Username), []byte(admin.Username)) == 1

	// Ask for the second factor only after the password checked out; a
	// wrong code counts as a failed attempt.
	if okUser && okPass {
		required, okCode, err := s.checkTOTP(ctx, req.Code)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if required && strings.TrimSpace(req.Code) == "" {
			writeError(w, http.StatusUnauthorized, "totp_required", "enter the code from your authenticator app")
			return
		}
		okPass = okCode
	}

	if !okUser || !okPass {
		lockedNow, until := s.opts.Limiter.Fail(ip)
		s.authLog("authentication failure from %s", ip)
		s.audit(ctx, truncate(req.Username, 64), ip, "login.failure", "", "")
		if lockedNow {
			s.authLog("login locked for %s until %s", ip, until.UTC().Format(time.RFC3339))
			s.audit(ctx, "system", ip, "login.locked", ip, "until "+until.UTC().Format(time.RFC3339))
		}
		writeError(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
		return
	}

	s.opts.Limiter.Succeed(ip)
	token := auth.RandomToken(32)
	now := time.Now()
	sess := store.Session{
		TokenHash: auth.HashToken(token),
		CSRFToken: auth.RandomToken(24),
		CreatedAt: now,
		ExpiresAt: now.Add(s.opts.SessionTTL),
		IP:        ip,
		UserAgent: truncate(r.UserAgent(), 256),
	}
	if err := s.opts.Store.CreateSession(ctx, sess); err != nil {
		s.internalError(w, r, err)
		return
	}
	_ = s.opts.Store.PruneSessions(ctx)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	s.audit(ctx, admin.Username, ip, "login.success", "", "")
	writeJSON(w, http.StatusOK, meResponse{Username: admin.Username, CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.opts.Store.DeleteSession(r.Context(), sess.TokenHash); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.clearCookie(w, r)
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "logout", "", "")
	w.WriteHeader(http.StatusNoContent)
}

type passwordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req passwordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	admin, err := s.opts.Store.GetAdmin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.hashSlots <- struct{}{}
	ok, err := auth.VerifyPassword(req.Current, admin.PasswordHash)
	<-s.hashSlots
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "bad_credentials", "current password is wrong")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	// SetAdmin also signs out every session, including this one.
	if err := s.opts.Store.SetAdmin(ctx, admin.Username, hash); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.clearCookie(w, r)
	s.audit(ctx, admin.Username, s.clientIP(r), "password.change", "", "")
	w.WriteHeader(http.StatusNoContent)
}

var validAdminName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{2,31}$`)

type accountRequest struct {
	Username string `json:"username"`
	Current  string `json:"current"` // current password, to confirm
}

// handleAccount changes the administrator's login name. It needs the
// current password: a session alone (e.g. an unlocked browser) is not enough.
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req accountRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	name := strings.TrimSpace(req.Username)
	if !validAdminName.MatchString(name) {
		writeError(w, http.StatusBadRequest, "bad_username", "username must be 3-32 letters, digits or . _ @ -, starting with a letter or digit")
		return
	}
	admin, err := s.opts.Store.GetAdmin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.hashSlots <- struct{}{}
	ok, err := auth.VerifyPassword(req.Current, admin.PasswordHash)
	<-s.hashSlots
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "bad_credentials", "current password is wrong")
		return
	}
	if name != admin.Username {
		if err := s.opts.Store.RenameAdmin(ctx, name); err != nil {
			s.internalError(w, r, err)
			return
		}
		s.audit(ctx, name, s.clientIP(r), "account.rename", admin.Username, name)
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": name})
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
}

// actor names the signed-in administrator for audit records.
func (s *Server) actor(r *http.Request) string {
	if a, err := s.opts.Store.GetAdmin(r.Context()); err == nil {
		return a.Username
	}
	return "admin"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
