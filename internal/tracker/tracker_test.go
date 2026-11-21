package tracker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/detect"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/store"
)

// fakeIdent treats "*.game.exe" as games and everything else as apps.
type fakeIdent struct{}

func (fakeIdent) Identify(p procs.Process) (detect.Identity, bool) {
	if p.Name == "" || p.Name == "ignored.exe" {
		return detect.Identity{}, false
	}
	kind := model.KindApp
	if strings.HasSuffix(p.Name, ".game.exe") {
		kind = model.KindGame
	}
	name := strings.TrimSuffix(strings.TrimSuffix(p.Name, ".exe"), ".game")
	return detect.Identity{Key: "k:" + name, Name: name, Kind: kind, Source: model.SourceAuto}, true
}

type harness struct {
	t     *testing.T
	now   time.Time
	snap  procs.Snapshot
	st    settings.Settings
	store *store.Store
	tr    *Tracker
	evs   []Event
}

func newHarness(t *testing.T) *harness {
	return newHarnessIn(t, t.TempDir())
}

func newHarnessIn(t *testing.T, dir string) *harness {
	h := &harness{t: t, now: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC), st: settings.Defaults()}
	h.st.MinSessionSeconds = 30
	h.st.MergeGapSeconds = 120
	h.st.PollSeconds = 5
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.store = s
	h.tr = New(Options{
		Scanner:         procs.Func(func() (procs.Snapshot, error) { return h.snap, nil }),
		Detector:        fakeIdent{},
		Store:           s,
		Settings:        func() settings.Settings { return h.st },
		Now:             func() time.Time { return h.now },
		CheckpointEvery: 30 * time.Second,
	})
	h.tr.Subscribe(func(e Event) {
		if e.Type != EventTick {
			h.evs = append(h.evs, e)
		}
	})
	return h
}

func (h *harness) run(names ...string) {
	h.snap.Procs = nil
	for i, n := range names {
		h.snap.Procs = append(h.snap.Procs, procs.Process{PID: uint32(100 + i), Name: n, HasWindow: true})
	}
}

