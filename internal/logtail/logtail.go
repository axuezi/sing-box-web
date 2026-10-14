// Package logtail reads and follows sing-box's log file.
package logtail

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Line is one parsed log line.
type Line struct {
	Time  string `json:"time,omitempty"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

var (
	ansi    = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	pattern = regexp.MustCompile(`^[+-]\d{4} (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) (TRACE|DEBUG|INFO|WARN|ERROR|FATAL|PANIC) (.*)$`)
)

// Parse splits a sing-box log line into time, level and message. Lines that
// do not match the timestamped format keep their full text with level "".
func Parse(s string) Line {
	s = ansi.ReplaceAllString(strings.TrimRight(s, "\r\n"), "")
	if m := pattern.FindStringSubmatch(s); m != nil {
		return Line{Time: m[1], Level: m[2], Text: m[3]}
	}
	return Line{Text: s}
}

var levelRank = map[string]int{"TRACE": 0, "DEBUG": 1, "INFO": 2, "WARN": 3, "ERROR": 4, "FATAL": 5, "PANIC": 6}

// Filter selects lines by minimum level and a case-insensitive substring.
type Filter struct {
	MinLevel string
	Query    string
}

// Match reports whether l passes the filter. Unparsed lines always pass the
// level check so stack traces and the like are never hidden.
func (f Filter) Match(l Line) bool {
	if min, ok := levelRank[strings.ToUpper(f.MinLevel)]; ok && l.Level != "" && levelRank[l.Level] < min {
		return false
	}
	if f.Query != "" && !strings.Contains(strings.ToLower(l.Text), strings.ToLower(f.Query)) {
		return false
	}
	return true
}

// maxBacklogBytes bounds how far back Backlog reads.
const maxBacklogBytes = 4 << 20

// Backlog returns up to n of the last lines of path, oldest first, and the
// offset just past them — pass it to Follow so no line falls in between.
func Backlog(path string, n int) ([]string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := st.Size()
	start := max(size-maxBacklogBytes, 0)
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	// Stop at the last complete line; a partial one is left for Follow.
	end := size
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		end = start + int64(i) + 1
		buf = buf[:i+1]
	} else {
		end, buf = start, nil
	}
	if start > 0 {
		// Drop the partial first line.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, end, nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, end, nil
}

// Follow calls fn for every complete line written to path from offset on
// (-1 = the current end), until ctx is done. It survives logrotate (rename +
// create, or truncate).
func Follow(ctx context.Context, path string, offset int64, interval time.Duration, fn func(string)) error {
	f, ino, err := openAt(path, offset)
	if err != nil {
		return err
	}
	defer func() { f.Close() }()
	r := bufio.NewReader(f)
	var partial strings.Builder

	drain := func() {
		for {
			chunk, err := r.ReadString('\n')
			partial.WriteString(chunk)
			if err != nil {
				return
			}
			fn(partial.String())
			partial.Reset()
		}
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		drain()

		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}

		// Detect rotation: a new file at path, or the file shrank.
		st, err := os.Stat(path)
		if err != nil {
			continue // between rename and create; try again next tick
		}
		pos, _ := f.Seek(0, io.SeekCurrent)
		pos -= int64(r.Buffered())
		if inode(st) != ino || st.Size() < pos {
			nf, nino, err := openAt(path, 0)
			if err != nil {
				continue
			}
			if inode(st) != ino {
				drain() // lines written to the old file just before the rename
			}
			f.Close()
			f, ino = nf, nino
			r.Reset(f)
			partial.Reset()
		}
	}
}

func openAt(path string, offset int64) (*os.File, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	whence := io.SeekStart
	if offset < 0 || offset > st.Size() {
		offset, whence = 0, io.SeekEnd
	}
	if _, err := f.Seek(offset, whence); err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, inode(st), nil
}

func inode(st os.FileInfo) uint64 {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return s.Ino
	}
	return 0
}
