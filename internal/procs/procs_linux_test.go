//go:build linux

package procs

import "testing"

func TestParseStat(t *testing.T) {
	comm, ppid := parseStat("1234 (my (weird) game) S 99 1234 1234 0 -1")
	if comm != "my (weird) game" || ppid != 99 {
		t.Errorf("got %q %d", comm, ppid)
	}
	if c, p := parseStat("garbage"); c != "" || p != 0 {
		t.Error("garbage should give nothing")
	}
}
