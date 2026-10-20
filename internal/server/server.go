// Package server wires the HTTP API and the embedded frontend.
package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/access"
	"github.com/axuezi/sing-box-web/internal/apply"
	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/flows"
	"github.com/axuezi/sing-box-web/internal/gateway"
	"github.com/axuezi/sing-box-web/internal/inbounds"
	"github.com/axuezi/sing-box-web/internal/presence"
	"github.com/axuezi/sing-box-web/internal/release"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/sysinfo"
	"github.com/axuezi/sing-box-web/internal/traffic"
	"github.com/axuezi/sing-box-web/internal/upgrade"
)

// Options configures the server. Zero values get sensible defaults in New.
type Options struct {
	// Static is the built frontend. Requests that match no file fall back to
	// index.html so client-side routes work on reload.
	Static fs.FS
	Logger *slog.Logger
	Store  *store.Store

	Controller control.Controller

	// SingboxConfig is the path to sing-box's config.json.
	SingboxConfig string
	// SingboxLog is the path to sing-box's log file (log.output).
	SingboxLog string

	// AuthLog, if set, receives one line per failed login for fail2ban.
	AuthLog string
	// TrustedProxies may set X-Forwarded-For / X-Forwarded-Proto.
	TrustedProxies []netip.Prefix
	SessionTTL     time.Duration
	Limiter        *auth.Limiter
	// SubLimiter throttles clients guessing subscription tokens.
	SubLimiter *auth.Limiter

	// Apply performs every config change.
	Apply *apply.Manager
	// Collector records per-user traffic; Hub streams live throughput.
	Collector *traffic.Collector
	Hub       *traffic.Hub
	// Presence attributes live connections to users; nil = only by
	// elimination (inbounds with a single user).
	Presence *presence.Tracker
	// Flows records traffic per client IP and site; nil = not available.
	Flows *flows.Recorder
	// Gateway maps a local relay's addresses back to real clients; may be nil.
	Gateway *gateway.Resolver
	// System reads the host's CPU, memory and disk; nil = not shown.
	System *sysinfo.Reader
	// Release knows the newest sing-box release; nil = update check off.
	Release *release.Checker
	// PanelRelease knows the newest sing-box-web release; nil = update
	// check off.
	PanelRelease *release.Checker
	// Upgrade submits one-click upgrades; nil or without Dir = off (new
	// versions are then only shown).
	Upgrade *upgrade.Manager
	// KernelManaged: install.sh installed sing-box, so one-click upgrades
	// may replace it with the build attached to sing-box-web releases.
	KernelManaged bool
	// Sockets lists ports bound on the host, to refuse an inbound port that
	// another program holds; nil = read /proc/net.
	Sockets func() ([]inbounds.Socket, bool)
	// Access manages IP rules for the proxy inbounds; nil = unavailable.
	Access *access.Manager
}

// Server holds the handler and its dependencies.
type Server struct {
	opts      Options
	log       *slog.Logger
	startedAt time.Time
	hashSlots chan struct{} // bounds concurrent argon2 work (64 MiB each)

	authLogMu sync.Mutex
	upgradeMu sync.Mutex
}

