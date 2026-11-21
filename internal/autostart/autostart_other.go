//go:build !windows

package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// desktopFile is the XDG autostart entry (Linux and the BSDs).
func desktopFile() (string, error) {
	if runtime.GOOS == "darwin" {
		return "", errors.New("autostart is not supported on macOS yet")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", "simpelplaytime.desktop"), nil
}

func desktopEntry(exe string) string {
	return "[Desktop Entry]\nType=Application\nName=Simpel Playtime\nExec=" + Command(exe) +
		"\nX-GNOME-Autostart-enabled=true\nNoDisplay=false\nComment=Playtime tracker for games and apps\n"
}

func enable(exe string) error {
	p, err := desktopFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(desktopEntry(exe)), 0o644)
}

func disable() error {
	p, err := desktopFile()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func enabled() (bool, string) {
	p, err := desktopFile()
	if err != nil {
		return false, ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Exec=") {
			return true, strings.TrimPrefix(line, "Exec=")
		}
	}
	return true, ""
}
