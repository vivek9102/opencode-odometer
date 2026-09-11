//go:build !windows

package opencode

import (
	"os"
	"syscall"
)

// processAlive reports whether a PID belongs to a running process.
//
// Signal 0 performs the permission and existence checks without delivering a
// signal, which is the portable way to test liveness on Unix.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but belongs to another user.
	return err == syscall.EPERM
}
