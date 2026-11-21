package ui

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/stats"
)

// drawRenderer rebuilds its objects from a draw function whenever the
// widget is laid out or refreshed. Charts only redraw on resize, data change
// or hover, so this stays cheap.
type drawRenderer struct {
	w    fyne.Widget
	draw func(fyne.Size) []fyne.CanvasObject
	min  func() fyne.Size
	objs []fyne.CanvasObject
	size fyne.Size
}

func (r *drawRenderer) Layout(s fyne.Size) {
	r.size = s
	r.objs = r.draw(s)
}

func (r *drawRenderer) MinSize() fyne.Size { return r.min() }

func (r *drawRenderer) Refresh() {
	r.objs = r.draw(r.size)
	canvas.Refresh(r.w)
}

func (r *drawRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *drawRenderer) Destroy()                     {}

func place(o fyne.CanvasObject, x, y, w, h float32) fyne.CanvasObject {
	o.Move(fyne.NewPos(x, y))
	o.Resize(fyne.NewSize(w, h))
	return o
}

func textAt(s string, size float32, col color.Color, x, y float32, align fyne.TextAlign) *canvas.Text {
	t := txt(s, size, col, false)
	ms := t.MinSize()
	switch align {
	case fyne.TextAlignTrailing:
		x -= ms.Width
	case fyne.TextAlignCenter:
		x -= ms.Width / 2
	}
	t.Move(fyne.NewPos(x, y))
	t.Resize(ms)
	return t
}

// tooltip draws a small box with lines of text, kept inside the bounds.
func tooltip(lines []string, colors []color.Color, x, y float32, bounds fyne.Size) []fyne.CanvasObject {
	if len(lines) == 0 {
		return nil
	}
	var texts []*canvas.Text
	w, h := float32(0), float32(8)
	for i, l := range lines {
		size := float32(12)
		bold := i == 0
		t := txt(l, size, colText, bold)
		if i > 0 {
			t.Color = colTextDim
		}
		ms := t.MinSize()
		if ms.Width > w {
			w = ms.Width
		}
		texts = append(texts, t)
		h += ms.Height
	}
	w += 20
	h += 6
	swatch := float32(0)
	if len(colors) > 0 {
		swatch = 14
		w += swatch
	}
	bx := x + 14
	if bx+w > bounds.Width {
		bx = x - w - 14
	}
	if bx < 0 {
		bx = 0
	}
	by := y - h/2
	if by+h > bounds.Height {
		by = bounds.Height - h
	}
	if by < 0 {
		by = 0
	}
	bg := canvas.NewRectangle(withAlpha(rgb(0x0b, 0x08, 0x11), 0xf0))
	bg.CornerRadius = 8
	bg.StrokeColor = withAlpha(colPrimary, 0x88)
	bg.StrokeWidth = 1
	out := []fyne.CanvasObject{place(bg, bx, by, w, h)}
	cy := by + 6
	for i, t := range texts {
		ms := t.MinSize()
		tx := bx + 10
		if i > 0 && i-1 < len(colors) && colors[i-1] != nil {
			sw := canvas.NewRectangle(colors[i-1])
			sw.CornerRadius = 2
			out = append(out, place(sw, tx, cy+ms.Height/2-4, 8, 8))
			tx += swatch
		} else if i > 0 && swatch > 0 {
			tx += swatch
		}
		t.Move(fyne.NewPos(tx, cy))
		t.Resize(ms)
		out = append(out, t)
		cy += ms.Height
	}
	return out
}

func hoursLabel(h, step float64) string {
	if h == 0 {
		return "0"
	}
	if step < 1 {
		return fmt.Sprintf("%dm", int(math.Round(h*60)))
	}
	return fmt.Sprintf("%gh", h)
}

// ---- stacked bar chart ------------------------------------------------------

type series struct {
	Name  string
	Color color.Color
}

// barChart shows hours per bucket, optionally stacked by series.
type barChart struct {
	widget.BaseWidget
	labels   []string
	tips     []string    // bucket title for the tooltip
	values   [][]float64 // [bucket][series] in hours
	series   []series
	hover    int
	slots    []float32 // x start of each slot (for hover)
	slotW    float32
	minH     float32
	emptyMsg string
}

