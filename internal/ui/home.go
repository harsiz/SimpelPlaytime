package ui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
	"github.com/harsiz/SimpelPlaytime/internal/tracker"
)

// liveText is a label that follows a running session every second.
type liveText struct {
	key   string
	text  *canvas.Text
	total bool // show total playtime instead of the session clock
	base  int64
}

type homePage struct {
	u     *UI
	box   *fyne.Container
	lives []liveText
}

func (p *homePage) build() fyne.CanvasObject {
	p.box = container.NewVBox()
	p.refresh()
	return container.NewVScroll(container.New(layout.NewCustomPaddedLayout(22, 28, 28, 28), p.box))
}

func (p *homePage) tick() {
	for _, l := range p.lives {
		lv, ok := p.u.core.Tracker.LiveFor(l.key)
		if !ok {
			continue
		}
		secs := liveSecs(p.u, lv)
		var s string
		if l.total {
			s = p.u.fmtDur(float64(l.base + secs))
		} else {
			s = stats.FormatClock(secs)
		}
		if l.text.Text != s {
			l.text.Text = s
			l.text.Refresh()
		}
	}
}

// liveSecs extrapolates a running session between polls so clocks tick
// smoothly every second.
func liveSecs(u *UI, l tracker.Live) int64 {
	secs := l.Secs
	if l.Running && !l.Idle && !u.core.Tracker.Paused() {
		extra := time.Since(l.LastSeen)
		maxExtra := time.Duration(u.settings().PollSeconds) * 2 * time.Second
		if extra > maxExtra {
			extra = maxExtra
		}
		if extra > 0 {
			secs += int64(extra.Seconds())
		}
	}
	return secs
}

func greeting(now time.Time) string {
	switch h := now.Hour(); {
	case h < 5:
		return "Still up?"
	case h < 12:
		return "Good morning"
	case h < 18:
		return "Good afternoon"
	}
	return "Good evening"
}

func (p *homePage) refresh() {
	u := p.u
	p.lives = nil
	now := time.Now()
	sessions := u.core.Sessions(true)
	games := u.core.KindFilter("game")
	sum := stats.Summarize(sessions, games, now, u.weekStart())
	totals := u.core.Totals()

	objs := []fyne.CanvasObject{}
	head := container.NewVBox(
		txt(greeting(now), 26, colText, true),
		txt(fmt.Sprintf("%s  |  %s of games today, %s this week", now.Format("Monday, January 2"), u.fmtDur(sum.Today), u.fmtDur(sum.Week)), 12.5, colTextDim, false),
	)
	objs = append(objs, head, spacer(10))

	// now playing
	objs = append(objs, sectionTitle("Now playing"))
	var games0, apps []tracker.Live
	for _, l := range u.core.Tracker.Live() {
		if !l.Running {
			continue
		}
		if l.Kind == model.KindGame {
			games0 = append(games0, l)
		} else {
			apps = append(apps, l)
		}
	}
	if len(games0) == 0 {
		objs = append(objs, p.nothingPlaying())
	}
	for i, l := range games0 {
		if i >= 3 {
			break
		}
		e, ok := u.core.Store.Entry(l.Key)
		if !ok {
			continue
		}
		objs = append(objs, p.nowPlayingCard(e, l, totals[l.Key]))
	}
	if len(apps) > 0 {
		objs = append(objs, p.openApps(apps, totals))
	}

	// quick stats
	objs = append(objs, spacer(6), sectionTitle("At a glance"))
	most, mostSub := "-", "play something"
	if top := stats.Top(sessions, stats.Align(now, stats.Week, u.weekStart()), time.Time{}, games, 1); len(top) > 0 {
		if e, ok := u.core.Store.Entry(top[0].Key); ok {
			most, mostSub = truncate(e.DisplayName(), 16), u.fmtDur(top[0].Secs)+" this week"
		}
	}
	streakSub := "days in a row"
	if sum.CurrentStreak == 1 {
		streakSub = "day, keep it going"
	}
	tiles := container.NewGridWithColumns(4,
		newStatTile("Today", u.fmtDur(sum.Today), "vs "+u.fmtDur(sum.Yesterday)+" yesterday", theme.NewColoredResource(clockIcon, theme.ColorNamePrimary), colText).obj,
		newStatTile("This week", u.fmtDur(sum.Week), "last week "+u.fmtDur(sum.LastWeek), theme.NewColoredResource(chartIcon, theme.ColorNamePrimary), colText).obj,
		newStatTile("Streak", fmt.Sprintf("%d", sum.CurrentStreak), streakSub, theme.NewColoredResource(fireIcon, theme.ColorNamePrimary), colText).obj,
		newStatTile("Top game this week", most, mostSub, theme.NewColoredResource(trophyIcon, theme.ColorNamePrimary), colText).obj,
	)
	objs = append(objs, tiles)

	// recently played
	recent := p.recentEntries(totals)
	if len(recent) > 0 {
		objs = append(objs, spacer(6), sectionTitle("Recently played"))
		row := container.NewHBox()
		for _, e := range recent {
			row.Add(p.miniCard(e, totals[e.Key]))
		}
		objs = append(objs, container.NewHScroll(row))
	}

	// last 7 days
	objs = append(objs, spacer(6), sectionTitle("Last 7 days"))
	from, to, _ := stats.Resolve("7d", now, time.Time{})
	buckets := stats.Buckets(sessions, from, to, stats.Day, u.weekStart(), games)
	chart := newBarChart(170)
	labels, tips := make([]string, len(buckets)), make([]string, len(buckets))
	values := make([][]float64, len(buckets))
	for i, b := range buckets {
		labels[i] = b.Start.Format("Mon")
		tips[i] = b.Start.Format("Mon Jan 2")
		values[i] = []float64{b.Secs / 3600}
	}
	chart.SetData(labels, tips, values, []series{{Name: "Games", Color: colPrimary}})
	objs = append(objs, panel(chart))

	p.box.Objects = objs
	p.box.Refresh()
}

