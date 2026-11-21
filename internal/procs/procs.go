// Package procs lists running processes, their windows, the foreground window
// and how long the user has been idle. There is a real implementation for
// Windows, a /proc based one for Linux and a no-op one for everything else.
package procs

import (
	"path/filepath"
	"strings"
	"time"
)

// Process is one running process.
type Process struct {
	PID  uint32 `json:"pid"`
	PPID uint32 `json:"ppid"`
	// Name is the exe file name, for example "Hades2.exe".
	Name string `json:"name"`
	// Path is the full exe path. It can be empty when the OS refuses to tell.
	Path string `json:"path,omitempty"`
	// Title is the title of the main visible window (if any).
	Title string `json:"title,omitempty"`
	// HasWindow is true when the process owns a visible top level window.
	HasWindow bool `json:"hasWindow"`
}

// Snapshot is one scan of the system.
type Snapshot struct {
	Time          time.Time
	Procs         []Process
	ForegroundPID uint32
	// Idle is how long since the last keyboard/mouse input.
	Idle time.Duration
}

// Scanner takes snapshots.
type Scanner interface {
	Scan() (Snapshot, error)
}

// Func adapts a function into a Scanner (handy for tests).
type Func func() (Snapshot, error)

// Scan calls f.
func (f Func) Scan() (Snapshot, error) { return f() }

// ExeName returns the file name part of a path that may use either slash.
func ExeName(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	return filepath.Base(p)
}
