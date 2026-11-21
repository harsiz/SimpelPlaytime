package ui

import (
	"hash/fnv"
	"image/color"
	"os"
	"strings"
	"sync"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ---- text helpers ----------------------------------------------------------

func txt(s string, size float32, col color.Color, bold bool) *canvas.Text {
	t := canvas.NewText(s, col)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Bold: bold}
	return t
}

func mono(s string, size float32, col color.Color) *canvas.Text {
	t := canvas.NewText(s, col)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	return t
}

func wrapLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func dimLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Importance = widget.LowImportance
	return l
}

// truncate keeps a string short enough for a card.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// ---- containers -------------------------------------------------------------

// panel draws content on a rounded surface card.
func panel(content fyne.CanvasObject) fyne.CanvasObject {
	return panelColor(content, colSurface)
}

func panelColor(content fyne.CanvasObject, bg color.Color) fyne.CanvasObject {
	r := canvas.NewRectangle(bg)
	r.CornerRadius = 12
	r.StrokeColor = colBorder
	r.StrokeWidth = 1
	return container.NewStack(r, container.New(layout.NewCustomPaddedLayout(14, 14, 16, 16), content))
}

// fixed forces a minimum size on an object.
type fixedLayout struct{ size fyne.Size }

func (f fixedLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	for _, o := range objs {
		o.Resize(s)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (f fixedLayout) MinSize([]fyne.CanvasObject) fyne.Size { return f.size }

func fixed(w, h float32, objs ...fyne.CanvasObject) *fyne.Container {
	return container.New(fixedLayout{fyne.NewSize(w, h)}, objs...)
}

// sectionTitle renders "--- NOW PLAYING" style headers.
func sectionTitle(s string) fyne.CanvasObject {
	line := canvas.NewRectangle(colPrimary)
	line.SetMinSize(fyne.NewSize(26, 2))
	t := txt(strings.ToUpper(s), 12, colPrimaryLite, true)
	return container.NewHBox(container.NewCenter(line), t)
}

// chip is a small rounded tag.
func chip(label string, icon fyne.Resource, bg, fg color.Color) fyne.CanvasObject {
	r := canvas.NewRectangle(bg)
	r.CornerRadius = 7
	items := []fyne.CanvasObject{}
	if icon != nil {
		ic := widget.NewIcon(theme.NewColoredResource(icon, theme.ColorNameForeground))
		items = append(items, fixed(14, 14, ic))
	}
	t := txt(label, 11.5, fg, true)
	items = append(items, t)
	return container.NewStack(r, container.New(layout.NewCustomPaddedLayout(4, 4, 9, 9), container.NewHBox(items...)))
}

// statTile is a label + big value block.
type statTile struct {
	obj   fyne.CanvasObject
	value *canvas.Text
	sub   *canvas.Text
}

func newStatTile(title, value, sub string, icon fyne.Resource, valueColor color.Color) *statTile {
	st := &statTile{}
	head := []fyne.CanvasObject{}
	if icon != nil {
		head = append(head, fixed(14, 14, widget.NewIcon(icon)))
	}
	head = append(head, txt(strings.ToUpper(title), 11, colTextDim, true))
	st.value = mono(value, 24, valueColor)
	st.sub = txt(sub, 11, colTextMuted, false)
	st.obj = panel(container.NewVBox(container.NewHBox(head...), st.value, st.sub))
	return st
}

func (s *statTile) Set(value, sub string) {
	if s.value.Text != value {
		s.value.Text = value
		s.value.Refresh()
	}
	if s.sub.Text != sub {
		s.sub.Text = sub
		s.sub.Refresh()
	}
}

// ---- tappable card -----------------------------------------------------------

// tapCard wraps content, highlights on hover and calls onTap when clicked.
type tapCard struct {
	widget.BaseWidget
	content   fyne.CanvasObject
	onTap     func()
	onSecond  func(*fyne.PointEvent)
	bg        *canvas.Rectangle
	hovered   bool
	baseColor color.Color
}

func newTapCard(content fyne.CanvasObject, onTap func()) *tapCard {
	c := &tapCard{content: content, onTap: onTap, baseColor: colSurface}
	c.bg = canvas.NewRectangle(colSurface)
	c.bg.CornerRadius = 12
	c.bg.StrokeColor = colBorder
	c.bg.StrokeWidth = 1
	c.ExtendBaseWidget(c)
	return c
}

func (c *tapCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(c.bg, c.content))
}

func (c *tapCard) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

func (c *tapCard) TappedSecondary(e *fyne.PointEvent) {
	if c.onSecond != nil {
		c.onSecond(e)
	}
}

func (c *tapCard) MouseIn(*desktop.MouseEvent) {
	c.hovered = true
	c.bg.FillColor = colSurface2
	c.bg.StrokeColor = withAlpha(colPrimary, 0x99)
	c.bg.Refresh()
}

func (c *tapCard) MouseMoved(*desktop.MouseEvent) {}

func (c *tapCard) MouseOut() {
	c.hovered = false
	c.bg.FillColor = c.baseColor
	c.bg.StrokeColor = colBorder
	c.bg.Refresh()
}

func (c *tapCard) Cursor() desktop.Cursor { return desktop.PointerCursor }

// ---- star toggle -----------------------------------------------------------

type starButton struct {
	widget.BaseWidget
	on       bool
	onChange func(bool)
	icon     *canvas.Image
}

func newStar(on bool, onChange func(bool)) *starButton {
	s := &starButton{on: on, onChange: onChange}
	s.icon = canvas.NewImageFromResource(s.res())
	s.icon.FillMode = canvas.ImageFillContain
	s.ExtendBaseWidget(s)
	return s
}

func (s *starButton) res() fyne.Resource {
	if s.on {
		return theme.NewColoredResource(starFilled, theme.ColorNameWarning)
	}
	return theme.NewColoredResource(starOutline, theme.ColorNamePlaceHolder)
}

func (s *starButton) set(on bool) {
	s.on = on
	s.icon.Resource = s.res()
	s.icon.Refresh()
}

func (s *starButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.icon)
}

