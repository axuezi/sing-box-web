// Package sysinfo reads the host's state from /proc and statfs: CPU, load,
// memory, swap, disk and uptime.
//
// In a container these files still describe the host (unless lxcfs is in
// use), except /etc/os-release, which is the image's: mount the host's at
// /host/etc/os-release to name the host OS.
package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Info is one reading.
type Info struct {
	Hostname string     `json:"hostname"`
	OS       string     `json:"os"`
	Kernel   string     `json:"kernel"`
	Arch     string     `json:"arch"`
	Uptime   float64    `json:"uptime"` // seconds
	CPU      CPU        `json:"cpu"`
	Load     [3]float64 `json:"load"`
	Memory   Memory     `json:"memory"`
	Swap     Swap       `json:"swap"`
	Disk     *Disk      `json:"disk,omitempty"`
	At       time.Time  `json:"at"`
}

// CPU usage is over the interval since the previous reading (at most a
// minute; otherwise a short fresh sample), as fractions of all cores.
type CPU struct {
	Cores  int     `json:"cores"`
	Model  string  `json:"model"`
	Busy   float64 `json:"busy"`
	User   float64 `json:"user"`
	System float64 `json:"system"`
	IOWait float64 `json:"iowait"`
	Steal  float64 `json:"steal"`  // taken by the hypervisor: a sign of a crowded VPS host
	Window float64 `json:"window"` // seconds measured
}

// Memory in bytes. Available is what can be used without swapping.
type Memory struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
	Cached    int64 `json:"cached"`
}

// Swap in bytes, with page traffic per second over the CPU window.
type Swap struct {
	Total     int64   `json:"total"`
	Used      int64   `json:"used"`
	InPerSec  float64 `json:"in_per_sec"`  // pages
	OutPerSec float64 `json:"out_per_sec"` // pages
}

// Disk is the filesystem holding Path.
type Disk struct {
	Path       string `json:"path"`
	Total      int64  `json:"total"`
	Free       int64  `json:"free"` // available to unprivileged users
	Inodes     int64  `json:"inodes"`
	InodesFree int64  `json:"inodes_free"`
}

type cpuTimes struct {
	user, system, idle, iowait, steal, total uint64
	swapIn, swapOut                          uint64
	at                                       time.Time
}

// Reader keeps the previous CPU reading between calls.
type Reader struct {
	DiskPath string // filesystem to report; "" = /
	Root     string // prefix for /proc etc. in tests; "" = real system

	mu   sync.Mutex
	prev *cpuTimes
}

func (r *Reader) path(p string) string { return r.Root + p }

// Read takes a reading. It may block for a fraction of a second when there
// is no recent previous one to measure CPU against.
func (r *Reader) Read() Info {
	info := Info{Arch: runtime.GOARCH, At: time.Now()}
	info.Hostname, _ = os.Hostname()
	info.Kernel = strings.TrimSpace(r.readString("/proc/sys/kernel/osrelease"))
	info.OS = r.osName()
	if f := strings.Fields(r.readString("/proc/uptime")); len(f) > 0 {
		info.Uptime, _ = strconv.ParseFloat(f[0], 64)
	}
	if f := strings.Fields(r.readString("/proc/loadavg")); len(f) >= 3 {
		for i := range 3 {
			info.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	info.CPU.Cores, info.CPU.Model = r.cpuInfo()
	r.cpuUsage(&info)
	r.memory(&info)
	info.Disk = r.disk()
	return info
}

func (r *Reader) readString(p string) string {
	b, err := os.ReadFile(r.path(p))
	if err != nil {
		return ""
	}
	return string(b)
}

func (r *Reader) osName() string {
	for _, p := range []string{"/host/etc/os-release", "/etc/os-release"} {
		s := r.readString(p)
		if s == "" {
			continue
		}
		for _, line := range strings.Split(s, "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				return strings.Trim(v, `"'`)
			}
		}
	}
	return ""
}

func (r *Reader) cpuInfo() (int, string) {
	n, model := 0, ""
	for _, line := range strings.Split(r.readString("/proc/cpuinfo"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "processor":
			n++
		case "model name":
			if model == "" {
				model = strings.TrimSpace(v)
			}
		}
	}
	if n == 0 {
		n = runtime.NumCPU()
	}
	return n, model
}

func (r *Reader) sample() *cpuTimes {
	t := &cpuTimes{at: time.Now()}
	for _, line := range strings.Split(r.readString("/proc/stat"), "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || f[0] != "cpu" {
			continue
		}
		// user nice system idle iowait irq softirq steal [guest guest_nice, already in user]
		var v [8]uint64
		for i := range v {
			v[i], _ = strconv.ParseUint(f[i+1], 10, 64)
		}
		t.user = v[0] + v[1]
		t.system = v[2] + v[5] + v[6]
		t.idle, t.iowait, t.steal = v[3], v[4], v[7]
		t.total = v[0] + v[1] + v[2] + v[3] + v[4] + v[5] + v[6] + v[7]
		break
	}
	for _, line := range strings.Split(r.readString("/proc/vmstat"), "\n") {
		k, v, _ := strings.Cut(line, " ")
		switch k {
		case "pswpin":
			t.swapIn, _ = strconv.ParseUint(v, 10, 64)
		case "pswpout":
			t.swapOut, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	return t
}

func (r *Reader) cpuUsage(info *Info) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.prev
	if prev == nil || time.Since(prev.at) > time.Minute || time.Since(prev.at) < 200*time.Millisecond {
		prev = r.sample()
		time.Sleep(400 * time.Millisecond)
	}
	cur := r.sample()
	r.prev = cur
	dt := float64(cur.total - prev.total)
	if cur.total <= prev.total {
		return
	}
	frac := func(a, b uint64) float64 {
		if a < b {
			return 0
		}
		return float64(a-b) / dt
	}
	c := &info.CPU
	c.User = frac(cur.user, prev.user)
	c.System = frac(cur.system, prev.system)
	c.IOWait = frac(cur.iowait, prev.iowait)
	c.Steal = frac(cur.steal, prev.steal)
	c.Busy = 1 - frac(cur.idle, prev.idle) - c.IOWait
	if c.Busy < 0 {
		c.Busy = 0
	}
	sec := cur.at.Sub(prev.at).Seconds()
	c.Window = sec
	if sec > 0 {
		if cur.swapIn >= prev.swapIn {
			info.Swap.InPerSec = float64(cur.swapIn-prev.swapIn) / sec
		}
		if cur.swapOut >= prev.swapOut {
			info.Swap.OutPerSec = float64(cur.swapOut-prev.swapOut) / sec
		}
	}
}

func (r *Reader) memory(info *Info) {
	f, err := os.Open(r.path("/proc/meminfo"))
	if err != nil {
		return
	}
	defer f.Close()
	kv := map[string]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(fs[0], 10, 64)
		kv[k] = n * 1024
	}
	info.Memory = Memory{Total: kv["MemTotal"], Available: kv["MemAvailable"], Cached: kv["Cached"] + kv["Buffers"]}
	info.Swap.Total = kv["SwapTotal"]
	info.Swap.Used = kv["SwapTotal"] - kv["SwapFree"]
}

func (r *Reader) disk() *Disk {
	p := r.DiskPath
	if p == "" {
		p = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return nil
	}
	bs := int64(st.Bsize)
	return &Disk{
		Path: p, Total: int64(st.Blocks) * bs, Free: int64(st.Bavail) * bs,
		Inodes: int64(st.Files), InodesFree: int64(st.Ffree),
	}
}
