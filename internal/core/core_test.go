package core

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/launchers"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
)

type testCore struct {
	*Core
	snap procs.Snapshot
	now  time.Time
}

func newCore(t *testing.T, ix *launchers.Index) *testCore {
	t.Helper()
	tc := &testCore{now: time.Date(2026, 6, 1, 18, 0, 0, 0, time.UTC)}
	if ix == nil {
		ix = launchers.NewIndex(nil, "")
	}
	c, err := New(Options{
		DataDir:   t.TempDir(),
		Version:   "test",
		Scanner:   procs.Func(func() (procs.Snapshot, error) { return tc.snap, nil }),
		Launchers: ix,
		NoArt:     true,
		Now:       func() time.Time { return tc.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	tc.Core = c
	return tc
}

func (tc *testCore) tick(t *testing.T, d time.Duration, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		tc.now = tc.now.Add(d)
		if err := tc.Tracker.Tick(); err != nil {
			t.Fatal(err)
		}
	}
}

func touch(t *testing.T, p string, size int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTrackExeCustom(t *testing.T) {
	tc := newCore(t, nil)
	p := filepath.Join(t.TempDir(), "MyIndie.exe")
	touch(t, p, 10)
	e, err := tc.TrackExe(p, "My Indie", model.KindGame, MatchByName)
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != "custom:my-indie" || e.Kind != model.KindGame || len(e.Matchers) != 1 || e.Matchers[0].Value != "MyIndie.exe" {
		t.Fatalf("entry = %+v", e)
	}
	// the detector now maps the exe to the entry, wherever it runs from
	id, ok := tc.Detector.Identify(procs.Process{Name: "myindie.exe", Path: `D:\Other\MyIndie.exe`})
	if !ok || id.Key != e.Key {
		t.Errorf("identify = %+v %v", id, ok)
	}
	// tracking the same name again makes a new unique key
	e2, _ := tc.TrackExe(filepath.Join(t.TempDir(), "Other.exe"), "My Indie", model.KindApp, MatchByPath)
	if e2.Key != "custom:my-indie-2" || e2.Matchers[0].Type != model.MatchPath {
		t.Errorf("second = %+v", e2)
	}
}

func TestTrackExeKnownGameUpdatesDatabaseEntry(t *testing.T) {
	tc := newCore(t, nil)
	e, err := tc.TrackExe(`C:\Games\Hades II\Hades2.exe`, "Hades 2 baby", model.KindGame, MatchByName)
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != "db:hades-ii" || e.Name != "Hades 2 baby" || e.SteamAppID != 1145350 {
		t.Errorf("entry = %+v", e)
	}
}

func TestTrackFolderAndLiveTotals(t *testing.T) {
	tc := newCore(t, nil)
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "bin", "game.exe"), 5000)
	touch(t, filepath.Join(dir, "unins000.exe"), 9000)
	e, err := tc.TrackFolder(dir, "", model.KindGame)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != filepath.Base(dir) || !strings.HasSuffix(e.ExePath, "game.exe") {
		t.Errorf("entry = %+v", e)
	}
	tc.snap = procs.Snapshot{Procs: []procs.Process{{PID: 5, Name: "game.exe", Path: filepath.Join(dir, "bin", "game.exe")}}}
	tc.tick(t, 5*time.Second, 13)
	tot := tc.TotalFor(e.Key)
	if tot.Secs != 60 || tot.Sessions != 1 {
		t.Errorf("live totals = %+v", tot)
	}
	if n := len(tc.Sessions(true)); n != 1 {
		t.Errorf("sessions with live = %d", n)
	}
	if n := len(tc.Sessions(false)); n != 0 {
		t.Errorf("stored sessions = %d", n)
	}
}

func TestTrackShortcutSteam(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "steamapps", "common", "Tiny"), 0o755)
	touch(t, filepath.Join(root, "steamapps", "common", "Tiny", "Tiny.exe"), 100)
	os.WriteFile(filepath.Join(root, "steamapps", "appmanifest_777.acf"), []byte(`"AppState" { "appid" "777" "name" "Tiny Game" "installdir" "Tiny" }`), 0o644)
	tc := newCore(t, launchers.NewIndex([]string{root}, ""))
	lnk := filepath.Join(t.TempDir(), "Tiny.url")
	os.WriteFile(lnk, []byte("[InternetShortcut]\nURL=steam://rungameid/777\n"), 0o644)
	info, err := tc.InspectShortcut(lnk)
	if err != nil || info.Name != "Tiny Game" {
		t.Fatalf("inspect = %+v %v", info, err)
	}
	e, err := tc.TrackShortcut(lnk, "", model.KindGame)
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != "steam:777" || e.SteamAppID != 777 || len(e.Matchers) != 1 || e.Matchers[0].Type != model.MatchFolder {
		t.Errorf("entry = %+v", e)
	}
	missing := filepath.Join(t.TempDir(), "Nope.url")
	os.WriteFile(missing, []byte("[InternetShortcut]\nURL=steam://rungameid/1\n"), 0o644)
	if _, err := tc.TrackShortcut(missing, "", model.KindGame); err == nil {
		t.Error("uninstalled steam game should fail")
	}
}

