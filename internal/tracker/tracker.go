// Package tracker turns process snapshots into play sessions.
//
// Every poll it asks the scanner what is running, maps every process to a
// library entry through the detector and adds the elapsed time to the live
// session of that entry. A game that closes and comes back within the merge
// gap keeps its session, sleep/hibernate gaps are never counted, idle time
// can be skipped and running sessions are checkpointed to disk so a crash or
// power cut loses at most a few seconds.
package tracker

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/detect"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/store"
)

// Store is what the tracker needs from the store.
type Store interface {
	Ensure(model.Entry) (model.Entry, bool, error)
	Entry(key string) (model.Entry, bool)
	AddSession(model.Session) error
	SaveActive([]model.Session) error
	LoadActive() ([]model.Session, error)
	Resolve(key string) string
}

// Identifier maps processes to library identities.
type Identifier interface {
	Identify(procs.Process) (detect.Identity, bool)
}

// EventType says what happened.
type EventType int

const (
	EventStarted  EventType = iota // a session started
	EventEnded                     // a session finished and was saved
	EventNewEntry                  // something new was added to the library
	EventBreak                     // break reminder
	EventTick                      // a poll finished (for UI refreshes)
)

// Event is sent to subscribers after each change.
type Event struct {
	Type    EventType
	Key     string
	Name    string
	Kind    model.Kind
	Session model.Session
	Played  time.Duration
}

// Live is a running (or just stopped, waiting for the merge gap) session.
type Live struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	Kind     model.Kind `json:"kind"`
	Start    time.Time  `json:"start"`
	LastSeen time.Time  `json:"lastSeen"`
	Secs     int64      `json:"secs"`
	Focus    int64      `json:"focus"`
	Running  bool       `json:"running"`
	Focused  bool       `json:"focused"`
	Idle     bool       `json:"idle"`
	PIDs     []uint32   `json:"pids,omitempty"`
	ExePath  string     `json:"exePath,omitempty"`
}

// Session converts the live state into a session record.
func (l Live) Session() model.Session {
	return model.Session{Key: l.Key, Start: l.Start, End: l.LastSeen, Secs: l.Secs, Focus: l.Focus}
}

type liveSession struct {
	id        string
	key       string
	name      string
	kind      model.Kind
	start     time.Time
	lastSeen  time.Time
	played    time.Duration
	focus     time.Duration
	running   bool
	focused   bool
	idle      bool
	pids      []uint32
	exePath   string
	nextBreak time.Duration
}

func (ls *liveSession) live() Live {
	return Live{
		Key: ls.key, Name: ls.name, Kind: ls.kind, Start: ls.start, LastSeen: ls.lastSeen,
		Secs: secs(ls.played), Focus: secs(ls.focus), Running: ls.running, Focused: ls.focused,
		Idle: ls.idle, PIDs: append([]uint32(nil), ls.pids...), ExePath: ls.exePath,
	}
}

func (ls *liveSession) session() model.Session {
	f := secs(ls.focus)
	p := secs(ls.played)
	if f > p {
		f = p
	}
	return model.Session{ID: ls.id, Key: ls.key, Start: ls.start, End: ls.lastSeen, Secs: p, Focus: f}
}

func secs(d time.Duration) int64 { return int64(math.Round(d.Seconds())) }

// Options configures a tracker.
type Options struct {
	Scanner  procs.Scanner
	Detector Identifier
	Store    Store
	Settings func() settings.Settings
	Now      func() time.Time
	// CheckpointEvery controls how often active.json is written.
	CheckpointEvery time.Duration
}

// Tracker is the playtime engine.
type Tracker struct {
	opts Options

	mu             sync.Mutex
	live           map[string]*liveSession
	lastTick       time.Time
	lastCheckpoint time.Time
	paused         bool
	lastSnap       procs.Snapshot
	lastErr        error
	dirty          bool
	wake           chan struct{}

	lmu       sync.Mutex
	listeners []func(Event)
}

// New makes a tracker.
func New(opts Options) *Tracker {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Settings == nil {
		opts.Settings = settings.Defaults
	}
	if opts.CheckpointEvery <= 0 {
		opts.CheckpointEvery = 30 * time.Second
	}
	return &Tracker{opts: opts, live: map[string]*liveSession{}, wake: make(chan struct{}, 1)}
}

// Subscribe registers a listener. Listeners run on the tracker goroutine, so
// they must be quick (UI code should hop to the UI thread).
func (t *Tracker) Subscribe(fn func(Event)) {
	t.lmu.Lock()
	t.listeners = append(t.listeners, fn)
	t.lmu.Unlock()
}

func (t *Tracker) emit(evs []Event) {
	if len(evs) == 0 {
		return
	}
	t.lmu.Lock()
	ls := append([]func(Event){}, t.listeners...)
	t.lmu.Unlock()
	for _, e := range evs {
		for _, l := range ls {
			l(e)
		}
	}
}

