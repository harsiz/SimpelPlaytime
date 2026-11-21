// Package core wires every part of Simpel Playtime together: the store, the
// tracker, the detector, the artwork worker and all the "Customise"
// operations. The UI and the local API both talk to a *Core.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/art"
	"github.com/harsiz/SimpelPlaytime/internal/autostart"
	"github.com/harsiz/SimpelPlaytime/internal/detect"
	"github.com/harsiz/SimpelPlaytime/internal/exeinfo"
	"github.com/harsiz/SimpelPlaytime/internal/fsutil"
	"github.com/harsiz/SimpelPlaytime/internal/gamedb"
	"github.com/harsiz/SimpelPlaytime/internal/launchers"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/shortcut"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
	"github.com/harsiz/SimpelPlaytime/internal/store"
	"github.com/harsiz/SimpelPlaytime/internal/tracker"
)

// Options for New.
type Options struct {
	DataDir string
	Version string
	// Scanner overrides the platform process scanner (tests).
	Scanner procs.Scanner
	// Launchers overrides the launcher index (tests).
	Launchers *launchers.Index
	// Fetcher overrides the art fetcher (tests). Set NoArt to disable it.
	Fetcher *art.Fetcher
	NoArt   bool
	Now     func() time.Time
}

// Core is the running app minus the UI.
type Core struct {
	Version   string
	DataDir   string
	StartedAt time.Time

	Store     *store.Store
	Settings  *settings.Manager
	DB        *gamedb.DB
	Launchers *launchers.Index
	Detector  *detect.Detector
	Tracker   *tracker.Tracker
	Art       *art.Fetcher

	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	qmu     sync.Mutex
	queue   []enrichJob
	queued  map[string]bool
	qsignal chan struct{}

	tmu        sync.Mutex
	totals     map[string]stats.Totals
	totalsGood bool

	lmu       sync.Mutex
	listeners []func()
}

type enrichJob struct {
	key   string
	force bool
}

// New opens everything in dataDir.
func New(opts Options) (*Core, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	st, err := store.Open(opts.DataDir)
	if err != nil {
		return nil, err
	}
	sm, err := settings.Load(opts.DataDir)
	if err != nil {
		return nil, err
	}
	db, err := gamedb.Default()
	if err != nil {
		return nil, err
	}
	c := &Core{
		Version: opts.Version, DataDir: opts.DataDir, StartedAt: opts.Now(),
		Store: st, Settings: sm, DB: db, now: opts.Now,
		queued: map[string]bool{}, qsignal: make(chan struct{}, 1),
	}
	c.Launchers = opts.Launchers
	if c.Launchers == nil {
		c.Launchers = launchers.DefaultIndex()
	}
	c.Detector = detect.New(detect.Options{
		DB:        db,
		Launchers: c.Launchers,
		Settings:  sm.Get,
		Entries:   st.Entries,
		ExeInfo:   exeinfo.ProductAndDescription,
		SelfPID:   uint32(os.Getpid()),
	})
	scanner := opts.Scanner
	if scanner == nil {
		scanner = procs.New()
	}
	c.Tracker = tracker.New(tracker.Options{
		Scanner: scanner, Detector: c.Detector, Store: st, Settings: sm.Get, Now: opts.Now,
	})
	if !opts.NoArt {
		c.Art = opts.Fetcher
		if c.Art == nil {
			c.Art = art.New(st.ArtDir(), opts.Version)
		}
		c.Art.SteamGridKey = func() string { return sm.Get().SteamGridDBKey }
	}

	st.Subscribe(func(ch store.Change) {
		if ch.What != "session" {
			c.Detector.Invalidate()
		}
		c.invalidateTotals()
		c.changed()
	})
	sm.OnChange(func(old, new settings.Settings) {
		c.Detector.Invalidate()
		c.Tracker.Wake()
		if old.DownloadArt != new.DownloadArt || old.SteamGridDBKey != new.SteamGridDBKey {
			c.EnqueueMissing()
		}
		c.changed()
	})
	c.Tracker.Subscribe(func(e tracker.Event) {
		switch e.Type {
		case tracker.EventNewEntry:
			c.Enqueue(e.Key, false)
		case tracker.EventStarted:
			c.Enqueue(e.Key, false)
		}
	})
	return c, nil
}

