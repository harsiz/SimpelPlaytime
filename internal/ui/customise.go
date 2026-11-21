package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/core"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
)

func showErrorDialog(w fyne.Window, err error) {
	if err == nil {
		return
	}
	dialog.ShowError(err, w)
}

// popupMenuBelow shows a menu under a button (or in the middle of the window
// when there is no button).
func popupMenuBelow(u *UI, m *fyne.Menu, anchor fyne.CanvasObject) {
	c := u.win.Canvas()
	pos := fyne.NewPos(c.Size().Width/2-100, 120)
	if anchor != nil {
		d := fyne.CurrentApp().Driver()
		p := d.AbsolutePositionForObject(anchor)
		pos = fyne.NewPos(p.X, p.Y+anchor.Size().Height+4)
		if pos.X+240 > c.Size().Width {
			pos.X = c.Size().Width - 250
		}
	}
	widget.ShowPopUpMenuAtPosition(m, c, pos)
}

// showCustomiseMenu is the "Customise" button of the library.
func showCustomiseMenu(u *UI, anchor fyne.CanvasObject) {
	m := fyne.NewMenu("",
		fyne.NewMenuItem("Track an .exe file...", func() { pickAndTrackExe(u) }),
		fyne.NewMenuItem("Track a shortcut (.lnk / .url)...", func() { pickAndTrackShortcut(u) }),
		fyne.NewMenuItem("Track a whole folder...", func() { pickAndTrackFolder(u) }),
		fyne.NewMenuItem("Pick from running apps...", func() { runningAppsDialog(u) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Add playtime manually...", func() { manualTimeDialog(u, model.Entry{}) }),
	)
	popupMenuBelow(u, m, anchor)
}

func kindRadio(k model.Kind) *widget.RadioGroup {
	r := widget.NewRadioGroup([]string{"Game", "App"}, nil)
	r.Horizontal = true
	if k == model.KindApp {
		r.SetSelected("App")
	} else {
		r.SetSelected("Game")
	}
	return r
}

func radioKind(r *widget.RadioGroup) model.Kind {
	if r.Selected == "App" {
		return model.KindApp
	}
	return model.KindGame
}

func pickAndTrackExe(u *UI) {
	pickFile(u, "Choose a program to track", []fileFilter{{"Programs", "*.exe"}, {"All files", "*.*"}}, false, func(path string) {
		trackExeDialog(u, path, "", model.KindGame)
	})
}

// trackExeDialog asks for name/type/match mode and starts tracking.
func trackExeDialog(u *UI, path, name string, kind model.Kind) {
	exe := procs.ExeName(path)
	if name == "" {
		if id, ok := u.core.Detector.Identify(procs.Process{Name: exe, Path: path, HasWindow: true}); ok {
			name = id.Name
			if id.Kind.Valid() {
				kind = id.Kind
			}
			if e, exists := u.core.Store.Entry(id.Key); exists {
				name = e.DisplayName()
				kind = e.Kind
			}
		}
	}
	nameE := widget.NewEntry()
	nameE.SetText(name)
	nameE.SetPlaceHolder("Name (leave empty to use the exe's own name)")
	kindR := kindRadio(kind)
	match := widget.NewRadioGroup([]string{"Any " + exe + " anywhere", "Only this exact file"}, nil)
	match.SetSelected("Any " + exe + " anywhere")
	pathL := widget.NewLabel(path)
	pathL.Wrapping = fyne.TextWrapBreak
	items := []*widget.FormItem{
		widget.NewFormItem("File", pathL),
		widget.NewFormItem("Name", nameE),
		widget.NewFormItem("Type", kindR),
		widget.NewFormItem("Count", match),
	}
	d := dialog.NewForm("Track this program", "Track it", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		by := core.MatchByName
		if match.Selected == "Only this exact file" {
			by = core.MatchByPath
		}
		e, err := u.core.TrackExe(path, strings.TrimSpace(nameE.Text), radioKind(kindR), by)
		if err != nil {
			u.showError(err)
			return
		}
		u.openDetail(e.Key)
	}, u.win)
	d.Resize(fyne.NewSize(560, 340))
	d.Show()
}

func pickAndTrackShortcut(u *UI) {
	pickFile(u, "Choose a shortcut", []fileFilter{{"Shortcuts", "*.lnk;*.url"}, {"All files", "*.*"}}, true, func(path string) {
		info, err := u.core.InspectShortcut(path)
		if err != nil {
			u.showError(err)
			return
		}
		nameE := widget.NewEntry()
		nameE.SetText(info.Name)
		kindR := kindRadio(info.Kind)
		detail := widget.NewLabel(info.Detail)
		detail.Wrapping = fyne.TextWrapBreak
		items := []*widget.FormItem{
			widget.NewFormItem("Shortcut", widget.NewLabel(filepath.Base(path))),
			widget.NewFormItem("Points to", detail),
			widget.NewFormItem("Name", nameE),
			widget.NewFormItem("Type", kindR),
		}
		d := dialog.NewForm("Track this shortcut", "Track it", "Cancel", items, func(ok bool) {
			if !ok {
				return
			}
			e, err := u.core.TrackShortcut(path, strings.TrimSpace(nameE.Text), radioKind(kindR))
			if err != nil {
				u.showError(err)
				return
			}
			u.openDetail(e.Key)
		}, u.win)
		d.Resize(fyne.NewSize(560, 320))
		d.Show()
	})
}

func pickAndTrackFolder(u *UI) {
	pickFolder(u, func(dir string) {
		nameE := widget.NewEntry()
		nameE.SetText(filepath.Base(dir))
		kindR := kindRadio(model.KindGame)
		items := []*widget.FormItem{
			widget.NewFormItem("Folder", widget.NewLabel(dir)),
			widget.NewFormItem("Name", nameE),
			widget.NewFormItem("Type", kindR),
		}
		d := dialog.NewForm("Track everything in this folder", "Track it", "Cancel", items, func(ok bool) {
			if !ok {
				return
			}
			e, err := u.core.TrackFolder(dir, strings.TrimSpace(nameE.Text), radioKind(kindR))
			if err != nil {
				u.showError(err)
				return
			}
			u.openDetail(e.Key)
		}, u.win)
		d.Resize(fyne.NewSize(520, 260))
		d.Show()
	})
}

func runningAppsDialog(u *UI) {
	cands := u.core.RunningCandidates()
	if len(cands) == 0 {
		dialog.ShowInformation("Nothing found", "No programs with a window were seen in the last scan. Give it a few seconds.", u.win)
		return
	}
	var d dialog.Dialog
	list := widget.NewList(
		func() int { return len(cands) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, widget.NewLabel("status"), container.NewVBox(widget.NewLabel("name"), widget.NewLabel("path")))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := cands[i]
			b := o.(*fyne.Container)
			texts := b.Objects[0].(*fyne.Container)
			name := c.Process.Name
			if c.Process.Title != "" {
				name += "  -  " + truncate(c.Process.Title, 50)
			}
			texts.Objects[0].(*widget.Label).SetText(name)
			pl := texts.Objects[1].(*widget.Label)
			pl.SetText(truncate(c.Process.Path, 80))
			pl.Importance = widget.LowImportance
			status := "not tracked"
			if c.Tracked {
				status = "tracked as " + truncate(c.Identity.Name, 22)
			}
			b.Objects[1].(*widget.Label).SetText(status)
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		c := cands[i]
		d.Hide()
		name := c.Identity.Name
		if !c.Tracked {
			name = ""
		}
		trackExeDialog(u, c.Process.Path, name, model.KindGame)
	}
	d = dialog.NewCustom("Pick a running program", "Cancel", container.NewStack(list), u.win)
	d.Resize(fyne.NewSize(720, 520))
	d.Show()
}

// manualTimeDialog adds playtime by hand. When e is empty the user picks the
// entry from a list.
func manualTimeDialog(u *UI, e model.Entry) {
	entries := u.core.Store.Entries()
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].DisplayName()) < strings.ToLower(entries[j].DisplayName())
	})
	names := make([]string, 0, len(entries))
	byName := map[string]string{}
	for _, x := range entries {
		n := x.DisplayName()
		if _, dup := byName[n]; dup {
			n += " (" + x.Key + ")"
		}
		names = append(names, n)
		byName[n] = x.Key
	}
	sel := widget.NewSelectEntry(names)
	sel.SetPlaceHolder("Which game or app?")
	if e.Key != "" {
		sel.SetText(e.DisplayName())
		byName[e.DisplayName()] = e.Key
	}
	hours := widget.NewEntry()
	hours.SetPlaceHolder("hours, like 12 or 1.5")
	mins := widget.NewEntry()
	mins.SetPlaceHolder("minutes")
	date := widget.NewEntry()
	date.SetText(time.Now().Format("2006-01-02"))
	items := []*widget.FormItem{
		widget.NewFormItem("Entry", sel),
		widget.NewFormItem("Hours", hours),
		widget.NewFormItem("Minutes", mins),
		widget.NewFormItem("Played on", date),
	}
	d := dialog.NewForm("Add playtime manually", "Add", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		key, found := byName[sel.Text]
		if !found {
			u.showError(errors.New("pick an entry from the list"))
			return
		}
		h, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(hours.Text), ",", "."), 64)
		m, _ := strconv.ParseFloat(strings.TrimSpace(mins.Text), 64)
		dur := time.Duration((h*60 + m) * float64(time.Minute))
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(date.Text), time.Local)
		if err != nil {
			u.showError(errors.New("date must look like 2026-11-21"))
			return
		}
		end := day.Add(20 * time.Hour)
		if end.After(time.Now()) {
			end = time.Now()
		}
		u.showError(u.core.AddManualTime(key, end, dur))
	}, u.win)
	d.Resize(fyne.NewSize(460, 320))
	d.Show()
}