// New returns the root handler.
func New(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Controller == nil {
		opts.Controller = control.None{}
	}
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 12 * time.Hour
	}
	if opts.Limiter == nil {
		opts.Limiter = auth.NewLimiter(5, 15*time.Minute, 15*time.Minute)
	}
	if opts.SubLimiter == nil {
		opts.SubLimiter = auth.NewLimiter(10, 15*time.Minute, 15*time.Minute)
	}
	if opts.Sockets == nil {
		opts.Sockets = inbounds.Bound
	}
	if opts.Hub == nil {
		opts.Hub = &traffic.Hub{ConfigPath: opts.SingboxConfig}
	}
	s := &Server{opts: opts, log: opts.Logger, startedAt: time.Now(), hashSlots: make(chan struct{}, 2)}
	return s.routes()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/me", s.handleMe)

	mux.Handle("POST /api/logout", s.authed(s.handleLogout))
	mux.Handle("POST /api/password", s.authed(s.handlePassword))
	mux.Handle("PUT /api/account", s.authed(s.handleAccount))
	mux.Handle("GET /api/status", s.authed(s.handleStatus))
	mux.Handle("GET /api/system", s.authed(s.handleSystem))
	mux.Handle("GET /api/logs/stream", s.authed(s.handleLogStream))
	mux.Handle("GET /api/logs/download", s.authed(s.handleLogDownload))
	mux.Handle("GET /api/audit", s.authed(s.handleAudit))

	mux.Handle("GET /api/users", s.authed(s.handleListUsers))
	mux.Handle("POST /api/users", s.authed(s.handleCreateUser))
	mux.Handle("PATCH /api/users/{id}", s.authed(s.handleUpdateUser))
	mux.Handle("DELETE /api/users/{id}", s.authed(s.handleDeleteUser))
	mux.Handle("POST /api/users/{id}/reset", s.authed(s.handleResetUser))
	mux.Handle("POST /api/users/{id}/renew", s.authed(s.handleRenewUser))
	mux.Handle("POST /api/users/{id}/reset-usage", s.authed(s.handleResetUsage))
	mux.Handle("GET /api/users/{id}/client", s.authed(s.handleUserClient))

	mux.Handle("GET /api/access", s.authed(s.handleGetAccess))
	mux.Handle("POST /api/access/rules", s.authed(s.handleAddAccessRule))
	mux.Handle("DELETE /api/access/rules/{id}", s.authed(s.handleDeleteAccessRule))
	mux.Handle("PUT /api/access/allowlist", s.authed(s.handleSetAllowlist))
	mux.Handle("POST /api/access/allowlist/confirm", s.authed(s.handleConfirmAllowlist))
	mux.Handle("GET /api/access/bans", s.authed(s.handleListBans))
	mux.Handle("POST /api/access/bans", s.authed(s.handleBanAction))
	mux.Handle("GET /api/inbounds", s.authed(s.handleListInbounds))
	mux.Handle("POST /api/inbounds", s.authed(s.handleCreateInbound))
	mux.Handle("POST /api/inbounds/generate", s.authed(s.handleGenerateInbound))
	mux.Handle("PUT /api/inbounds/{tag}", s.authed(s.handleUpdateInbound))
	mux.Handle("DELETE /api/inbounds/{tag}", s.authed(s.handleDeleteInbound))
	mux.Handle("GET /api/outbounds", s.authed(s.handleListOutbounds))
	mux.Handle("POST /api/outbounds", s.authed(s.handleCreateOutbound))
	mux.Handle("PUT /api/outbounds/{tag}", s.authed(s.handleUpdateOutbound))
	mux.Handle("DELETE /api/outbounds/{tag}", s.authed(s.handleDeleteOutbound))
	mux.Handle("GET /api/dns", s.authed(s.handleGetDNS))
	mux.Handle("PUT /api/dns", s.authed(s.handleUpdateDNS))
	mux.Handle("POST /api/dns/servers", s.authed(s.handleCreateDNSServer))
	mux.Handle("PUT /api/dns/servers/{tag}", s.authed(s.handleUpdateDNSServer))
	mux.Handle("DELETE /api/dns/servers/{tag}", s.authed(s.handleDeleteDNSServer))
	mux.Handle("POST /api/service/reload", s.authed(s.handleService(false)))
	mux.Handle("POST /api/service/restart", s.authed(s.handleService(true)))
	mux.Handle("GET /api/upgrade", s.authed(s.handleGetUpgrade))
	mux.Handle("POST /api/upgrade", s.authed(s.handleStartUpgrade))
	mux.Handle("GET /api/settings/subscription", s.authed(s.handleGetSubSettings))
	mux.Handle("PUT /api/settings/subscription", s.authed(s.handlePutSubSettings))

	mux.Handle("GET /api/realtime", s.authed(s.handleRealtime))
	mux.Handle("GET /api/connections", s.authed(s.handleConnections))
	mux.Handle("DELETE /api/connections", s.authed(s.handleCloseConnection))
	mux.Handle("DELETE /api/connections/{id}", s.authed(s.handleCloseConnection))
	mux.Handle("GET /api/stats/traffic", s.authed(s.handleTrafficStats))
	mux.Handle("GET /api/stats/analysis", s.authed(s.handleTrafficAnalysis))
	mux.Handle("GET /api/stats/flows", s.authed(s.handleFlows))
	mux.Handle("DELETE /api/stats/flows", s.authed(s.handleClearFlows))
	mux.Handle("PUT /api/settings/flows", s.authed(s.handlePutFlowsSetting))
	mux.Handle("GET /api/users/{id}/access", s.authed(s.handleSubAccess))
	mux.Handle("GET /api/totp", s.authed(s.handleTOTPStatus))
	mux.Handle("POST /api/totp/setup", s.authed(s.handleTOTPSetup))
	mux.Handle("POST /api/totp/enable", s.authed(s.handleTOTPEnable))
	mux.Handle("POST /api/totp/disable", s.authed(s.handleTOTPDisable))

	mux.HandleFunc("GET /sub/{token}", s.handleSubscription)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	if s.opts.Static != nil {
		mux.Handle("/", spaHandler(s.opts.Static))
	}
	return securityHeaders(s.sameOrigin(mux))
}

