package gamedb

import (
	"strings"
	"testing"

	"github.com/harsiz/SimpelPlaytime/internal/model"
)

func TestEmbeddedDatabaseParses(t *testing.T) {
	db, err := Default()
	if err != nil {
		t.Fatalf("embedded db does not parse: %v", err)
	}
	if n := db.Count(model.KindGame); n < 300 {
		t.Errorf("expected at least 300 games, got %d", n)
	}
	if n := db.Count(model.KindApp); n < 50 {
		t.Errorf("expected at least 50 apps, got %d", n)
	}
}

func TestNoConflictingPlainExes(t *testing.T) {
	db := MustDefault()
	owner := map[string]string{}
	for _, e := range db.All() {
		for _, r := range e.Rules {
			if r.Conditional() || strings.Contains(r.Exe, "*") {
				continue
			}
			if prev, ok := owner[r.Exe]; ok && prev != e.ID {
				t.Errorf("exe %q is a plain rule for both %q and %q", r.Exe, prev, e.ID)
			}
			owner[r.Exe] = e.ID
		}
	}
}

func TestKnownMatches(t *testing.T) {
	db := MustDefault()
	cases := []struct {
		exe, path, title, want string
	}{
		{"osu!.exe", `C:\Users\me\AppData\Local\osu!\osu!.exe`, "", "osu"},
		{"osu!.exe", `C:\Users\me\AppData\Local\osulazer\current\osu!.exe`, "", "osu"},
		{"OSU!.EXE", "", "", "osu"},
		{"javaw.exe", `C:\jdk\bin\javaw.exe`, "Minecraft* 1.20.1 - Singleplayer", "minecraft"},
		{"javaw.exe", "", "Lunar Client (1.8.9-abc)", "lunar-client"},
		{"Minecraft.Windows.exe", "", "", "minecraft"},
		{"RobloxPlayerBeta.exe", "", "Roblox", "roblox"},
		{"GTA5_Enhanced.exe", "", "", "grand-theft-auto-v"},
		{"GTA5.exe", "", "", "grand-theft-auto-v"},
		{"RainbowSix_Vulkan.exe", "", "", "tom-clancys-rainbow-six-siege"},
		{"Cyberpunk2077.exe", "", "", "cyberpunk-2077"},
		{"hl2.exe", `D:\SteamLibrary\steamapps\common\Half-Life 2\hl2.exe`, "", "half-life-2"},
		{"hl2.exe", `D:\SteamLibrary\steamapps\common\GarrysMod\hl2.exe`, "", "garrys-mod"},
		{"hl2.exe", "/home/me/.steam/steam/steamapps/common/Portal/hl2.exe", "", "portal"},
		{"PathOfExile.exe", `C:\Games\Path of Exile 2\PathOfExile.exe`, "", "path-of-exile-2"},
		{"PathOfExile.exe", `C:\Games\Path of Exile\PathOfExile.exe`, "", "path-of-exile"},
		{"Hades2.exe", "", "", "hades-ii"},
		{"Photoshop.exe", "", "", "adobe-photoshop"},
		{"opera.exe", `C:\Users\me\AppData\Local\Programs\Opera GX\opera.exe`, "", "opera-gx"},
		{"opera.exe", `C:\Users\me\AppData\Local\Programs\Opera\opera.exe`, "", "opera"},
		{"godot_v4.3-stable_win64.exe", "", "", "godot-engine"},
	}
	for _, c := range cases {
		e := db.Match(c.exe, c.path, c.title)
		if e == nil {
			t.Errorf("Match(%q, %q, %q) = nil, want %q", c.exe, c.path, c.title, c.want)
			continue
		}
		if e.ID != c.want {
			t.Errorf("Match(%q, %q, %q) = %q, want %q", c.exe, c.path, c.title, e.ID, c.want)
		}
	}
}

func TestNoMatch(t *testing.T) {
	db := MustDefault()
	cases := []struct{ exe, path, title string }{
		{"javaw.exe", "", "Some Random Java App"},
		{"javaw.exe", "", ""},
		{"hl2.exe", `C:\Random\hl2.exe`, ""},
		{"definitelynotagame.exe", "", ""},
		{"ShooterGame.exe", `C:\UE\Samples\ShooterGame.exe`, ""},
	}
	for _, c := range cases {
		if e := db.Match(c.exe, c.path, c.title); e != nil {
			t.Errorf("Match(%q, %q, %q) = %q, want nil", c.exe, c.path, c.title, e.ID)
		}
	}
}

func TestNeedsTitle(t *testing.T) {
	db := MustDefault()
	if !db.NeedsTitle("javaw.exe") {
		t.Error("javaw.exe needs a title for Minecraft detection")
	}
	if db.NeedsTitle("cs2.exe") {
		t.Error("cs2.exe should not need a title")
	}
}

func TestSteamLookup(t *testing.T) {
	db := MustDefault()
	e := db.BySteamID(1145350)
	if e == nil || e.Name != "Hades II" {
		t.Fatalf("BySteamID(1145350) = %+v", e)
	}
	if db.Get("hades-ii") != e {
		t.Error("Get should return the same entry")
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"NoExes |",
		"Bad | thing.txt",
		"Bad | a.exe | steam=abc",
		"Bad | a.exe{title:(}",
		"Bad | a.exe{nope:1}",
		"Bad | a.exe{title:x",
		"@weird",
		"Dup | a.exe\nDup | b.exe",
		"One | a.exe | steam=5\nTwo | b.exe | steam=5",
	}
	for _, src := range bad {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("Parse(%q) should fail", src)
		}
	}
}

func TestParseSmall(t *testing.T) {
	src := `
# comment
@games
My Game | MyGame.exe, launcher.exe{path:\my game\;title:^my} | steam=42 | wiki=My Game (video game)
@apps
Tool | tool*.exe
`
	db, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g := db.Get("my-game")
	if g == nil || g.Kind != model.KindGame || g.SteamID != 42 || g.Wiki != "My Game (video game)" {
		t.Fatalf("bad entry %+v", g)
	}
	if len(g.Exes()) != 2 {
		t.Errorf("Exes() = %v", g.Exes())
	}
	if db.Match("launcher.exe", `C:\My Game\launcher.exe`, "My Game Launcher") != g {
		t.Error("conditional rule should match")
	}
	if db.Match("launcher.exe", `C:\My Game\launcher.exe`, "Other") != nil {
		t.Error("title condition should block")
	}
	if e := db.Match("tool_v2.exe", "", ""); e == nil || e.Kind != model.KindApp {
		t.Error("glob app should match")
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Garry's Mod":          "garrys-mod",
		"osu!":                 "osu",
		"Grand Theft Auto V":   "grand-theft-auto-v",
		"  Spaces  Here ":      "spaces-here",
		"R.E.P.O.":             "repo",
		"NieR:Automata":        "nier-automata",
		"Ratchet & Clank":      "ratchet-clank",
		"Guilty Gear -Strive-": "guilty-gear-strive",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