func renameDialog(u *UI, e model.Entry) {
	name := widget.NewEntry()
	name.SetText(e.DisplayName())
	dialog.ShowForm("Rename", "Save", "Cancel", []*widget.FormItem{widget.NewFormItem("Name", name)}, func(ok bool) {
		if ok {
			u.showError(u.core.Rename(e.Key, name.Text))
		}
	}, u.win)
}

func deleteDialog(u *UI, e model.Entry) {
	ignore := widget.NewCheck("Also ignore "+e.ExeName+" so it is not tracked again", nil)
	ignore.SetChecked(e.Kind == model.KindApp && e.ExeName != "")
	content := container.NewVBox(
		wrapLabel(fmt.Sprintf("This removes %s and all of its %s of playtime. There is no undo (except a backup).", e.DisplayName(), u.fmtDur(float64(u.core.TotalFor(e.Key).Secs)))),
	)
	if e.ExeName != "" {
		content.Add(ignore)
	}
	dialog.ShowCustomConfirm("Delete "+e.DisplayName()+"?", "Delete", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		if err := u.core.DeleteEntry(e.Key, ignore.Checked && e.ExeName != ""); err != nil {
			u.showError(err)
			return
		}
		if u.pageName == "detail" {
			u.back()
		}
	}, u.win)
}