func newBarChart(minHeight float32) *barChart {
	c := &barChart{hover: -1, minH: minHeight, emptyMsg: "No playtime in this range yet"}
	c.ExtendBaseWidget(c)
	return c
}

func (c *barChart) SetData(labels, tips []string, values [][]float64, s []series) {
	c.labels, c.tips, c.values, c.series = labels, tips, values, s
	c.hover = -1
	c.Refresh()
}

func (c *barChart) CreateRenderer() fyne.WidgetRenderer {
	return &drawRenderer{w: c, draw: c.draw, min: func() fyne.Size { return fyne.NewSize(280, c.minH) }}
}

func (c *barChart) total(i int) float64 {
	t := 0.0
	for _, v := range c.values[i] {
		t += v
	}
	return t
}

func (c *barChart) draw(size fyne.Size) []fyne.CanvasObject {
	var objs []fyne.CanvasObject
	if size.Width < 50 || size.Height < 50 {
		return nil
	}
	const left, bottom, top, right = float32(46), float32(22), float32(10), float32(6)
	plotW := size.Width - left - right
	plotH := size.Height - top - bottom
	n := len(c.values)
	max := 0.0
	for i := 0; i < n; i++ {
		if t := c.total(i); t > max {
			max = t
		}
	}
	yTop, step := stats.NiceCeil(max)
	// recessive grid + y labels
	for v := 0.0; v <= yTop+1e-9; v += step {
		y := top + plotH - float32(v/yTop)*plotH
		line := canvas.NewLine(withAlpha(colBorder, 0xaa))
		line.StrokeWidth = 1
		line.Position1 = fyne.NewPos(left, y)
		line.Position2 = fyne.NewPos(size.Width-right, y)
		objs = append(objs, line)
		objs = append(objs, textAt(hoursLabel(v, step), 10.5, colTextMuted, left-8, y-7, fyne.TextAlignTrailing))
	}
	if n == 0 || max == 0 {
		msg := textAt(c.emptyMsg, 12, colTextMuted, left+plotW/2, top+plotH/2-8, fyne.TextAlignCenter)
		return append(objs, msg)
	}
	slot := plotW / float32(n)
	barW := slot - 2
	if barW > 34 {
		barW = 34
	}
	if barW < 1.5 {
		barW = 1.5
	}
	c.slots = c.slots[:0]
	c.slotW = slot
	for i := 0; i < n; i++ {
		x := left + slot*float32(i) + (slot-barW)/2
		c.slots = append(c.slots, left+slot*float32(i))
		if c.hover == i {
			hl := canvas.NewRectangle(withAlpha(colPrimary, 0x1c))
			hl.CornerRadius = 4
			objs = append(objs, place(hl, left+slot*float32(i), top, slot, plotH))
		}
		base := top + plotH
		// find the top most non zero segment so only it gets rounded
		topSeg := -1
		for s := range c.values[i] {
			if c.values[i][s] > 0 {
				topSeg = s
			}
		}
		for s, v := range c.values[i] {
			if v <= 0 {
				continue
			}
			h := float32(v/yTop) * plotH
			gap := float32(0)
			if s != topSeg && len(c.series) > 1 && h > 3 {
				gap = 2 // surface gap between stacked segments
			}
			var col color.Color = colPrimary
			if s < len(c.series) && c.series[s].Color != nil {
				col = c.series[s].Color
			}
			r := canvas.NewRectangle(col)
			if s == topSeg {
				rad := float32(4)
				if barW < 8 {
					rad = barW / 2
				}
				r.TopLeftCornerRadius, r.TopRightCornerRadius = rad, rad
			}
			if h < 1 {
				h = 1
			}
			objs = append(objs, place(r, x, base-h, barW, h-gap))
			base -= h
		}
	}
	// x labels, thinned so they never collide
	maxLabel := float32(0)
	for _, l := range c.labels {
		if w := txt(l, 10.5, colTextMuted, false).MinSize().Width; w > maxLabel {
			maxLabel = w
		}
	}
	every := int(math.Ceil(float64((maxLabel + 10) / slot)))
	if every < 1 {
		every = 1
	}
	for i := 0; i < n && i < len(c.labels); i += every {
		objs = append(objs, textAt(c.labels[i], 10.5, colTextMuted, left+slot*float32(i)+slot/2, top+plotH+4, fyne.TextAlignCenter))
	}
	// tooltip
	if c.hover >= 0 && c.hover < n {
		title := ""
		if c.hover < len(c.tips) {
			title = c.tips[c.hover]
		}
		lines := []string{title + "  " + stats.FormatHM(c.total(c.hover)*3600)}
		var cols []color.Color
		if len(c.series) > 1 {
			for s := len(c.values[c.hover]) - 1; s >= 0; s-- {
				v := c.values[c.hover][s]
				if v <= 0 || s >= len(c.series) {
					continue
				}
				lines = append(lines, c.series[s].Name+"  "+stats.FormatHM(v*3600))
				cols = append(cols, c.series[s].Color)
			}
		}
		x := left + slot*float32(c.hover) + slot/2
		y := top + plotH - float32(c.total(c.hover)/yTop)*plotH
		objs = append(objs, tooltip(lines, cols, x, y+20, size)...)
	}
	return objs
}

