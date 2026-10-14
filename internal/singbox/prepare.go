package singbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// PrepareOptions says what Prepare may add to a config.
type PrepareOptions struct {
	// LogPath becomes log.output when the config writes no log file.
	LogPath string
	// V2RayAPI adds experimental.v2ray_api. Only builds with the
	// with_v2ray_api tag accept it; others refuse to start.
	V2RayAPI bool
	// PortFree reports whether a loopback TCP port can be used. nil means
	// probing with a listener.
	PortFree func(port int) bool
	// Reserved ports are never picked, e.g. the panel's own port when it is
	// not running yet.
	Reserved []int
}

// minimalConfig is the starting point when no config exists yet: no
// inbounds (the panel adds them), everything leaves directly.
const minimalConfig = `{
  "log": {"level": "info", "timestamp": true},
  "inbounds": [],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`

// Prepare returns raw (or a minimal config when raw is empty) with the pieces
// the panel reads added where missing: a log file, the Clash API on loopback
// with a secret, and optionally the V2Ray stats API. Fields that already
// exist are left alone. changes lists what was added, for the installer to
// show.
func Prepare(raw []byte, opt PrepareOptions) (out []byte, changes []string, err error) {
	if len(raw) == 0 {
		raw = []byte(minimalConfig)
		changes = append(changes, "created a minimal config (no inbounds, direct outbound)")
	}
	if !gjson.ValidBytes(raw) {
		return nil, nil, fmt.Errorf("config is not valid JSON")
	}
	free := opt.PortFree
	if free == nil {
		free = loopbackPortFree
	}
	out = raw
	set := func(path string, v any) {
		if err == nil {
			out, err = sjson.SetBytes(out, path, v)
		}
	}

	if opt.LogPath != "" && !gjson.GetBytes(out, "log.output").Exists() && !gjson.GetBytes(out, "log.disabled").Bool() {
		set("log.output", opt.LogPath)
		changes = append(changes, "log.output = "+opt.LogPath)
		if lvl := gjson.GetBytes(out, "log.level").String(); lvl != "" && lvl != "trace" && lvl != "debug" && lvl != "info" {
			set("log.level", "info")
			changes = append(changes, "log.level = info (online status needs connection logs)")
		}
	}

	used := map[int]bool{}
	for _, p := range opt.Reserved {
		used[p] = true
	}
	pick := func(preferred int) (int, error) {
		for p := preferred; p < preferred+100; p++ {
			if !used[p] && free(p) {
				used[p] = true
				return p, nil
			}
		}
		return 0, fmt.Errorf("no free loopback port near %d", preferred)
	}

	if !gjson.GetBytes(out, "experimental.clash_api").Exists() {
		port, perr := pick(9090)
		if perr != nil {
			return nil, nil, perr
		}
		secret, serr := randomHex(16)
		if serr != nil {
			return nil, nil, serr
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		set("experimental.clash_api", map[string]any{"external_controller": addr, "secret": secret})
		changes = append(changes, "experimental.clash_api on "+addr+" (with a random secret)")
	}

	if opt.V2RayAPI && !gjson.GetBytes(out, "experimental.v2ray_api").Exists() {
		port, perr := pick(10085)
		if perr != nil {
			return nil, nil, perr
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		// The panel keeps stats.inbounds and stats.users in step with the
		// inbounds and users it manages; start with the existing tags.
		tags, names := []string{}, []string{}
		seen := map[string]bool{}
		gjson.GetBytes(out, "inbounds").ForEach(func(_, in gjson.Result) bool {
			if t := in.Get("tag").String(); t != "" {
				tags = append(tags, t)
			}
			in.Get("users.#.name").ForEach(func(_, n gjson.Result) bool {
				if n.String() != "" && !seen[n.String()] {
					seen[n.String()] = true
					names = append(names, n.String())
				}
				return true
			})
			return true
		})
		set("experimental.v2ray_api", map[string]any{
			"listen": addr,
			"stats":  map[string]any{"enabled": true, "inbounds": tags, "users": names},
		})
		changes = append(changes, "experimental.v2ray_api on "+addr)
	}
	if err != nil {
		return nil, nil, err
	}
	return out, changes, nil
}

func loopbackPortFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