func steamIDDialog(u *UI, e model.Entry) {
	id := widget.NewEntry()
	if e.SteamAppID != 0 {
		id.SetText(strconv.Itoa(e.SteamAppID))
	}
	id.SetPlaceHolder("e.g. 1145350 (the number in the store URL)")
	dialog.ShowForm("Steam app id", "Save", "Cancel", []*widget.FormItem{
		widget.NewFormItem("App id", id),
		widget.NewFormItem("", hint("Used for the poster, banner and game info. Leave empty to clear.")),
	}, func(ok bool) {
		if !ok {
			return
		}
		v := strings.TrimSpace(id.Text)
		n := 0
		if v != "" {
			var err error
			if n, err = strconv.Atoi(v); err != nil || n <= 0 {
				u.showError(errors.New("the app id is a number"))
				return
			}
		}
		u.showError(u.core.SetSteamAppID(e.Key, n))
	}, u.win)
}

func mergeDialog(u *UI, e model.Entry) {
	var names []string
	byName := map[string]string{}
	for _, x := range u.core.Store.Entries() {
		if x.Key == e.Key {
			continue
		}
		n := x.DisplayName()
		if _, dup := byName[n]; dup {
			n += " (" + x.Key + ")"
		}
		names = append(names, n)
		byName[n] = x.Key
	}
	sort.Strings(names)
	sel := widget.NewSelectEntry(names)
	sel.SetPlaceHolder("Merge into...")
	dialog.ShowForm("Merge "+e.DisplayName(), "Merge", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Into", sel),
		widget.NewFormItem("", hint("All playtime moves over and future launches of this one count for the other.")),
	}, func(ok bool) {
		if !ok {
			return
		}
		into, found := byName[sel.Text]
		if !found {
			u.showError(errors.New("pick an entry from the list"))
			return
		}
		if err := u.core.Merge(e.Key, into); err != nil {
			u.showError(err)
			return
		}
		u.openDetail(into)
	}, u.win)
}