func (c *barChart) hoverAt(p fyne.Position) {
	idx := -1
	if c.slotW > 0 {
		for i, x := range c.slots {
			if p.X >= x && p.X < x+c.slotW {
				idx = i
				break
			}
		}
	}
	if idx != c.hover {
		c.hover = idx
		c.Refresh()
	}
}

func (c *barChart) MouseIn(e *desktop.MouseEvent)    { c.hoverAt(e.Position) }
func (c *barChart) MouseMoved(e *desktop.MouseEvent) { c.hoverAt(e.Position) }
func (c *barChart) MouseOut() {
	if c.hover != -1 {
		c.hover = -1
		c.Refresh()
	}
}

// ---- calendar heatmap ---------------------------------------------------

// calendar is a GitHub style "year in play" grid.
type calendar struct {
	widget.BaseWidget
	daily     map[string]float64
	end       time.Time
	weeks     int
	weekStart time.Weekday
	hover     string
	cells     map[string]fyne.Position
	cell      float32
}

func newCalendar() *calendar {
	c := &calendar{weeks: 53, daily: map[string]float64{}, end: time.Now(), weekStart: time.Monday}
	c.ExtendBaseWidget(c)
	return c
}

func (c *calendar) SetData(daily map[string]float64, end time.Time, weekStart time.Weekday) {
	c.daily, c.end, c.weekStart = daily, end, weekStart
	c.Refresh()
}

func (c *calendar) CreateRenderer() fyne.WidgetRenderer {
	return &drawRenderer{w: c, draw: c.draw, min: func() fyne.Size { return fyne.NewSize(300, 130) }}
}

// heatColor maps a value to the ramp using fixed hour thresholds so colours
// mean the same thing everywhere.
func heatColor(secs float64) color.Color {
	h := secs / 3600
	switch {
	case secs < 60:
		return colEmptyCell
	case h < 0.5:
		return heatRamp[0]
	case h < 1.5:
		return heatRamp[1]
	case h < 3:
		return heatRamp[2]
	case h < 5:
		return heatRamp[3]
	}
	return heatRamp[4]
}

