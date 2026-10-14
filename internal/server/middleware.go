package server

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/store"
)

const (
	sessionCookie = "sbw_session"
	csrfHeader    = "X-CSRF-Token"
)

type ctxKey int

const sessionKey ctxKey = iota

func sessionFrom(ctx context.Context) store.Session {
	sess, _ := ctx.Value(sessionKey).(store.Session)
	return sess
}

// authed requires a valid session, and for state-changing methods a CSRF
// token matching that session.
func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.lookupSession(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
				writeError(w, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

func (s *Server) lookupSession(r *http.Request) (store.Session, bool) {
	if s.opts.Store == nil {
		return store.Session{}, false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.Session{}, false
	}
	sess, err := s.opts.Store.GetSession(r.Context(), auth.HashToken(c.Value))
	if err != nil {
		return store.Session{}, false
	}
	return sess, true
}

// sameOrigin rejects cross-site state-changing requests whose Origin header
// names another host. Browsers always send Origin on such requests.
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, s.requestHost(r)) {
					writeError(w, http.StatusForbidden, "cross_origin", "cross-origin request refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) fromTrustedProxy(r *http.Request) bool {
	ip, ok := remoteIP(r)
	return ok && s.trusted(ip)
}

func (s *Server) trusted(ip netip.Addr) bool {
	for _, p := range s.opts.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// clientIP is the address of the browser: the peer, or, behind trusted
// proxies, the right-most untrusted X-Forwarded-For entry.
func (s *Server) clientIP(r *http.Request) string {
	ip, ok := remoteIP(r)
	if !ok {
		return r.RemoteAddr
	}
	if !s.trusted(ip) {
		return ip.String()
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		h, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		h = h.Unmap()
		if !s.trusted(h) {
			return h.String()
		}
		ip = h
	}
	return ip.String()
}

func (s *Server) requestHost(r *http.Request) string {
	if s.fromTrustedProxy(r) {
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			return h
		}
	}
	return r.Host
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.fromTrustedProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
