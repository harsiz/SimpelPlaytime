package ui

import (
	"fmt"
	"image/color"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/api"
	"github.com/harsiz/SimpelPlaytime/internal/core"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
	"github.com/harsiz/SimpelPlaytime/internal/tracker"
)

// page is one screen of the app.
type page interface {
	build() fyne.CanvasObject
	// refresh updates the page after data changed.
	refresh()
	// tick runs every second while the window is visible (live clocks).
	tick()
}

// UI is the desktop app.
type UI struct {
	core *core.Core
	api  *api.Server
	app  fyne.App
	win  fyne.Window

	content  *fyne.Container
	navBtns  map[string]*navButton
	pageName string
	current  page
	history  []string
	detail   string

	visible bool
	quit    bool

	statusDot  *canvas.Circle
	statusText *canvas.Text
	pauseBtn   *widget.Button

	trayMenu  *fyne.Menu
	trayNow   *fyne.MenuItem
	trayPause *fyne.MenuItem

	refreshMu      sync.Mutex
	refreshPending bool
	// noBackground disables refreshes from other goroutines (tests drive
	// refreshes by hand because the test driver runs fyne.Do inline)
	noBackground bool
	liveSig      string

	lib   *libraryState
	stats *statsState
}

// New makes the UI. Call Run on the main goroutine.
func New(c *core.Core, srv *api.Server, a fyne.App) *UI {
	u := &UI{core: c, api: srv, app: a, navBtns: map[string]*navButton{},
		lib: newLibraryState(), stats: newStatsState()}
	a.Settings().SetTheme(purpleTheme{})
	a.SetIcon(Logo)
	u.win = a.NewWindow("Simpel Playtime")
	u.win.SetIcon(Logo)
	u.win.Resize(fyne.NewSize(1240, 800))
	u.win.SetMaster()
	u.win.SetCloseIntercept(func() {
		if u.core.Settings.Get().CloseToTray && u.hasTray() {
			u.Hide()
			return
		}
		u.Quit()
	})
	u.content = container.NewStack()
	u.win.SetContent(container.NewBorder(nil, nil, u.sidebar(), nil, u.content))
	u.setupTray()

	c.OnChange(u.scheduleRefresh)
	c.Tracker.Subscribe(u.onTracker)
	return u
}

func (u *UI) hasTray() bool {
	_, ok := u.app.(desktop.App)
	return ok
}

// Run shows the window (unless hidden) and runs the event loop.
func (u *UI) Run(startHidden bool) {
	if startHidden && u.hasTray() {
		u.visible = false
	} else {
		u.showPage("home")
		u.visible = true
		u.win.Show()
	}
	go u.ticker()
	u.app.Run()
}

// Show brings the window back (safe from any goroutine).
func (u *UI) Show() {
	fyne.Do(func() {
		if !u.visible {
			u.visible = true
			name := u.pageName
			if name == "" {
				name = "home"
			}
			u.showPage(name)
		}
		u.win.Show()
		u.win.RequestFocus()
	})
}

// Hide sends the app to the tray and frees what it can.
func (u *UI) Hide() {
	u.win.Hide()
	u.visible = false
	if u.core.Settings.Get().FreeMemoryWhenHidden {
		u.content.Objects = nil
		u.content.Refresh()
		u.current = nil
		dropResourceCache()
		go func() {
			time.Sleep(500 * time.Millisecond)
			runtime.GC()
			debug.FreeOSMemory()
		}()
	}
}

// Quit stops everything.
func (u *UI) Quit() {
	if u.quit {
		return
	}
	u.quit = true
	u.app.Quit()
}

// ---- refresh plumbing -------------------------------------------------------