func (c *calendar) draw(size fyne.Size) []fyne.CanvasObject {
	var objs []fyne.CanvasObject
	const left, top = float32(30), float32(18)
	gap := float32(3)
	cell := (size.Width-left)/float32(c.weeks) - gap
	if cell > 14 {
		cell = 14
	}
	if cell < 4 {
		cell = 4
	}
	c.cell = cell
	h := top + 7*(cell+gap)
	if h > size.Height {
		cell = (size.Height-top)/7 - gap
		c.cell = cell
	}
	last := stats.Align(c.end, stats.Week, c.weekStart)
	first := last.AddDate(0, 0, -7*(c.weeks-1))
	c.cells = map[string]fyne.Position{}
	for i, name := range []string{"Mon", "Wed", "Fri"} {
		_ = i
		wd := map[string]time.Weekday{"Mon": time.Monday, "Wed": time.Wednesday, "Fri": time.Friday}[name]
		row := (int(wd) - int(c.weekStart) + 7) % 7
		objs = append(objs, textAt(name, 9.5, colTextMuted, 0, top+float32(row)*(cell+gap)-1, fyne.TextAlignLeading))
	}
	lastMonth := -1
	today := stats.StartOfDay(c.end)
	for w := 0; w < c.weeks; w++ {
		weekStart := first.AddDate(0, 0, 7*w)
		x := left + float32(w)*(cell+gap)
		if m := int(weekStart.Month()); m != lastMonth {
			if w > 0 || weekStart.Day() <= 7 {
				objs = append(objs, textAt(weekStart.Format("Jan"), 9.5, colTextMuted, x, 0, fyne.TextAlignLeading))
			}
			lastMonth = m
		}
		for d := 0; d < 7; d++ {
			day := weekStart.AddDate(0, 0, d)
			if day.After(today) {
				continue
			}
			key := stats.DayKey(day)
			y := top + float32(d)*(cell+gap)
			r := canvas.NewRectangle(heatColor(c.daily[key]))
			r.CornerRadius = 2.5
			if key == c.hover {
				r.StrokeColor = colText
				r.StrokeWidth = 1.5
			}
			objs = append(objs, place(r, x, y, cell, cell))
			c.cells[key] = fyne.NewPos(x, y)
		}
	}
	if c.hover != "" {
		if p, ok := c.cells[c.hover]; ok {
			t, _ := time.ParseInLocation("2006-01-02", c.hover, c.end.Location())
			lines := []string{t.Format("Mon, Jan 2 2006"), stats.FormatHM(c.daily[c.hover]) + " played"}
			objs = append(objs, tooltip(lines, nil, p.X+cell/2, p.Y+cell/2, size)...)
		}
	}
	return objs
}

func (c *calendar) hoverAt(p fyne.Position) {
	found := ""
	for k, pos := range c.cells {
		if p.X >= pos.X-1 && p.X <= pos.X+c.cell+1 && p.Y >= pos.Y-1 && p.Y <= pos.Y+c.cell+1 {
			found = k
			break
		}
	}
	if found != c.hover {
		c.hover = found
		c.Refresh()
	}
}

func (c *calendar) MouseIn(e *desktop.MouseEvent)    { c.hoverAt(e.Position) }
func (c *calendar) MouseMoved(e *desktop.MouseEvent) { c.hoverAt(e.Position) }
func (c *calendar) MouseOut() {
	if c.hover != "" {
		c.hover = ""
		c.Refresh()
	}
}

// ---- punch card (weekday x hour) -------------------------------------------

type punchcard struct {
	widget.BaseWidget
	data      [7][24]float64
	weekStart time.Weekday
	hover     [2]int
	geo       struct{ left, top, cw, ch float32 }
}

func newPunchcard() *punchcard {
	p := &punchcard{hover: [2]int{-1, -1}, weekStart: time.Monday}
	p.ExtendBaseWidget(p)
	return p
}

func (p *punchcard) SetData(d [7][24]float64, weekStart time.Weekday) {
	p.data, p.weekStart = d, weekStart
	p.Refresh()
}

func (p *punchcard) CreateRenderer() fyne.WidgetRenderer {
	return &drawRenderer{w: p, draw: p.draw, min: func() fyne.Size { return fyne.NewSize(300, 180) }}
}