// Restore loads the last checkpoint. Sessions in it are kept "pending": if
// the game is still running at the next poll (within the merge gap) the
// session simply continues, otherwise it is saved.
func (t *Tracker) Restore() error {
	active, err := t.opts.Store.LoadActive()
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range active {
		if s.Key == "" {
			continue
		}
		key := t.opts.Store.Resolve(s.Key)
		ls := &liveSession{
			id: s.ID, key: key, start: s.Start, lastSeen: s.End,
			played: time.Duration(s.Secs) * time.Second, focus: time.Duration(s.Focus) * time.Second,
			kind: model.KindApp,
		}
		if ls.id == "" {
			ls.id = store.NewID()
		}
		if e, ok := t.opts.Store.Entry(key); ok {
			ls.name, ls.kind = e.DisplayName(), e.Kind
		}
		if prev, ok := t.live[key]; ok {
			// two checkpoints for one key (should not happen), keep the longer
			if prev.played >= ls.played {
				continue
			}
		}
		t.live[key] = ls
	}
	return nil
}

type group struct {
	id      detect.Identity
	pids    []uint32
	exePath string
	exeName string
	focused bool
}

// Tick runs one poll.
func (t *Tracker) Tick() error {
	snap, scanErr := t.opts.Scanner.Scan()
	now := t.opts.Now()
	st := t.opts.Settings()
	poll := time.Duration(st.PollSeconds) * time.Second
	mergeGap := time.Duration(st.MergeGapSeconds) * time.Second
	idleLimit := time.Duration(st.IdleMinutes) * time.Minute
	maxStep := 3 * poll
	if maxStep < 30*time.Second {
		maxStep = 30 * time.Second
	}

	var events []Event
	var toSave []model.Session
	var toEnsure []model.Entry

	t.mu.Lock()
	dt := time.Duration(0)
	if !t.lastTick.IsZero() {
		dt = now.Sub(t.lastTick)
	}
	if dt > maxStep {
		// The PC slept or the app stalled. Never count the gap: everything
		// stopped at the last tick.
		for _, ls := range t.live {
			ls.running = false
		}
		dt = 0
	}
	if dt < 0 {
		dt = 0
	}
	t.lastTick = now
	t.lastErr = scanErr
	if scanErr != nil {
		t.mu.Unlock()
		return scanErr
	}
	t.lastSnap = snap

	groups := map[string]*group{}
	order := []string{}
	for _, p := range snap.Procs {
		id, ok := t.opts.Detector.Identify(p)
		if !ok || id.Key == "" {
			continue
		}
		key := t.opts.Store.Resolve(id.Key)
		g := groups[key]
		if g == nil {
			g = &group{id: id}
			groups[key] = g
			order = append(order, key)
		}
		g.pids = append(g.pids, p.PID)
		if g.exePath == "" && p.Path != "" {
			g.exePath, g.exeName = p.Path, p.Name
		}
		if g.exeName == "" {
			g.exeName = p.Name
		}
		if snap.ForegroundPID != 0 && p.PID == snap.ForegroundPID {
			g.focused = true
		}
	}

	for _, key := range order {
		g := groups[key]
		ls := t.live[key]
		if ls != nil && !ls.running && now.Sub(ls.lastSeen) > mergeGap {
			if s, ok := t.finishLocked(ls, st); ok {
				toSave = append(toSave, s)
			}
			ls = nil
		}
		entry, known := t.opts.Store.Entry(key)
		if ls == nil {
			ls = &liveSession{id: store.NewID(), key: key, start: now, kind: g.id.Kind, name: g.id.Name}
			if known {
				ls.name, ls.kind = entry.DisplayName(), entry.Kind
			} else {
				toEnsure = append(toEnsure, model.Entry{
					Key: key, Name: g.id.Name, Kind: g.id.Kind, Source: g.id.Source,
					SteamAppID: g.id.SteamAppID, Wiki: g.id.Wiki, ExeName: g.exeName,
					ExePath: g.exePath, FirstSeen: now,
				})
			}
			if !ls.kind.Valid() {
				ls.kind = model.KindApp
			}
			if st.BreakReminderMinutes > 0 {
				ls.nextBreak = time.Duration(st.BreakReminderMinutes) * time.Minute
			}
			t.live[key] = ls
			t.dirty = true
			events = append(events, Event{Type: EventStarted, Key: key, Name: ls.name, Kind: ls.kind})
		} else {
			if known {
				ls.name, ls.kind = entry.DisplayName(), entry.Kind
				if g.exePath != "" && entry.ExePath != g.exePath {
					toEnsure = append(toEnsure, model.Entry{Key: key, ExePath: g.exePath, ExeName: g.exeName})
				}
			}
			wasRunning := ls.running
			ls.running = true
			idleApplies := st.IdlePauseApps
			if ls.kind == model.KindGame {
				idleApplies = st.IdlePauseGames
			}
			ls.idle = idleApplies && snap.Idle >= idleLimit
			if wasRunning && dt > 0 && !t.paused && !ls.idle {
				ls.played += dt
				if g.focused {
					ls.focus += dt
				}
			}
			if ls.nextBreak > 0 && ls.kind == model.KindGame && ls.played >= ls.nextBreak {
				events = append(events, Event{Type: EventBreak, Key: key, Name: ls.name, Kind: ls.kind, Played: ls.played})
				step := time.Duration(st.BreakReminderMinutes) * time.Minute
				if step <= 0 {
					ls.nextBreak = 0
				} else {
					for ls.nextBreak <= ls.played {
						ls.nextBreak += step
					}
				}
			}
		}
		ls.running = true
		ls.lastSeen = now
		ls.pids = g.pids
		ls.focused = g.focused
		if g.exePath != "" {
			ls.exePath = g.exePath
		}
	}

	for key, ls := range t.live {
		if _, seen := groups[key]; seen {
			continue
		}
		if ls.running {
			ls.running = false
			ls.focused = false
			t.dirty = true
		}
		if now.Sub(ls.lastSeen) > mergeGap {
			if s, ok := t.finishLocked(ls, st); ok {
				toSave = append(toSave, s)
			}
		}
	}

	var checkpoint []model.Session
	doCheckpoint := t.dirty || now.Sub(t.lastCheckpoint) >= t.opts.CheckpointEvery
	if doCheckpoint {
		checkpoint = t.activeLocked()
		t.lastCheckpoint = now
		t.dirty = false
	}
	t.mu.Unlock()

	for _, e := range toEnsure {
		if _, created, err := t.opts.Store.Ensure(e); err == nil && created {
			events = append(events, Event{Type: EventNewEntry, Key: e.Key, Name: e.Name, Kind: e.Kind})
		}
	}
	for _, s := range toSave {
		if err := t.opts.Store.AddSession(s); err == nil {
			name := s.Key
			kind := model.KindApp
			if e, ok := t.opts.Store.Entry(s.Key); ok {
				name, kind = e.DisplayName(), e.Kind
			}
			events = append(events, Event{Type: EventEnded, Key: s.Key, Name: name, Kind: kind, Session: s})
		}
	}
	if doCheckpoint {
		_ = t.opts.Store.SaveActive(checkpoint)
	}
	events = append(events, Event{Type: EventTick})
	t.emit(events)
	return nil
}

