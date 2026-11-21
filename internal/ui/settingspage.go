package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/store"
)

type settingsPage struct {
	u         *UI
	apiStatus *widget.Label
	ignored   *fyne.Container
}

func (p *settingsPage) tick()    {}
func (p *settingsPage) refresh() { p.updateAPIStatus() }

func (p *settingsPage) update(fn func(*settings.Settings)) {
	p.u.showError(p.u.core.Settings.Update(fn))
}

func check(label string, on bool, fn func(bool)) *widget.Check {
	c := widget.NewCheck(label, fn)
	c.SetChecked(on)
	return c
}

func hint(s string) *widget.Label {
	l := wrapLabel(s)
	l.Importance = widget.LowImportance
	l.TextStyle = fyne.TextStyle{Italic: false}
	return l
}

func section(title string, icon fyne.Resource, items ...fyne.CanvasObject) fyne.CanvasObject {
	var ic fyne.CanvasObject
	if icon == Logo {
		img := canvas.NewImageFromResource(Logo) // a PNG, cannot be recoloured
		img.FillMode = canvas.ImageFillContain
		ic = img
	} else {
		ic = widget.NewIcon(theme.NewColoredResource(icon, theme.ColorNamePrimary))
	}
	head := container.NewHBox(fixed(18, 18, ic), txt(title, 15, colText, true))
	return panel(container.NewVBox(append([]fyne.CanvasObject{head, widget.NewSeparator()}, items...)...))
}

func labelled(label string, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, fixed(170, 34, container.NewVBox(layout.NewSpacer(), widget.NewLabel(label), layout.NewSpacer())), nil, obj)
}

