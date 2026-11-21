package autostart

import (
	"runtime"
	"testing"
)

func TestCommand(t *testing.T) {
	got := Command(`C:\Apps\Simpel "Playtime"\SimpelPlaytime.exe`)
	want := `"C:\Apps\Simpel Playtime\SimpelPlaytime.exe" --minimized`
	if got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
}

func TestSyncRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only the XDG backend is safe to exercise in tests")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	exe := "/opt/simpel/SimpelPlaytime"
	if on, _ := Enabled(); on {
		t.Fatal("should start disabled")
	}
	if err := Sync(true, exe); err != nil {
		t.Fatal(err)
	}
	on, cmd := Enabled()
	if !on || cmd != Command(exe) {
		t.Errorf("enabled = %v %q", on, cmd)
	}
	// moving the exe re-points the entry
	if err := Sync(true, "/new/place/SimpelPlaytime"); err != nil {
		t.Fatal(err)
	}
	if _, cmd := Enabled(); cmd != Command("/new/place/SimpelPlaytime") {
		t.Errorf("not re-pointed: %q", cmd)
	}
	if err := Sync(false, exe); err != nil {
		t.Fatal(err)
	}
	if on, _ := Enabled(); on {
		t.Error("should be disabled")
	}
	if err := Disable(); err != nil {
		t.Errorf("double disable should be fine: %v", err)
	}
}
