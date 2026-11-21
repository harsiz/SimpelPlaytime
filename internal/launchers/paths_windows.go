//go:build windows

package launchers

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func regString(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_32KEY)
	if err != nil {
		k, err = registry.OpenKey(root, path, registry.QUERY_VALUE)
		if err != nil {
			return ""
		}
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return filepath.FromSlash(v)
}

func platformSteamRoots() []string {
	pf86 := os.Getenv("ProgramFiles(x86)")
	if pf86 == "" {
		pf86 = `C:\Program Files (x86)`
	}
	return []string{
		regString(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"),
		regString(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"),
		regString(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath"),
		filepath.Join(pf86, "Steam"),
	}
}

// EpicManifestDir is where the Epic Games Launcher keeps install manifests.
func EpicManifestDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "Epic", "EpicGamesLauncher", "Data", "Manifests")
}