func (p *settingsPage) build() fyne.CanvasObject {
	u := p.u
	s := u.settings()

	// general
	general := section("General", theme.ComputerIcon(),
		check("Start with Windows (minimised in the tray)", s.StartWithWindows, func(b bool) {
			p.update(func(s *settings.Settings) { s.StartWithWindows = b })
			if err := u.core.ApplyAutostart(); err != nil {
				u.showError(fmt.Errorf("could not change the startup entry: %w", err))
			}
		}),
		check("Start minimised when opened by Windows", s.StartMinimized, func(b bool) {
			p.update(func(s *settings.Settings) { s.StartMinimized = b })
		}),
		check("Close button sends it to the tray", s.CloseToTray, func(b bool) {
			p.update(func(s *settings.Settings) { s.CloseToTray = b })
		}),
		check("Free memory while minimised", s.FreeMemoryWhenHidden, func(b bool) {
			p.update(func(s *settings.Settings) { s.FreeMemoryWhenHidden = b })
		}),
	)

	// tracking
	modes := []string{"Games + windowed apps", "Games + every .exe", "Games only"}
	modeIDs := []string{settings.TrackWindowed, settings.TrackAll, settings.TrackGames}
	mode := widget.NewSelect(modes, nil)
	for i, id := range modeIDs {
		if id == s.TrackMode {
			mode.SetSelected(modes[i])
		}
	}
	mode.OnChanged = func(v string) {
		for i, m := range modes {
			if m == v {
				id := modeIDs[i]
				p.update(func(s *settings.Settings) { s.TrackMode = id })
			}
		}
	}
	poll := intSelect([]int{2, 3, 5, 10, 15, 30}, s.PollSeconds, func(v int) string { return fmt.Sprintf("every %d seconds", v) }, func(v int) {
		p.update(func(s *settings.Settings) { s.PollSeconds = v })
	})
	idle := intSelect([]int{1, 2, 3, 5, 10, 15, 30, 60}, s.IdleMinutes, func(v int) string { return fmt.Sprintf("after %d min without input", v) }, func(v int) {
		p.update(func(s *settings.Settings) { s.IdleMinutes = v })
	})
	minSess := intSelect([]int{0, 10, 30, 60, 120, 300}, s.MinSessionSeconds, func(v int) string {
		if v == 0 {
			return "keep everything"
		}
		return fmt.Sprintf("shorter than %ds", v)
	}, func(v int) { p.update(func(s *settings.Settings) { s.MinSessionSeconds = v }) })
	merge := intSelect([]int{0, 60, 120, 300, 600}, s.MergeGapSeconds, func(v int) string {
		if v == 0 {
			return "never merge"
		}
		return fmt.Sprintf("restarts within %d min", v/60)
	}, func(v int) { p.update(func(s *settings.Settings) { s.MergeGapSeconds = v }) })
	tracking := section("Tracking", theme.NewColoredResource(clockIcon, theme.ColorNameForeground),
		labelled("What to track", mode),
		hint("\"Windowed apps\" (recommended) counts anything with a visible window. \"Every .exe\" also counts background programs."),
		labelled("Check running programs", poll),
		check("Apps only count while they are focused", s.AppsFocusOnly, func(b bool) {
			p.update(func(s *settings.Settings) { s.AppsFocusOnly = b })
		}),
		hint("Games always count while they run (like Steam). Apps only count while you actually use them, so Discord in the background does not rack up 16 hours a day."),
		check("Pause apps when I'm away (idle)", s.IdlePauseApps, func(b bool) { p.update(func(s *settings.Settings) { s.IdlePauseApps = b }) }),
		check("Pause games when I'm away too", s.IdlePauseGames, func(b bool) {
			p.update(func(s *settings.Settings) { s.IdlePauseGames = b })
		}),
		hint("Controllers do not count as input for Windows, so pausing games on idle is off by default."),
		labelled("Away means", idle),
		labelled("Throw away sessions", minSess),
		labelled("Count as one session", merge),
		hint("Games from the built in database, Steam/Epic libraries and game folders are always tracked. Everything is checked locally, nothing ever leaves your PC (except poster downloads, see below)."),
	)

	// ignore list
	p.ignored = container.NewVBox()
	p.fillIgnored()
	add := widget.NewEntry()
	add.SetPlaceHolder("something.exe")
	addBtn := widget.NewButtonWithIcon("Ignore", theme.ContentAddIcon(), func() {
		v := strings.TrimSpace(add.Text)
		if v == "" {
			return
		}
		if !strings.Contains(v, ".") {
			v += ".exe"
		}
		p.update(func(s *settings.Settings) { s.Ignored = append(s.Ignored, v) })
		add.SetText("")
		p.fillIgnored()
	})
	ignore := section("Ignored programs", theme.VisibilityOffIcon(),
		hint("These exes are never tracked. Deleting an entry with \"and ignore it\" also lands here."),
		container.NewBorder(nil, nil, nil, addBtn, add),
		p.ignored,
	)

	// artwork
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder("optional SteamGridDB API key")
	key.SetText(s.SteamGridDBKey)
	saveKey := widget.NewButton("Save key", func() {
		v := strings.TrimSpace(key.Text)
		p.update(func(s *settings.Settings) { s.SteamGridDBKey = v })
	})
	artwork := section("Posters and info", theme.MediaPhotoIcon(),
		check("Download posters and game info", s.DownloadArt, func(b bool) {
			p.update(func(s *settings.Settings) { s.DownloadArt = b })
		}),
		hint("Posters come from Steam's public store (no account needed), with Wikipedia as a fallback for games that are not on Steam. Only the game name or Steam app id is sent. A free SteamGridDB key (steamgriddb.com, profile > API) adds covers for pretty much everything else."),
		container.NewBorder(nil, nil, nil, saveKey, key),
		container.NewGridWithColumns(2,
			widget.NewButtonWithIcon("Fetch missing", theme.DownloadIcon(), func() { u.core.EnqueueMissing() }),
			widget.NewButtonWithIcon("Re-download all", theme.ViewRefreshIcon(), func() {
				for _, e := range u.core.Store.Entries() {
					if !e.CustomPoster {
						u.core.RefreshArt(e.Key)
					}
				}
			}),
		),
	)

	// api
	port := widget.NewEntry()
	port.SetText(strconv.Itoa(s.APIPort))
	applyPort := widget.NewButton("Apply", func() {
		n, err := strconv.Atoi(strings.TrimSpace(port.Text))
		if err != nil || n < 1024 || n > 65535 {
			u.showError(fmt.Errorf("pick a port between 1024 and 65535"))
			return
		}
		p.update(func(s *settings.Settings) { s.APIPort = n })
		p.applyAPI()
	})
	token := widget.NewEntry()
	token.SetText(s.APIToken)
	token.Disable()
	copyTok := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		u.app.Clipboard().SetContent(u.settings().APIToken)
	})
	regen := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		t := settings.NewToken()
		p.update(func(s *settings.Settings) { s.APIToken = t })
		token.SetText(t)
	})
	p.apiStatus = widget.NewLabel("")
	p.updateAPIStatus()
	openAPI := widget.NewButtonWithIcon("Open in browser", theme.ComputerIcon(), func() {
		st := u.settings()
		raw := fmt.Sprintf("http://127.0.0.1:%d/api/v1?pretty=1", st.APIPort)
		if st.APIRequireToken {
			raw += "&token=" + st.APIToken
		}
		if lu, err := url.Parse(raw); err == nil {
			_ = u.app.OpenURL(lu)
		}
	})
	copyPy := widget.NewButtonWithIcon("Copy Python example", theme.ContentCopyIcon(), func() {
		u.app.Clipboard().SetContent(pythonExample(u.settings()))
	})
	apiSec := section("Local API", codeIcon,
		hint("A tiny JSON API on your own PC, great for Python scripts, stream overlays, Rainmeter, whatever. It only listens on 127.0.0.1."),
		check("Enable the local API", s.APIEnabled, func(b bool) {
			p.update(func(s *settings.Settings) { s.APIEnabled = b })
			p.applyAPI()
		}),
		labelled("Port", container.NewBorder(nil, nil, nil, applyPort, port)),
		check("Require a token", s.APIRequireToken, func(b bool) {
			p.update(func(s *settings.Settings) { s.APIRequireToken = b })
		}),
		hint("Send it as \"Authorization: Bearer <token>\" or add ?token=<token> to the URL."),
		labelled("Token", container.NewBorder(nil, nil, nil, container.NewHBox(copyTok, regen), token)),
		check("Allow browser pages (CORS)", s.APIAllowCORS, func(b bool) {
			p.update(func(s *settings.Settings) { s.APIAllowCORS = b })
		}),
		p.apiStatus,
		container.NewGridWithColumns(2, openAPI, copyPy),
	)

	// notifications
	brk := intSelect([]int{0, 30, 60, 90, 120, 180}, s.BreakReminderMinutes, func(v int) string {
		if v == 0 {
			return "off"
		}
		return fmt.Sprintf("every %d min of gaming", v)
	}, func(v int) { p.update(func(s *settings.Settings) { s.BreakReminderMinutes = v }) })
	notif := section("Notifications", theme.InfoIcon(),
		check("When a new game gets detected", s.NotifyNewGame, func(b bool) { p.update(func(s *settings.Settings) { s.NotifyNewGame = b }) }),
		check("When a game session ends", s.NotifySessionEnd, func(b bool) {
			p.update(func(s *settings.Settings) { s.NotifySessionEnd = b })
		}),
		labelled("Break reminder", brk),
	)

	// display
	tf := widget.NewSelect([]string{"27h 12m", "27.2h"}, nil)
	if s.TimeFormat == settings.TimeDecimal {
		tf.SetSelected("27.2h")
	} else {
		tf.SetSelected("27h 12m")
	}
	tf.OnChanged = func(v string) {
		f := settings.TimeHM
		if v == "27.2h" {
			f = settings.TimeDecimal
		}
		p.update(func(s *settings.Settings) { s.TimeFormat = f })
	}
	display := section("Display", theme.ColorPaletteIcon(),
		check("Weeks start on Monday", s.WeekStartsMonday, func(b bool) { p.update(func(s *settings.Settings) { s.WeekStartsMonday = b }) }),
		labelled("Time format", tf),
	)

	// data
	dir := widget.NewLabel(u.core.DataDir)
	dir.Wrapping = fyne.TextWrapBreak
	data := section("Data", theme.StorageIcon(),
		hint("Everything lives in plain files in this folder (library.json, sessions.jsonl, settings.json). Back it up, sync it, edit it, it's yours."),
		dir,
		container.NewGridWithColumns(2,
			widget.NewButtonWithIcon("Open folder", theme.FolderOpenIcon(), func() { u.showError(openFolder(u.core.DataDir)) }),
			widget.NewButtonWithIcon("Import backup", theme.UploadIcon(), p.importJSON),
			widget.NewButtonWithIcon("Export backup", theme.DocumentSaveIcon(), p.exportJSON),
			widget.NewButtonWithIcon("Export CSV", theme.DocumentSaveIcon(), p.exportCSV),
		),
		container.NewGridWithColumns(1,
			widget.NewButtonWithIcon("Rescan Steam/Epic libraries", theme.ViewRefreshIcon(), func() {
				go func() {
					u.core.Launchers.Refresh()
					u.core.Detector.Invalidate()
					fyne.Do(func() {
						dialog.ShowInformation("Done", fmt.Sprintf("Found %d installed launcher games.", len(u.core.Launchers.All())), u.win)
					})
				}()
			}),
		),
	)

	about := section("About", Logo,
		widget.NewLabel(fmt.Sprintf("Simpel Playtime v%s", u.core.Version)),
		hint(fmt.Sprintf("Built in database: %d games and %d apps. %d entries in your library, %d sessions recorded.",
			u.core.DB.Count(model.KindGame), u.core.DB.Count(model.KindApp), len(u.core.Store.Entries()), u.core.Store.SessionCount())),
		widget.NewHyperlink("github.com/harsiz/SimpelPlaytime", mustURL("https://github.com/harsiz/SimpelPlaytime")),
	)

	col1 := container.NewVBox(general, tracking, ignore, display)
	col2 := container.NewVBox(apiSec, artwork, notif, data, about)
	body := container.NewVBox(txt("Settings", 26, colText, true), container.NewGridWithColumns(2, col1, col2))
	return container.NewVScroll(container.New(layout.NewCustomPaddedLayout(22, 28, 28, 28), body))
}

func mustURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

func intSelect(opts []int, cur int, label func(int) string, onChange func(int)) *widget.Select {
	labels := make([]string, len(opts))
	found := false
	for i, o := range opts {
		labels[i] = label(o)
		if o == cur {
			found = true
		}
	}
	if !found {
		opts = append(opts, cur)
		labels = append(labels, label(cur))
	}
	sel := widget.NewSelect(labels, nil)
	for i, o := range opts {
		if o == cur {
			sel.SetSelected(labels[i])
		}
	}
	sel.OnChanged = func(v string) {
		for i, l := range labels {
			if l == v {
				onChange(opts[i])
			}
		}
	}
	return sel
}

func (p *settingsPage) fillIgnored() {
	u := p.u
	p.ignored.Objects = nil
	list := u.settings().Ignored
	if len(list) == 0 {
		p.ignored.Add(dimLabel("Nothing ignored."))
	}
	for _, name := range list {
		n := name
		del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			p.update(func(s *settings.Settings) {
				kept := s.Ignored[:0]
				for _, x := range s.Ignored {
					if !strings.EqualFold(x, n) {
						kept = append(kept, x)
					}
				}
				s.Ignored = kept
			})
			p.fillIgnored()
		})
		del.Importance = widget.LowImportance
		p.ignored.Add(container.NewBorder(nil, nil, nil, del, widget.NewLabel(n)))
	}
	p.ignored.Refresh()
}

