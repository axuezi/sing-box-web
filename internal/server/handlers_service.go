package server

import "net/http"

func (s *Server) handleService(restart bool) http.HandlerFunc {
	action := "service.reload"
	if restart {
		action = "service.restart"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := s.opts.Apply.Reload(r.Context(), restart)
		s.audit(r.Context(), s.actor(r), s.clientIP(r), action, "sing-box", errString(err, out))
		if err != nil {
			writeError(w, http.StatusBadGateway, "service_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": out})
	}
}

func errString(err error, ok string) string {
	if err != nil {
		return truncate(err.Error(), 500)
	}
	return ok
}
