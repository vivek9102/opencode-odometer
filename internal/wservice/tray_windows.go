//go:build windows

package wservice

import (
	"fmt"
	"os"
	goruntime "runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type trayWindowClass struct {
	Size, Style                        uint32
	Callback                           uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type trayPoint struct{ X, Y int32 }
type trayMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          trayPoint
	Private        uint32
}
type trayIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Timeout             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

// The tray owns a hidden Win32 window on a dedicated message-loop thread.
// Keeping it alive when Wails hides the main window preserves both telemetry
// and budget enforcement, and the single-instance lock prevents relaunches.
func startTray(s *Service) (func(), bool) {
	ready := make(chan uintptr, 1)
	go func() {
		goruntime.LockOSThread()
		defer goruntime.UnlockOSThread()
		shell := windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
		create := user32.NewProc("CreateWindowExW")
		destroy := user32.NewProc("DestroyWindow")
		def := user32.NewProc("DefWindowProcW")
		instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
		// Wails packages the application icon as RT_GROUP_ICON resource 3.
		// Resource 1 is an individual image, so loading it as a group fails.
		icon, _, _ := user32.NewProc("LoadIconW").Call(instance, 3)
		if icon == 0 {
			icon, _, _ = user32.NewProc("LoadIconW").Call(0, 32512)
		}
		className, _ := windows.UTF16PtrFromString(fmt.Sprintf("OpenCodeOdometerTray-%d", os.Getpid()))
		taskbarName, _ := windows.UTF16PtrFromString("TaskbarCreated")
		taskbarMessage, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(taskbarName)))
		var data trayIconData
		addIcon := func() { shell.Call(0, uintptr(unsafe.Pointer(&data))) }
		callback := windows.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
			switch {
			case msg == uint32(taskbarMessage) && taskbarMessage != 0:
				addIcon()
				return 0
			case msg == 0x8001:
				if lparam == 0x202 || lparam == 0x203 {
					go s.ShowFromTray()
					return 0
				}
				if lparam == 0x205 || lparam == 0x7b {
					menu, _, _ := user32.NewProc("CreatePopupMenu").Call()
					if menu == 0 {
						return 0
					}
					defer user32.NewProc("DestroyMenu").Call(menu)
					appendItem := func(id uintptr, label string) {
						text, _ := windows.UTF16PtrFromString(label)
						user32.NewProc("AppendMenuW").Call(menu, 0, id, uintptr(unsafe.Pointer(text)))
					}
					appendItem(1, "Show Odometer")
					appendItem(2, "Hide Odometer")
					label := "Pause enforcement"
					if s.App.Preferences().Paused {
						label = "Resume enforcement"
					}
					appendItem(3, label)
					user32.NewProc("AppendMenuW").Call(menu, 0x800, 0, 0)
					appendItem(4, "Quit Odometer")
					var point trayPoint
					user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
					user32.NewProc("SetForegroundWindow").Call(hwnd)
					selected, _, _ := user32.NewProc("TrackPopupMenuEx").Call(menu, 0x100|0x80|0x2, uintptr(point.X), uintptr(point.Y), hwnd, 0)
					user32.NewProc("PostMessageW").Call(hwnd, 0, 0, 0)
					switch selected {
					case 1:
						go s.ShowFromTray()
					case 2:
						go s.HideToTray()
					case 3:
						go s.TogglePause()
					case 4:
						go s.Quit()
					}
				}
				return 0
			case msg == 0x10:
				shell.Call(2, uintptr(unsafe.Pointer(&data)))
				destroy.Call(hwnd)
				return 0
			case msg == 2:
				user32.NewProc("PostQuitMessage").Call(0)
				return 0
			}
			value, _, _ := def.Call(hwnd, uintptr(msg), wparam, lparam)
			return value
		})
		class := trayWindowClass{Callback: callback, Instance: instance, ClassName: className}
		class.Size = uint32(unsafe.Sizeof(class))
		atom, _, _ := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
		if atom == 0 {
			ready <- 0
			return
		}
		defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
		hwnd, _, _ := create.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
		if hwnd == 0 {
			ready <- 0
			return
		}
		data = trayIconData{Window: hwnd, ID: 1, Flags: 1 | 2 | 4, Callback: 0x8001, Icon: icon}
		data.Size = uint32(unsafe.Sizeof(data))
		tip, _ := windows.UTF16FromString("OpenCode Odometer · click to show")
		copy(data.Tip[:], tip)
		added, _, _ := shell.Call(0, uintptr(unsafe.Pointer(&data)))
		if added == 0 {
			destroy.Call(hwnd)
			ready <- 0
			return
		}
		ready <- hwnd
		var msg trayMessage
		for {
			result, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(result) <= 0 {
				break
			}
			user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	select {
	case hwnd := <-ready:
		if hwnd == 0 {
			return func() {}, false
		}
		var once sync.Once
		return func() { once.Do(func() { user32.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0) }) }, true
	case <-time.After(5 * time.Second):
		go func() {
			if hwnd := <-ready; hwnd != 0 {
				user32.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0)
			}
		}()
		return func() {}, false
	}
}
