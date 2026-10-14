// Command sing-box-web is a web admin UI for sing-box.
//
// Usage:
//
//	sing-box-web [serve] [flags]   run the server (default)
//	sing-box-web passwd [flags]    set the administrator password
//	sing-box-web prepare [flags]   add what the panel needs to a sing-box config (installer)
//	sing-box-web version
//
// Every flag can also be set through an SBW_* environment variable, e.g.
// -singbox-config ↔ SBW_SINGBOX_CONFIG.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/term"

	"github.com/axuezi/sing-box-web/internal/access"
	"github.com/axuezi/sing-box-web/internal/apply"
	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/control"
	"github.com/axuezi/sing-box-web/internal/flows"
	"github.com/axuezi/sing-box-web/internal/gateway"
	"github.com/axuezi/sing-box-web/internal/limits"
	"github.com/axuezi/sing-box-web/internal/presence"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/release"
	"github.com/axuezi/sing-box-web/internal/server"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/sysinfo"
	"github.com/axuezi/sing-box-web/internal/traffic"
	"github.com/axuezi/sing-box-web/internal/version"
	"github.com/axuezi/sing-box-web/web"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "passwd":
		err = passwd(args)
	case "prepare":
		err = prepare(args)
	case "version":
		fmt.Printf("sing-box-web %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
	default:
		err = fmt.Errorf("unknown command %q (want serve, passwd, prepare or version)", cmd)
	}
	if errors.Is(err, errAdminExists) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// envDefault returns $SBW_<NAME> or def.
