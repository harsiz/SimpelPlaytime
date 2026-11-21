package ui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
)

const (
	cardW   = float32(166)
	cardH   = float32(318)
	posterW = float32(146)
	posterH = float32(219)
)

var libFilters = []string{"Games", "Apps", "Favorites", "Everything", "Hidden"}
var libSorts = []string{"Most played", "Recently played", "Name", "Recently added"}

// libraryState survives page switches so filters stick.
type libraryState struct {
	filter string
	sort   string
	search string
}

func newLibraryState() *libraryState {
	return &libraryState{filter: "Games", sort: "Most played"}
}

type libraryPage struct {
	u      *UI
	st     *libraryState
	items  []model.Entry
	totals map[string]stats.Totals
	live   map[string]bool
	grid   *widget.GridWrap
	count  *widget.Label
	empty  *fyne.Container
	stack  *fyne.Container
}

func (p *libraryPage) build() fyne.CanvasObject {
	u := p.u
	search := widget.NewEntry()
	search.SetPlaceHolder("Search your library")
	search.SetText(p.st.search)
	search.OnChanged = func(s string) { p.st.search = s; p.refresh() }
	search.ActionItem = widget.NewIcon(theme.SearchIcon())

	filter := widget.NewSelect(libFilters, func(s string) { p.st.filter = s; p.refresh() })
	filter.SetSelected(p.st.filter)
	sortSel := widget.NewSelect(libSorts, func(s string) { p.st.sort = s; p.refresh() })
	sortSel.SetSelected(p.st.sort)

	customise := widget.NewButtonWithIcon("Customise", tuneIcon, nil)
	customise.Importance = widget.HighImportance
	customise.OnTapped = func() { showCustomiseMenu(u, customise) }

	p.count = widget.NewLabel("")
	p.count.TextStyle = fyne.TextStyle{Bold: true}
	title := container.NewHBox(txt("Library", 26, colText, true), container.NewCenter(p.count))
	bar := container.NewBorder(nil, nil, nil, container.NewHBox(fixed(150, 36, filter), fixed(170, 36, sortSel), customise), search)

	p.grid = widget.NewGridWrap(
		func() int { return len(p.items) },
		func() fyne.CanvasObject { return fixed(cardW, cardH) },
		func(id widget.GridWrapItemID, o fyne.CanvasObject) {
			c := o.(*fyne.Container)
			if id >= len(p.items) {
				c.Objects = nil
				return
			}
			c.Objects = []fyne.CanvasObject{p.card(p.items[id])}
			c.Refresh()
		},
	)
	p.empty = container.NewCenter(container.NewVBox())
	p.stack = container.NewStack(p.grid, p.empty)
	p.refresh()
	header := container.NewVBox(title, bar, spacer(4))
	return container.New(layout.NewCustomPaddedLayout(22, 10, 28, 20), container.NewBorder(header, nil, nil, nil, p.stack))
}

func (p *libraryPage) tick() {}

func (p *libraryPage) refresh() {
	u := p.u
	p.totals = u.core.Totals()
	p.live = map[string]bool{}
	for _, l := range u.core.Tracker.Live() {
		if l.Running {
			p.live[l.Key] = true
		}
	}
	q := strings.ToLower(strings.TrimSpace(p.st.search))
	var items []model.Entry
	for _, e := range u.core.Store.Entries() {
		switch p.st.filter {
		case "Games":
			if e.Kind != model.KindGame || e.Hidden {
				continue
			}
		case "Apps":
			if e.Kind != model.KindApp || e.Hidden {
				continue
			}
		case "Favorites":
			if !e.Favorite || e.Hidden {
				continue
			}
		case "Hidden":
			if !e.Hidden {
				continue
			}
		default:
			if e.Hidden {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(e.DisplayName()), q) && !strings.Contains(strings.ToLower(e.ExeName), q) {
			continue
		}
		items = append(items, e)
	}
	last := func(e model.Entry) time.Time {
		t := e.LastPlayed
		if x := p.totals[e.Key].Last; x.After(t) {
			t = x
		}
		return t
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		// running stuff always first, then favourites
		if p.live[a.Key] != p.live[b.Key] {
			return p.live[a.Key]
		}
		switch p.st.sort {
		case "Recently played":
			return last(a).After(last(b))
		case "Name":
			return strings.ToLower(a.DisplayName()) < strings.ToLower(b.DisplayName())
		case "Recently added":
			return a.FirstSeen.After(b.FirstSeen)
		}
		if p.totals[a.Key].Secs != p.totals[b.Key].Secs {
			return p.totals[a.Key].Secs > p.totals[b.Key].Secs
		}
		return strings.ToLower(a.DisplayName()) < strings.ToLower(b.DisplayName())
	})
	p.items = items
	if p.count != nil {
		p.count.SetText(fmt.Sprintf("(%d)", len(items)))
	}
	if p.empty != nil {
		if len(items) == 0 {
			msg := "Nothing here yet. Play something, or press Customise to track any .exe or shortcut."
			if q != "" {
				msg = "No matches for \"" + p.st.search + "\""
			}
			p.empty.Objects = []fyne.CanvasObject{container.NewVBox(
				container.NewCenter(fixed(64, 64, widget.NewIcon(theme.NewColoredResource(gamepadIcon, theme.ColorNamePlaceHolder)))),
				container.NewCenter(txt(msg, 13, colTextDim, false)),
			)}
		} else {
			p.empty.Objects = nil
		}
		p.empty.Refresh()
	}
	if p.grid != nil {
		p.grid.Refresh()
	}
}