func (p *settingsPage) applyAPI() {
	if p.u.api != nil {
		_ = p.u.api.Apply()
	}
	p.updateAPIStatus()
}

func (p *settingsPage) updateAPIStatus() {
	if p.apiStatus == nil {
		return
	}
	st := p.u.settings()
	switch {
	case !st.APIEnabled:
		p.apiStatus.SetText("Status: off")
	case p.u.api == nil:
		p.apiStatus.SetText("Status: not available")
	case p.u.api.LastError() != nil:
		p.apiStatus.SetText("Status: " + p.u.api.LastError().Error())
	case p.u.api.Addr() != "":
		p.apiStatus.SetText("Status: listening on http://" + p.u.api.Addr() + "/api/v1")
	default:
		p.apiStatus.SetText("Status: starting...")
	}
}

func pythonExample(st settings.Settings) string {
	headers := ""
	if st.APIRequireToken {
		headers = fmt.Sprintf(", headers={\"Authorization\": \"Bearer %s\"}", st.APIToken)
	}
	return fmt.Sprintf(`import requests

API = "http://127.0.0.1:%d/api/v1"

now = requests.get(f"{API}/now-playing"%s).json()
for game in now:
    print(f"playing {game['name']} for {game['session']} (total {game['total']})")

for game in requests.get(f"{API}/games?kind=game&limit=5"%s).json():
    print(f"{game['name']:30} {game['totalHours']:8.1f}h")
`, st.APIPort, headers, headers)
}

func (p *settingsPage) exportJSON() {
	u := p.u
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			u.showError(err)
			return
		}
		defer w.Close()
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		u.showError(enc.Encode(u.core.Store.Export()))
	}, u.win)
	d.SetFileName("simpelplaytime-backup.json")
	d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	d.Show()
}

func (p *settingsPage) exportCSV() {
	u := p.u
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			u.showError(err)
			return
		}
		defer w.Close()
		u.showError(u.core.Store.WriteCSV(w))
	}, u.win)
	d.SetFileName("simpelplaytime-sessions.csv")
	d.Show()
}

func (p *settingsPage) importJSON() {
	u := p.u
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			u.showError(err)
			return
		}
		defer r.Close()
		data, err := io.ReadAll(io.LimitReader(r, 512<<20))
		if err != nil {
			u.showError(err)
			return
		}
		var backup store.ExportData
		if err := json.Unmarshal(data, &backup); err != nil {
			u.showError(fmt.Errorf("that does not look like a Simpel Playtime backup: %w", err))
			return
		}
		ne, ns, err := u.core.Store.Import(backup)
		if err != nil {
			u.showError(err)
			return
		}
		u.core.EnqueueMissing()
		dialog.ShowInformation("Imported", fmt.Sprintf("Added %d entries and %d sessions.", ne, ns), u.win)
	}, u.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	d.Show()
}