func (u *UI) scheduleRefresh() {
	if u.noBackground {
		return
	}
	u.refreshMu.Lock()
	if u.refreshPending {
		u.refreshMu.Unlock()
		return
	}
	u.refreshPending = true
	u.refreshMu.Unlock()
	time.AfterFunc(350*time.Millisecond, func() {
		u.refreshMu.Lock()
		u.refreshPending = false
		u.refreshMu.Unlock()
		fyne.Do(func() {
			if u.visible && u.current != nil {
				u.current.refresh()
			}
			u.updateStatus()
		})
	})
}

func (u *UI) onTracker(e tracker.Event) {
	if u.noBackground {
		return
	}
	st := u.core.Settings.Get()
	switch e.Type {
	case tracker.EventTick:
		sig := liveSignature(u.core.Tracker.Live())
		u.refreshMu.Lock()
		changed := sig != u.liveSig
		u.liveSig = sig
		u.refreshMu.Unlock()
		if changed {
			u.scheduleRefresh()
		}
		fyne.Do(u.updateTray)
	case tracker.EventEnded:
		if st.NotifySessionEnd && e.Kind == model.KindGame {
			u.notify("Session saved", fmt.Sprintf("You played %s for %s", e.Name, stats.FormatHM(float64(e.Session.Secs))))
		}
	case tracker.EventNewEntry:
		if st.NotifyNewGame && e.Kind == model.KindGame {
			u.notify("New game detected", e.Name+" is now being tracked")
		}
	case tracker.EventBreak:
		u.notify("Break time?", fmt.Sprintf("You've been playing %s for %s. Stretch, drink some water, touch some grass.", e.Name, stats.FormatHM(e.Played.Seconds())))
	}
}

func (u *UI) notify(title, body string) {
	fyne.Do(func() { u.app.SendNotification(fyne.NewNotification(title, body)) })
}

func liveSignature(live []tracker.Live) string {
	var b strings.Builder
	for _, l := range live {
		b.WriteString(l.Key)
		if l.Running {
			b.WriteByte('+')
		}
		if l.Idle {
			b.WriteByte('i')
		}
		b.WriteByte(';')
	}
	return b.String()
}

func (u *UI) ticker() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		fyne.Do(func() {
			if u.visible && u.current != nil {
				u.current.tick()
			}
		})
	}
}

// ---- navigation -----------------------------------------------------------

func (u *UI) makePage(name string) page {
	switch name {
	case "library":
		return &libraryPage{u: u, st: u.lib}
	case "stats":
		return &statsPage{u: u, st: u.stats}
	case "settings":
		return &settingsPage{u: u}
	case "detail":
		return &detailPage{u: u, key: u.detail}
	}
	return &homePage{u: u}
}

func (u *UI) showPage(name string) {
	if name != "detail" {
		u.history = nil
	}
	u.pageName = name
	p := u.makePage(name)
	u.current = p
	u.content.Objects = []fyne.CanvasObject{p.build()}
	u.content.Refresh()
	for n, b := range u.navBtns {
		b.setSelected(n == name || (name == "detail" && n == "library"))
	}
	u.updateStatus()
}

// openDetail shows the detail page of an entry.
func (u *UI) openDetail(key string) {
	if u.pageName != "detail" {
		u.history = append(u.history, u.pageName)
	}
	u.detail = key
	prev := u.history
	u.showPage("detail")
	u.history = prev
}

func (u *UI) back() {
	name := "library"
	if len(u.history) > 0 {
		name = u.history[len(u.history)-1]
	}
	u.showPage(name)
}

// navButton is a sidebar item.
type navButton struct {
	widget.BaseWidget
	label    string
	icon     fyne.Resource
	onTap    func()
	selected bool
	hovered  bool
	bg       *canvas.Rectangle
	bar      *canvas.Rectangle
	text     *canvas.Text
	img      *widget.Icon
}

func newNavButton(label string, icon fyne.Resource, onTap func()) *navButton {
	b := &navButton{label: label, icon: icon, onTap: onTap}
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = 8
	b.bar = canvas.NewRectangle(color.Transparent)
	b.bar.CornerRadius = 2
	b.text = txt(label, 13.5, colTextDim, true)
	b.img = widget.NewIcon(theme.NewColoredResource(icon, theme.ColorNamePlaceHolder))
	b.ExtendBaseWidget(b)
	return b
}