func spacer(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(1, h))
	return r
}

func (p *homePage) nothingPlaying() fyne.CanvasObject {
	u := p.u
	ic := widget.NewIcon(theme.NewColoredResource(gamepadIcon, theme.ColorNamePrimary))
	msg := container.NewVBox(
		txt("Nothing running rn", 17, colText, true),
		wrapLabel("Launch a game and it shows up here automatically. Simpel Playtime knows "+fmt.Sprint(u.core.DB.Count(model.KindGame))+" games out of the box, finds Steam and Epic installs, and you can track anything else with Customise."),
	)
	btn := widget.NewButtonWithIcon("Customise", tuneIcon, func() { showCustomiseMenu(u, nil) })
	btn.Importance = widget.HighImportance
	return panel(container.NewBorder(nil, nil, fixed(56, 56, ic), container.NewCenter(btn), msg))
}

// nowPlayingCard is the big hero card of a running game.
func (p *homePage) nowPlayingCard(e model.Entry, l tracker.Live, t stats.Totals) fyne.CanvasObject {
	u := p.u
	clock := mono(stats.FormatClock(liveSecs(u, l)), 34, colPrimaryLite)
	base := t.Secs - l.Secs
	total := mono(u.fmtDur(float64(t.Secs)), 24, colText)
	p.lives = append(p.lives, liveText{key: l.Key, text: clock}, liveText{key: l.Key, text: total, total: true, base: base})

	status := chip("NOW PLAYING", theme.MediaPlayIcon(), colGoodSoft, rgb(0x6c, 0xe0, 0x6c))
	if l.Idle {
		status = chip("IDLE, PAUSED", theme.MediaPauseIcon(), withAlpha(colWarn, 0x30), colWarn)
	} else if u.core.Tracker.Paused() {
		status = chip("TRACKING PAUSED", theme.MediaPauseIcon(), withAlpha(colWarn, 0x30), colWarn)
	}
	title := txt(truncate(e.DisplayName(), 40), 30, colText, true)
	meta := metaLine(e)
	chips := container.NewHBox(chip(e.Source.Label(), nil, withAlpha(colPrimary, 0x40), colPrimaryLite))
	if e.ExeName != "" {
		chips.Add(chip(e.ExeName, nil, colSurface2, colTextDim))
	}
	if l.Focused {
		chips.Add(chip("In focus", nil, colSurface2, colTextDim))
	}
	details := widget.NewButton("Details", func() { u.openDetail(e.Key) })
	right := container.NewVBox(container.NewHBox(status, layout.NewSpacer(), details), title)
	if meta != "" {
		right.Add(txt(meta, 12.5, colTextDim, false))
	}
	right.Add(chips)

	strip := container.NewGridWithColumns(3,
		statCell("This session", clock, theme.MediaPlayIcon()),
		statCell("Total playtime", total, theme.NewColoredResource(clockIcon, theme.ColorNameForeground)),
		statCell("Sessions", mono(fmt.Sprint(t.Sessions), 24, colText), theme.NewColoredResource(chartIcon, theme.ColorNameForeground)),
	)
	stripBg := canvas.NewRectangle(withAlpha(rgb(0x0b, 0x08, 0x11), 0xb0))
	stripBg.CornerRadius = 10
	stripBg.StrokeColor = colBorder
	stripBg.StrokeWidth = 1
	stripBox := container.NewStack(stripBg, container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16), strip))

	poster := posterImage(u.posterPath(e), u.iconPath(e), e.DisplayName(), 150, 225)
	body := container.NewBorder(nil, nil, poster, nil, container.NewBorder(nil, stripBox, nil, nil, container.NewPadded(right)))
	return heroBackground(u.core.Store.HeroPath(e), container.New(layout.NewCustomPaddedLayout(18, 18, 18, 18), body))
}