func (p *libraryPage) card(e model.Entry) fyne.CanvasObject {
	u := p.u
	t := p.totals[e.Key]
	key := e.Key
	poster := posterImage(u.posterPath(e), u.iconPath(e), e.DisplayName(), posterW, posterH)
	star := newStar(e.Favorite, func(on bool) { u.showError(u.core.SetFavorite(key, on)) })
	starBox := container.NewVBox(container.NewHBox(fixed(26, 26, container.NewPadded(star)), layout.NewSpacer()))
	top := container.NewStack(poster, starBox)
	if p.live[e.Key] {
		top.Add(container.NewVBox(layout.NewSpacer(), container.NewHBox(layout.NewSpacer(),
			container.NewPadded(chip("PLAYING", nil, colGood, colText)))))
	}
	name := txt(truncate(e.DisplayName(), 19), 13, colText, true)
	name.Alignment = fyne.TextAlignCenter
	val, unit := stats.FormatShort(float64(t.Secs))
	lastT := e.LastPlayed
	if t.Last.After(lastT) {
		lastT = t.Last
	}
	playCol := container.NewVBox(cap10("Play total"), txtC(val, 17, colText, true), cap10(unit))
	lastCol := container.NewVBox(cap10("Last played"), txtC(shortAgo(lastT), 13, colText, true), cap10(fmt.Sprintf("%d sessions", t.Sessions)))
	statsRow := container.NewGridWithColumns(2, playCol, lastCol)
	body := container.New(layout.NewCustomPaddedLayout(10, 8, 10, 10),
		container.NewVBox(container.NewCenter(top), name, statsRow))
	card := newTapCard(body, func() { u.openDetail(key) })
	card.onSecond = func(ev *fyne.PointEvent) { showEntryMenu(u, e, ev.AbsolutePosition) }
	return card
}

func cap10(s string) *fyne.Container {
	t := txt(s, 10, colTextMuted, false)
	t.Alignment = fyne.TextAlignCenter
	return container.NewCenter(t)
}

func txtC(s string, size float32, col color.Color, bold bool) *fyne.Container {
	t := txt(s, size, col, bold)
	t.Alignment = fyne.TextAlignCenter
	return container.NewCenter(t)
}

func shortAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return strings.TrimSuffix(stats.Ago(t, time.Now()), " ago")
}

// showEntryMenu is the right click menu of a library card.
func showEntryMenu(u *UI, e model.Entry, pos fyne.Position) {
	key := e.Key
	fav := "Add to favorites"
	if e.Favorite {
		fav = "Remove from favorites"
	}
	hide := "Hide from library"
	if e.Hidden {
		hide = "Show in library"
	}
	kind := "Mark as app"
	if e.Kind == model.KindApp {
		kind = "Mark as game"
	}
	m := fyne.NewMenu("",
		fyne.NewMenuItem("Open details", func() { u.openDetail(key) }),
		fyne.NewMenuItem(fav, func() { u.showError(u.core.SetFavorite(key, !e.Favorite)) }),
		fyne.NewMenuItem(kind, func() {
			k := model.KindApp
			if e.Kind == model.KindApp {
				k = model.KindGame
			}
			u.showError(u.core.SetKind(key, k))
		}),
		fyne.NewMenuItem("Rename...", func() { renameDialog(u, e) }),
		fyne.NewMenuItem(hide, func() { u.showError(u.core.SetHidden(key, !e.Hidden)) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Delete...", func() { deleteDialog(u, e) }),
	)
	widget.ShowPopUpMenuAtPosition(m, u.win.Canvas(), pos)
}
