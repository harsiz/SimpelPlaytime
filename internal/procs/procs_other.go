//go:build !windows && !linux

package procs

import "time"

type noopScanner struct{}

// New returns a scanner that sees nothing (unsupported OS).
func New() Scanner { return noopScanner{} }

func (noopScanner) Scan() (Snapshot, error) { return Snapshot{Time: time.Now()}, nil }
