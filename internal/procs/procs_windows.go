//go:build windows

package procs

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procGetWindow            = user32.NewProc("GetWindow")
	procGetWindowLongW       = user32.NewProc("GetWindowLongW")
	procGetWindowRect        = user32.NewProc("GetWindowRect")
	procGetLastInputInfo     = user32.NewProc("GetLastInputInfo")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
)

const (
	gwOwner         = 4
	wsExToolWindow  = 0x00000080
	dwmwaCloaked    = 14
	maxTitleLength  = 512
	maxPathLength   = 32768
	gwlExStyleValue = -20
)

type rect struct{ Left, Top, Right, Bottom int32 }

type lastInputInfo struct {
	CbSize uint32
	DwTime uint32
}

type cachedPath struct {
	name string
	path string
}

type winScanner struct {
	mu    sync.Mutex
	paths map[uint32]cachedPath
}

// New returns the Windows scanner.
func New() Scanner { return &winScanner{paths: map[uint32]cachedPath{}} }

// window enumeration uses one callback for the whole process lifetime
// (Windows callbacks are a limited resource in Go).
var (
	enumMu     sync.Mutex
	enumOnce   sync.Once
	enumCb     uintptr
	enumResult map[uint32]string
)

func gwlExStyle() uintptr {
	v := int32(gwlExStyleValue)
	return uintptr(uint32(v))
}

func windowText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	if n > maxTitleLength {
		n = maxTitleLength
	}
	buf := make([]uint16, n+1)
	r, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:r])
}

func enumProc(hwnd uintptr, _ uintptr) uintptr {
	h := windows.HWND(hwnd)
	if !windows.IsWindowVisible(h) {
		return 1
	}
	if owner, _, _ := procGetWindow.Call(hwnd, gwOwner); owner != 0 {
		return 1
	}
	ex, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle())
	if uint32(ex)&wsExToolWindow != 0 {
		return 1
	}
	var cloaked uint32
	if windows.DwmGetWindowAttribute(h, dwmwaCloaked, unsafe.Pointer(&cloaked), 4) == nil && cloaked != 0 {
		return 1
	}
	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok != 0 {
		if r.Right-r.Left < 2 || r.Bottom-r.Top < 2 {
			return 1
		}
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(h, &pid); err != nil || pid == 0 {
		return 1
	}
	title := windowText(hwnd)
	if prev, ok := enumResult[pid]; !ok || (prev == "" && title != "") {
		enumResult[pid] = title
	}
	return 1
}

func visibleWindows() map[uint32]string {
	enumOnce.Do(func() { enumCb = windows.NewCallback(enumProc) })
	enumMu.Lock()
	defer enumMu.Unlock()
	enumResult = map[uint32]string{}
	_ = windows.EnumWindows(enumCb, nil)
	out := enumResult
	enumResult = nil
	return out
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	for {
		size := uint32(len(buf))
		err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size])
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER || len(buf) >= maxPathLength {
			return ""
		}
		buf = make([]uint16, len(buf)*2)
	}
}

func (s *winScanner) pathFor(pid uint32, name string) string {
	if c, ok := s.paths[pid]; ok && c.name == name {
		return c.path
	}
	p := imagePath(pid)
	s.paths[pid] = cachedPath{name: name, path: p}
	return p
}

func foregroundPID() uint32 {
	h := windows.GetForegroundWindow()
	if h == 0 {
		return 0
	}
	var pid uint32
	_, _ = windows.GetWindowThreadProcessId(h, &pid)
	return pid
}

func idleTime() time.Duration {
	info := lastInputInfo{CbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0
	}
	now, _, _ := procGetTickCount64.Call()
	idle := uint32(now) - info.DwTime
	return time.Duration(idle) * time.Millisecond
}

// Scan takes a snapshot of every process.
func (s *winScanner) Scan() (Snapshot, error) {
	snap := Snapshot{Time: time.Now()}
	wins := visibleWindows()

	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return snap, err
	}
	defer windows.CloseHandle(h)

	s.mu.Lock()
	defer s.mu.Unlock()
	alive := make(map[uint32]bool, len(s.paths))
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(h, &pe); err == nil; err = windows.Process32Next(h, &pe) {
		pid := pe.ProcessID
		if pid <= 4 {
			continue
		}
		name := windows.UTF16ToString(pe.ExeFile[:])
		p := Process{PID: pid, PPID: pe.ParentProcessID, Name: name, Path: s.pathFor(pid, name)}
		if t, ok := wins[pid]; ok {
			p.HasWindow = true
			p.Title = t
		}
		snap.Procs = append(snap.Procs, p)
		alive[pid] = true
	}
	for pid := range s.paths {
		if !alive[pid] {
			delete(s.paths, pid)
		}
	}
	snap.ForegroundPID = foregroundPID()
	snap.Idle = idleTime()
	return snap, nil
}
