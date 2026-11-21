package ui

import (
	"fmt"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
)

var statScopes = []string{"Games", "Apps", "Everything"}
var statGroups = []string{"Auto", "Daily", "Weekly", "Monthly"}

type statsState struct {
	scope string
	rng   string
	group string
}

func newStatsState() *statsState {
	return &statsState{scope: "Games", rng: "Last 30 days", group: "Auto"}
}

type statsPage struct {
	u   *UI
	st  *statsState
	box *fyne.Container
}

func (p *statsPage) build() fyne.CanvasObject {
	labels := make([]string, len(stats.Ranges))
	for i, r := range stats.Ranges {
		labels[i] = r.Label
	}
	scope := widget.NewSelect(statScopes, func(s string) { p.st.scope = s; p.refresh() })
	scope.SetSelected(p.st.scope)
	rng := widget.NewSelect(labels, func(s string) { p.st.rng = s; p.refresh() })
	rng.SetSelected(p.st.rng)
	group := widget.NewSelect(statGroups, func(s string) { p.st.group = s; p.refresh() })
	group.SetSelected(p.st.group)
	filters := container.NewHBox(fixed(140, 36, scope), fixed(170, 36, rng), fixed(130, 36, group))
	header := container.NewBorder(nil, nil, txt("Stats", 26, colText, true), filters)
	p.box = container.NewVBox()
	p.refresh()
	return container.NewVScroll(container.New(layout.NewCustomPaddedLayout(22, 28, 28, 28), container.NewVBox(header, p.box)))
}

func (p *statsPage) tick() {}

func (p *statsPage) rangeID() string {
	for _, r := range stats.Ranges {
		if r.Label == p.st.rng {
			return r.ID
		}
	}
	return "30d"
}

