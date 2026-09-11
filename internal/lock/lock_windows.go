//go:build windows

package lock

import (
	"syscall"
)

const processQueryLimitedInformation = 0x1000

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = kernel32.NewProc("OpenProcess")
	procCloseHandle = kernel32.NewProc("CloseHandle")
)

func probeAlive(pid int) (bool, error) {
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		// A NULL handle means the process does not exist (or access denied).
		return false, nil
	}
	_, _, _ = procCloseHandle.Call(h)
	return true, nil
}