func envDefault(flagName, def string) string {
	key := "SBW_" + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func stringFlag(fs *flag.FlagSet, name, def, usage string) *string {
	return fs.String(name, envDefault(name, def), usage+" (env SBW_"+strings.ToUpper(strings.ReplaceAll(name, "-", "_"))+")")
}

func openStore(ctx context.Context, dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	return store.Open(ctx, filepath.Join(dataDir, "sing-box-web.db"))
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := stringFlag(fs, "listen", "127.0.0.1:9091", "address to listen on")
	dataDir := stringFlag(fs, "data-dir", "./data", "directory for the database")
	sbConfig := stringFlag(fs, "singbox-config", "/etc/sing-box/config.json", "path to sing-box config.json")
	sbLog := stringFlag(fs, "singbox-log", "", "path to sing-box log file (default: log.output from the config)")
	controller := stringFlag(fs, "controller", "none", "how to control sing-box: none | ctlfile | signal | systemd")
	ctlDir := stringFlag(fs, "ctl-dir", "/run/sing-box-web/ctl", "directory shared with sing-box-web-ctl (ctlfile controller)")
	pidFile := stringFlag(fs, "pid-file", "", "sing-box PID file (signal controller)")
	unit := stringFlag(fs, "systemd-unit", "sing-box.service", "sing-box unit (systemd controller)")
	checkCmd := stringFlag(fs, "check-cmd", "sing-box check -c {config}", "command that validates a candidate config; {config} is its path")
	authLog := stringFlag(fs, "auth-log", "", "append failed logins here for fail2ban (empty = off)")
	gatewayLog := stringFlag(fs, "gateway-log", "", "UDP address to receive a TCP relay's connection log on, e.g. 127.0.0.1:5514, to show real client IPs (empty = off)")
	proxies := stringFlag(fs, "trusted-proxies", "127.0.0.1/32,::1/128", "comma-separated CIDRs allowed to set X-Forwarded-*")
	updateCheck := stringFlag(fs, "update-check", "on", "ask GitHub every 6 hours whether a newer sing-box release exists (only shown, never installed): on | off")
	fs.Parse(args)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	trusted, err := parsePrefixes(*proxies)
	if err != nil {
		return err
	}
	logPath := *sbLog
	if logPath == "" {
		if raw, err := os.ReadFile(*sbConfig); err == nil {
			logPath = gjson.GetBytes(raw, "log.output").String()
		}
	}

	var ctl control.Controller
	switch *controller {
	case "none":
		ctl = control.None{}
	case "ctlfile":
		ctl = &control.CtlFile{Dir: *ctlDir}
	case "signal":
		if *pidFile == "" {
			return errors.New("-pid-file is required with -controller signal")
		}
		ctl = &control.Signal{PIDFile: *pidFile}
	case "systemd":
		ctl = &control.Systemd{Unit: *unit}
	default:
		return fmt.Errorf("unknown controller %q", *controller)
	}

	st, err := openStore(ctx, *dataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	if _, err := st.GetAdmin(ctx); errors.Is(err, store.ErrNotFound) {
		logger.Warn("no administrator yet; run: sing-box-web passwd -data-dir " + *dataDir)
	}

	checkArgs, err := apply.ParseCommand(*checkCmd)
	if err != nil {
		return err
	}
	tmpDir := filepath.Join(*dataDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	mgr := &apply.Manager{
		ConfigPath: *sbConfig,
		LogPath:    logPath,
		Store:      st,
		Controller: ctl,
		Checker:    &apply.CommandChecker{Command: checkArgs, TempDir: tmpDir},
		Logger:     logger,
	}
	collector := &traffic.Collector{ConfigPath: *sbConfig, Store: st, Logger: logger}
	mgr.BeforeReload = func(ctx context.Context) { _ = collector.Flush(ctx) }
	mgr.BeforeChange = func(ctx context.Context) error {
		if err := collector.Flush(ctx); err != nil && !errors.Is(err, traffic.ErrNoStatsAPI) {
			return err
		}
		return st.RollCycles(ctx, time.Now(), time.Local)
	}
	if err := mgr.Recover(ctx); err != nil {
		return fmt.Errorf("recover configuration: %w", err)
	}
	// Check quotas right after fresh usage is recorded, not up to a minute later.
	enforcer := &limits.Enforcer{Store: st, Apply: mgr, Logger: logger}
	collector.AfterCollect = enforcer.Kick
	go collector.Run(ctx)
	if err := mgr.SyncExternal(ctx); err != nil {
		logger.Warn("could not snapshot current config", "err", err)
	}
	if err := importUsers(ctx, st, *sbConfig, logger); err != nil {
		logger.Warn("could not import existing users", "err", err)
	}
	go enforcer.Run(ctx)
	accessMgr := &access.Manager{Store: st, ConfigPath: *sbConfig, Logger: logger}
	if a, ok := ctl.(control.Access); ok {
		accessMgr.Ctl = a
	}
	go accessMgr.Run(ctx)
	tracker := &presence.Tracker{LogPath: logPath, Logger: logger}
	go tracker.Run(ctx)
	resolver := &gateway.Resolver{Listen: *gatewayLog, Logger: logger}
	go resolver.Run(ctx)
	recorder := &flows.Recorder{Store: st, Presence: tracker, Gateway: resolver, Logger: logger}
	go recorder.Run(ctx)
	hub := &traffic.Hub{ConfigPath: *sbConfig, OnSnapshot: recorder.Observe}
	go hub.Run(ctx)
	var releases *release.Checker
	switch *updateCheck {
	case "on":
		releases = &release.Checker{Logger: logger}
		go releases.Run(ctx)
	case "off":
	default:
		return fmt.Errorf("-update-check must be on or off, not %q", *updateCheck)
	}

	limiter := auth.NewLimiter(5, 15*time.Minute, 15*time.Minute)
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				limiter.Prune()
				_ = st.PruneSessions(ctx)
				now := time.Now()
				_ = st.PruneTraffic(ctx, now.AddDate(0, 0, -400).Format(time.DateOnly), now.AddDate(0, 0, -62).Format("2006-01-02 15"))
				_ = st.PruneFlows(ctx, now.AddDate(0, 0, -flows.KeepDays).Format("2006-01-02 15"))
			}
		}
	}()

	handler := server.New(server.Options{
		Static:         web.Dist(),
		Logger:         logger,
		Store:          st,
		Controller:     ctl,
		SingboxConfig:  *sbConfig,
		SingboxLog:     logPath,
		AuthLog:        *authLog,
		TrustedProxies: trusted,
		Limiter:        limiter,
		Apply:          mgr,
		Collector:      collector,
		Hub:            hub,
		Presence:       tracker,
		Flows:          recorder,
		Gateway:        resolver,
		System:         &sysinfo.Reader{DiskPath: *dataDir},
		Release:        releases,
		Access:         accessMgr,
	})
	srv := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", *listen, "version", version.Version,
			"singbox_config", *sbConfig, "singbox_log", logPath, "controller", ctl.Name())
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// importUsers adopts the users already in config.json the first time the
// panel runs against it, so existing clients keep working unchanged.
func importUsers(ctx context.Context, st *store.Store, configPath string, logger *slog.Logger) error {
	if done, err := st.GetSetting(ctx, "users_imported"); err != nil || done == "1" {
		return err
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	users := protocol.Import(raw)
	ptrs := make([]*store.User, len(users))
	for i := range users {
		ptrs[i] = &users[i]
	}
	if err := st.SaveUsers(ctx, ptrs, nil); err != nil {
		return err
	}
	for _, u := range users {
		logger.Info("imported user from config", "name", u.Name, "inbounds", u.Inbounds)
	}
	return st.SetSetting(ctx, "users_imported", "1")
}

func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("trusted-proxies: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// errAdminExists makes `passwd -if-unset` exit with status 3, so the
// installer can tell "kept the existing password" from a failure.
var errAdminExists = errors.New("administrator already set; password unchanged")

func passwd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	dataDir := stringFlag(fs, "data-dir", "./data", "directory for the database")
	username := fs.String("username", "admin", "administrator username")
	ifUnset := fs.Bool("if-unset", false, "do nothing (exit status 3) when an administrator already exists")
	fs.Parse(args)

	ctx := context.Background()
	st, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if *ifUnset {
		if _, err := st.GetAdmin(ctx); err == nil {
			return errAdminExists
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	pw, err := readPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.SetAdmin(ctx, *username, hash); err != nil {
		return err
	}
	_ = st.Audit(ctx, store.AuditEntry{Actor: "cli", IP: "local", Action: "password.set", Target: *username})
	fmt.Fprintf(os.Stderr, "administrator %q set; all sessions signed out\n", *username)
	return nil
}

// readPassword prompts twice on a terminal, or reads one line from stdin.
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "New password: ")
	a, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat password: ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errors.New("passwords do not match")
	}
	return string(a), nil
}