// Start runs the tracker and the artwork worker in the background.
func (c *Core) Start() {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.wg.Add(2)
	go func() { defer c.wg.Done(); c.Tracker.Run(c.ctx) }()
	go func() { defer c.wg.Done(); c.enrichLoop(c.ctx) }()
	c.EnqueueMissing()
}

// Stop saves running sessions and stops background work.
func (c *Core) Stop() {
	if c.cancel != nil {
		c.cancel()
		c.wg.Wait()
		c.cancel = nil
	}
}

// OnChange registers a callback for "something changed, refresh". It runs on
// background goroutines.
func (c *Core) OnChange(fn func()) {
	c.lmu.Lock()
	c.listeners = append(c.listeners, fn)
	c.lmu.Unlock()
}

func (c *Core) changed() {
	c.lmu.Lock()
	ls := append([]func(){}, c.listeners...)
	c.lmu.Unlock()
	for _, l := range ls {
		l()
	}
}

// ---- totals -------------------------------------------------------------

func (c *Core) invalidateTotals() {
	c.tmu.Lock()
	c.totalsGood = false
	c.tmu.Unlock()
}

// StoredTotals returns lifetime totals of finished sessions (cached).
func (c *Core) StoredTotals() map[string]stats.Totals {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	if !c.totalsGood {
		c.totals = stats.PerEntry(c.Store.Sessions())
		c.totalsGood = true
	}
	out := make(map[string]stats.Totals, len(c.totals))
	for k, v := range c.totals {
		out[k] = v
	}
	return out
}

// Totals returns lifetime totals including the running sessions.
func (c *Core) Totals() map[string]stats.Totals {
	out := c.StoredTotals()
	for _, l := range c.Tracker.Live() {
		t := out[l.Key]
		t.Secs += l.Secs
		t.Focus += l.Focus
		if l.Secs > 0 {
			t.Sessions++
		}
		if l.Secs > t.Longest {
			t.Longest = l.Secs
		}
		if t.First.IsZero() || l.Start.Before(t.First) {
			t.First = l.Start
		}
		if l.LastSeen.After(t.Last) {
			t.Last = l.LastSeen
		}
		out[l.Key] = t
	}
	return out
}

// TotalFor returns the totals of one entry, including a running session.
func (c *Core) TotalFor(key string) stats.Totals {
	key = c.Store.Resolve(key)
	return c.Totals()[key]
}

// Sessions returns every session, plus the running ones when live is true.
func (c *Core) Sessions(live bool) []model.Session {
	out := c.Store.Sessions()
	if live {
		for _, l := range c.Tracker.Live() {
			if l.Secs > 0 {
				s := l.Session()
				s.ID = "live:" + l.Key
				out = append(out, s)
			}
		}
	}
	return out
}

// KindFilter returns a stats filter for a kind ("" or "all" keeps all).
func (c *Core) KindFilter(kind string) stats.Filter {
	if kind == "" || kind == "all" {
		return nil
	}
	kinds := map[string]model.Kind{}
	for _, e := range c.Store.Entries() {
		kinds[e.Key] = e.Kind
	}
	want := model.Kind(kind)
	return func(key string) bool {
		k, ok := kinds[key]
		if !ok {
			return want == model.KindApp
		}
		return k == want
	}
}

// ---- artwork + icons ------------------------------------------------------

// Enqueue asks the worker to fetch the icon/poster of an entry. force
// refetches art even if it was tried recently.
func (c *Core) Enqueue(key string, force bool) {
	c.qmu.Lock()
	if c.queued[key] && !force {
		c.qmu.Unlock()
		return
	}
	c.queued[key] = true
	c.queue = append(c.queue, enrichJob{key: key, force: force})
	c.qmu.Unlock()
	select {
	case c.qsignal <- struct{}{}:
	default:
	}
}

