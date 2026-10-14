package flows

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/gateway"
	"github.com/axuezi/sing-box-web/internal/store"
)

func TestSite(t *testing.T) {
	for _, c := range []struct{ host, ip, want string }{
		{"r3---sn-abc.googlevideo.com", "1.1.1.1", "googlevideo.com"},
		{"WWW.Example.CO.UK.", "", "example.co.uk"},
		{"", "::ffff:8.8.8.8", "8.8.8.8"},
		{"localhost", "", "localhost"},
		{"10.0.0.1", "", "10.0.0.1"},
	} {
		if got := Site(c.host, c.ip); got != c.want {
			t.Errorf("Site(%q, %q) = %q, want %q", c.host, c.ip, got, c.want)
		}
	}
}

func mk(id, src, host string, up, down int64) clashapi.Connection {
	var c clashapi.Connection
	c.ID = id
	c.Metadata.Type = "vless/in"
	c.Metadata.SourceIP, c.Metadata.SourcePort = src, "40000"
	c.Metadata.Host = host
	c.Upload, c.Download = up, down
	c.Chains = []string{"direct", "select"}
	return c
}

func snap(cs ...clashapi.Connection) clashapi.Snapshot { return clashapi.Snapshot{Connections: cs} }

func TestRecorder(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &Recorder{Store: st}
	t0 := time.Date(2026, 10, 13, 10, 0, 0, 0, time.Local)

	// Off: nothing is tracked.
	r.Observe(snap(mk("a", "1.2.3.4", "a.example.com", 100, 1000)), t0)
	if r.conns != nil {
		t.Fatal("tracked while off")
	}
	if err := st.SetSetting(ctx, SettingKey, "1"); err != nil {
		t.Fatal(err)
	}
	r.Refresh(ctx)

	// First snapshot only sets the baseline for connections already open.
	r.Observe(snap(mk("a", "1.2.3.4", "a.example.com", 100, 1000)), t0)
	// a grows; b is new but unattributed, so it waits up to attrWait.
	r.Observe(snap(mk("a", "1.2.3.4", "a.example.com", 150, 1600), mk("b", "::ffff:5.6.7.8", "", 10, 20)), t0.Add(time.Second))
	// a has closed; b is past attrWait and counts from its start.
	r.Observe(snap(mk("b", "::ffff:5.6.7.8", "", 30, 70)), t0.Add(7*time.Second))
	r.Flush(ctx)

	rows, err := st.FlowsBetween(ctx, "2026-10-13 00", "2026-10-13 23")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.FlowRow{}
	for _, x := range rows {
		got[x.IP+" "+x.Site] = x
	}
	if a := got["1.2.3.4 example.com"]; a.Up != 50 || a.Down != 600 || a.Conns != 0 || a.Outbound != "direct" {
		t.Errorf("a = %+v, want 50/600, 0 new conns, outbound direct", a)
	}
	// b has no domain, so its site is the (empty) destination IP.
	if b := got["5.6.7.8 "]; b.Up != 30 || b.Down != 70 || b.Conns != 1 {
		t.Errorf("b = %+v, want 30/70 and 1 conn", b)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2: %+v", len(rows), rows)
	}

	// A new connection that closes before its user is known still counts.
	r.Observe(snap(mk("c", "9.9.9.9", "x.org", 0, 0)), t0.Add(8*time.Second))
	r.Observe(snap(mk("c", "9.9.9.9", "x.org", 5, 6)), t0.Add(9*time.Second))
	r.Observe(snap(), t0.Add(10*time.Second))
	r.Flush(ctx)
	rows, _ = st.FlowsBetween(ctx, "2026-10-13 10", "2026-10-13 10")
	found := false
	for _, x := range rows {
		if x.Site == "x.org" {
			found = x.Up == 5 && x.Down == 6 && x.Conns == 1
		}
	}
	if !found {
		t.Errorf("short-lived unattributed connection lost: %+v", rows)
	}

	// A connection relayed through a local proxy is filed under the client
	// the proxy reported.
	r.Gateway = &gateway.Resolver{}
	r.Gateway.Feed("sbw-gw 203.0.113.50 127.0.0.1:40000")
	r.Observe(snap(mk("d", "127.0.0.1", "y.org", 1, 2)), t0.Add(20*time.Second))
	r.Observe(snap(), t0.Add(30*time.Second))
	r.Flush(ctx)
	rows, _ = st.FlowsBetween(ctx, "2026-10-13 10", "2026-10-13 10")
	relayed := false
	for _, x := range rows {
		if x.Site == "y.org" {
			relayed = x.IP == "203.0.113.50"
		}
	}
	if !relayed {
		t.Errorf("relayed connection not resolved to its client: %+v", rows)
	}

	// Turning it off drops state; back on, open connections are baselined again.
	_ = st.SetSetting(ctx, SettingKey, "0")
	r.Refresh(ctx)
	_ = st.SetSetting(ctx, SettingKey, "1")
	r.Refresh(ctx)
	r.Observe(snap(mk("b", "5.6.7.8", "", 500, 500)), t0.Add(time.Hour))
	r.Flush(ctx)
	rows, _ = st.FlowsBetween(ctx, "2026-10-13 11", "2026-10-13 11")
	if len(rows) != 0 {
		t.Errorf("counted a connection open before re-enabling: %+v", rows)
	}
}
