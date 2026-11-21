package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefaults(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := m.Get()
	if !s.StartWithWindows || !s.StartMinimized || s.TrackMode != TrackWindowed || s.APIPort != DefaultAPIPort {
		t.Errorf("unexpected defaults: %+v", s)
	}
	if len(s.APIToken) != 32 {
		t.Errorf("token should be 32 hex chars, got %q", s.APIToken)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Errorf("settings file not written: %v", err)
	}
}

func TestUpdatePersistsAndNotifies(t *testing.T) {
	dir := t.TempDir()
	m, _ := Load(dir)
	called := false
	m.OnChange(func(old, new Settings) {
		called = old.PollSeconds == 5 && new.PollSeconds == 10
	})
	if err := m.Update(func(s *Settings) {
		s.PollSeconds = 10
		s.Ignored = []string{"b.exe", " a.exe ", "B.EXE", ""}
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("listener not called with old/new")
	}
	m2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := m2.Get()
	if s.PollSeconds != 10 {
		t.Errorf("PollSeconds = %d", s.PollSeconds)
	}
	if len(s.Ignored) != 2 || s.Ignored[0] != "a.exe" || s.Ignored[1] != "b.exe" {
		t.Errorf("Ignored = %v", s.Ignored)
	}
	if !s.IsIgnored("A.EXE") || s.IsIgnored("c.exe") {
		t.Error("IsIgnored is wrong")
	}
}

func TestNormalizeClamps(t *testing.T) {
	s := Settings{TrackMode: "lol", PollSeconds: 999, IdleMinutes: -3, APIPort: 80, TimeFormat: "x", BreakReminderMinutes: -1}
	s.Normalize()
	if s.TrackMode != TrackWindowed || s.PollSeconds != 60 || s.IdleMinutes != 1 || s.APIPort != DefaultAPIPort || s.TimeFormat != TimeHM || s.BreakReminderMinutes != 0 {
		t.Errorf("bad normalize: %+v", s)
	}
	if s.APIToken == "" {
		t.Error("token should be filled")
	}
}

func TestBrokenFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{nope"), 0o600)
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Get().PollSeconds != 5 {
		t.Error("should fall back to defaults")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.broken")); err != nil {
		t.Error("broken copy should be kept")
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"pollSeconds": 3}`), 0o600)
	m, _ := Load(dir)
	s := m.Get()
	if s.PollSeconds != 3 || !s.DownloadArt || !s.APIEnabled {
		t.Errorf("partial file should keep defaults for missing keys: %+v", s)
	}
}
