package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
)

var detailRanges = []string{"Last 30 days", "Last 90 days", "Last 12 months", "All time"}

type detailPage struct {
	u       *UI
	key     string
	box     *fyne.Container
	notes   *widget.Entry
	saved   string
	lives   []liveText
	rangeID string
	chart   *barChart
}

func (p *detailPage) build() fyne.CanvasObject {
	p.box = container.NewVBox()
	p.rangeID = "Last 30 days"
	p.notes = widget.NewMultiLineEntry()
	p.notes.SetPlaceHolder("Journal: thoughts, progress, builds, whatever. Saved automatically.")
	p.notes.SetMinRowsVisible(4)
	p.notes.Wrapping = fyne.TextWrapWord
	if e, ok := p.u.core.Store.Entry(p.key); ok {
		p.notes.SetText(e.Notes)
		p.saved = e.Notes
	}
	p.notes.OnChanged = func(string) {
		text := p.notes.Text
		time.AfterFunc(800*time.Millisecond, func() {
			fyne.Do(func() {
				if p.notes.Text == text && text != p.saved {
					p.saved = text
					p.u.showError(p.u.core.SetNotes(p.key, text))
				}
			})
		})
	}
	p.refresh()
	return container.NewVScroll(container.New(layout.NewCustomPaddedLayout(18, 28, 28, 28), p.box))
}

func (p *detailPage) tick() {
	for _, l := range p.lives {
		lv, ok := p.u.core.Tracker.LiveFor(l.key)
		if !ok {
			continue
		}
		secs := liveSecs(p.u, lv)
		s := stats.FormatClock(secs)
		if l.total {
			s = p.u.fmtDur(float64(l.base + secs))
		}
		if l.text.Text != s {
			l.text.Text = s
			l.text.Refresh()
		}
	}
}

