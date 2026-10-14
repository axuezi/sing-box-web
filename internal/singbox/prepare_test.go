package singbox

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func allFree(int) bool { return true }

func TestPrepareMinimal(t *testing.T) {
	out, changes, err := Prepare(nil, PrepareOptions{LogPath: "/var/log/sing-box/sing-box.log", V2RayAPI: true, PortFree: allFree})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"log.output": "/var/log/sing-box/sing-box.log",
		"experimental.clash_api.external_controller": "127.0.0.1:9090",
		"experimental.v2ray_api.listen":              "127.0.0.1:10085",
		"experimental.v2ray_api.stats.enabled":       "true",
		"outbounds.0.tag":                            "direct",
	} {
		if got := gjson.GetBytes(out, path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if len(gjson.GetBytes(out, "experimental.clash_api.secret").String()) != 32 {
		t.Errorf("clash_api secret missing: %s", out)
	}
	if !gjson.GetBytes(out, "experimental.v2ray_api.stats.users").IsArray() {
		t.Errorf("stats.users should be an array: %s", out)
	}
	if len(changes) != 4 {
		t.Errorf("changes = %q", changes)
	}
}

func TestPrepareKeepsExisting(t *testing.T) {
	raw := []byte(`{
  "log": {"level": "warn", "output": "/srv/sb.log"},
  "inbounds": [{"type": "vless", "tag": "in", "users": [{"name": "alice"}, {"name": "bob"}]},
               {"type": "hysteria2", "tag": "hy", "users": [{"name": "alice"}]}],
  "experimental": {"clash_api": {"external_controller": "127.0.0.1:1234"}}
}`)
	out, changes, err := Prepare(raw, PrepareOptions{LogPath: "/var/log/x.log", V2RayAPI: true, PortFree: allFree})
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(out, "log.output").String(); got != "/srv/sb.log" {
		t.Errorf("log.output changed to %q", got)
	}
	if got := gjson.GetBytes(out, "log.level").String(); got != "warn" {
		t.Errorf("log.level changed to %q", got)
	}
	if got := gjson.GetBytes(out, "experimental.clash_api").Raw; got != `{"external_controller": "127.0.0.1:1234"}` {
		t.Errorf("clash_api changed: %s", got)
	}
	if got := gjson.GetBytes(out, "experimental.v2ray_api.stats.inbounds").Raw; got != `["in","hy"]` {
		t.Errorf("stats.inbounds = %s", got)
	}
	if got := gjson.GetBytes(out, "experimental.v2ray_api.stats.users").Raw; got != `["alice","bob"]` {
		t.Errorf("stats.users = %s", got)
	}
	if len(changes) != 1 || !strings.Contains(changes[0], "v2ray_api") {
		t.Errorf("changes = %q", changes)
	}
	// Nothing left to add: a second pass is a no-op.
	again, changes, err := Prepare(out, PrepareOptions{LogPath: "/var/log/x.log", V2RayAPI: true, PortFree: allFree})
	if err != nil || len(changes) != 0 || string(again) != string(out) {
		t.Errorf("second pass changed things: %q %v", changes, err)
	}
}

func TestPrepareWithoutV2RayAPI(t *testing.T) {
	out, _, err := Prepare([]byte(`{"log": {"level": "error"}}`), PrepareOptions{LogPath: "/l.log", PortFree: func(p int) bool { return p != 9090 }, Reserved: []int{9091}})
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(out, "experimental.v2ray_api").Exists() {
		t.Errorf("v2ray_api added without V2RayAPI: %s", out)
	}
	if got := gjson.GetBytes(out, "experimental.clash_api.external_controller").String(); got != "127.0.0.1:9092" {
		t.Errorf("busy or reserved port not skipped: %s", got)
	}
	if got := gjson.GetBytes(out, "log.level").String(); got != "info" {
		t.Errorf("log.level = %q, want info once a log file is added", got)
	}
}

func TestPrepareRejectsBadJSON(t *testing.T) {
	if _, _, err := Prepare([]byte(`{"log":`), PrepareOptions{PortFree: allFree}); err == nil {
		t.Error("want an error for invalid JSON")
	}
}
