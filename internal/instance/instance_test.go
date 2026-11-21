package instance

import (
	"runtime"
	"testing"
	"time"
)

func TestSingleInstanceAndShow(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	shown := make(chan struct{}, 1)
	if err := a.Serve(func() { shown <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if _, err := Acquire(dir); err != ErrAlreadyRunning {
			t.Errorf("second Acquire = %v, want ErrAlreadyRunning", err)
		}
	}
	if err := SignalShow(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("show callback not called")
	}
	a.Release()
	if err := SignalShow(dir); err == nil {
		t.Error("signal after release should fail")
	}
	b, err := Acquire(dir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	b.Release()
}