func TestManualTimeDeleteAndIgnore(t *testing.T) {
	tc := newCore(t, nil)
	e, _ := tc.TrackExe(filepath.Join(t.TempDir(), "x.exe"), "X", model.KindGame, MatchByName)
	if err := tc.AddManualTime(e.Key, time.Time{}, 90*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := tc.AddManualTime(e.Key, time.Time{}, -time.Minute); err == nil {
		t.Error("negative manual time should fail")
	}
	if tot := tc.TotalFor(e.Key); tot.Secs != 5400 {
		t.Errorf("total = %+v", tot)
	}
	if err := tc.DeleteEntry(e.Key, true); err != nil {
		t.Fatal(err)
	}
	if !tc.Settings.Get().IsIgnored("x.exe") {
		t.Error("exe should be ignored after delete")
	}
	if len(tc.Store.Sessions()) != 0 {
		t.Error("sessions should be gone")
	}
	// tracking it again removes it from the ignore list
	tc.TrackExe(filepath.Join(t.TempDir(), "x.exe"), "X", model.KindGame, MatchByName)
	if tc.Settings.Get().IsIgnored("x.exe") {
		t.Error("re-tracking should un-ignore")
	}
}

func TestEditsAndFilters(t *testing.T) {
	tc := newCore(t, nil)
	g, _ := tc.TrackExe(filepath.Join(t.TempDir(), "g.exe"), "G", model.KindGame, MatchByName)
	a, _ := tc.TrackExe(filepath.Join(t.TempDir(), "a.exe"), "A", model.KindApp, MatchByName)
	tc.AddManualTime(g.Key, time.Time{}, time.Hour)
	tc.AddManualTime(a.Key, time.Time{}, 2*time.Hour)
	if err := tc.Rename(g.Key, "  "); err == nil {
		t.Error("empty rename should fail")
	}
	tc.Rename(g.Key, "Game!")
	tc.SetFavorite(g.Key, true)
	tc.SetHidden(a.Key, true)
	tc.SetNotes(g.Key, "gg")
	got, _ := tc.Store.Entry(g.Key)
	if got.Name != "Game!" || !got.Favorite || got.Notes != "gg" {
		t.Errorf("edits = %+v", got)
	}
	games := tc.KindFilter("game")
	if !games(g.Key) || games(a.Key) || tc.KindFilter("all") != nil {
		t.Error("kind filter wrong")
	}
	if err := tc.SetKind(a.Key, model.KindGame); err != nil {
		t.Fatal(err)
	}
	if !tc.KindFilter("game")(a.Key) {
		t.Error("kind change not applied")
	}
	if err := tc.Merge(a.Key, g.Key); err != nil {
		t.Fatal(err)
	}
	if tot := tc.TotalFor(g.Key); tot.Secs != 3*3600 {
		t.Errorf("merged total = %+v", tot)
	}
	if err := tc.SetSteamAppID(g.Key, 620); err != nil {
		t.Fatal(err)
	}
	if e, _ := tc.Store.Entry(g.Key); e.SteamAppID != 620 {
		t.Error("steam id not set")
	}
}

func TestCustomPosterAndIcon(t *testing.T) {
	tc := newCore(t, nil)
	e, _ := tc.TrackExe(filepath.Join(t.TempDir(), "p.exe"), "P", model.KindGame, MatchByName)
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	p := filepath.Join(t.TempDir(), "cover.png")
	f, _ := os.Create(p)
	png.Encode(f, img)
	f.Close()
	if err := tc.SetCustomPoster(e.Key, p); err != nil {
		t.Fatal(err)
	}
	got, _ := tc.Store.Entry(e.Key)
	if !got.CustomPoster || got.PosterFile == "" {
		t.Fatalf("poster = %+v", got)
	}
	if _, err := os.Stat(tc.Store.PosterPath(got)); err != nil {
		t.Error("poster file missing")
	}
	if err := tc.SetCustomIcon(e.Key, p); err != nil {
		t.Fatal(err)
	}
	got, _ = tc.Store.Entry(e.Key)
	if _, err := os.Stat(tc.Store.IconPath(got)); err != nil {
		t.Error("icon file missing")
	}
	if err := tc.SetCustomPoster(e.Key, filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Error("missing poster should fail")
	}
	_ = color.White
}

func TestRunningCandidates(t *testing.T) {
	tc := newCore(t, nil)
	tc.snap = procs.Snapshot{Procs: []procs.Process{
		{PID: 1, Name: "cs2.exe", Path: `C:\cs2\cs2.exe`},
		{PID: 2, Name: "tray.exe", Path: `C:\tray.exe`},
		{PID: 3, Name: "Editor.exe", Path: `C:\Editor.exe`, HasWindow: true},
		{PID: 4, Name: "Editor.exe", Path: `C:\Editor.exe`, HasWindow: true},
		{PID: 5, Name: "explorer.exe", Path: `C:\Windows\explorer.exe`, HasWindow: true},
	}}
	tc.tick(t, time.Second, 1)
	c := tc.RunningCandidates()
	if len(c) != 2 || c[0].Process.Name != "Editor.exe" || c[1].Process.Name != "cs2.exe" {
		t.Errorf("candidates = %+v", c)
	}
}

func TestStartStop(t *testing.T) {
	tc := newCore(t, nil)
	tc.Settings.Update(func(s *settings.Settings) { s.PollSeconds = 1 })
	tc.Start()
	time.Sleep(30 * time.Millisecond)
	tc.Stop()
	tc.Stop() // twice is fine
}
