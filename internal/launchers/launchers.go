package launchers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Game is a game installed through a launcher.
type Game struct {
	Store      string `json:"store"` // "steam" or "epic"
	ID         string `json:"id"`    // steam app id or epic AppName
	SteamAppID int    `json:"steamAppId,omitempty"`
	Name       string `json:"name"`
	InstallDir string `json:"installDir"`
	// LaunchExe is the main exe relative to InstallDir (Epic only).
	LaunchExe string `json:"launchExe,omitempty"`
}

// Index is a cached view of every launcher manifest on the machine.
type Index struct {
	steamRoots []string
	epicDir    string
	ttl        time.Duration
	now        func() time.Time

	mu      sync.Mutex
	loaded  time.Time
	games   []Game // longest install dir first
	bySteam map[int]Game
	byEpic  map[string]Game
}

// NewIndex makes an index over the given Steam roots and Epic manifest dir.
func NewIndex(steamRoots []string, epicManifestDir string) *Index {
	return &Index{steamRoots: steamRoots, epicDir: epicManifestDir, ttl: 5 * time.Minute, now: time.Now}
}

// DefaultIndex uses the standard install locations of this OS.
func DefaultIndex() *Index {
	return NewIndex(SteamRoots(), EpicManifestDir())
}

func norm(p string) string {
	p = strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	if !strings.HasSuffix(p, `\`) {
		p += `\`
	}
	return p
}

// Refresh re-reads every manifest.
func (ix *Index) Refresh() {
	var games []Game
	seenLib := map[string]bool{}
	for _, root := range ix.steamRoots {
		for _, lib := range SteamLibraries(root) {
			k := norm(lib)
			if seenLib[k] {
				continue
			}
			seenLib[k] = true
			games = append(games, SteamApps(lib)...)
		}
	}
	if ix.epicDir != "" {
		games = append(games, EpicGames(ix.epicDir)...)
	}
	sort.SliceStable(games, func(i, j int) bool { return len(games[i].InstallDir) > len(games[j].InstallDir) })
	bySteam := map[int]Game{}
	byEpic := map[string]Game{}
	for _, g := range games {
		if g.SteamAppID != 0 {
			bySteam[g.SteamAppID] = g
		}
		if g.Store == "epic" {
			byEpic[strings.ToLower(g.ID)] = g
		}
	}
	ix.mu.Lock()
	ix.games, ix.bySteam, ix.byEpic = games, bySteam, byEpic
	ix.loaded = ix.now()
	ix.mu.Unlock()
}

func (ix *Index) ensure() {
	ix.mu.Lock()
	stale := ix.loaded.IsZero() || ix.now().Sub(ix.loaded) > ix.ttl
	ix.mu.Unlock()
	if stale {
		ix.Refresh()
	}
}

// Lookup finds the launcher game whose install folder contains exePath.
func (ix *Index) Lookup(exePath string) (Game, bool) {
	if exePath == "" {
		return Game{}, false
	}
	ix.ensure()
	p := strings.ToLower(strings.ReplaceAll(exePath, "/", `\`))
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, g := range ix.games {
		if g.InstallDir == "" {
			continue
		}
		if strings.HasPrefix(p, norm(g.InstallDir)) {
			return g, true
		}
	}
	return Game{}, false
}

// SteamApp returns an installed Steam app by id.
func (ix *Index) SteamApp(id int) (Game, bool) {
	ix.ensure()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	g, ok := ix.bySteam[id]
	return g, ok
}

// EpicApp returns an installed Epic game by AppName.
func (ix *Index) EpicApp(appName string) (Game, bool) {
	ix.ensure()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	g, ok := ix.byEpic[strings.ToLower(appName)]
	return g, ok
}

// All returns every known launcher game.
func (ix *Index) All() []Game {
	ix.ensure()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return append([]Game(nil), ix.games...)
}

// SteamLibraries returns every Steam library folder of a Steam install
// (including the install itself).
func SteamLibraries(steamRoot string) []string {
	if steamRoot == "" {
		return nil
	}
	libs := []string{steamRoot}
	for _, name := range []string{"libraryfolders.vdf"} {
		f, err := os.Open(filepath.Join(steamRoot, "steamapps", name))
		if err != nil {
			f, err = os.Open(filepath.Join(steamRoot, "config", name))
			if err != nil {
				continue
			}
		}
		kv, err := ParseVDF(f)
		f.Close()
		if err != nil {
			continue
		}
		lf := kv.Get("libraryfolders")
		if lf == nil {
			continue
		}
		for _, c := range lf.Children {
			if p := c.String("path"); p != "" {
				libs = append(libs, p)
			} else if c.Value != "" && strings.ContainsAny(c.Value, `:/\`) {
				libs = append(libs, c.Value) // very old format: "1" "D:\\SteamLibrary"
			}
		}
	}
	return libs
}

// SteamApps reads every appmanifest_*.acf of one Steam library.
func SteamApps(library string) []Game {
	dir := filepath.Join(library, "steamapps")
	matches, _ := filepath.Glob(filepath.Join(dir, "appmanifest_*.acf"))
	var out []Game
	for _, m := range matches {
		f, err := os.Open(m)
		if err != nil {
			continue
		}
		kv, err := ParseVDF(f)
		f.Close()
		if err != nil {
			continue
		}
		st := kv.Get("AppState")
		if st == nil {
			continue
		}
		id, _ := strconv.Atoi(st.String("appid"))
		installdir := st.String("installdir")
		if id == 0 || installdir == "" {
			continue
		}
		out = append(out, Game{
			Store:      "steam",
			ID:         strconv.Itoa(id),
			SteamAppID: id,
			Name:       st.String("name"),
			InstallDir: filepath.Join(dir, "common", installdir),
		})
	}
	return out
}

type epicManifest struct {
	AppName          string `json:"AppName"`
	DisplayName      string `json:"DisplayName"`
	InstallLocation  string `json:"InstallLocation"`
	LaunchExecutable string `json:"LaunchExecutable"`
	CatalogNamespace string `json:"CatalogNamespace"`
	AppCategories    []string
}

// EpicGames reads the Epic Games Launcher *.item manifests.
func EpicGames(dir string) []Game {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.item"))
	var out []Game
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var em epicManifest
		if err := json.Unmarshal(data, &em); err != nil || em.AppName == "" || em.InstallLocation == "" {
			continue
		}
		isGame := len(em.AppCategories) == 0
		for _, c := range em.AppCategories {
			if strings.EqualFold(c, "games") {
				isGame = true
			}
		}
		if !isGame {
			continue
		}
		name := em.DisplayName
		if name == "" {
			name = em.AppName
		}
		out = append(out, Game{
			Store:      "epic",
			ID:         em.AppName,
			Name:       name,
			InstallDir: em.InstallLocation,
			LaunchExe:  em.LaunchExecutable,
		})
	}
	return out
}

// SteamRoots returns the likely Steam install folders of this machine.
func SteamRoots() []string {
	roots := platformSteamRoots()
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		if r == "" {
			continue
		}
		k := norm(r)
		if seen[k] {
			continue
		}
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}