// EnqueueMissing queues every entry that misses an icon or a poster.
func (c *Core) EnqueueMissing() {
	for _, e := range c.Store.Entries() {
		if c.needsIcon(e) || c.needsArt(e, false) {
			c.Enqueue(e.Key, false)
		}
	}
}

func (c *Core) needsIcon(e model.Entry) bool {
	if e.ExePath == "" {
		return false
	}
	if e.IconFile != "" && fsutil.Exists(c.Store.IconPath(e)) {
		return false
	}
	return e.IconChecked.IsZero() || c.now().Sub(e.IconChecked) > 24*time.Hour
}

func (c *Core) needsArt(e model.Entry, force bool) bool {
	if c.Art == nil || e.CustomPoster {
		return false
	}
	if !c.Settings.Get().DownloadArt {
		return false
	}
	if e.Kind != model.KindGame && e.SteamAppID == 0 && e.Wiki == "" {
		return false
	}
	if force {
		return true
	}
	if e.PosterFile != "" && fsutil.Exists(c.Store.PosterPath(e)) {
		return false
	}
	return e.ArtChecked.IsZero() || c.now().Sub(e.ArtChecked) > 7*24*time.Hour
}

func (c *Core) enrichLoop(ctx context.Context) {
	for {
		c.qmu.Lock()
		var job enrichJob
		has := len(c.queue) > 0
		if has {
			job = c.queue[0]
			c.queue = c.queue[1:]
			delete(c.queued, job.key)
		}
		c.qmu.Unlock()
		if !has {
			select {
			case <-ctx.Done():
				return
			case <-c.qsignal:
				continue
			}
		}
		c.enrich(ctx, job)
		if ctx.Err() != nil {
			return
		}
	}
}

func (c *Core) enrich(ctx context.Context, job enrichJob) {
	e, ok := c.Store.Entry(job.key)
	if !ok {
		return
	}
	if c.needsIcon(e) || (job.force && e.ExePath != "") {
		name := fsutil.SafeName(e.Key) + ".png"
		err := exeinfo.SaveIconPNG(e.ExePath, filepath.Join(c.Store.IconsDir(), name), 128)
		_ = c.Store.Update(e.Key, func(x *model.Entry) {
			x.IconChecked = c.now()
			if err == nil {
				x.IconFile = name
			}
		})
	}
	e, _ = c.Store.Entry(job.key)
	if !c.needsArt(e, job.force) {
		return
	}
	res, err := c.Art.Fetch(ctx, art.Request{Key: e.Key, Name: e.DisplayName(), Kind: e.Kind, SteamAppID: e.SteamAppID, Wiki: e.Wiki})
	if ctx.Err() != nil {
		return
	}
	_ = c.Store.Update(e.Key, func(x *model.Entry) {
		x.ArtChecked = c.now()
		if err != nil && !errors.Is(err, art.ErrNotFound) {
			// network trouble: try again in an hour instead of a week
			x.ArtChecked = c.now().Add(-7*24*time.Hour + time.Hour)
		}
		if res.SteamAppID != 0 && x.SteamAppID == 0 {
			x.SteamAppID = res.SteamAppID
		}
		if res.PosterFile != "" && !x.CustomPoster {
			x.PosterFile = res.PosterFile
		}
		if res.HeroFile != "" {
			x.HeroFile = res.HeroFile
		}
		if res.Meta != nil {
			x.Meta = res.Meta
		}
	})
}

// QueueLength reports pending artwork jobs (for the UI).
func (c *Core) QueueLength() int {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	return len(c.queue)
}

// ---- customise --------------------------------------------------------

// MatchBy options for TrackExe.
const (
	MatchByName = "name" // any exe with this file name
	MatchByPath = "path" // only this exact file
)

func (c *Core) uniqueKey(prefix, name string) string {
	base := prefix + gamedb.Slug(name)
	if base == prefix {
		base = prefix + "item"
	}
	key := base
	for i := 2; ; i++ {
		if _, ok := c.Store.Entry(key); !ok {
			return key
		}
		key = base + "-" + strconv.Itoa(i)
	}
}

func hasMatcher(ms []model.Matcher, m model.Matcher) bool {
	for _, x := range ms {
		if x.Type == m.Type && strings.EqualFold(x.Value, m.Value) && x.Title == m.Title {
			return true
		}
	}
	return false
}