// heroBackground puts the game's hero art behind content, faded to the left
// so text stays readable.
func heroBackground(heroPath string, content fyne.CanvasObject) fyne.CanvasObject {
	base := canvas.NewRectangle(colSurface)
	base.CornerRadius = 14
	base.StrokeColor = colBorder
	base.StrokeWidth = 1
	layers := []fyne.CanvasObject{base}
	if res := fileResource(heroPath); res != nil {
		img := canvas.NewImageFromResource(res)
		img.FillMode = canvas.ImageFillCover
		img.CornerRadius = 14
		img.Translucency = 0.45
		fade := canvas.NewHorizontalGradient(withAlpha(colSurface, 0xff), withAlpha(colSurface, 0x55))
		dark := canvas.NewRectangle(withAlpha(colBackground, 0x40))
		dark.CornerRadius = 14
		layers = append(layers, img, fade, dark)
	}
	return container.NewStack(append(layers, content)...)
}

func statCell(title string, value *canvas.Text, icon fyne.Resource) fyne.CanvasObject {
	head := container.NewHBox(fixed(13, 13, widget.NewIcon(icon)), txt(strings.ToUpper(title), 10.5, colTextDim, true))
	return container.NewVBox(head, value)
}

// metaLine is "2024 | Action, RPG | Supergiant Games".
func metaLine(e model.Entry) string {
	if e.Meta == nil {
		if e.Kind == model.KindApp {
			return "App"
		}
		return ""
	}
	var parts []string
	if e.Meta.Year > 0 {
		parts = append(parts, fmt.Sprint(e.Meta.Year))
	}
	if len(e.Meta.Genres) > 0 {
		g := e.Meta.Genres
		if len(g) > 3 {
			g = g[:3]
		}
		parts = append(parts, strings.Join(g, ", "))
	}
	if len(e.Meta.Developers) > 0 {
		parts = append(parts, e.Meta.Developers[0])
	}
	return strings.Join(parts, "  |  ")
}

func (p *homePage) openApps(apps []tracker.Live, totals map[string]stats.Totals) fyne.CanvasObject {
	u := p.u
	list := container.NewVBox(txt("ALSO OPEN", 10.5, colTextDim, true))
	sort.Slice(apps, func(i, j int) bool { return apps[i].Secs > apps[j].Secs })
	for i, l := range apps {
		if i >= 6 {
			list.Add(txt(fmt.Sprintf("+ %d more", len(apps)-i), 11.5, colTextMuted, false))
			break
		}
		e, ok := u.core.Store.Entry(l.Key)
		if !ok {
			continue
		}
		clock := mono(stats.FormatClock(liveSecs(u, l)), 13, colPrimaryLite)
		p.lives = append(p.lives, liveText{key: l.Key, text: clock})
		name := txt(truncate(e.DisplayName(), 40), 13, colText, false)
		sub := txt("total "+u.fmtDur(float64(totals[l.Key].Secs)), 11, colTextMuted, false)
		if l.Idle {
			sub.Text = "idle, not counting"
		}
		key := l.Key
		row := newTapCard(container.New(layout.NewCustomPaddedLayout(6, 6, 10, 10),
			container.NewBorder(nil, nil, iconImage(u.iconPath(e), e.DisplayName(), 24), clock, container.NewHBox(name, sub))),
			func() { u.openDetail(key) })
		row.baseColor = colSurface
		list.Add(row)
	}
	return panel(list)
}

func (p *homePage) recentEntries(totals map[string]stats.Totals) []model.Entry {
	var out []model.Entry
	for _, e := range p.u.core.Store.Entries() {
		if e.Hidden || e.Kind != model.KindGame {
			continue
		}
		last := e.LastPlayed
		if t := totals[e.Key].Last; t.After(last) {
			last = t
		}
		if last.IsZero() {
			continue
		}
		e.LastPlayed = last
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastPlayed.After(out[j].LastPlayed) })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func (p *homePage) miniCard(e model.Entry, t stats.Totals) fyne.CanvasObject {
	u := p.u
	poster := posterImage(u.posterPath(e), u.iconPath(e), e.DisplayName(), 120, 180)
	name := txt(truncate(e.DisplayName(), 17), 12.5, colText, true)
	sub := txt(u.fmtDur(float64(t.Secs))+"  |  "+stats.Ago(e.LastPlayed, time.Now()), 10.5, colTextMuted, false)
	key := e.Key
	card := newTapCard(container.New(layout.NewCustomPaddedLayout(8, 8, 8, 8), container.NewVBox(poster, name, sub)), func() { u.openDetail(key) })
	return card
}