func (p *detailPage) refresh() {
	u := p.u
	e, ok := u.core.Store.Entry(p.key)
	if !ok {
		p.box.Objects = []fyne.CanvasObject{
			widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), u.back),
			txt("This entry does not exist anymore.", 15, colTextDim, false),
		}
		p.box.Refresh()
		return
	}
	p.key = e.Key
	p.lives = nil
	t := u.core.TotalFor(e.Key)
	live, isLive := u.core.Tracker.LiveFor(e.Key)
	running := isLive && live.Running
	sessions := u.core.Sessions(true)
	only := func(k string) bool { return k == e.Key }
	now := time.Now()
	sum := stats.Summarize(sessions, only, now, u.weekStart())

	back := widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), u.back)
	back.Importance = widget.LowImportance

	// header
	star := newStar(e.Favorite, func(on bool) { u.showError(u.core.SetFavorite(e.Key, on)) })
	customise := widget.NewButtonWithIcon("Customise", tuneIcon, nil)
	customise.OnTapped = func() { showEntryCustomise(u, e, customise) }
	topRow := container.NewHBox()
	if running {
		topRow.Add(chip("NOW PLAYING", theme.MediaPlayIcon(), colGoodSoft, rgb(0x6c, 0xe0, 0x6c)))
	} else {
		kind := "GAME"
		if e.Kind == model.KindApp {
			kind = "APP"
		}
		topRow.Add(chip(kind, nil, withAlpha(colPrimary, 0x40), colPrimaryLite))
	}
	if e.Hidden {
		topRow.Add(chip("HIDDEN", theme.VisibilityOffIcon(), colSurface2, colTextDim))
	}
	topRow.Add(layout.NewSpacer())
	topRow.Add(fixed(30, 30, container.NewPadded(star)))
	topRow.Add(customise)

	title := txt(truncate(e.DisplayName(), 44), 32, colText, true)
	info := container.NewVBox(topRow, title)
	if m := metaLine(e); m != "" {
		info.Add(txt(m, 12.5, colTextDim, false))
	}
	chips := container.NewHBox(chip(e.Source.Label(), nil, withAlpha(colPrimary, 0x40), colPrimaryLite))
	if e.ExeName != "" {
		chips.Add(chip(e.ExeName, nil, colSurface2, colTextDim))
	}
	if e.SteamAppID != 0 {
		chips.Add(chip(fmt.Sprintf("Steam %d", e.SteamAppID), nil, colSurface2, colTextDim))
	}
	info.Add(chips)

	sessionVal := mono("-", 26, colTextMuted)
	if running {
		sessionVal = mono(stats.FormatClock(liveSecs(u, live)), 26, colPrimaryLite)
		p.lives = append(p.lives, liveText{key: e.Key, text: sessionVal})
	}
	totalVal := mono(u.fmtDur(float64(t.Secs)), 26, colText)
	if running {
		p.lives = append(p.lives, liveText{key: e.Key, text: totalVal, total: true, base: t.Secs - live.Secs})
	}
	strip := container.NewGridWithColumns(3,
		statCell("This session", sessionVal, theme.MediaPlayIcon()),
		statCell("Total playtime", totalVal, theme.NewColoredResource(clockIcon, theme.ColorNameForeground)),
		statCell("Sessions", mono(fmt.Sprint(t.Sessions), 26, colText), theme.NewColoredResource(chartIcon, theme.ColorNameForeground)),
	)
	poster := posterImage(u.posterPath(e), u.iconPath(e), e.DisplayName(), 170, 255)
	header := heroBackground(u.core.Store.HeroPath(e), container.New(layout.NewCustomPaddedLayout(18, 18, 18, 18),
		container.NewBorder(nil, nil, poster, nil, container.NewBorder(nil, panelColor(strip, withAlpha(colBackground, 0xb0)), nil, nil, container.NewPadded(info)))))

	// tiles
	first := "-"
	if !t.First.IsZero() {
		first = t.First.Format("Jan 2, 2006")
	}
	last := "-"
	lastT := e.LastPlayed
	if t.Last.After(lastT) {
		lastT = t.Last
	}
	if running {
		last = "right now"
	} else if !lastT.IsZero() {
		last = stats.Ago(lastT, now)
	}
	focus := "-"
	if t.Secs > 0 {
		focus = fmt.Sprintf("%d%%", int(math.Round(t.FocusRatio()*100)))
	}
	tiles := container.NewGridWithColumns(4,
		newStatTile("Average session", u.fmtDur(t.Average()), "", nil, colText).obj,
		newStatTile("Longest session", u.fmtDur(float64(t.Longest)), "", nil, colText).obj,
		newStatTile("This week", u.fmtDur(sum.Week), "today "+u.fmtDur(sum.Today), nil, colText).obj,
		newStatTile("This month", u.fmtDur(sum.Month), "year "+u.fmtDur(sum.Year), nil, colText).obj,
		newStatTile("First played", first, "added "+e.FirstSeen.Format("Jan 2, 2006"), nil, colText).obj,
		newStatTile("Last played", last, "", nil, colText).obj,
		newStatTile("In focus", focus, "of the time it ran", nil, colText).obj,
		newStatTile("Streak", fmt.Sprintf("%d days", sum.CurrentStreak), fmt.Sprintf("best %d days", sum.LongestStreak), nil, colText).obj,
	)

	// chart
	rangeSel := widget.NewSelect(detailRanges, nil)
	rangeSel.SetSelected(p.rangeID)
	rangeSel.OnChanged = func(s string) { p.rangeID = s; p.updateChart(sessions, only) }
	p.chart = newBarChart(200)
	p.updateChart(sessions, only)
	chartBox := panel(container.NewBorder(container.NewBorder(nil, nil, txt("PLAYTIME", 11, colTextDim, true), fixed(170, 34, rangeSel)), nil, nil, nil, p.chart))

	// sessions
	mine := u.core.Store.SessionsFor(e.Key)
	list := container.NewVBox()
	if running {
		list.Add(sessionRow(u, model.Session{Start: live.Start, End: now, Secs: liveSecs(u, live)}, true, nil))
	}
	shown := 0
	for i := len(mine) - 1; i >= 0 && shown < 30; i-- {
		s := mine[i]
		list.Add(sessionRow(u, s, false, func() {
			dialog.ShowConfirm("Delete session?", fmt.Sprintf("Remove %s from %s?", u.fmtDur(float64(s.Secs)), s.Start.Format("Jan 2 15:04")), func(ok bool) {
				if ok {
					u.showError(u.core.Store.DeleteSession(s.ID))
				}
			}, u.win)
		}))
		shown++
	}
	if len(mine) == 0 && !running {
		list.Add(dimLabel("No sessions yet."))
	}
	if len(mine) > shown {
		list.Add(dimLabel(fmt.Sprintf("+ %d older sessions (get them all from the API or a CSV export)", len(mine)-shown)))
	}
	addTime := widget.NewButtonWithIcon("Add time", theme.ContentAddIcon(), func() { manualTimeDialog(u, e) })
	addTime.Importance = widget.LowImportance
	sessionsBox := panel(container.NewVBox(container.NewBorder(nil, nil, txt("SESSIONS", 11, colTextDim, true), addTime), list))

	// tracking info
	how := container.NewVBox(txt("TRACKED BY", 11, colTextDim, true))
	how.Add(dimLabel(trackingExplain(u, e)))
	for _, m := range e.Matchers {
		desc := map[string]string{model.MatchExe: "Any exe named", model.MatchPath: "Exactly", model.MatchFolder: "Anything inside"}[m.Type]
		line := desc + " " + m.Value
		if m.Title != "" {
			line += " (window title matches " + m.Title + ")"
		}
		how.Add(widget.NewLabel(line))
	}
	if e.ExePath != "" {
		l := dimLabel("Last seen at " + e.ExePath)
		l.Wrapping = fyne.TextWrapBreak
		how.Add(l)
	}
	journal := panel(container.NewVBox(txt("JOURNAL", 11, colTextDim, true), p.notes))
	right := container.NewVBox(journal)
	if m := e.Meta; m != nil && (m.Description != "" || len(m.Developers) > 0) {
		about := container.NewVBox(txt("ABOUT", 11, colTextDim, true))
		if m.Description != "" {
			about.Add(wrapLabel(m.Description))
		}
		if len(m.Developers) > 0 {
			about.Add(factRow("Developer", strings.Join(m.Developers, ", ")))
		}
		if len(m.Publishers) > 0 {
			about.Add(factRow("Publisher", strings.Join(m.Publishers, ", ")))
		}
		if m.ReleaseDate != "" {
			about.Add(factRow("Released", m.ReleaseDate))
		}
		right.Add(panel(about))
	}
	right.Add(panel(how))

	p.box.Objects = []fyne.CanvasObject{
		container.NewHBox(back), header, spacer(4), tiles, chartBox,
		container.NewGridWithColumns(2, sessionsBox, right),
	}
	p.box.Refresh()
}