// TrackExe starts tracking an exe chosen by the user. When the exe already
// belongs to an entry (from the database or auto detection) that entry is
// updated instead of making a duplicate.
func (c *Core) TrackExe(path, name string, kind model.Kind, matchBy string) (model.Entry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return model.Entry{}, errors.New("no file chosen")
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return model.Entry{}, errors.New("that is not an .exe")
	}
	exe := procs.ExeName(path)
	if name == "" {
		if p, d := exeinfo.ProductAndDescription(path); d != "" {
			name = d
		} else if p != "" {
			name = p
		} else {
			name = detect.PrettyExeName(exe)
		}
	}
	if !kind.Valid() {
		kind = model.KindGame
	}
	m := model.Matcher{Type: model.MatchExe, Value: exe}
	if matchBy == MatchByPath {
		m = model.Matcher{Type: model.MatchPath, Value: path}
	}
	var key string
	if id, ok := c.Detector.Identify(procs.Process{Name: exe, Path: path, HasWindow: true}); ok {
		key = c.Store.Resolve(id.Key)
		_, exists := c.Store.Entry(key)
		switch {
		case exists:
			// already tracked (maybe with history): update that entry
		case id.Source == model.SourceDatabase || id.Source == model.SourceSteam ||
			id.Source == model.SourceEpic || id.Source == model.SourceHeuristic:
			// a known game that was never played yet: keep its proper identity
			_, _, err := c.Store.Ensure(model.Entry{Key: key, Name: id.Name, Kind: id.Kind, Source: id.Source,
				SteamAppID: id.SteamAppID, Wiki: id.Wiki, ExeName: exe, ExePath: path, FirstSeen: c.now()})
			if err != nil {
				return model.Entry{}, err
			}
		default:
			key = ""
		}
	}
	if key == "" {
		key = c.uniqueKey("custom:", name)
		if err := c.Store.Put(model.Entry{Key: key, Name: name, Kind: kind, Source: model.SourceCustom,
			ExeName: exe, ExePath: path, Matchers: []model.Matcher{m}, FirstSeen: c.now()}); err != nil {
			return model.Entry{}, err
		}
	} else {
		err := c.Store.Update(key, func(e *model.Entry) {
			e.Name = name
			e.Kind = kind
			e.Hidden = false
			if e.ExePath == "" {
				e.ExePath, e.ExeName = path, exe
			}
			if !hasMatcher(e.Matchers, m) {
				e.Matchers = append(e.Matchers, m)
			}
		})
		if err != nil {
			return model.Entry{}, err
		}
	}
	c.unignore(exe)
	c.Enqueue(key, true)
	c.Tracker.Wake()
	e, _ := c.Store.Entry(key)
	return e, nil
}

// TrackFolder tracks every exe inside a folder as one entry.
func (c *Core) TrackFolder(dir, name string, kind model.Kind) (model.Entry, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return model.Entry{}, errors.New("no folder chosen")
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	if !kind.Valid() {
		kind = model.KindGame
	}
	key := c.uniqueKey("custom:", name)
	e := model.Entry{Key: key, Name: name, Kind: kind, Source: model.SourceCustom,
		Matchers: []model.Matcher{{Type: model.MatchFolder, Value: dir}}, FirstSeen: c.now()}
	if exe := biggestExe(dir); exe != "" {
		e.ExePath, e.ExeName = exe, filepath.Base(exe)
	}
	if err := c.Store.Put(e); err != nil {
		return model.Entry{}, err
	}
	c.Enqueue(key, true)
	c.Tracker.Wake()
	return e, nil
}

// biggestExe finds the largest exe directly inside dir or one level below
// (usually the game itself, used for the icon).
func biggestExe(dir string) string {
	var best string
	var bestSize int64
	for _, pattern := range []string{"*.exe", "*/*.exe", "*/*/*.exe"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, m := range matches {
			l := strings.ToLower(filepath.Base(m))
			if strings.Contains(l, "unins") || strings.Contains(l, "crash") || strings.Contains(l, "setup") || strings.Contains(l, "redist") {
				continue
			}
			if st, err := os.Stat(m); err == nil && st.Size() > bestSize {
				best, bestSize = m, st.Size()
			}
		}
	}
	return best
}

