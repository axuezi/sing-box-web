package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	root, _ := filepath.Abs("testdata")
	r := &Reader{Root: root, DiskPath: t.TempDir()}
	// Simulate time passing between two CPU samples by rewriting /proc/stat.
	r.prev = r.sample()
	r.prev.at = r.prev.at.Add(-10e9)
	stat := filepath.Join(root, "proc/stat")
	orig, _ := os.ReadFile(stat)
	t.Cleanup(func() { os.WriteFile(stat, orig, 0o644) })
	// +60 user, +20 system, +100 idle, +10 iowait, +10 steal = 200 ticks
	os.WriteFile(stat, []byte("cpu  160 0 70 900 20 0 0 50 0 0\n"), 0o644)

	in := r.Read()
	if in.Kernel != "6.1.0-test" || in.OS != "Debian GNU/Linux 12 (bookworm)" || in.Uptime != 1000.5 {
		t.Errorf("host: %+v", in)
	}
	if in.Load != [3]float64{0.19, 0.76, 0.75} {
		t.Errorf("load %v", in.Load)
	}
	if in.CPU.Cores != 2 || in.CPU.Model != "Test CPU" {
		t.Errorf("cpu info %+v", in.CPU)
	}
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
	if !near(in.CPU.User, 0.3) || !near(in.CPU.System, 0.1) || !near(in.CPU.IOWait, 0.05) || !near(in.CPU.Steal, 0.05) || !near(in.CPU.Busy, 0.45) {
		t.Errorf("cpu usage %+v", in.CPU)
	}
	if in.Memory.Total != 2000000*1024 || in.Memory.Available != 750000*1024 {
		t.Errorf("memory %+v", in.Memory)
	}
	if in.Swap.Total != 2000000*1024 || in.Swap.Used != 700000*1024 {
		t.Errorf("swap %+v", in.Swap)
	}
	if in.Disk == nil || in.Disk.Total <= 0 {
		t.Errorf("disk %+v", in.Disk)
	}
}