func (b *navButton) CreateRenderer() fyne.WidgetRenderer {
	row := container.NewHBox(fixed(4, 22, b.bar), fixed(20, 20, b.img), b.text)
	return widget.NewSimpleRenderer(container.NewStack(b.bg, container.New(layout.NewCustomPaddedLayout(8, 8, 2, 10), row)))
}

func (b *navButton) setSelected(s bool) {
	b.selected = s
	b.restyle()
}

func (b *navButton) restyle() {
	switch {
	case b.selected:
		b.bg.FillColor = colPrimarySoft
		b.bar.FillColor = colPrimary
		b.text.Color = colText
		b.img.SetResource(theme.NewColoredResource(b.icon, theme.ColorNamePrimary))
	case b.hovered:
		b.bg.FillColor = withAlpha(colPrimary, 0x14)
		b.bar.FillColor = color.Transparent
		b.text.Color = colText
		b.img.SetResource(theme.NewColoredResource(b.icon, theme.ColorNameForeground))
	default:
		b.bg.FillColor = color.Transparent
		b.bar.FillColor = color.Transparent
		b.text.Color = colTextDim
		b.img.SetResource(theme.NewColoredResource(b.icon, theme.ColorNamePlaceHolder))
	}
	b.bg.Refresh()
	b.bar.Refresh()
	b.text.Refresh()
}

func (b *navButton) Tapped(*fyne.PointEvent)        { b.onTap() }
func (b *navButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.restyle() }
func (b *navButton) MouseMoved(*desktop.MouseEvent) {}
func (b *navButton) MouseOut()                      { b.hovered = false; b.restyle() }
func (b *navButton) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (u *UI) sidebar() fyne.CanvasObject {
	logo := canvas.NewImageFromResource(Logo)
	logo.FillMode = canvas.ImageFillContain
	title := container.NewVBox(txt("Simpel", 16, colText, true), txt("Playtime", 12, colPrimaryLite, true))
	brand := container.NewHBox(fixed(38, 38, logo), container.NewCenter(title))

	items := []struct {
		name, label string
		icon        fyne.Resource
	}{
		{"home", "Home", theme.HomeIcon()},
		{"library", "Library", appsIcon},
		{"stats", "Stats", chartIcon},
		{"settings", "Settings", theme.SettingsIcon()},
	}
	nav := container.NewVBox()
	for _, it := range items {
		name := it.name
		b := newNavButton(it.label, it.icon, func() { u.showPage(name) })
		u.navBtns[name] = b
		nav.Add(b)
	}

	u.statusDot = canvas.NewCircle(colGood)
	u.statusText = txt("Tracking", 11.5, colTextDim, false)
	u.pauseBtn = widget.NewButtonWithIcon("Pause tracking", theme.MediaPauseIcon(), func() {
		u.core.Tracker.SetPaused(!u.core.Tracker.Paused())
		u.updateStatus()
		u.updateTray()
	})
	status := container.NewVBox(
		container.NewHBox(fixed(9, 9, u.statusDot), u.statusText),
		u.pauseBtn,
		txt("v"+u.core.Version, 10.5, colTextMuted, false),
	)
	bg := canvas.NewRectangle(colSidebar)
	edge := canvas.NewRectangle(colBorder)
	body := container.New(layout.NewCustomPaddedLayout(18, 16, 14, 14),
		container.NewBorder(container.NewVBox(brand, widget.NewSeparator(), nav), status, nil, nil))
	return container.NewStack(bg, container.NewBorder(nil, nil, nil, fixed(1, 1, edge), fixed(210, 0, body)))
}

