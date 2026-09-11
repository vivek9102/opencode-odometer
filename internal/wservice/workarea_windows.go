//go:build windows

package wservice

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// workArea is the usable desktop rectangle, i.e. the monitor minus the
// taskbar and any other appbars.
type workArea struct {
	Left, Top, Right, Bottom int
}

func (w workArea) width() int  { return w.Right - w.Left }
func (w workArea) height() int { return w.Bottom - w.Top }

type rect struct {
	Left, Top, Right, Bottom int32
}

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	procSystemParamsInfo  = user32.NewProc("SystemParametersInfoW")
	shcore                = windows.NewLazySystemDLL("shcore.dll")
	procSetProcessDpiAware = shcore.NewProc("SetProcessDpiAwareness")
)

const spiGetWorkArea = 0x0030

var (
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procEnumWindows        = user32.NewProc("EnumWindows")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procGetWindow          = user32.NewProc("GetWindow")
)

// windowRect returns the odometer window's true screen position.
//
// The window is found by walking top-level windows for one owned by this
// process. FindWindowW by title does not work here: Wails registers a custom
// window class, and matching on title alone is unreliable.
func (s *Service) windowRect() (left, top int, ok bool) {
	const gwOwner = 4

	self := uint32(os.Getpid())
	var found uintptr

	cb := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != self {
			return 1 // keep enumerating
		}
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		// Skip owned windows (tooltips, popups); we want the main frame.
		if owner, _, _ := procGetWindow.Call(hwnd, gwOwner); owner != 0 {
			return 1
		}
		found = hwnd
		return 0 // stop
	})
	procEnumWindows.Call(cb, 0)

	if found == 0 {
		return 0, 0, false
	}
	var r rect
	if ret, _, _ := procGetWindowRect.Call(found, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return 0, 0, false
	}
	return int(r.Left), int(r.Top), true
}

// primaryWorkArea returns the taskbar-aware usable area of the primary
// monitor.
//
// Wails only exposes full screen dimensions, so docking previously subtracted
// a hardcoded 56px "taskbar" guess. That is wrong for any taskbar that is not
// the default height, is hidden, or is docked to the side of the screen —
// leaving the bar floating above the taskbar or pushed off-screen. Windows
// already knows the real answer, so ask it.
func primaryWorkArea() (workArea, bool) {
	var r rect
	ret, _, _ := procSystemParamsInfo.Call(
		uintptr(spiGetWorkArea),
		0,
		uintptr(unsafe.Pointer(&r)),
		0,
	)
	if ret == 0 {
		return workArea{}, false
	}
	wa := workArea{
		Left:   int(r.Left),
		Top:    int(r.Top),
		Right:  int(r.Right),
		Bottom: int(r.Bottom),
	}
	if wa.width() <= 0 || wa.height() <= 0 {
		return workArea{}, false
	}
	return wa, true
}
