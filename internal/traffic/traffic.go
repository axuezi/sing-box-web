// Package traffic records per-user traffic from the V2Ray stats API and
// streams live throughput from the Clash API.
package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/axuezi/sing-box-web/internal/clashapi"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/v2rayapi"
)

// ErrNoStatsAPI means the config has no experimental.v2ray_api.
var ErrNoStatsAPI = errors.New("v2ray_api is not enabled in the sing-box config")

// Collector moves counters from sing-box into daily and hourly totals.
// Bytes counted since the last collection are lost if sing-box restarts or
// reloads (its counters start over), which is why Flush also runs right
// before every reload the panel makes.
type Collector struct {
	ConfigPath string
	Store      *store.Store
	Interval   time.Duration
	Location   *time.Location
	Logger     *slog.Logger
	// AfterCollect runs after each periodic collection that recorded usage,
	// outside the usage lock, e.g. to enforce quotas without waiting for
	// the next check. Flushes made by config changes do not call it.
	AfterCollect func()
	// Now is the wall clock (nil = time.Now); tests move it.
	Now func() time.Time

	mu      sync.Mutex
	lastRun time.Time
	lastErr error
}

// Run collects every Interval until ctx is done.
func (c *Collector) Run(ctx context.Context) {
	iv := c.Interval
	if iv == 0 {
		iv = time.Minute
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if err := c.Flush(ctx); err != nil {
			if !errors.Is(err, ErrNoStatsAPI) {
				c.Logger.Debug("traffic collection failed", "err", err)
			}
		} else if c.AfterCollect != nil {
			c.AfterCollect()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Status reports when collection last ran and how it went.
func (c *Collector) Status() (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastRun, c.lastErr
}

// baselineKey stores the counter values seen at the last collection.
const baselineKey = "stats_baseline"

// baseline is what sing-box's counters read at the last collection, and
// how long the sing-box instance that produced them had been up.
type baseline struct {
	Collected time.Time        `json:"collected"`
	Started   int64            `json:"started"` // unix seconds, wall clock; informational
	Uptime    int64            `json:"uptime"`  // seconds; 0 in baselines from older versions
	Values    map[string]int64 `json:"values"`
}

// startSlack tolerates rounding of the uptime (whole seconds) and the time
// between reading it and the clock, for baselines without Uptime.
const startSlack = 5

// Flush collects now. Counters are read without resetting them, so other
// readers of the stats API cannot take traffic away; the increase since the
// last collection is what gets recorded. sing-box zeroes its counters on
// every start and reload, which the stats service's uptime reveals.
func (c *Collector) Flush(ctx context.Context) (err error) {
	ctx, unlock := c.Store.LockUsage(ctx)
	defer unlock()
	c.mu.Lock()
	defer func() {
		c.lastRun, c.lastErr = time.Now(), err
		c.mu.Unlock()
	}()
	file, err := singbox.ReadFile(c.ConfigPath)
	if err != nil {
		return err
	}
	addr := singbox.Summarize(file.Raw).V2RayAPI
	if addr == "" {
		return ErrNoStatsAPI
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	api := &v2rayapi.Client{Addr: addr}
	up1, err := api.Uptime(ctx)
	if err != nil {
		return err
	}
	stats, err := api.QueryStats(ctx, []string{"user>>>", "inbound>>>", "outbound>>>"}, false)
	if err != nil {
		return err
	}
	up2, err := api.Uptime(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	started := now.Add(-up2).Unix()
	uptime := int64(up2 / time.Second)
	if up2 < up1 {
		return errors.New("sing-box restarted while reading counters; retrying next time")
	}

	var base baseline
	if raw, err := c.Store.GetSetting(ctx, baselineKey); err != nil {
		return err
	} else if raw != "" {
		_ = json.Unmarshal([]byte(raw), &base)
	}
	next := baseline{Collected: now, Started: started, Uptime: uptime, Values: make(map[string]int64, len(stats))}
	for _, s := range stats {
		next.Values[s.Name] = s.Value
	}
	nextRaw, _ := json.Marshal(next)
	if base.Values == nil {
		// First run: nothing to compare with. Start counting from here
		// rather than book everything sing-box counted since it started.
		return c.Store.AddTrafficAndSet(context.WithoutCancel(ctx), nil, baselineKey, string(nextRaw))
	}
	if newInstance(base, started, uptime, stats) {
		base.Values = map[string]int64{} // a new sing-box instance: counters began at zero
		if boot := time.Unix(started, 0); boot.After(base.Collected) {
			base.Collected = boot
		}
	}

	users, err := c.Store.ListUsers(ctx)
	if err != nil {
		return err
	}
	idByName := map[string]int64{}
	for _, u := range users {
		idByName[u.Name] = u.ID
	}
	loc := c.Location
	if loc == nil {
		loc = time.Local
	}
	local := now.In(loc)
	day, hour := local.Format(time.DateOnly), local.Format("2006-01-02 15")
	agg := map[[2]string]*store.TrafficDelta{}
	for _, s := range stats {
		delta := s.Value - base.Values[s.Name]
		if delta < 0 {
			delta = s.Value // reset we could not see coming: count from zero
		}
		ctr, ok := v2rayapi.ParseName(s.Name)
		if !ok || delta == 0 {
			continue
		}
		key := ctr.Name
		if ctr.Kind == "user" {
			id, ok := idByName[ctr.Name]
			if !ok {
				continue // user deleted or renamed since
			}
			key = strconv.FormatInt(id, 10)
		}
		k := [2]string{ctr.Kind, key}
		d := agg[k]
		if d == nil {
			d = &store.TrafficDelta{From: base.Collected, To: now, Location: loc, Day: day, Hour: hour, Kind: ctr.Kind, Key: key}
			agg[k] = d
		}
		if ctr.Direction == "uplink" {
			d.Up += delta
		} else {
			d.Down += delta
		}
	}
	deltas := make([]store.TrafficDelta, 0, len(agg))
	for _, d := range agg {
		deltas = append(deltas, *d)
	}
	return c.Store.AddTrafficAndSet(context.WithoutCancel(ctx), deltas, baselineKey, string(nextRaw))
}

// newInstance reports whether the counters come from a sing-box started
// (or reloaded) since base was taken. Within one instance the uptime never
// goes back and no counter shrinks; the wall clock is not trusted, since NTP
// steps, VM suspends and manual changes move it. Baselines written before
// the uptime was stored fall back to comparing start times once.
func newInstance(base baseline, started, uptime int64, stats []v2rayapi.Stat) bool {
	if base.Uptime > 0 {
		if uptime < base.Uptime {
			return true
		}
	} else if diff := started - base.Started; diff > startSlack || diff < -startSlack {
		return true
	}
	for _, s := range stats {
		if s.Value < base.Values[s.Name] {
			return true
		}
	}
	return false
}

// Sample is one second of live throughput.
type Sample struct {
	T     int64 `json:"t"`    // unix ms
	Up    int64 `json:"up"`   // bytes/s
	Down  int64 `json:"down"` // bytes/s
	Conns int   `json:"conns"`
	Mem   int64 `json:"mem"`
}

// Hub polls the Clash API once a second and fans samples out, keeping the
// last few minutes for new subscribers. It polls while anyone is
// subscribed, or all the time once Run is called.
type Hub struct {
	ConfigPath string
	History    int
	// OnSnapshot, if set, gets every connection snapshot read successfully.
	OnSnapshot func(clashapi.Snapshot, time.Time)

	mu      sync.Mutex
	subs    map[chan Sample]struct{}
	hist    []Sample
	running bool
	always  bool
	lastErr string
}

// Run keeps sampling in the background until ctx is done, so the live chart
// already shows the last few minutes when someone opens it.
func (h *Hub) Run(ctx context.Context) {
	h.mu.Lock()
	h.always = true
	start := !h.running
	h.running = true
	h.mu.Unlock()
	if start {
		go h.loop()
	}
	<-ctx.Done()
	h.mu.Lock()
	h.always = false
	h.mu.Unlock()
}

// Subscribe returns recent samples and a channel of new ones; call cancel
// when done.
func (h *Hub) Subscribe() (recent []Sample, ch <-chan Sample, cancel func()) {
	c := make(chan Sample, 8)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[chan Sample]struct{}{}
	}
	h.subs[c] = struct{}{}
	recent = append([]Sample(nil), h.hist...)
	if !h.running {
		h.running = true
		go h.loop()
	}
	return recent, c, func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.mu.Unlock()
	}
}

// Err is the last polling error ("" when healthy).
func (h *Hub) Err() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}

func (h *Hub) loop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var rates RateWindow
	for range t.C {
		h.mu.Lock()
		if len(h.subs) == 0 && !h.always {
			h.running = false
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()

		snap, err := h.snapshot()
		now := time.Now()
		if err == nil && h.OnSnapshot != nil {
			h.OnSnapshot(snap, now)
		}
		s := Sample{T: now.UnixMilli()}
		if err == nil {
			s.Conns, s.Mem = len(snap.Connections), snap.Memory
			s.Up, s.Down = rates.Add(now, snap.UploadTotal, snap.DownloadTotal)
		} else {
			rates.Reset()
		}

		h.mu.Lock()
		h.lastErr = ""
		if err != nil {
			h.lastErr = err.Error()
		}
		max := h.History
		if max == 0 {
			max = 300
		}
		h.hist = append(h.hist, s)
		if len(h.hist) > max {
			h.hist = h.hist[len(h.hist)-max:]
		}
		for c := range h.subs {
			select {
			case c <- s:
			default: // slow subscriber: drop
			}
		}
		h.mu.Unlock()
	}
}

// RateSpan is how far back live rates look. sing-box counts bytes when it
// hands them to the connection towards the client, not when the client
// receives them; with Hysteria2's large QUIC buffers a slow client shows as
// bursts of many MB/s followed by seconds of nothing. Averaging over five
// seconds keeps the error within about 15% in normal use (measured) at the
// cost of reacting a few seconds late.
const RateSpan = 5 * time.Second

// RateWindow turns sing-box's cumulative byte counters into rates averaged
// over the last RateSpan.
type RateWindow struct {
	points []ratePoint
}

type ratePoint struct {
	at       time.Time
	up, down int64
}

// Add records the counters read at time at and returns the average upload
// and download rates in bytes per second over the window (0 until there
// are two readings).
func (w *RateWindow) Add(at time.Time, up, down int64) (int64, int64) {
	if n := len(w.points); n > 0 && (up < w.points[n-1].up || down < w.points[n-1].down) {
		w.points = nil // counters went backwards: sing-box restarted
	}
	w.points = append(w.points, ratePoint{at, up, down})
	// Keep the newest reading at least RateSpan old as the base.
	for len(w.points) > 2 && at.Sub(w.points[1].at) >= RateSpan {
		w.points = w.points[1:]
	}
	first := w.points[0]
	dt := at.Sub(first.at).Seconds()
	if len(w.points) < 2 || dt <= 0 {
		return 0, 0
	}
	return int64(float64(up-first.up) / dt), int64(float64(down-first.down) / dt)
}

// Reset forgets past readings, e.g. after a failed read.
func (w *RateWindow) Reset() { w.points = nil }

func (h *Hub) snapshot() (clashapi.Snapshot, error) {
	c, err := ClashClient(h.ConfigPath)
	if err != nil {
		return clashapi.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	return c.Connections(ctx)
}

// ErrNoClashAPI means the config has no experimental.clash_api.
var ErrNoClashAPI = errors.New("clash_api is not enabled in the sing-box config")

// ClashClient builds a Clash API client from the current config.
func ClashClient(configPath string) (*clashapi.Client, error) {
	file, err := singbox.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	addr := singbox.Summarize(file.Raw).ClashAPI
	if addr == "" {
		return nil, ErrNoClashAPI
	}
	return &clashapi.Client{BaseURL: "http://" + addr, Secret: singbox.ClashAPISecret(file.Raw)}, nil
}
