package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/store"
)

func TestEmptyInboundFlowSurvivesQuotaAndReenable(t *testing.T) {
	for _, flow := range []string{"", "xtls-rprx-vision"} {
		t.Run("flow="+flow, func(t *testing.T) {
			m := newConfigEnv(t)
			ctx := context.Background()
			_, keys := m.call("POST", "/api/inbounds/generate", `{"kind":"reality_keypair"}`)
			form := map[string]any{"tag": "empty-vless", "type": "vless", "listen_port": 2443, "flow": flow,
				"tls": map[string]any{"mode": "reality", "server_name": "www.example.com", "handshake_server": "www.example.com", "private_key": keys.Get("private_key").String(), "short_ids": []string{"ab12"}}}
			req, _ := json.Marshal(map[string]any{"form": form})
			if code, b := m.call("POST", "/api/inbounds", string(req)); code != 200 {
				t.Fatalf("create %d %s", code, b.Raw)
			}
			_, b := m.call("GET", "/api/inbounds", "")
			if got := b.Get(`inbounds.#(form.tag=="empty-vless").form.flow`).String(); got != flow {
				t.Fatalf("empty form flow=%q", got)
			}
			if code, b := m.call("PATCH", "/api/users/1", `{"inbounds":["empty-vless"],"quota_bytes":100}`); code != 200 {
				t.Fatalf("assign %d %s", code, b.Raw)
			}
			check := func() {
				t.Helper()
				in := m.config().Get(`inbounds.#(tag=="empty-vless")`)
				if in.Get("users.#").Int() != 1 || in.Get("users.0.flow").String() != flow {
					t.Fatalf("flow lost: %s", in.Raw)
				}
			}
			check()
			if err := m.st.AddTraffic(ctx, []store.TrafficDelta{{Day: time.Now().Format(time.DateOnly), Kind: "user", Key: "1", Down: 100}}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.enforcer().Check(ctx); err != nil {
				t.Fatal(err)
			}
			if n := m.config().Get(`inbounds.#(tag=="empty-vless").users.#`).Int(); n != 0 {
				t.Fatal("quota did not remove user")
			}
			// Even while empty, subscription must advertise the saved flow.
			u, _ := m.st.GetUser(ctx, 1)
			raw := []byte(m.config().Raw)
			flows, err := protocol.StoredFlows(ctx, m.st)
			if err != nil {
				t.Fatal(err)
			}
			effective := protocol.EffectiveFlows(raw, flows)
			if got, ok := effective["empty-vless"]; !ok || got != flow {
				t.Fatalf("stored flow %v", effective)
			}
			p, _ := protocol.Get("vless")
			in := protocol.Inbounds(raw)
			for _, i := range in {
				if i.Tag == "empty-vless" {
					v := effective[i.Tag]
					out, err := p.SingBox(protocol.Client{Name: "node", Inbound: i, Endpoint: protocol.Endpoint{Host: "example.com", Port: 443}, User: u, Flow: &v})
					if err != nil {
						t.Fatal(err)
					}
					if got, _ := out["flow"].(string); got != flow {
						t.Fatalf("subscription flow %v", out)
					}
				}
			}
			if code, b := m.call("POST", "/api/users/1/reset-usage", `{}`); code != 200 {
				t.Fatalf("reset %d %s", code, b.Raw)
			}
			check()
			for _, enabled := range []bool{false, true} {
				if code, b := m.call("PATCH", "/api/users/1", fmt.Sprintf(`{"enabled":%v}`, enabled)); code != 200 {
					t.Fatalf("toggle %d %s", code, b.Raw)
				}
			}
			check()
		})
	}
}