// showEntryCustomise is the Customise button on the detail page.
func showEntryCustomise(u *UI, e model.Entry, anchor fyne.CanvasObject) {
	kind := "Switch to app"
	if e.Kind == model.KindApp {
		kind = "Switch to game"
	}
	hide := "Hide from library"
	if e.Hidden {
		hide = "Show in library"
	}
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Rename...", func() { renameDialog(u, e) }),
		fyne.NewMenuItem(kind, func() {
			k := model.KindApp
			if e.Kind == model.KindApp {
				k = model.KindGame
			}
			u.showError(u.core.SetKind(e.Key, k))
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Set poster from an image...", func() {
			pickFile(u, "Choose a poster image", []fileFilter{{"Images", "*.png;*.jpg;*.jpeg;*.webp;*.bmp;*.gif"}}, false, func(p string) {
				u.showError(u.core.SetCustomPoster(e.Key, p))
			})
		}),
		fyne.NewMenuItem("Set icon from a file...", func() {
			pickFile(u, "Choose an .exe, .ico or .png", []fileFilter{{"Icons", "*.exe;*.ico;*.png"}}, false, func(p string) {
				u.showError(u.core.SetCustomIcon(e.Key, p))
			})
		}),
		fyne.NewMenuItem("Set Steam app id...", func() { steamIDDialog(u, e) }),
		fyne.NewMenuItem("Re-download artwork", func() { u.core.RefreshArt(e.Key) }),
	}
	if e.CustomPoster {
		items = append(items, fyne.NewMenuItem("Use the downloaded poster again", func() { u.showError(u.core.ClearCustomPoster(e.Key)) }))
	}
	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Also count another .exe...", func() {
			pickFile(u, "Choose another exe for "+e.DisplayName(), []fileFilter{{"Programs", "*.exe"}}, false, func(p string) {
				e2 := e
				_, err := u.core.TrackExe(p, e2.DisplayName(), e2.Kind, core.MatchByName)
				if err == nil {
					// TrackExe may have made a new entry, fold it into this one
					if id, ok := u.core.Detector.Identify(procs.Process{Name: procs.ExeName(p), Path: p, HasWindow: true}); ok && id.Key != e2.Key {
						err = u.core.Merge(id.Key, e2.Key)
					}
				}
				u.showError(err)
			})
		}),
		fyne.NewMenuItem("Add playtime manually...", func() { manualTimeDialog(u, e) }),
		fyne.NewMenuItem("Merge into another entry...", func() { mergeDialog(u, e) }),
		fyne.NewMenuItem(hide, func() { u.showError(u.core.SetHidden(e.Key, !e.Hidden)) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Reset playtime...", func() {
			dialog.ShowConfirm("Reset playtime?", "Delete every session of "+e.DisplayName()+" but keep it in the library?", func(ok bool) {
				if ok {
					u.showError(u.core.Store.ClearSessions(e.Key))
				}
			}, u.win)
		}),
		fyne.NewMenuItem("Delete...", func() { deleteDialog(u, e) }),
	)
	popupMenuBelow(u, fyne.NewMenu("", items...), anchor)
}

// fileFilter is one entry of a file type filter ("Programs", "*.exe").
type fileFilter struct {
	Name    string
	Pattern string // ";" separated globs
}

// extensions returns the extensions of the filters for the Fyne dialog.
func extensions(filters []fileFilter) []string {
	var out []string
	for _, f := range filters {
		for _, p := range strings.Split(f.Pattern, ";") {
			p = strings.TrimPrefix(strings.TrimSpace(p), "*")
			if p != "" && p != ".*" {
				out = append(out, p)
			}
		}
	}
	return out
}
