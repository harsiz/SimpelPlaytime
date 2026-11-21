//go:build windows

package ui

import (
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf16"
	"unsafe"

	"fyne.io/fyne/v2"
	"golang.org/x/sys/windows"
)

var (
	comdlg32             = windows.NewLazySystemDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
)

const (
	ofnPathMustExist      = 0x00000800
	ofnFileMustExist      = 0x00001000
	ofnNoChangeDir        = 0x00000008
	ofnExplorer           = 0x00080000
	ofnNoDereferenceLinks = 0x00100000
	ofnHideReadOnly       = 0x00000004
)

type openFileName struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

func filterString(filters []fileFilter) []uint16 {
	var b strings.Builder
	for _, f := range filters {
		b.WriteString(f.Name + " (" + f.Pattern + ")")
		b.WriteByte(0)
		b.WriteString(f.Pattern)
		b.WriteByte(0)
	}
	b.WriteByte(0)
	return utf16.Encode([]rune(b.String()))
}

// pickFile uses the real Windows open dialog. noDeref keeps .lnk files as
// they are instead of jumping to their target (needed to track shortcuts).
func pickFile(u *UI, title string, filters []fileFilter, noDeref bool, onPicked func(string)) {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_ = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
		defer windows.CoUninitialize()

		buf := make([]uint16, 32768)
		filter := filterString(filters)
		t, _ := windows.UTF16PtrFromString(title)
		ofn := openFileName{
			lpstrFilter:  &filter[0],
			nFilterIndex: 1,
			lpstrFile:    &buf[0],
			nMaxFile:     uint32(len(buf)),
			lpstrTitle:   t,
			flags:        ofnPathMustExist | ofnFileMustExist | ofnNoChangeDir | ofnExplorer | ofnHideReadOnly,
		}
		if noDeref {
			ofn.flags |= ofnNoDereferenceLinks
		}
		ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
		r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
		if r == 0 {
			return // cancelled
		}
		path := windows.UTF16ToString(buf)
		if path == "" {
			return
		}
		fyne.Do(func() { onPicked(path) })
	}()
}

func openFolder(dir string) error {
	return exec.Command("explorer.exe", dir).Start()
}
