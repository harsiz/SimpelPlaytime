// Package autostart registers Simpel Playtime to run when the user logs in.
package autostart

import "strings"

// Name is the name of the startup entry.
const Name = "SimpelPlaytime"

// Flag is passed when started by the OS so the app opens minimised.
const Flag = "--minimized"

// Command builds the command line stored in the startup entry.
func Command(exe string) string {
	return `"` + strings.ReplaceAll(exe, `"`, "") + `" ` + Flag
}

// Enable turns autostart on for exe.
func Enable(exe string) error { return enable(exe) }

// Disable turns autostart off.
func Disable() error { return disable() }

// Enabled reports whether autostart is on and the command it runs.
func Enabled() (bool, string) { return enabled() }

// Sync makes the OS state match want, re-pointing the entry at exe when the
// app was moved. It returns an error when the OS refused.
func Sync(want bool, exe string) error {
	on, cmd := Enabled()
	switch {
	case want && (!on || cmd != Command(exe)):
		return Enable(exe)
	case !want && on:
		return Disable()
	}
	return nil
}
