package server

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/release"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/version"
)

type statusResponse struct {
	Panel struct {
		Version   string    `json:"version"`
		Commit    string    `json:"commit"`
		StartedAt time.Time `json:"started_at"`
		RSSBytes  int64     `json:"rss_bytes"`
	} `json:"panel"`
	Service struct {
		Controller string         `json:"controller"`
		Status     control.Status `json:"status"`
		Error      string         `json:"error,omitempty"`
	} `json:"service"`
	Runtime struct {
		Available bool   `json:"available"`
		Version   string `json:"version,omitempty"`
		Error     string `json:"error,omitempty"`
		// Latest is the newest stable sing-box release last seen; absent
		// before the first check or with the check off. Update repeats it
		// only when it is newer than the running version.
		Latest *release.Latest `json:"latest,omitempty"`
		Update *release.Latest `json:"update,omitempty"`
	} `json:"runtime"`
	Config struct {
		Path    string           `json:"path"`
		SHA256  string           `json:"sha256,omitempty"`
		ModTime time.Time        `json:"mod_time,omitzero"`
		Size    int              `json:"size"`
		Error   string           `json:"error,omitempty"`
		Summary *singbox.Summary `json:"summary,omitempty"`
	} `json:"config"`
	Log struct {
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		Error string `json:"error,omitempty"`
	} `json:"log"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var resp statusResponse

	resp.Panel.Version = version.Version
	resp.Panel.Commit = version.Commit
	resp.Panel.StartedAt = s.startedAt
	resp.Panel.RSSBytes = selfRSS()

	resp.Config.Path = s.opts.SingboxConfig
	file, cfgErr := singbox.ReadFile(s.opts.SingboxConfig)
	if cfgErr != nil {
		resp.Config.Error = cfgErr.Error()
	} else {
		sum := singbox.Summarize(file.Raw)
		resp.Config.SHA256 = file.SHA256
		resp.Config.ModTime = file.ModTime
		resp.Config.Size = len(file.Raw)
		resp.Config.Summary = &sum
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		resp.Service.Controller = s.opts.Controller.Name()
		st, err := s.opts.Controller.Status(ctx)
		resp.Service.Status = st
		if err != nil {
			resp.Service.Error = err.Error()
		}
	})
	if cfgErr == nil && resp.Config.Summary.ClashAPI != "" {
		wg.Go(func() {
			c := &clashapi.Client{BaseURL: "http://" + resp.Config.Summary.ClashAPI, Secret: singbox.ClashAPISecret(file.Raw)}
			v, err := c.Version(ctx)
			if err != nil {
				resp.Runtime.Error = err.Error()
				return
			}
			resp.Runtime.Available = true
			resp.Runtime.Version = v.Version
		})
	} else {
		resp.Runtime.Error = "clash_api not enabled in config"
	}

	resp.Log.Path = s.opts.SingboxLog
	if st, err := os.Stat(s.opts.SingboxLog); err != nil {
		resp.Log.Error = err.Error()
	} else {
		resp.Log.Size = st.Size()
	}

	wg.Wait()
	if s.opts.Release != nil && resp.Runtime.Available {
		if l := s.opts.Release.Latest(); l != nil {
			resp.Runtime.Latest = l
			if release.Newer(l.Version, resp.Runtime.Version) {
				resp.Runtime.Update = l
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSystem reports the host's CPU, load, memory, swap and disk.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	if s.opts.System == nil {
		writeError(w, http.StatusNotFound, "not_available", "system information is not available")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.System.Read())
}

// selfRSS reads this process's resident set size from /proc (Linux only).
func selfRSS() int64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmRSS:"); ok {
			kb, _ := strconv.ParseInt(strings.Fields(v)[0], 10, 64)
			return kb * 1024
		}
	}
	return 0
}
