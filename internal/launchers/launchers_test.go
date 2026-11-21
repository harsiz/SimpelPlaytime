package launchers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const libraryVDF = `"libraryfolders"
{
	"0"
	{
		"path"		"%s"
		"label"		""
		"apps"
		{
			"1145350"		"123"
		}
	}
	"1"
	{
		"path"		"%s"
	}
}
`

const manifest = `"AppState"
{
	"appid"		"%s"
	"universe"		"1"
	"name"		"%s"
	"installdir"		"%s"
	// comment
	"UserConfig"
	{
		"language"		"english"
	}
}
`

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func esc(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

func TestParseVDF(t *testing.T) {
	kv, err := ParseVDF(strings.NewReader(`"a" { "b" "c\"d" "e" { "f" "g" } bare value } // tail`))
	if err != nil {
		t.Fatal(err)
	}
	a := kv.Get("A")
	if a == nil || a.String("b") != `c"d` || a.Get("e").String("f") != "g" || a.String("bare") != "value" {
		t.Fatalf("bad parse: %+v", kv)
	}
	for _, bad := range []string{`"a" {`, `}`, `{ }`, `"a" "unterminated`} {
		if _, err := ParseVDF(strings.NewReader(bad)); err == nil {
			t.Errorf("ParseVDF(%q) should fail", bad)
		}
	}
}

func TestSteamIndex(t *testing.T) {
	root := t.TempDir()
	lib2 := t.TempDir()
	write(t, filepath.Join(root, "steamapps", "libraryfolders.vdf"), strings.ReplaceAll(
		strings.Replace(strings.Replace(libraryVDF, "%s", esc(root), 1), "%s", esc(lib2), 1), "\t", " "))
	write(t, filepath.Join(root, "steamapps", "appmanifest_1145350.acf"), sprintf(manifest, "1145350", "Hades II", "Hades II"))
	write(t, filepath.Join(lib2, "steamapps", "appmanifest_999.acf"), sprintf(manifest, "999", "Tiny Indie", "TinyIndie"))
	write(t, filepath.Join(lib2, "steamapps", "appmanifest_bad.acf"), "garbage {")

	ix := NewIndex([]string{root, root}, "")
	g, ok := ix.Lookup(filepath.Join(lib2, "steamapps", "common", "TinyIndie", "bin", "game.exe"))
	if !ok || g.SteamAppID != 999 || g.Name != "Tiny Indie" {
		t.Fatalf("Lookup = %+v %v", g, ok)
	}
	// case and slash insensitive
	up := strings.ToUpper(filepath.Join(root, "steamapps", "common", "Hades II", "Hades2.exe"))
	if g, ok := ix.Lookup(up); !ok || g.SteamAppID != 1145350 {
		t.Errorf("upper case lookup = %+v %v", g, ok)
	}
	if _, ok := ix.Lookup(filepath.Join(root, "steamapps", "common", "Other", "x.exe")); ok {
		t.Error("unknown folder should not match")
	}
	if g, ok := ix.SteamApp(1145350); !ok || g.Name != "Hades II" {
		t.Errorf("SteamApp = %+v", g)
	}
	if n := len(ix.All()); n != 2 {
		t.Errorf("All = %d (dupe roots should be merged)", n)
	}
}

func TestEpicIndex(t *testing.T) {
	dir := t.TempDir()
	inst := filepath.Join(t.TempDir(), "Fortnite")
	write(t, filepath.Join(dir, "a.item"), `{"AppName":"Fortnite","DisplayName":"Fortnite","InstallLocation":"`+esc(inst)+`","LaunchExecutable":"FortniteGame/Binaries/Win64/FortniteLauncher.exe","AppCategories":["public","games","applications"]}`)
	write(t, filepath.Join(dir, "b.item"), `{"AppName":"UE_5","DisplayName":"Unreal Engine","InstallLocation":"C:\\UE","AppCategories":["engines"]}`)
	write(t, filepath.Join(dir, "c.item"), `nope`)
	ix := NewIndex(nil, dir)
	g, ok := ix.Lookup(filepath.Join(inst, "FortniteGame", "Binaries", "Win64", "FortniteClient-Win64-Shipping.exe"))
	if !ok || g.Store != "epic" || g.ID != "Fortnite" {
		t.Fatalf("Lookup = %+v %v", g, ok)
	}
	if _, ok := ix.EpicApp("fortnite"); !ok {
		t.Error("EpicApp should be case insensitive")
	}
	if _, ok := ix.EpicApp("UE_5"); ok {
		t.Error("engines are not games")
	}
}

func sprintf(format string, args ...string) string {
	out := format
	for _, a := range args {
		out = strings.Replace(out, "%s", a, 1)
	}
	return out
}