// ShortcutInfo describes what a shortcut will be tracked as (for the UI).
type ShortcutInfo struct {
	Target shortcut.Target
	Name   string
	Kind   model.Kind
	Detail string
}

// InspectShortcut resolves a shortcut and explains what will be tracked.
func (c *Core) InspectShortcut(path string) (ShortcutInfo, error) {
	t, err := shortcut.Resolve(path)
	if err != nil {
		return ShortcutInfo{}, err
	}
	info := ShortcutInfo{Target: t, Name: t.Name, Kind: model.KindGame}
	switch t.Kind {
	case shortcut.TargetExe:
		info.Detail = t.Path
		if id, ok := c.Detector.Identify(procs.Process{Name: procs.ExeName(t.Path), Path: t.Path, HasWindow: true}); ok && id.Kind.Valid() {
			info.Kind = id.Kind
			if id.Name != "" && id.Source == model.SourceDatabase {
				info.Name = id.Name
			}
		}
	case shortcut.TargetSteam:
		g, ok := c.Launchers.SteamApp(t.SteamAppID)
		if !ok {
			return info, fmt.Errorf("Steam game %d is not installed on this PC (or its library folder was not found)", t.SteamAppID)
		}
		info.Detail = "Steam app " + strconv.Itoa(t.SteamAppID) + " in " + g.InstallDir
		if g.Name != "" {
			info.Name = g.Name
		}
	case shortcut.TargetEpic:
		g, ok := c.Launchers.EpicApp(t.EpicApp)
		if !ok {
			return info, fmt.Errorf("Epic game %q is not installed on this PC", t.EpicApp)
		}
		info.Detail = "Epic game in " + g.InstallDir
		if g.Name != "" {
			info.Name = g.Name
		}
	default:
		return info, fmt.Errorf("this shortcut opens %q, which is not a game or app", t.URL)
	}
	if db := c.DB.BySteamID(t.SteamAppID); db != nil && t.SteamAppID != 0 {
		info.Name = db.Name
	}
	return info, nil
}

// TrackShortcut tracks whatever a .lnk/.url shortcut launches.
func (c *Core) TrackShortcut(path, name string, kind model.Kind) (model.Entry, error) {
	info, err := c.InspectShortcut(path)
	if err != nil {
		return model.Entry{}, err
	}
	if name == "" {
		name = info.Name
	}
	t := info.Target
	switch t.Kind {
	case shortcut.TargetExe:
		return c.TrackExe(t.Path, name, kind, MatchByName)
	case shortcut.TargetSteam:
		g, _ := c.Launchers.SteamApp(t.SteamAppID)
		if db := c.DB.BySteamID(t.SteamAppID); db != nil {
			// the database already knows it, make sure the entry exists and
			// also count anything in the install folder
			key := "db:" + db.ID
			if _, _, err := c.Store.Ensure(model.Entry{Key: key, Name: db.Name, Kind: db.Kind, Source: model.SourceDatabase, SteamAppID: db.SteamID, FirstSeen: c.now()}); err != nil {
				return model.Entry{}, err
			}
			return c.addFolderMatcher(key, g.InstallDir, name, kind)
		}
		key := "steam:" + strconv.Itoa(t.SteamAppID)
		if _, _, err := c.Store.Ensure(model.Entry{Key: key, Name: name, Kind: kind, Source: model.SourceSteam, SteamAppID: t.SteamAppID, FirstSeen: c.now()}); err != nil {
			return model.Entry{}, err
		}
		return c.addFolderMatcher(key, g.InstallDir, name, kind)
	case shortcut.TargetEpic:
		g, _ := c.Launchers.EpicApp(t.EpicApp)
		key := "epic:" + strings.ToLower(g.ID)
		if _, _, err := c.Store.Ensure(model.Entry{Key: key, Name: name, Kind: kind, Source: model.SourceEpic, FirstSeen: c.now()}); err != nil {
			return model.Entry{}, err
		}
		return c.addFolderMatcher(key, g.InstallDir, name, kind)
	}
	return model.Entry{}, errors.New("unsupported shortcut")
}