// finishLocked removes a live session and returns it if it is long enough to
// keep.
func (t *Tracker) finishLocked(ls *liveSession, st settings.Settings) (model.Session, bool) {
	delete(t.live, ls.key)
	t.dirty = true
	s := ls.session()
	if s.Secs < int64(st.MinSessionSeconds) || s.Secs <= 0 {
		return model.Session{}, false
	}
	return s, true
}

func (t *Tracker) activeLocked() []model.Session {
	out := make([]model.Session, 0, len(t.live))
	for _, ls := range t.live {
		out = append(out, ls.session())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Live returns the current sessions: running ones first, newest first.
func (t *Tracker) Live() []Live {
	t.mu.Lock()
	out := make([]Live, 0, len(t.live))
	for _, ls := range t.live {
		out = append(out, ls.live())
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == model.KindGame
		}
		return out[i].Start.After(out[j].Start)
	})
	return out
}

// LiveFor returns the live session of one entry.
func (t *Tracker) LiveFor(key string) (Live, bool) {
	key = t.opts.Store.Resolve(key)
	t.mu.Lock()
	defer t.mu.Unlock()
	ls, ok := t.live[key]
	if !ok {
		return Live{}, false
	}
	return ls.live(), true
}

// Paused reports whether counting is paused.
func (t *Tracker) Paused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paused
}

// SetPaused pauses or resumes counting.
func (t *Tracker) SetPaused(p bool) {
	t.mu.Lock()
	t.paused = p
	t.mu.Unlock()
	t.emit([]Event{{Type: EventTick}})
}

// LastSnapshot returns the last process snapshot.
func (t *Tracker) LastSnapshot() procs.Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastSnap
}

// LastError returns the error of the last scan (nil when fine).
func (t *Tracker) LastError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastErr
}

// Wake makes Run poll right away (after a settings change for example).
func (t *Tracker) Wake() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Forget drops the live session of a key without saving it (used when the
// user deletes an entry while it runs).
func (t *Tracker) Forget(key string) {
	t.mu.Lock()
	delete(t.live, key)
	t.dirty = true
	t.mu.Unlock()
}

// Shutdown saves every live session and clears the checkpoint.
func (t *Tracker) Shutdown() {
	st := t.opts.Settings()
	t.mu.Lock()
	var toSave []model.Session
	for _, ls := range t.live {
		if s, ok := t.finishLocked(ls, st); ok {
			toSave = append(toSave, s)
		}
	}
	t.mu.Unlock()
	for _, s := range toSave {
		_ = t.opts.Store.AddSession(s)
	}
	_ = t.opts.Store.SaveActive([]model.Session{})
}

// Run polls until ctx is cancelled, then shuts down.
func (t *Tracker) Run(ctx context.Context) {
	_ = t.Restore()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Shutdown()
			return
		case <-timer.C:
		case <-t.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		_ = t.Tick()
		poll := time.Duration(t.opts.Settings().PollSeconds) * time.Second
		if poll <= 0 {
			poll = 5 * time.Second
		}
		timer.Reset(poll)
	}
}