func (p *statsPage) refresh() {
	if p.box == nil {
		return
	}
	u := p.u
	now := time.Now()
	scopeKind := map[string]string{"Games": "game", "Apps": "app", "Everything": "all"}[p.st.scope]
	filter := u.core.KindFilter(scopeKind)
	all := u.core.Sessions(true)
	var first time.Time
	for _, s := range all {
		if filter == nil || filter(s.Key) {
			first = s.Start
			break
		}
	}
	from, to, g := stats.Resolve(p.rangeID(), now, first)
	switch p.st.group {
	case "Daily":
		g = stats.Day
	case "Weekly":
		g = stats.Week
	case "Monthly":
		g = stats.Month
	}
	clipped := stats.Clip(all, from, to)
	sum := stats.Summarize(clipped, filter, now, u.weekStart())
	allSum := stats.Summarize(all, filter, now, u.weekStart())
	names := map[string]model.Entry{}
	for _, e := range u.core.Store.Entries() {
		names[e.Key] = e
	}
	nameOf := func(k string) string {
		if e, ok := names[k]; ok {
			return e.DisplayName()
		}
		return k
	}

	days := to.Sub(from).Hours() / 24
	perDay := 0.0
	if days > 0 {
		perDay = sum.Total / days
	}
	longest := "-"
	if sum.Longest.Secs > 0 {
		longest = truncate(nameOf(sum.Longest.Key), 18) + ", " + sum.Longest.Start.Format("Jan 2")
	}
	best := "-"
	if sum.BestDay != "" {
		if t, err := time.Parse("2006-01-02", sum.BestDay); err == nil {
			best = t.Format("Mon, Jan 2 2006")
		}
	}
	top := "-"
	if sum.TopKey != "" {
		top = truncate(nameOf(sum.TopKey), 16)
	}
	fav := "-"
	if sum.FavWeekday != "" {
		fav = sum.FavWeekday + "s"
	}
	peak := "-"
	if sum.Total > 0 {
		peak = stats.HourLabel(sum.FavHour)
	}
	ic := func(r fyne.Resource) fyne.Resource { return theme.NewColoredResource(r, theme.ColorNamePrimary) }
	tiles := container.NewGridWithColumns(4,
		newStatTile("Total", u.fmtDur(sum.Total), p.st.rng, ic(clockIcon), colPrimaryLite).obj,
		newStatTile("Per day", u.fmtDur(perDay), fmt.Sprintf("%s on days you played", u.fmtDur(sum.DailyAverage)), ic(chartIcon), colText).obj,
		newStatTile("Sessions", fmt.Sprint(sum.Sessions), "avg "+u.fmtDur(sum.AvgSession), ic(theme.MediaPlayIcon()), colText).obj,
		newStatTile("Longest session", u.fmtDur(float64(sum.Longest.Secs)), longest, ic(boltIcon), colText).obj,
		newStatTile("Current streak", fmt.Sprintf("%d days", allSum.CurrentStreak), fmt.Sprintf("best ever %d days", allSum.LongestStreak), ic(fireIcon), colText).obj,
		newStatTile("Most played", top, u.fmtDur(sum.TopSecs), ic(trophyIcon), colText).obj,
		newStatTile("Best day", u.fmtDur(sum.BestDaySecs), best, ic(theme.CalendarIcon()), colText).obj,
		newStatTile("Played", fmt.Sprintf("%d", sum.UniqueEntries), fmt.Sprintf("different %s, %d active days", lowerScope(p.st.scope), sum.ActiveDays), ic(gamepadIcon), colText).obj,
		newStatTile("Favourite day", fav, "the day you play most", ic(theme.CalendarIcon()), colText).obj,
		newStatTile("Peak hour", peak, "when you usually play", ic(clockIcon), colText).obj,
		newStatTile("Night owl", fmt.Sprintf("%d%%", int(math.Round(sum.NightOwlShare*100))), "of time between 0:00 and 5:00", ic(moonIcon), colText).obj,
		newStatTile("In focus", fmt.Sprintf("%d%%", int(math.Round(sum.FocusRatio*100))), "foreground share of tracked time", ic(theme.VisibilityIcon()), colText).obj,
	)

	// stacked chart by the top five + other
	buckets := stats.Buckets(all, from, to, g, u.weekStart(), filter)
	ranked := stats.Top(all, from, to, filter, 0)
	var ser []series
	idx := map[string]int{}
	for i, r := range ranked {
		if i >= len(seriesColors) {
			break
		}
		idx[r.Key] = i
		ser = append(ser, series{Name: truncate(nameOf(r.Key), 24), Color: seriesColors[i]})
	}
	hasOther := len(ranked) > len(seriesColors)
	if hasOther {
		ser = append(ser, series{Name: "Other", Color: colOther})
	}
	labels, tips := make([]string, len(buckets)), make([]string, len(buckets))
	values := make([][]float64, len(buckets))
	for i, b := range buckets {
		labels[i] = stats.BucketLabel(b.Start, g)
		tips[i] = bucketTip(b, g)
		row := make([]float64, len(ser))
		for k, v := range b.ByKey {
			if j, ok := idx[k]; ok {
				row[j] += v / 3600
			} else if hasOther {
				row[len(ser)-1] += v / 3600
			}
		}
		values[i] = row
	}
	chart := newBarChart(260)
	chart.SetData(labels, tips, values, ser)
	legend := container.NewHBox()
	for i, s := range ser {
		sw := fixed(10, 10, rectColor(s.Color))
		total := 0.0
		if i < len(ranked) && s.Name != "Other" {
			total = ranked[i].Secs
		} else {
			for _, r := range ranked[len(seriesColors):] {
				total += r.Secs
			}
		}
		legend.Add(container.NewHBox(container.NewCenter(sw), txt(s.Name, 11.5, colTextDim, false), txt(u.fmtDur(total), 11.5, colText, true), spacerW(8)))
	}
	chartBox := panel(container.NewVBox(txt("PLAYTIME OVER TIME", 11, colTextDim, true), chart, container.NewHScroll(legend)))

	// top list
	topList := container.NewVBox(txt("TOP "+fmt.Sprint(min(10, len(ranked))), 11, colTextDim, true))
	maxSecs := 0.0
	if len(ranked) > 0 {
		maxSecs = ranked[0].Secs
	}
	for i, r := range ranked {
		if i >= 10 {
			break
		}
		e := names[r.Key]
		key := r.Key
		col := colOther
		if i < len(seriesColors) {
			col = seriesColors[i]
		}
		rank := txt(fmt.Sprintf("%2d", i+1), 13, colTextMuted, true)
		name := txt(truncate(nameOf(r.Key), 26), 13, colText, true)
		val := txt(u.fmtDur(r.Secs), 12.5, colText, true)
		sub := txt(fmt.Sprintf("%d sessions", r.Sessions), 10.5, colTextMuted, false)
		frac := 0.0
		if maxSecs > 0 {
			frac = r.Secs / maxSecs
		}
		row := container.NewBorder(nil, nil,
			container.NewHBox(fixed(22, 28, container.NewCenter(rank)), iconImage(u.iconPath(e), nameOf(r.Key), 28)),
			container.NewVBox(val, sub),
			container.NewVBox(name, newFracBar(frac, col)))
		topList.Add(newTapCard(container.New(layout.NewCustomPaddedLayout(5, 5, 8, 8), row), func() { u.openDetail(key) }))
	}
	if len(ranked) == 0 {
		topList.Add(dimLabel("Nothing played in this range."))
	}

	// heat maps
	cal := newCalendar()
	cal.SetData(stats.Daily(all, time.Local, filter), now, u.weekStart())
	calBox := panel(container.NewVBox(txt("LAST 12 MONTHS", 11, colTextDim, true), cal, heatLegend()))
	pc := newPunchcard()
	pc.SetData(stats.Heatmap(clipped, time.Local, filter), u.weekStart())
	pcBox := panel(container.NewVBox(txt("WHEN YOU PLAY ("+p.st.rng+")", 11, colTextDim, true), pc))

	// lifetime facts
	facts := container.NewVBox(txt("ALL TIME", 11, colTextDim, true))
	if allSum.Sessions == 0 {
		facts.Add(dimLabel("No playtime recorded yet. It starts counting the moment something runs."))
	} else {
		facts.Add(factRow("Tracked since", allSum.First.Format("Jan 2, 2006")))
		facts.Add(factRow("Total", u.fmtDur(allSum.Total)+fmt.Sprintf("  (%.1f days)", allSum.Total/86400)))
		facts.Add(factRow("This year", u.fmtDur(allSum.Year)))
		facts.Add(factRow("Sessions", fmt.Sprint(allSum.Sessions)))
		facts.Add(factRow("Longest streak", fmt.Sprintf("%d days", allSum.LongestStreak)))
		facts.Add(factRow("Most played ever", nameOf(allSum.TopKey)+" ("+u.fmtDur(allSum.TopSecs)+")"))
		movie := allSum.Total / (2 * 3600)
		facts.Add(dimLabel(fmt.Sprintf("That's about %d movies, or %d full nights of sleep.", int(movie), int(allSum.Total/(8*3600)))))
	}

	p.box.Objects = []fyne.CanvasObject{
		tiles, chartBox,
		container.NewGridWithColumns(2, panel(topList), container.NewVBox(pcBox, panel(facts))),
		calBox,
		tableNote(),
	}
	p.box.Refresh()
}

func lowerScope(s string) string {
	switch s {
	case "Games":
		return "games"
	case "Apps":
		return "apps"
	}
	return "things"
}

func factRow(k, v string) fyne.CanvasObject {
	return container.NewBorder(nil, nil, txt(k, 12.5, colTextDim, false), txt(v, 12.5, colText, true))
}

func spacerW(w float32) fyne.CanvasObject { return fixed(w, 1) }

func heatLegend() fyne.CanvasObject {
	row := container.NewHBox(layout.NewSpacer(), txt("Less", 10, colTextMuted, false))
	row.Add(fixed(11, 11, rectColor(colEmptyCell)))
	for _, c := range heatRamp {
		row.Add(fixed(11, 11, rectColor(c)))
	}
	row.Add(txt("More (5h+)", 10, colTextMuted, false))
	return row
}

func tableNote() fyne.CanvasObject {
	t := txt("Want the raw numbers? Settings > Data exports every session as CSV, or ask the local API.", 11, colTextMuted, false)
	return container.NewCenter(t)
}
