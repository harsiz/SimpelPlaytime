//go:build linux

package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type linuxScanner struct{ root string }

// New returns the /proc based scanner.
func New() Scanner { return &linuxScanner{root: "/proc"} }

// Scan reads /proc. Window, focus and idle info are not available here, so
// only games (and everything in "all" mode) get tracked on Linux.
func (s *linuxScanner) Scan() (Snapshot, error) {
	snap := Snapshot{Time: time.Now()}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return snap, err
	}
	for _, e := range entries {
		pid, err := strconv.ParseUint(e.Name(), 10, 32)
		if err != nil || pid <= 1 {
			continue
		}
		dir := filepath.Join(s.root, e.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		comm, ppid := parseStat(string(stat))
		path, _ := os.Readlink(filepath.Join(dir, "exe"))
		path = strings.TrimSuffix(path, " (deleted)")
		name := comm
		if path != "" {
			name = filepath.Base(path)
		}
		if name == "" {
			continue
		}
		snap.Procs = append(snap.Procs, Process{PID: uint32(pid), PPID: ppid, Name: name, Path: path})
	}
	return snap, nil
}

// parseStat pulls the command name and parent pid out of /proc/<pid>/stat.
func parseStat(stat string) (comm string, ppid uint32) {
	open := strings.IndexByte(stat, '(')
	close := strings.LastIndexByte(stat, ')')
	if open < 0 || close < open {
		return "", 0
	}
	comm = stat[open+1 : close]
	fields := strings.Fields(stat[close+1:])
	if len(fields) >= 2 {
		if v, err := strconv.ParseUint(fields[1], 10, 32); err == nil {
			ppid = uint32(v)
		}
	}
	return comm, ppid
}
