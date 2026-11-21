//go:build windows

package instance

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

func lock(dir string) (func(), error) {
	sum := sha1.Sum([]byte(strings.ToLower(dir)))
	name, err := windows.UTF16PtrFromString(`Local\SimpelPlaytime-` + hex.EncodeToString(sum[:8]))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			if h != 0 {
				windows.CloseHandle(h)
			}
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}