func (s *starButton) MinSize() fyne.Size { return fyne.NewSize(20, 20) }

func (s *starButton) Tapped(*fyne.PointEvent) {
	s.set(!s.on)
	if s.onChange != nil {
		s.onChange(s.on)
	}
}

func (s *starButton) Cursor() desktop.Cursor { return desktop.PointerCursor }

// ---- images ---------------------------------------------------------------

type cachedRes struct {
	res   fyne.Resource
	mtime int64
	size  int64
}

var (
	resMu    sync.Mutex
	resCache = map[string]cachedRes{}
)

// fileResource loads (and caches) an image file as a resource. It returns
// nil when the file does not exist.
func fileResource(path string) fyne.Resource {
	if path == "" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	resMu.Lock()
	if c, ok := resCache[path]; ok && c.mtime == st.ModTime().UnixNano() && c.size == st.Size() {
		resMu.Unlock()
		return c.res
	}
	resMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	res := fyne.NewStaticResource(path, data)
	resMu.Lock()
	if len(resCache) > 400 {
		resCache = map[string]cachedRes{}
	}
	resCache[path] = cachedRes{res: res, mtime: st.ModTime().UnixNano(), size: st.Size()}
	resMu.Unlock()
	return res
}

// dropResourceCache frees cached image bytes (used when the window hides).
func dropResourceCache() {
	resMu.Lock()
	resCache = map[string]cachedRes{}
	resMu.Unlock()
}

// tintFor picks a stable colour for a name (used by letter avatars and
// generated posters).
func tintFor(name string) color.NRGBA {
	tints := []color.NRGBA{
		rgb(0x7b, 0x52, 0xcc), rgb(0x3a, 0x6f, 0xc4), rgb(0x1f, 0x8a, 0x70), rgb(0xb0, 0x4a, 0x7c),
		rgb(0xb8, 0x6b, 0x1e), rgb(0x5b, 0x4b, 0xc8), rgb(0x2f, 0x86, 0xa8), rgb(0x9a, 0x3f, 0xb5),
	}
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(name)))
	return tints[h.Sum32()%uint32(len(tints))]
}

func initial(name string) string {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	return "?"
}

// letterAvatar is a coloured rounded square with the first letter.
func letterAvatar(name string, size float32) fyne.CanvasObject {
	bg := canvas.NewRectangle(tintFor(name))
	bg.CornerRadius = size * 0.22
	t := txt(initial(name), size*0.48, color.White, true)
	t.Alignment = fyne.TextAlignCenter
	return fixed(size, size, container.NewStack(bg, container.NewCenter(t)))
}

// iconImage shows an entry icon (or a letter avatar when there is none).
func iconImage(path, name string, size float32) fyne.CanvasObject {
	if res := fileResource(path); res != nil {
		img := canvas.NewImageFromResource(res)
		img.FillMode = canvas.ImageFillContain
		img.ScaleMode = canvas.ImageScaleSmooth
		return fixed(size, size, img)
	}
	return letterAvatar(name, size)
}

// posterImage shows a poster, or a generated one (gradient + icon + name).
func posterImage(posterPath, iconPath, name string, w, h float32) fyne.CanvasObject {
	if res := fileResource(posterPath); res != nil {
		img := canvas.NewImageFromResource(res)
		img.FillMode = canvas.ImageFillCover
		img.ScaleMode = canvas.ImageScaleSmooth
		img.CornerRadius = 10
		return fixed(w, h, img)
	}
	tint := tintFor(name)
	grad := canvas.NewVerticalGradient(withAlpha(tint, 0xff), rgb(0x16, 0x11, 0x20))
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 10
	bg.StrokeColor = withAlpha(tint, 0x88)
	bg.StrokeWidth = 1
	icon := iconImage(iconPath, name, w*0.42)
	return fixed(w, h, container.NewStack(grad, bg, container.NewCenter(icon)))
}

func rectColor(c color.Color) fyne.CanvasObject {
	r := canvas.NewRectangle(c)
	r.CornerRadius = 2.5
	return r
}
