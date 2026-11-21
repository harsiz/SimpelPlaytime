//go:build windows

package main

import (
	"log"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fatal shows a message box (there is no console in the GUI build) and exits.
func fatal(msg string) {
	log.Print(msg)
	title, _ := windows.UTF16PtrFromString("Simpel Playtime")
	text, _ := windows.UTF16PtrFromString(msg)
	user32 := windows.NewLazySystemDLL("user32.dll")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
	os.Exit(1)
}