func (c *Core) addFolderMatcher(key, dir, name string, kind model.Kind) (model.Entry, error) {
	exe := biggestExe(dir)
	err := c.Store.Update(key, func(e *model.Entry) {
		if name != "" {
			e.Name = name
		}
		if kind.Valid() {
			e.Kind = kind
		}
		e.Hidden = false
		m := model.Matcher{Type: model.MatchFolder, Value: dir}
		if dir != "" && !hasMatcher(e.Matchers, m) {
			e.Matchers = append(e.Matchers, m)
		}
		if e.ExePath == "" && exe != "" {
			e.ExePath, e.ExeName = exe, filepath.Base(exe)
		}
	})
	if err != nil {
		return model.Entry{}, err
	}
	c.Enqueue(key, true)
	c.Tracker.Wake()
	e, _ := c.Store.Entry(key)
	return e, nil
}

// RunningCandidates lists the processes seen in the last scan that could be
// tracked, one per exe, with what they are detected as right now.
type Candidate struct {
	Process  procs.Process
	Identity detect.Identity
	Tracked  bool
}

// RunningCandidates is used by "Customise > pick from running apps".
func (c *Core) RunningCandidates() []Candidate {
	snap := c.Tracker.LastSnapshot()
	seen := map[string]bool{}
	var out []Candidate
	for _, p := range snap.Procs {
		if p.Path == "" || detect.IsBuiltinIgnored(p.Name) {
			continue
		}
		k := strings.ToLower(p.Path)
		if seen[k] {
			continue
		}
		seen[k] = true
		if !p.HasWindow {
			// only windowed apps are interesting here, plus known games
			if id, ok := c.Detector.Identify(p); !ok || id.Kind != model.KindGame {
				continue
			}
		}
		id, ok := c.Detector.Identify(p)
		cand := Candidate{Process: p, Identity: id, Tracked: ok}
		if ok {
			if e, exists := c.Store.Entry(id.Key); exists {
				cand.Identity.Name = e.DisplayName()
				cand.Identity.Kind = e.Kind
			}
		}
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Process.HasWindow != out[j].Process.HasWindow {
			return out[i].Process.HasWindow
		}
		return strings.ToLower(out[i].Process.Name) < strings.ToLower(out[j].Process.Name)
	})
	return out
}

// SetKind switches an entry between game and app.
func (c *Core) SetKind(key string, kind model.Kind) error {
	if !kind.Valid() {
		return errors.New("bad kind")
	}
	err := c.Store.Update(key, func(e *model.Entry) { e.Kind = kind })
	if err == nil && kind == model.KindGame {
		c.Enqueue(key, false)
	}
	return err
}

// Rename renames an entry.
func (c *Core) Rename(key, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name cannot be empty")
	}
	return c.Store.Update(key, func(e *model.Entry) { e.Name = name })
}

// SetFavorite stars or unstars an entry.
func (c *Core) SetFavorite(key string, fav bool) error {
	return c.Store.Update(key, func(e *model.Entry) { e.Favorite = fav })
}

// SetHidden hides an entry from the library (it is still tracked).
func (c *Core) SetHidden(key string, hidden bool) error {
	return c.Store.Update(key, func(e *model.Entry) { e.Hidden = hidden })
}

// SetNotes saves the journal notes of an entry.
func (c *Core) SetNotes(key, notes string) error {
	return c.Store.Update(key, func(e *model.Entry) { e.Notes = notes })
}

// SetSteamAppID sets (or clears with 0) the Steam app id and refetches art.
func (c *Core) SetSteamAppID(key string, id int) error {
	err := c.Store.Update(key, func(e *model.Entry) {
		e.SteamAppID = id
		e.ArtChecked = time.Time{}
		if !e.CustomPoster {
			e.PosterFile, e.HeroFile = "", ""
		}
		e.Meta = nil
	})
	if err == nil {
		c.Enqueue(key, true)
	}
	return err
}

