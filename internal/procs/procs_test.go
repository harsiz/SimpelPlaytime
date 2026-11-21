package procs

import (
	"os"
	"runtime"
	"testing"
)

func TestExeName(t *testing.T) {
	cases := map[string]string{
		`C:\Games\Hades II\Hades2.exe`: "Hades2.exe",
		"/usr/bin/bash":                "bash",
		"plain.exe":                    "plain.exe",
	}
	for in, want := range cases {
		if got := ExeName(in); got != want {
			t.Errorf("ExeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFunc(t *testing.T) {
	f := Func(func() (Snapshot, error) { return Snapshot{ForegroundPID: 7}, nil })
	s, err := f.Scan()
	if err != nil || s.ForegroundPID != 7 {
		t.Fatal("Func adapter broken")
	}
}

func TestRealScannerSeesItself(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("no real scanner on this OS")
	}
	snap, err := New().Scan()
	if err != nil {
		t.Fatal(err)
	}
	me := uint32(os.Getpid())
	for _, p := range snap.Procs {
		if p.PID == me {
			if p.Name == "" {
				t.Error("own process has no name")
			}
			return
		}
	}
	t.Errorf("own pid %d not found in %d processes", me, len(snap.Procs))
}
