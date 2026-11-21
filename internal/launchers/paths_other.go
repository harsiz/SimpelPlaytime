//go:build !windows

package launchers

import (
	"os"
	"path/filepath"
)

func platformSteamRoots() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".local", "share", "Steam"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
		filepath.Join(home, "Library", "Application Support", "Steam"),
	}
}

// EpicManifestDir is where the Epic Games Launcher keeps install manifests
// (there is none outside Windows).
func EpicManifestDir() string { return "" }
