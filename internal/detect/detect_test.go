package detect

import (
	"testing"

	"github.com/harsiz/SimpelPlaytime/internal/launchers"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
)

type fakeLaunchers map[string]launchers.Game

func (f fakeLaunchers) Lookup(p string) (launchers.Game, bool) {
	g, ok := f[p]
	return g, ok
}

func newDetector(mode string, entries []model.Entry, lx LauncherIndex) *Detector {
	st := settings.Defaults()
	st.TrackMode = mode
	st.Ignored = []string{"Annoying.exe"}
	return New(Options{
		Launchers:  lx,
		Settings:   func() settings.Settings { return st },
		Entries:    func() []model.Entry { return entries },
		SystemDirs: []string{`c:\windows\`},
		SelfPID:    42,
		ExeInfo: func(p string) (string, string) {
			switch p {
			case `C:\Program Files\Adobe\Photoshop 2025\Photoshop.exe`:
				return "Adobe Photoshop", "Adobe Photoshop 2025"
			case `C:\Tools\weird.exe`:
				return "Weird Tool Pro", ""
			case `D:\SteamLibrary\steamapps\common\Some Game\Binaries\SomeGame-Win64-Shipping.exe`:
				return "UnrealGame", "UnrealGame"
			}
			return "", ""
		},
	})
}

func TestDatabaseGame(t *testing.T) {
	d := newDetector(settings.TrackWindowed, nil, nil)
	id, ok := d.Identify(procs.Process{PID: 1, Name: "Hades2.exe", Path: `D:\Games\Hades II\Hades2.exe`})
	if !ok || id.Key != "db:hades-ii" || id.Kind != model.KindGame || id.SteamAppID != 1145350 || id.Source != model.SourceDatabase {
		t.Fatalf("got %+v %v", id, ok)
	}
	// games are tracked even without a window and even in games only mode
	d = newDetector(settings.TrackGames, nil, nil)
	if _, ok := d.Identify(procs.Process{PID: 1, Name: "cs2.exe"}); !ok {
		t.Error("games mode should still track games")
	}
}

func TestDatabaseAppNeedsWindow(t *testing.T) {
	d := newDetector(settings.TrackWindowed, nil, nil)
	if _, ok := d.Identify(procs.Process{PID: 1, Name: "Discord.exe", HasWindow: false}); ok {
		t.Error("Discord in the tray (no window) should not count")
	}
	id, ok := d.Identify(procs.Process{PID: 1, Name: "Discord.exe", HasWindow: true})
	if !ok || id.Key != "db:discord" || id.Kind != model.KindApp {
		t.Errorf("got %+v %v", id, ok)
	}
	d = newDetector(settings.TrackGames, nil, nil)
	if _, ok := d.Identify(procs.Process{PID: 1, Name: "Discord.exe", HasWindow: true}); ok {
		t.Error("games mode should skip apps")
	}
}

func TestIgnoredAndSelf(t *testing.T) {
	d := newDetector(settings.TrackAll, nil, nil)
	for _, p := range []procs.Process{
		{PID: 42, Name: "whatever.exe", HasWindow: true},
		{PID: 1, Name: "explorer.exe", HasWindow: true},
		{PID: 1, Name: "annoying.exe", HasWindow: true},
		{PID: 1, Name: "notepad.exe", Path: `C:\Windows\System32\notepad.exe`, HasWindow: true},
		{PID: 1, Name: "", Path: ""},
	} {
		if id, ok := d.Identify(p); ok {
			t.Errorf("%+v should be ignored, got %+v", p, id)
		}
	}
}

func TestCustomRulesWin(t *testing.T) {
	entries := []model.Entry{
		{Key: "custom:my-osu", Name: "My osu", Kind: model.KindGame, Matchers: []model.Matcher{{Type: model.MatchExe, Value: "osu!.exe"}}},
		{Key: "custom:folder", Kind: model.KindGame, Matchers: []model.Matcher{{Type: model.MatchFolder, Value: `E:\Emus\`}}},
		{Key: "custom:exact", Kind: model.KindGame, Matchers: []model.Matcher{{Type: model.MatchPath, Value: `E:\Emus\special.exe`}}},
		{Key: "custom:title", Kind: model.KindGame, Matchers: []model.Matcher{{Type: model.MatchExe, Value: "javaw.exe", Title: "^Modpack"}}},
	}
	d := newDetector(settings.TrackWindowed, entries, nil)
	cases := map[string]procs.Process{
		"custom:my-osu": {PID: 1, Name: "osu!.exe"},
		"custom:folder": {PID: 1, Name: "x.exe", Path: `e:\emus\sub\x.exe`},
		"custom:exact":  {PID: 1, Name: "special.exe", Path: `E:\Emus\special.exe`},
		"custom:title":  {PID: 1, Name: "javaw.exe", Title: "Modpack Cool 1.2"},
		"db:minecraft":  {PID: 1, Name: "javaw.exe", Title: "Minecraft 1.21"},
	}
	for want, p := range cases {
		id, ok := d.Identify(p)
		if !ok || id.Key != want {
			t.Errorf("Identify(%+v) = %+v %v, want %s", p, id, ok, want)
		}
	}
	// the title is part of the cache key for title sensitive exes
	if id, _ := d.Identify(procs.Process{PID: 1, Name: "javaw.exe", Title: "Modpack Cool 1.2"}); id.Key != "custom:title" {
		t.Errorf("cached title lookup = %+v", id)
	}
}

func TestLauncherGames(t *testing.T) {
	lx := fakeLaunchers{
		`D:\SteamLibrary\steamapps\common\Tiny\tiny.exe`:   {Store: "steam", ID: "999", SteamAppID: 999, Name: "Tiny Indie"},
		`D:\SteamLibrary\steamapps\common\Hades\Hades.exe`: {Store: "steam", ID: "1145360", SteamAppID: 1145360, Name: "Hades"},
		`C:\Epic\Cool\Cool.exe`:                            {Store: "epic", ID: "CoolApp", Name: "Cool Game"},
	}
	d := newDetector(settings.TrackWindowed, nil, lx)
	id, ok := d.Identify(procs.Process{PID: 1, Name: "tiny.exe", Path: `D:\SteamLibrary\steamapps\common\Tiny\tiny.exe`})
	if !ok || id.Key != "steam:999" || id.Name != "Tiny Indie" || id.SteamAppID != 999 || id.Source != model.SourceSteam {
		t.Errorf("steam game = %+v %v", id, ok)
	}
	id, ok = d.Identify(procs.Process{PID: 1, Name: "Cool.exe", Path: `C:\Epic\Cool\Cool.exe`})
	if !ok || id.Key != "epic:coolapp" || id.Kind != model.KindGame {
		t.Errorf("epic game = %+v %v", id, ok)
	}
	// helper exes inside a launcher folder are skipped
	lx[`D:\SteamLibrary\steamapps\common\Tiny\UnityCrashHandler64.exe`] = launchers.Game{Store: "steam", SteamAppID: 999}
	if _, ok := d.Identify(procs.Process{PID: 1, Name: "UnityCrashHandler64.exe", Path: `D:\SteamLibrary\steamapps\common\Tiny\UnityCrashHandler64.exe`}); ok {
		t.Error("crash handler should be ignored")
	}
}

func TestHeuristicGameFolder(t *testing.T) {
	d := newDetector(settings.TrackGames, nil, nil)
	p := procs.Process{PID: 1, Name: "SomeGame-Win64-Shipping.exe", Path: `D:\SteamLibrary\steamapps\common\Some Game\Binaries\SomeGame-Win64-Shipping.exe`}
	id, ok := d.Identify(p)
	if !ok || id.Key != "game:some-game" || id.Name != "Some Game" || id.Kind != model.KindGame {
		t.Errorf("heuristic = %+v %v", id, ok)
	}
	p = procs.Process{PID: 1, Name: "vc_redist.x64.exe", Path: `D:\SteamLibrary\steamapps\common\Some Game\_CommonRedist\vc_redist.x64.exe`}
	if _, ok := d.Identify(p); ok {
		t.Error("redist installer should be ignored")
	}
}

func TestAutoApps(t *testing.T) {
	d := newDetector(settings.TrackWindowed, nil, nil)
	id, ok := d.Identify(procs.Process{PID: 1, Name: "weird.exe", Path: `C:\Tools\weird.exe`, HasWindow: true})
	if !ok || id.Key != "exe:weird.exe" || id.Name != "Weird Tool Pro" || id.Kind != model.KindApp || id.Source != model.SourceAuto {
		t.Errorf("auto app = %+v %v", id, ok)
	}
	if _, ok := d.Identify(procs.Process{PID: 1, Name: "background.exe", Path: `C:\Tools\background.exe`}); ok {
		t.Error("windowless app should not count in windowed mode")
	}
	d = newDetector(settings.TrackAll, nil, nil)
	if id, ok := d.Identify(procs.Process{PID: 1, Name: "background.exe", Path: `C:\Tools\background.exe`}); !ok || id.Name != "Background" {
		t.Errorf("all mode should track everything: %+v %v", id, ok)
	}
	// db apps get their nice name
	d = newDetector(settings.TrackWindowed, nil, nil)
	id, _ = d.Identify(procs.Process{PID: 1, Name: "Photoshop.exe", Path: `C:\Program Files\Adobe\Photoshop 2025\Photoshop.exe`, HasWindow: true})
	if id.Name != "Adobe Photoshop" || id.Key != "db:adobe-photoshop" {
		t.Errorf("photoshop = %+v", id)
	}
}

func TestAppKey(t *testing.T) {
	cases := []struct{ name, path, want string }{
		{"code.exe", `c:\users\me\appdata\local\programs\microsoft vs code\code.exe`, "code.exe"},
		{"launcher.exe", `c:\games\cool thing\launcher.exe`, "launcher.exe@cool thing"},
		{"java.exe", `c:\program files\temurin\bin\java.exe`, "java.exe@temurin"},
		{"launcher.exe", "", "launcher.exe"},
	}
	for _, c := range cases {
		if got := AppKey(c.name, c.path); got != c.want {
			t.Errorf("AppKey(%q, %q) = %q, want %q", c.name, c.path, got, c.want)
		}
	}
}

func TestGameFolder(t *testing.T) {
	cases := []struct {
		path, want string
		ok         bool
	}{
		{`D:\SteamLibrary\steamapps\common\Hades II\Hades2.exe`, "Hades II", true},
		{`C:\Program Files\Epic Games\Fortnite\FortniteGame\Binaries\Win64\x.exe`, "Fortnite", true},
		{`C:\Program Files (x86)\Epic Games\Launcher\Portal\Binaries\Win64\EpicGamesLauncher.exe`, "", false},
		{`D:\Games\Celeste\Celeste.exe`, "Celeste", true},
		{`C:\Users\me\Games\Thing\thing.exe`, "Thing", true},
		{`C:\Program Files\Some\games\x\x.exe`, "", false},
		{`/home/me/.steam/steam/steamapps/common/Terraria/Terraria.bin.x86_64`, "Terraria", true},
		{`D:\SteamLibrary\steamapps\common\loose.exe`, "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := GameFolder(c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("GameFolder(%q) = %q %v, want %q %v", c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestPrettyExeName(t *testing.T) {
	if got := PrettyExeName("my_cool-app.exe"); got != "My cool app" {
		t.Errorf("got %q", got)
	}
}

func TestInvalidateRebuildsCustom(t *testing.T) {
	var entries []model.Entry
	d := newDetector(settings.TrackWindowed, nil, nil)
	d.opts.Entries = func() []model.Entry { return entries }
	p := procs.Process{PID: 1, Name: "thing.exe", Path: `C:\X\thing.exe`, HasWindow: true}
	if id, _ := d.Identify(p); id.Key != "exe:thing.exe" {
		t.Fatalf("before = %+v", id)
	}
	entries = []model.Entry{{Key: "custom:thing", Kind: model.KindGame, Matchers: []model.Matcher{{Type: model.MatchExe, Value: "THING.exe"}}}}
	d.Invalidate()
	if id, _ := d.Identify(p); id.Key != "custom:thing" {
		t.Errorf("after invalidate = %+v", id)
	}
}