func (u *UI) updateStatus() {
	if u.statusText == nil {
		return
	}
	live := u.core.Tracker.Live()
	running := 0
	games := 0
	for _, l := range live {
		if l.Running {
			running++
			if l.Kind == model.KindGame {
				games++
			}
		}
	}
	switch {
	case u.core.Tracker.Paused():
		u.statusDot.FillColor = colWarn
		u.statusText.Text = "Paused, nothing is counted"
		u.pauseBtn.SetText("Resume tracking")
		u.pauseBtn.SetIcon(theme.MediaPlayIcon())
		u.pauseBtn.Importance = widget.HighImportance
	default:
		u.statusDot.FillColor = colGood
		u.statusText.Text = fmt.Sprintf("Tracking, %d open (%d games)", running, games)
		u.pauseBtn.SetText("Pause tracking")
		u.pauseBtn.SetIcon(theme.MediaPauseIcon())
		u.pauseBtn.Importance = widget.MediumImportance
	}
	if err := u.core.Tracker.LastError(); err != nil {
		u.statusDot.FillColor = colBad
		u.statusText.Text = "Scan error: " + truncate(err.Error(), 30)
	}
	u.statusDot.Refresh()
	u.statusText.Refresh()
	u.pauseBtn.Refresh()
}

// ---- tray -------------------------------------------------------------------

func (u *UI) setupTray() {
	desk, ok := u.app.(desktop.App)
	if !ok {
		return
	}
	u.trayNow = fyne.NewMenuItem("Nothing playing", nil)
	u.trayNow.Disabled = true
	u.trayPause = fyne.NewMenuItem("Pause tracking", func() {
		u.core.Tracker.SetPaused(!u.core.Tracker.Paused())
		u.updateTray()
		u.updateStatus()
	})
	open := fyne.NewMenuItem("Open Simpel Playtime", u.Show)
	stats := fyne.NewMenuItem("Stats", func() { u.Show(); fyne.Do(func() { u.showPage("stats") }) })
	quit := fyne.NewMenuItem("Quit", u.Quit)
	quit.IsQuit = true
	u.trayMenu = fyne.NewMenu("Simpel Playtime", open, stats, fyne.NewMenuItemSeparator(), u.trayNow, u.trayPause, fyne.NewMenuItemSeparator(), quit)
	desk.SetSystemTrayMenu(u.trayMenu)
	desk.SetSystemTrayIcon(Logo)
	desk.SetSystemTrayWindow(u.win)
}

func (u *UI) updateTray() {
	if u.trayMenu == nil {
		return
	}
	var games []tracker.Live
	for _, l := range u.core.Tracker.Live() {
		if l.Running && l.Kind == model.KindGame {
			games = append(games, l)
		}
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Start.Before(games[j].Start) })
	label := "Nothing playing"
	if len(games) > 0 {
		label = fmt.Sprintf("Playing %s (%s)", truncate(games[0].Name, 28), stats.FormatHM(float64(games[0].Secs)))
		if len(games) > 1 {
			label += fmt.Sprintf(" +%d", len(games)-1)
		}
	}
	pause := "Pause tracking"
	if u.core.Tracker.Paused() {
		pause = "Resume tracking"
	}
	if label != u.trayNow.Label || pause != u.trayPause.Label {
		u.trayNow.Label = label
		u.trayPause.Label = pause
		u.trayMenu.Refresh()
	}
}

// ---- small shared helpers -----------------------------------------------

func (u *UI) weekStart() time.Weekday {
	if u.core.Settings.Get().WeekStartsMonday {
		return time.Monday
	}
	return time.Sunday
}

func (u *UI) fmtDur(secs float64) string {
	return stats.Format(secs, u.core.Settings.Get().TimeFormat)
}

func (u *UI) iconPath(e model.Entry) string   { return u.core.Store.IconPath(e) }
func (u *UI) posterPath(e model.Entry) string { return u.core.Store.PosterPath(e) }

func (u *UI) showError(err error) {
	if err == nil {
		return
	}
	showErrorDialog(u.win, err)
}

func (u *UI) settings() settings.Settings { return u.core.Settings.Get() }
