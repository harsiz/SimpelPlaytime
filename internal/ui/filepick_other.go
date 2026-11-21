//go:build !windows

package ui

import (
	"os/exec"
	"runtime"
)

func pickFile(u *UI, title string, filters []fileFilter, noDeref bool, onPicked func(string)) {
	fynePickFile(u, filters, onPicked)
}

func openFolder(dir string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", dir).Start()
	}
	return exec.Command("xdg-open", dir).Start()
}
