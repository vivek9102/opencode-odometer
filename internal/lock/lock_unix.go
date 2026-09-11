//go:build !windows

package lock

import "syscall"

func probeAlive(pid int) (bool, error) {
	// A zero signal only checks whether we may send signals (i.e. the
	// process exists). No actual signal is delivered.
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if err == syscall.EPERM {
		return true, nil // exists but owned by another user
	}
	return false, nil
}
