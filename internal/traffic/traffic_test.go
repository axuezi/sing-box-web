package traffic

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/axuezi/sing-box-web/internal/store"
)

// fakeStats is a sing-box V2Ray stats service: counters plus an uptime.
type fakeStats struct {
	mu      sync.Mutex
	values  map[string]int64
	started time.Time
	// restartMidQuery simulates a reload between the uptime reads.
	restartMidQuery bool
	queries         int
}

func pb(field int, v uint64) []byte { return binary.AppendUvarint([]byte{byte(field<<3 | 0)}, v) }

func pbString(field int, s string) []byte {
	b := binary.AppendUvarint([]byte{byte(field<<3 | 2)}, uint64(len(s)))
	return append(b, s...)
}

func (f *fakeStats) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.ReadAll(r.Body)
	f.mu.Lock()
	var msg []byte
	if strings.HasSuffix(r.URL.Path, "/GetSysStats") {
		msg = pb(10, uint64(time.Since(f.started)/time.Second))
	} else {
		f.queries++
		for name, v := range f.values {
			stat := append(pbString(1, name), pb(2, uint64(v))...)
			msg = append(msg, pbString(1, string(stat))...)
		}
		if f.restartMidQuery {
			f.started = time.Now()
			f.restartMidQuery = false
		}
	}
	f.mu.Unlock()
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status")
	w.Write(append(frame, msg...))
	w.Header().Set("Grpc-Status", "0")
}

func (f *fakeStats) set(started time.Time, kv ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = started
	f.values = map[string]int64{}
	for i := 0; i < len(kv); i += 2 {
		f.values[kv[i].(string)] = int64(kv[i+1].(int))
	}
}

func TestCollectorCountsIncreases(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SaveUsers(ctx, []*store.User{{Name: "alice", Enabled: true, Password: "p", UUID: "u", SubToken: "t"}}, nil)

	fake := &fakeStats{}
	srv := httptest.NewUnstartedServer(fake)
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	srv.Start()
	defer srv.Close()
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte(fmt.Sprintf(`{"experimental":{"v2ray_api":{"listen":%q,"stats":{"enabled":true}}}}`,
		strings.TrimPrefix(srv.URL, "http://"))), 0o600)
	c := &Collector{ConfigPath: cfg, Store: st, Location: time.UTC, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	up, down := "user>>>alice>>>traffic>>>uplink", "user>>>alice>>>traffic>>>downlink"
	in := "inbound>>>hy2-in>>>traffic>>>downlink"
	total := func() (int64, int64) {
		u, _ := st.GetUser(ctx, 1)
		return u.CycleUp, u.CycleDown
	}
	boot := time.Now().Add(-time.Hour)

	// First run only records where the counters stand.
	fake.set(boot, up, 100, down, 1000, in, 1000)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if u, d := total(); u != 0 || d != 0 {
		t.Fatalf("first run counted %d/%d", u, d)
	}

	// Growth is counted once, however often the counters are read.
	fake.set(boot, up, 150, down, 1600, in, 1600)
	c.Flush(ctx)
	c.Flush(ctx)
	if u, d := total(); u != 50 || d != 600 {
		t.Fatalf("after growth %d/%d", u, d)
	}

	// sing-box reloads and its new counters already exceed the old ones:
	// the new start time says they began at zero.
	fake.set(time.Now().Add(-30*time.Second), up, 400, down, 2000, in, 2000)
	c.Flush(ctx)
	if u, d := total(); u != 450 || d != 2600 {
		t.Fatalf("after reload %d/%d", u, d)
	}

	// A reload between reading the uptime and the counters: skip the round.
	fake.mu.Lock()
	fake.values[down] = 9999
	fake.restartMidQuery = true
	fake.mu.Unlock()
	if err := c.Flush(ctx); err == nil {
		t.Fatal("expected a retry")
	}
	if u, d := total(); u != 450 || d != 2600 {
		t.Fatalf("mid-query restart counted: %d/%d", u, d)
	}

	rows, _ := st.Traffic(ctx, "2000-01-01")
	var inbound int64
	for _, r := range rows {
		if r.Kind == "inbound" {
			inbound += r.Down
		}
	}
	if inbound != 600+2000 {
		t.Fatalf("inbound total %d", inbound)
	}
}

func TestRateWindowAveragesOverSpan(t *testing.T) {
	var w RateWindow
	base := time.Unix(1_700_000_000, 0)
	// A slow client behind a big buffer: 10 MB in the first second, then
	// nothing for four seconds, then 1 MB/s.
	var down int64
	steps := []int64{10 << 20, 0, 0, 0, 0, 1 << 20, 1 << 20, 1 << 20, 1 << 20, 1 << 20, 1 << 20}
	var got []int64
	if _, d := w.Add(base, 0, 0); d != 0 {
		t.Fatalf("first reading gave %d, want 0", d)
	}
	for i, b := range steps {
		down += b
		_, d := w.Add(base.Add(time.Duration(i+1)*time.Second), 0, down)
		got = append(got, d)
	}
	// Second 5: the 10 MB burst spread over five seconds = 2 MB/s, not 0.
	if got[4] != 2<<20 {
		t.Errorf("rate after the burst = %d, want %d (5 s average)", got[4], 2<<20)
	}
	// Once the burst is more than five seconds old, the steady 1 MB/s shows.
	if got[len(got)-1] != 1<<20 {
		t.Errorf("steady rate = %d, want %d", got[len(got)-1], 1<<20)
	}
	for i, d := range got[1:5] {
		if d == 0 {
			t.Errorf("second %d read 0 during a 5 s window that contains traffic", i+2)
		}
	}

	// Counters going backwards (sing-box restarted) start over.
	if _, d := w.Add(base.Add(20*time.Second), 0, 5); d != 0 {
		t.Errorf("after a restart got %d, want 0", d)
	}
}
