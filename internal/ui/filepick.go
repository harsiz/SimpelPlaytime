package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// fynePickFile is the cross platform fallback file picker.
func fynePickFile(u *UI, filters []fileFilter, onPicked func(string)) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			u.showError(err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		r.Close()
		onPicked(path)
	}, u.win)
	if exts := extensions(filters); len(exts) > 0 {
		d.SetFilter(storage.NewExtensionFileFilter(exts))
	}
	d.Resize(fyne.NewSize(820, 560))
	d.Show()
}

// pickFolder asks for a folder.
func pickFolder(u *UI, onPicked func(string)) {
	d := dialog.NewFolderOpen(func(l fyne.ListableURI, err error) {
		if err != nil {
			u.showError(err)
			return
		}
		if l == nil {
			return
		}
		onPicked(l.Path())
	}, u.win)
	d.Resize(fyne.NewSize(820, 560))
	d.Show()
}