// spaHandler serves files from static and falls back to index.html for
// unknown paths. Hashed assets under assets/ are cached for a year.
func spaHandler(static fs.FS) http.Handler {
	files := http.FileServerFS(static)
	gz := &gzipCache{fs: static, m: map[string][]byte{}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if st, err := fs.Stat(static, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				if gz.serve(w, r, name) {
					return
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if gz.serve(w, r, "index.html") {
			return
		}
		index, err := fs.ReadFile(static, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "frontend not built: run `make web`", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(index)
	})
}

// gzipCache serves gzip-compressed copies of the embedded frontend. The
// files never change while the binary runs, so each is compressed once.
type gzipCache struct {
	fs fs.FS
	mu sync.Mutex
	m  map[string][]byte // nil value = not worth compressing
}

var compressible = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true, ".txt": true}

// serve writes the compressed file and reports whether it did.
func (g *gzipCache) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	if !compressible[path.Ext(name)] {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return false
	}
	g.mu.Lock()
	body, done := g.m[name]
	if !done {
		body = g.compress(name)
		g.m[name] = body
	}
	g.mu.Unlock()
	if body == nil {
		return false
	}
	h := w.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	}
	h.Set("Content-Encoding", "gzip")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return true
}

func (g *gzipCache) compress(name string) []byte {
	raw, err := fs.ReadFile(g.fs, name)
	if err != nil || len(raw) < 1024 {
		return nil
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	if buf.Len() >= len(raw) {
		return nil
	}
	return buf.Bytes()
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip
// ("gzip", "gzip;q=0.8" or "*", but not "gzip;q=0").
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		enc, params, _ := strings.Cut(part, ";")
		enc = strings.TrimSpace(enc)
		if !strings.EqualFold(enc, "gzip") && enc != "*" {
			continue
		}
		if _, q, ok := strings.Cut(strings.ReplaceAll(params, " ", ""), "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v == 0 {
				return false
			}
		}
		return true
	}
	return false
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; worker-src 'self' blob:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// apiError is the JSON error body. Code is stable for the UI; Message is
// for humans.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Code: code, Message: msg})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

// decodeJSON reads a small JSON body into v, rejecting unknown fields.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func (s *Server) audit(ctx context.Context, actor, ip, action, target, detail string) {
	if s.opts.Store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx) // the request may already be gone
	if err := s.opts.Store.Audit(ctx, store.AuditEntry{Actor: actor, IP: ip, Action: action, Target: target, Detail: detail}); err != nil {
		s.log.Error("audit write failed", "action", action, "err", err)
	}
}

// authLog appends a fail2ban-friendly line.
func (s *Server) authLog(format string, args ...any) {
	if s.opts.AuthLog == "" {
		return
	}
	s.authLogMu.Lock()
	defer s.authLogMu.Unlock()
	f, err := os.OpenFile(s.opts.AuthLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		s.log.Error("auth log", "err", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s sing-box-web[%d]: %s\n", time.Now().UTC().Format(time.RFC3339), os.Getpid(), fmt.Sprintf(format, args...))
}
