package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/traffic"
)

// handleRealtime streams one throughput sample per second over SSE.
func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	if _, err := traffic.ClashClient(s.opts.SingboxConfig); err != nil {
		writeError(w, http.StatusConflict, "no_clash_api", err.Error())
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	recent, ch, cancel := s.opts.Hub.Subscribe()
	defer cancel()
	send := func(event string, v any) bool {
		b, _ := json.Marshal(v)
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		return err == nil && rc.Flush() == nil
	}
	if !send("history", recent) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case smp := <-ch:
			if !send("sample", map[string]any{"sample": smp, "error": s.opts.Hub.Err()}) {
				return
			}
		}
	}
}

type connView struct {
	ID       string    `json:"id"`
	Inbound  string    `json:"inbound"`
	Network  string    `json:"network"`
	Source   string    `json:"source"`
	Host     string    `json:"host"`
	Dest     string    `json:"dest"`
	Outbound string    `json:"outbound"`
	Rule     string    `json:"rule"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	// User is who the connection belongs to; "" when it cannot be told.
	User string `json:"user"`
	// ClientIP is the real client behind a local relay (Source is then the
	// relay's address), when the relay reported it; "" otherwise.
	ClientIP string `json:"client_ip,omitempty"`
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	c, err := traffic.ClashClient(s.opts.SingboxConfig)
	if err != nil {
		writeError(w, http.StatusConflict, "no_clash_api", err.Error())
		return
	}
	snap, err := c.Connections(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "clash_api", err.Error())
		return
	}
	users, err := s.opts.Store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	live := s.livePresenceFrom(snap, users)
	out := make([]connView, 0, len(snap.Connections))
	for i, cn := range snap.Connections {
		m := cn.Metadata
		host := m.Host
		if host == "" {
			host = m.DestinationIP
		}
		v := connView{
			ID: cn.ID, Inbound: m.Type, Network: m.Network,
			Source: joinHostPort(m.SourceIP, m.SourcePort), Host: host,
			Dest: joinHostPort(m.DestinationIP, m.DestinationPort),
			Rule: cn.Rule, Upload: cn.Upload, Download: cn.Download, Start: cn.Start,
			User: live.attr.Users[i],
		}
		if ip, ok := s.opts.Gateway.Client(m.SourceIP, m.SourcePort); ok {
			v.ClientIP = ip
		}
		if len(cn.Chains) > 0 {
			v.Outbound = cn.Chains[0]
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	writeJSON(w, http.StatusOK, map[string]any{
		"connections": out, "upload_total": snap.UploadTotal, "download_total": snap.DownloadTotal, "memory": snap.Memory,
	})
}

func joinHostPort(host, port string) string {
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" || port == "0" {
		return host
	}
	return host + ":" + port
}

func (s *Server) handleCloseConnection(w http.ResponseWriter, r *http.Request) {
	c, err := traffic.ClashClient(s.opts.SingboxConfig)
	if err != nil {
		writeError(w, http.StatusConflict, "no_clash_api", err.Error())
		return
	}
	id := r.PathValue("id") // "" for DELETE /api/connections = close all
	if err := c.CloseConnection(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, "clash_api", err.Error())
		return
	}
	target := id
	if target == "" {
		target = "all"
	}
	s.audit(r.Context(), s.actor(r), s.clientIP(r), "connection.close", target, "")
	w.WriteHeader(http.StatusNoContent)
}
