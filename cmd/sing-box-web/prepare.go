package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
)

// prepare is run by the installer before the panel starts. It reads the
// sing-box config (missing = start from a minimal one), adds what the panel
// reads, and writes the result to -out. It never touches the live config:
// the installer checks the candidate with `sing-box check`, keeps a backup
// and swaps it in, so the "only internal/apply writes config.json" rule holds
// for the running panel.
func prepare(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	sbConfig := stringFlag(fs, "singbox-config", "/etc/sing-box/config.json", "path to sing-box config.json")
	out := fs.String("out", "", "write the prepared config here (required)")
	logFile := fs.String("log-file", "/var/log/sing-box/sing-box.log", "log.output to add when the config writes no log file")
	v2ray := fs.Bool("v2ray-api", false, "add experimental.v2ray_api (only for sing-box built with with_v2ray_api)")
	listen := stringFlag(fs, "listen", "127.0.0.1:9091", "the panel's listen address; its port is not handed to sing-box")
	fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}

	raw, err := os.ReadFile(*sbConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	opt := singbox.PrepareOptions{LogPath: *logFile, V2RayAPI: *v2ray}
	if _, p, err := net.SplitHostPort(*listen); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			opt.Reserved = []int{n}
		}
	}
	prepared, changes, err := singbox.Prepare(raw, opt)
	if err != nil {
		return fmt.Errorf("%s: %w", *sbConfig, err)
	}
	if len(raw) > 0 {
		prepared = protocol.FormatLike(raw, prepared)
	} else {
		prepared = protocol.FormatLike(prepared, prepared)
	}
	if err := os.WriteFile(*out, prepared, 0o600); err != nil {
		return err
	}
	for _, c := range changes {
		fmt.Println(c)
	}
	return nil
}
