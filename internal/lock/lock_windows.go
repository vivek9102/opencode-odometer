//go:build windows

package lock

import (
	"syscall"
	"unsafe"
)

const processQueryLimitedInformation = 0x1000

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess        = kernel32.NewProc("OpenProcess")
	procCloseHandle        = kernel32.NewProc("CloseHandle")
	procGetExitCodeProcess = kernel32.NewProc("GetExitCodeProcess")
)

func probeAlive(pid int) (bool, error) {
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		// A NULL handle means the process does not exist (or access denied).
		return false, nil
	}
	defer procCloseHandle.Call(h)
	// Windows can retain a process object after exit while another component
	// owns a handle. OpenProcess alone then mistakes a stale PID for a live app.
	var exitCode uint32
	ok, _, err := procGetExitCodeProcess.Call(h, uintptr(unsafe.Pointer(&exitCode)))
	if ok == 0 {
		return false, err
	}
	return exitCode == 259, nil // STILL_ACTIVE
}