// RefreshArt refetches the poster/icon of an entry.
func (c *Core) RefreshArt(key string) {
	_ = c.Store.Update(key, func(e *model.Entry) { e.ArtChecked = time.Time{}; e.IconChecked = time.Time{} })
	c.Enqueue(key, true)
}

// SetCustomPoster uses a local image as the poster.
func (c *Core) SetCustomPoster(key, imagePath string) error {
	e, ok := c.Store.Entry(key)
	if !ok {
		return store.ErrNotFound
	}
	img, err := loadImage(imagePath)
	if err != nil {
		return fmt.Errorf("could not read that image: %w", err)
	}
	name := fsutil.SafeName(e.Key) + "_custom_" + strconv.FormatInt(c.now().Unix(), 36) + ".jpg"
	if err := art.SaveJPEG(filepath.Join(c.Store.ArtDir(), name), art.Cover(img, art.PosterW, art.PosterH)); err != nil {
		return err
	}
	return c.Store.Update(key, func(e *model.Entry) { e.PosterFile = name; e.CustomPoster = true })
}

// ClearCustomPoster goes back to the downloaded poster.
func (c *Core) ClearCustomPoster(key string) error {
	err := c.Store.Update(key, func(e *model.Entry) { e.CustomPoster = false; e.PosterFile = ""; e.ArtChecked = time.Time{} })
	if err == nil {
		c.Enqueue(key, true)
	}
	return err
}

// SetCustomIcon uses a local exe/ico/png as the icon.
func (c *Core) SetCustomIcon(key, src string) error {
	e, ok := c.Store.Entry(key)
	if !ok {
		return store.ErrNotFound
	}
	name := fsutil.SafeName(e.Key) + "_custom.png"
	if err := exeinfo.SaveIconPNG(src, filepath.Join(c.Store.IconsDir(), name), 128); err != nil {
		return fmt.Errorf("could not read an icon from that file: %w", err)
	}
	return c.Store.Update(key, func(e *model.Entry) { e.IconFile = name; e.IconChecked = c.now() })
}

// AddManualTime adds playtime by hand (for hours played before installing
// Simpel Playtime, or on another PC).
func (c *Core) AddManualTime(key string, end time.Time, d time.Duration) error {
	if d <= 0 {
		return errors.New("duration must be positive")
	}
	if d > 24*365*time.Hour {
		return errors.New("that is more than a year of playtime, be serious")
	}
	if _, ok := c.Store.Entry(key); !ok {
		return store.ErrNotFound
	}
	if end.IsZero() {
		end = c.now()
	}
	return c.Store.AddSession(model.Session{Key: key, Start: end.Add(-d), End: end, Secs: int64(d.Seconds()), Manual: true})
}

// DeleteEntry removes an entry and its playtime. When ignore is true the exe
// is also put on the ignore list so it does not come straight back.
func (c *Core) DeleteEntry(key string, ignore bool) error {
	e, ok := c.Store.Entry(key)
	if !ok {
		return store.ErrNotFound
	}
	c.Tracker.Forget(e.Key)
	if err := c.Store.Delete(e.Key); err != nil {
		return err
	}
	if ignore && e.ExeName != "" {
		return c.Settings.Update(func(s *settings.Settings) { s.Ignored = append(s.Ignored, e.ExeName) })
	}
	return nil
}

// Merge moves all playtime of from into into.
func (c *Core) Merge(from, into string) error {
	c.Tracker.Forget(c.Store.Resolve(from))
	return c.Store.Merge(from, into)
}

func (c *Core) unignore(exe string) {
	if !c.Settings.Get().IsIgnored(exe) {
		return
	}
	_ = c.Settings.Update(func(s *settings.Settings) {
		kept := s.Ignored[:0]
		for _, i := range s.Ignored {
			if !strings.EqualFold(i, exe) {
				kept = append(kept, i)
			}
		}
		s.Ignored = kept
	})
}

// ApplyAutostart makes the OS startup entry match the setting.
func (c *Core) ApplyAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return autostart.Sync(c.Settings.Get().StartWithWindows, exe)
}