func (p *detailPage) updateChart(sessions []model.Session, only func(string) bool) {
	ids := map[string]string{"Last 30 days": "30d", "Last 90 days": "90d", "Last 12 months": "12m", "All time": "all"}
	var first time.Time
	for _, s := range sessions {
		if only(s.Key) {
			first = s.Start
			break
		}
	}
	from, to, g := stats.Resolve(ids[p.rangeID], time.Now(), first)
	b := stats.Buckets(sessions, from, to, g, p.u.weekStart(), only)
	labels, tips := make([]string, len(b)), make([]string, len(b))
	values := make([][]float64, len(b))
	for i, x := range b {
		labels[i] = stats.BucketLabel(x.Start, g)
		tips[i] = bucketTip(x, g)
		values[i] = []float64{x.Secs / 3600}
	}
	p.chart.SetData(labels, tips, values, []series{{Name: "Playtime", Color: colPrimary}})
}

func bucketTip(b stats.Bucket, g stats.Granularity) string {
	switch g {
	case stats.Week:
		return "Week of " + b.Start.Format("Jan 2")
	case stats.Month:
		return b.Start.Format("January 2006")
	}
	return b.Start.Format("Mon, Jan 2")
}

func sessionRow(u *UI, s model.Session, live bool, onDelete func()) fyne.CanvasObject {
	when := s.Start.Format("Mon Jan 2") + "   " + s.Start.Format("15:04") + " - " + s.End.Format("15:04")
	if live {
		when = "Now   started " + s.Start.Format("15:04")
	}
	left := container.NewHBox(widget.NewLabel(when))
	if s.Manual {
		left.Add(chip("manual", nil, colSurface2, colTextDim))
	}
	if live {
		left.Add(chip("live", nil, colGoodSoft, rgb(0x6c, 0xe0, 0x6c)))
	}
	dur := txt(u.fmtDur(float64(s.Secs)), 13, colText, true)
	right := container.NewHBox(container.NewCenter(dur))
	if onDelete != nil {
		del := widget.NewButtonWithIcon("", theme.DeleteIcon(), onDelete)
		del.Importance = widget.LowImportance
		right.Add(del)
	}
	return container.NewBorder(nil, nil, nil, right, left)
}

func trackingExplain(u *UI, e model.Entry) string {
	switch e.Source {
	case model.SourceDatabase:
		var exes []string
		if db := u.core.DB.Get(strings.TrimPrefix(e.Key, "db:")); db != nil {
			exes = db.Exes()
		}
		if len(exes) > 0 {
			return "Built in database: " + strings.Join(exes, ", ")
		}
		return "Built in database"
	case model.SourceSteam:
		return "Steam library (app manifest)"
	case model.SourceEpic:
		return "Epic Games library (install manifest)"
	case model.SourceHeuristic:
		return "Lives in a game library folder"
	case model.SourceAuto:
		return "Detected automatically (an app with a window)"
	}
	return "Your custom rules"
}
