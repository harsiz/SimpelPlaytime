package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"github.com/harsiz/SimpelPlaytime/internal/core"
	"github.com/harsiz/SimpelPlaytime/internal/launchers"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
)

// newTestUI builds the whole UI on Fyne's in-memory test driver. Set
// SP_DEMO to a data folder to render real data and SP_SHOTS to a folder to
// save screenshots of every page.
func newTestUI(t *testing.T) (*UI, *core.Core) {
	t.Helper()
	dir := os.Getenv("SP_DEMO")
	if dir == "" {
		dir = t.TempDir()
	}
	snap := procs.Snapshot{Procs: []procs.Process{
		{PID: 10, Name: "Hades2.exe", Path: `C:\Games\Hades II\Hades2.exe`, HasWindow: true},
		{PID: 11, Name: "Discord.exe", Path: `C:\Users\me\AppData\Local\Discord\Discord.exe`, HasWindow: true},
	}, ForegroundPID: 10}
	c, err := core.New(core.Options{
		DataDir:   dir,
		Version:   "test",
		Scanner:   procs.Func(func() (procs.Snapshot, error) { return snap, nil }),
		Launchers: launchers.NewIndex(nil, ""),
		NoArt:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SP_DEMO") == "" {
		now := time.Now()
		c.Store.Put(model.Entry{Key: "db:hades-ii", Name: "Hades II", Kind: model.KindGame, Source: model.SourceDatabase, Favorite: true,
			Meta: &model.Meta{Year: 2025, Genres: []string{"Action", "RPG"}, Developers: []string{"Supergiant Games"}}})
		c.Store.Put(model.Entry{Key: "db:osu", Name: "osu!", Kind: model.KindGame, Source: model.SourceDatabase})
		c.Store.Put(model.Entry{Key: "db:discord", Name: "Discord", Kind: model.KindApp, Source: model.SourceDatabase})
		for d := 0; d < 40; d++ {
			st := now.AddDate(0, 0, -d).Add(-3 * time.Hour)
			c.Store.AddSession(model.Session{Key: "db:osu", Start: st, End: st.Add(time.Hour), Secs: 3600, Focus: 3000})
			if d%3 == 0 {
				c.Store.AddSession(model.Session{Key: "db:hades-ii", Start: st.Add(-2 * time.Hour), End: st.Add(-time.Hour), Secs: 3600})
			}
		}
	}
	// two polls so the running game shows up as a live session
	c.Tracker.Tick()
	time.Sleep(10 * time.Millisecond)
	c.Tracker.Tick()

	a := test.NewTempApp(t)
	u := New(c, nil, a)
	u.noBackground = true
	w, h := float32(1320), float32(900)
	if os.Getenv("SP_TALL") != "" {
		h = 1900
	}
	u.win.Resize(fyne.NewSize(w, h))
	u.visible = true
	return u, c
}

func shot(t *testing.T, u *UI, name string) {
	t.Helper()
	dir := os.Getenv("SP_SHOTS")
	if dir == "" {
		return
	}
	img := u.win.Canvas().Capture()
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestEveryPageRenders(t *testing.T) {
	u, c := newTestUI(t)
	for _, name := range []string{"home", "library", "stats", "settings"} {
		u.showPage(name)
		u.current.refresh()
		u.current.tick()
		if len(u.content.Objects) != 1 {
			t.Fatalf("%s: no content", name)
		}
		shot(t, u, name)
	}
	u.openDetail("db:hades-ii")
	if u.pageName != "detail" {
		t.Fatal("detail did not open")
	}
	u.current.refresh()
	u.current.tick()
	shot(t, u, "detail")
	u.back()
	if u.pageName != "settings" {
		t.Errorf("back went to %q", u.pageName)
	}
	u.openDetail("missing:key")
	u.current.refresh()

	// library filters and search
	u.showPage("library")
	lp := u.current.(*libraryPage)
	lp.st.filter = "Everything"
	lp.refresh()
	if len(lp.items) < 2 {
		t.Errorf("library items = %d", len(lp.items))
	}
	lp.st.search = "zzzz-nothing"
	lp.refresh()
	if len(lp.items) != 0 {
		t.Error("search should filter everything out")
	}
	lp.st.search = ""
	lp.st.filter = "Games"
	lp.refresh()
	if len(lp.items) == 0 || lp.items[0].Key != "db:hades-ii" {
		t.Errorf("running game should sort first, got %+v", lp.items)
	}

	// stats with every range and scope
	u.showPage("stats")
	sp := u.current.(*statsPage)
	for _, r := range stats.Ranges {
		for _, scope := range statScopes {
			sp.st.rng, sp.st.scope = r.Label, scope
			sp.refresh()
		}
	}
	sp.st.group = "Monthly"
	sp.refresh()

	// hide frees the page, show rebuilds it
	c.Settings.Update(func(s *settingsT) { s.FreeMemoryWhenHidden = true })
	u.Hide()
	if u.current != nil || len(u.content.Objects) != 0 {
		t.Error("hide should drop the page")
	}
	u.visible = false
	u.showPage("home")
	if u.current == nil {
		t.Error("show should rebuild")
	}
}

func TestLiveHelpers(t *testing.T) {
	u, c := newTestUI(t)
	live, ok := c.Tracker.LiveFor("db:hades-ii")
	if !ok || !live.Running {
		t.Fatalf("hades should be live: %+v", live)
	}
	if s := liveSecs(u, live); s < live.Secs {
		t.Errorf("liveSecs went backwards: %d < %d", s, live.Secs)
	}
	if sig := liveSignature(c.Tracker.Live()); sig == "" {
		t.Error("empty live signature")
	}
	if greeting(time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local)) != "Still up?" {
		t.Error("greeting")
	}
	e, _ := c.Store.Entry("db:hades-ii")
	if m := metaLine(e); m == "" && os.Getenv("SP_DEMO") == "" {
		t.Error("meta line should not be empty")
	}
}

func TestWidgets(t *testing.T) {
	test.NewTempApp(t)
	if truncate("Hello world", 6) != "Hello\u2026" || truncate("Hi", 6) != "Hi" {
		t.Error("truncate")
	}
	if initial("  osu!") != "O" || initial("!!!") != "?" {
		t.Error("initial")
	}
	if tintFor("a") != tintFor("A") {
		t.Error("tint should ignore case")
	}
	if exts := extensions([]fileFilter{{"x", "*.lnk;*.url"}, {"all", "*.*"}}); len(exts) != 2 || exts[0] != ".lnk" {
		t.Errorf("extensions = %v", exts)
	}
	bc := newBarChart(100)
	bc.SetData([]string{"a", "b"}, []string{"A", "B"}, [][]float64{{1, 2}, {0, 0.5}}, []series{{"x", colPrimary}, {"y", colGood}})
	w := test.NewWindow(bc)
	defer w.Close()
	w.Resize(fyne.NewSize(400, 200))
	bc.hoverAt(fyne.NewPos(100, 100))
	if bc.hover != 0 {
		t.Errorf("hover = %d", bc.hover)
	}
	bc.MouseOut()
	cal := newCalendar()
	cal.SetData(map[string]float64{stats.DayKey(time.Now()): 7200}, time.Now(), time.Monday)
	pc := newPunchcard()
	var hm [7][24]float64
	hm[1][20] = 3600
	pc.SetData(hm, time.Sunday)
	w2 := test.NewWindow(container2(cal, pc))
	defer w2.Close()
	w2.Resize(fyne.NewSize(800, 400))
	pc.hoverAt(fyne.NewPos(40, 10))
	if heatColor(0) != colEmptyCell || heatColor(10*3600) != heatRamp[4] {
		t.Error("heat colours")
	}
}

type settingsT = settings.Settings

func container2(a, b fyne.CanvasObject) fyne.CanvasObject { return container.NewVBox(a, b) }