func (p *punchcard) draw(size fyne.Size) []fyne.CanvasObject {
	var objs []fyne.CanvasObject
	const left, top, bottom = float32(34), float32(4), float32(18)
	gap := float32(2)
	cw := (size.Width-left)/24 - gap
	ch := (size.Height-top-bottom)/7 - gap
	if ch > 20 {
		ch = 20
	}
	p.geo.left, p.geo.top, p.geo.cw, p.geo.ch = left, top, cw, ch
	max := 0.0
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			if p.data[d][h] > max {
				max = p.data[d][h]
			}
		}
	}
	for row := 0; row < 7; row++ {
		wd := time.Weekday((int(p.weekStart) + row) % 7)
		y := top + float32(row)*(ch+gap)
		objs = append(objs, textAt(wd.String()[:3], 9.5, colTextMuted, 0, y+ch/2-7, fyne.TextAlignLeading))
		for h := 0; h < 24; h++ {
			v := p.data[wd][h]
			col := color.Color(colEmptyCell)
			if v > 0 && max > 0 {
				idx := int(math.Ceil(v/max*float64(len(heatRamp)))) - 1
				if idx < 0 {
					idx = 0
				}
				if idx >= len(heatRamp) {
					idx = len(heatRamp) - 1
				}
				col = heatRamp[idx]
			}
			r := canvas.NewRectangle(col)
			r.CornerRadius = 2.5
			if p.hover == [2]int{row, h} {
				r.StrokeColor = colText
				r.StrokeWidth = 1.5
			}
			objs = append(objs, place(r, left+float32(h)*(cw+gap), y, cw, ch))
		}
	}
	by := top + 7*(ch+gap) + 2
	for h := 0; h < 24; h += 3 {
		objs = append(objs, textAt(stats.HourLabel(h), 9, colTextMuted, left+float32(h)*(cw+gap), by, fyne.TextAlignLeading))
	}
	if p.hover[0] >= 0 {
		wd := time.Weekday((int(p.weekStart) + p.hover[0]) % 7)
		v := p.data[wd][p.hover[1]]
		lines := []string{fmt.Sprintf("%ss %s", wd.String(), stats.HourLabel(p.hover[1])), stats.FormatHM(v) + " in total"}
		x := left + float32(p.hover[1])*(cw+gap) + cw/2
		y := top + float32(p.hover[0])*(ch+gap) + ch/2
		objs = append(objs, tooltip(lines, nil, x, y, size)...)
	}
	return objs
}

func (p *punchcard) hoverAt(pos fyne.Position) {
	g := p.geo
	h := [2]int{-1, -1}
	if g.cw > 0 && pos.X >= g.left && pos.Y >= g.top {
		col := int((pos.X - g.left) / (g.cw + 2))
		row := int((pos.Y - g.top) / (g.ch + 2))
		if col >= 0 && col < 24 && row >= 0 && row < 7 {
			h = [2]int{row, col}
		}
	}
	if h != p.hover {
		p.hover = h
		p.Refresh()
	}
}

func (p *punchcard) MouseIn(e *desktop.MouseEvent)    { p.hoverAt(e.Position) }
func (p *punchcard) MouseMoved(e *desktop.MouseEvent) { p.hoverAt(e.Position) }
func (p *punchcard) MouseOut() {
	if p.hover[0] != -1 {
		p.hover = [2]int{-1, -1}
		p.Refresh()
	}
}

// ---- fraction bar ------------------------------------------------------------

// fracBar is a thin horizontal bar used by the top lists.
type fracBar struct {
	widget.BaseWidget
	frac  float64
	color color.Color
}

func newFracBar(frac float64, col color.Color) *fracBar {
	b := &fracBar{frac: frac, color: col}
	b.ExtendBaseWidget(b)
	return b
}

func (b *fracBar) CreateRenderer() fyne.WidgetRenderer {
	return &drawRenderer{w: b, min: func() fyne.Size { return fyne.NewSize(60, 8) }, draw: func(s fyne.Size) []fyne.CanvasObject {
		track := canvas.NewRectangle(colEmptyCell)
		track.CornerRadius = 3
		fill := canvas.NewRectangle(b.color)
		fill.CornerRadius = 3
		w := float32(b.frac) * s.Width
		if w < 3 && b.frac > 0 {
			w = 3
		}
		y := (s.Height - 6) / 2
		return []fyne.CanvasObject{place(track, 0, y, s.Width, 6), place(fill, 0, y, w, 6)}
	}}
}
