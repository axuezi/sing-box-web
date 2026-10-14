package logtail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	l := Parse("+0000 2026-10-13 03:24:53 INFO [4034709137 0ms] inbound/hysteria2[hy2-in]: inbound connection\n")
	if l.Time != "2026-10-13 03:24:53" || l.Level != "INFO" || l.Text != "[4034709137 0ms] inbound/hysteria2[hy2-in]: inbound connection" {
		t.Fatalf("got %+v", l)
	}
	l = Parse("\x1b[31mFATAL\x1b[0m[0000] start service: boom")
	if l.Level != "" || l.Text != "FATAL[0000] start service: boom" {
		t.Fatalf("unparsed line: %+v", l)
	}
}

func TestFilter(t *testing.T) {
	info := Line{Level: "INFO", Text: "inbound connection to api.anthropic.com"}
	warn := Line{Level: "WARN", Text: "something"}
	raw := Line{Text: "goroutine 1 [running]"}
	f := Filter{MinLevel: "warn"}
	if f.Match(info) || !f.Match(warn) || !f.Match(raw) {
		t.Fatal("level filter")
	}
	f = Filter{Query: "ANTHROPIC"}
	if !f.Match(info) || f.Match(warn) {
		t.Fatal("query filter")
	}
}

func TestBacklog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("1\n2\n3\n4\n"), 0o644)
	got, off, err := Backlog(p, 2)
	if err != nil || len(got) != 2 || got[0] != "3" || got[1] != "4" || off != 8 {
		t.Fatalf("got %q off %d err %v", got, off, err)
	}
	// A partial last line is not returned and the offset stops before it.
	os.WriteFile(p, []byte("1\n2\npart"), 0o644)
	if got, off, _ := Backlog(p, 5); len(got) != 2 || off != 4 {
		t.Fatalf("partial: %q off %d", got, off)
	}
	os.WriteFile(p, nil, 0o644)
	if got, _, _ := Backlog(p, 5); len(got) != 0 {
		t.Fatalf("empty file: %q", got)
	}
}

func TestFollowSurvivesRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sing-box.log")
	os.WriteFile(p, []byte("old line\n"), 0o644)

	var mu sync.Mutex
	var got []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Follow(ctx, p, -1, 10*time.Millisecond, func(s string) {
			mu.Lock()
			got = append(got, s)
			mu.Unlock()
		})
		close(done)
	}()
	waitFor := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			l := len(got)
			mu.Unlock()
			if l >= n {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d lines, got %q", n, got)
	}

	time.Sleep(30 * time.Millisecond)
	appendTo(t, p, "a\n")
	appendTo(t, p, "b-part")
	appendTo(t, p, "ial\n")
	waitFor(2)

	// logrotate "create" mode: rename, then a fresh file.
	os.Rename(p, p+".1")
	time.Sleep(30 * time.Millisecond)
	appendTo(t, p, "after rotate\n")
	waitFor(3)

	cancel()
	<-done
	want := fmt.Sprint([]string{"a\n", "b-partial\n", "after rotate\n"})
	if fmt.Sprint(got) != want {
		t.Fatalf("got %q, want %s", got, want)
	}
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}
