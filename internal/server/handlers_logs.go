package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/logtail"
)

// handleLogStream streams sing-box's log as Server-Sent Events: a backlog of
// recent lines, then new lines as they are written. Query: level, q, backlog.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := logtail.Filter{MinLevel: q.Get("level"), Query: q.Get("q")}
	backlog, _ := strconv.Atoi(q.Get("backlog"))
	if backlog <= 0 || backlog > 2000 {
		backlog = 300
	}

	if _, err := os.Stat(s.opts.SingboxLog); err != nil {
		writeError(w, http.StatusBadGateway, "log_unreadable", err.Error())
		return
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, v any) error {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		return rc.Flush()
	}

	// The backlog is read with a larger window than requested so that the
	// filter still has enough lines to choose from.
	followFrom := int64(-1)
	if lines, end, err := logtail.Backlog(s.opts.SingboxLog, backlog*20); err == nil {
		followFrom = end
		var picked []logtail.Line
		for _, raw := range lines {
			if l := logtail.Parse(raw); filter.Match(l) {
				picked = append(picked, l)
			}
		}
		if len(picked) > backlog {
			picked = picked[len(picked)-backlog:]
		}
		for _, l := range picked {
			if send("line", l) != nil {
				return
			}
		}
	}
	if send("ready", struct{}{}) != nil {
		return
	}

	ctx := r.Context()
	lines := make(chan logtail.Line, 512)
	dropped := 0
	var mu sync.Mutex
	go func() {
		err := logtail.Follow(ctx, s.opts.SingboxLog, followFrom, 500*time.Millisecond, func(raw string) {
			l := logtail.Parse(raw)
			if !filter.Match(l) {
				return
			}
			select {
			case lines <- l:
			default:
				mu.Lock()
				dropped++
				mu.Unlock()
			}
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("log follow", "err", err)
		}
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case l := <-lines:
			if send("line", l) != nil {
				return
			}
		case <-heartbeat.C:
			mu.Lock()
			n := dropped
			dropped = 0
			mu.Unlock()
			if n > 0 {
				if send("dropped", map[string]int{"count": n}) != nil {
					return
				}
			} else if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func (s *Server) handleLogDownload(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(s.opts.SingboxLog)
	if err != nil {
		writeError(w, http.StatusBadGateway, "log_unreadable", err.Error())
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(s.opts.SingboxLog)))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.opts.Store.ListAudit(r.Context(), before, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