// step advances the clock and ticks n times.
func (h *harness) step(d time.Duration, n int) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		h.now = h.now.Add(d)
		if err := h.tr.Tick(); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) countEvents(typ EventType) int {
	n := 0
	for _, e := range h.evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func TestBasicSession(t *testing.T) {
	h := newHarness(t)
	h.run("hades.game.exe")
	h.step(5*time.Second, 1) // first sighting, not counted yet
	h.step(5*time.Second, 12)
	live, ok := h.tr.LiveFor("k:hades")
	if !ok || !live.Running || live.Secs != 60 || live.Kind != model.KindGame {
		t.Fatalf("live = %+v %v", live, ok)
	}
	h.run()
	h.step(5*time.Second, 1)
	if l, _ := h.tr.LiveFor("k:hades"); l.Running {
		t.Error("should be stopped")
	}
	// still pending inside the merge gap
	if h.store.SessionCount() != 0 {
		t.Fatal("saved too early")
	}
	h.step(5*time.Second, 25) // > 120s
	sessions := h.store.SessionsFor("k:hades")
	if len(sessions) != 1 || sessions[0].Secs != 60 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if _, ok := h.tr.LiveFor("k:hades"); ok {
		t.Error("live session should be gone")
	}
	if h.countEvents(EventStarted) != 1 || h.countEvents(EventEnded) != 1 || h.countEvents(EventNewEntry) != 1 {
		t.Errorf("events = %+v", h.evs)
	}
	e, _ := h.store.Entry("k:hades")
	if e.Kind != model.KindGame || e.Name != "hades" || e.LastPlayed.IsZero() {
		t.Errorf("entry = %+v", e)
	}
}

func TestRestartWithinMergeGapKeepsSession(t *testing.T) {
	h := newHarness(t)
	h.run("x.game.exe")
	h.step(5*time.Second, 13) // 60s
	h.run()
	h.step(5*time.Second, 6) // 30s gap
	h.run("x.game.exe")
	h.step(5*time.Second, 13) // 60s more (first tick after revive not counted)
	l, _ := h.tr.LiveFor("k:x")
	if l.Secs != 120 {
		t.Errorf("merged secs = %d, want 120", l.Secs)
	}
	if h.countEvents(EventStarted) != 1 {
		t.Errorf("should be one session, events %+v", h.evs)
	}
}

func TestShortSessionsAreDropped(t *testing.T) {
	h := newHarness(t)
	h.run("quick.exe")
	h.step(5*time.Second, 4) // 15s
	h.run()
	h.step(time.Minute, 4)
	if n := h.store.SessionCount(); n != 0 {
		t.Errorf("short session saved (%d)", n)
	}
}

func TestSleepGapIsNotCounted(t *testing.T) {
	h := newHarness(t)
	h.run("x.game.exe")
	h.step(5*time.Second, 13)        // 60s
	h.now = h.now.Add(2 * time.Hour) // laptop lid closed
	h.tr.Tick()
	h.step(5*time.Second, 12) // 60s more, but a new session
	sessions := h.store.SessionsFor("k:x")
	if len(sessions) != 1 || sessions[0].Secs != 60 {
		t.Fatalf("first session = %+v", sessions)
	}
	l, _ := h.tr.LiveFor("k:x")
	if l.Secs != 60 {
		t.Errorf("second session secs = %d", l.Secs)
	}
}

func TestShortStallDoesNotSplitButIsNotCounted(t *testing.T) {
	h := newHarness(t)
	h.run("x.game.exe")
	h.step(5*time.Second, 13)           // 60s
	h.now = h.now.Add(60 * time.Second) // stall > maxStep (30s) but < merge gap
	h.tr.Tick()
	h.step(5*time.Second, 12)
	l, _ := h.tr.LiveFor("k:x")
	if l.Secs != 120 || h.store.SessionCount() != 0 {
		t.Errorf("secs = %d, saved = %d", l.Secs, h.store.SessionCount())
	}
}

func TestAppsFocusOnly(t *testing.T) {
	h := newHarness(t)
	h.st.AppsFocusOnly = true
	h.run("app.exe", "other.exe", "g.game.exe")
	h.snap.ForegroundPID = 100 // app.exe
	h.step(5*time.Second, 7)   // 30s
	h.snap.ForegroundPID = 101 // other.exe
	h.step(5*time.Second, 2)   // 10s
	app, _ := h.tr.LiveFor("k:app")
	other, _ := h.tr.LiveFor("k:other")
	game, _ := h.tr.LiveFor("k:g")
	if app.Secs != 30 || other.Secs != 10 || game.Secs != 40 {
		t.Errorf("app=%d other=%d game=%d", app.Secs, other.Secs, game.Secs)
	}
}

func TestIdleAndFocus(t *testing.T) {
	h := newHarness(t)
	h.st.AppsFocusOnly = false
	h.st.IdlePauseApps = true
	h.st.IdlePauseGames = false
	h.st.IdleMinutes = 5
	h.run("app.exe", "g.game.exe")
	h.snap.ForegroundPID = 100 // app.exe
	h.step(5*time.Second, 7)   // 30s focused, both counted
	h.snap.Idle = 10 * time.Minute
	h.step(5*time.Second, 6) // 30s idle: app paused, game keeps going
	app, _ := h.tr.LiveFor("k:app")
	game, _ := h.tr.LiveFor("k:g")
	if app.Secs != 30 || app.Focus != 30 || !app.Idle {
		t.Errorf("app = %+v", app)
	}
	if game.Secs != 60 || game.Focus != 0 {
		t.Errorf("game = %+v", game)
	}
}

func TestPause(t *testing.T) {
	h := newHarness(t)
	h.run("g.game.exe")
	h.step(5*time.Second, 7)
	h.tr.SetPaused(true)
	h.step(5*time.Second, 6)
	h.tr.SetPaused(false)
	h.step(5*time.Second, 2)
	l, _ := h.tr.LiveFor("k:g")
	if l.Secs != 40 || !(!h.tr.Paused()) {
		t.Errorf("secs = %d", l.Secs)
	}
}

func TestCheckpointAndRestore(t *testing.T) {
	dir := t.TempDir()
	h := newHarnessIn(t, dir)
	h.run("g.game.exe")
	h.step(5*time.Second, 13) // 60s, a checkpoint happens on start and after 30s
	now := h.now

	// "crash": a new tracker on the same data dir, game still running
	h2 := newHarnessIn(t, dir)
	h2.now = now.Add(20 * time.Second)
	if err := h2.tr.Restore(); err != nil {
		t.Fatal(err)
	}
	l, ok := h2.tr.LiveFor("k:g")
	if !ok || l.Running || l.Secs < 30 {
		t.Fatalf("restored = %+v %v", l, ok)
	}
	h2.run("g.game.exe")
	h2.step(5*time.Second, 7)
	l, _ = h2.tr.LiveFor("k:g")
	if !l.Running || l.Secs < 60 {
		t.Errorf("continued session = %+v", l)
	}
	if h2.store.SessionCount() != 0 {
		t.Error("should continue, not save")
	}

	// a restore long after: the pending session is saved on the next tick
	h3 := newHarnessIn(t, dir)
	h3.now = h2.now.Add(time.Hour)
	h3.tr.Restore()
	h3.run()
	h3.step(5*time.Second, 1)
	if n := len(h3.store.SessionsFor("k:g")); n != 1 {
		t.Errorf("sessions after late restore = %d", n)
	}
}

func TestShutdownSaves(t *testing.T) {
	h := newHarness(t)
	h.run("g.game.exe", "ignored.exe")
	h.step(5*time.Second, 13)
	h.tr.Shutdown()
	if n := len(h.store.SessionsFor("k:g")); n != 1 {
		t.Errorf("sessions = %d", n)
	}
	if a, _ := h.store.LoadActive(); len(a) != 0 {
		t.Errorf("active should be cleared, got %+v", a)
	}
	if _, ok := h.store.Entry("k:ignored"); ok {
		t.Error("ignored process got an entry")
	}
}

func TestBreakReminder(t *testing.T) {
	h := newHarness(t)
	h.st.BreakReminderMinutes = 1
	h.run("g.game.exe")
	h.step(5*time.Second, 1)
	h.step(5*time.Second, 25) // ~2 minutes
	if n := h.countEvents(EventBreak); n != 2 {
		t.Errorf("break events = %d, want 2", n)
	}
}

func TestUserKindChangeIsRespected(t *testing.T) {
	h := newHarness(t)
	h.st.AppsFocusOnly = false
	h.st.IdlePauseApps = true
	h.st.IdlePauseGames = false
	h.run("thing.exe")
	h.step(5*time.Second, 2)
	h.store.Update("k:thing", func(e *model.Entry) { e.Kind = model.KindGame; e.Name = "Thing Game" })
	h.snap.Idle = time.Hour
	h.step(5*time.Second, 6)
	l, _ := h.tr.LiveFor("k:thing")
	if l.Kind != model.KindGame || l.Name != "Thing Game" || l.Secs != 35 {
		t.Errorf("live = %+v", l)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	h := newHarness(t)
	h.run("g.game.exe")
	h.now = time.Now()
	h.tr.opts.Now = time.Now
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.tr.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	h.tr.Wake()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if _, ok := h.tr.LiveFor("k:g"); ok {
		t.Error("live sessions should be flushed on shutdown")
	}
}

func TestLiveOrdering(t *testing.T) {
	h := newHarness(t)
	h.run("app.exe", "g.game.exe")
	h.step(5*time.Second, 2)
	live := h.tr.Live()
	if len(live) != 2 || live[0].Kind != model.KindGame {
		t.Errorf("games should come first: %+v", live)
	}
	if len(h.tr.LastSnapshot().Procs) != 2 || h.tr.LastError() != nil {
		t.Error("snapshot not kept")
	}
}
