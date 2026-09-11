// Package lock provides a cross-process single-instance guard for the
// Odometer, matching the semantics of the original Python implementation
// (a pid file plus a liveness check).
package lock

import (
	"os"
	"strconv"
	"strings"
)

// pidAlive reports whether the given pid currently exists on this host.
// On Windows it uses OpenProcess (a handle is obtained iff the process
// exists); elsewhere it uses a zero-signal kill probe.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	alive, err := probeAlive(pid)
	if err != nil {
		return false
	}
	return alive
}

// Acquire tries to take a single-instance lock at `path`. It returns true if
// the lock was acquired (i.e. we are the only instance), false otherwise. A
// stale lock (pid not alive) is reclaimed.
func Acquire(path string) (bool, error) {
	if raw, err := os.ReadFile(path); err == nil {
		pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if perr == nil && pid > 0 && pidAlive(pid) {
			return false, nil // another live instance holds the lock
		}
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// Release removes the lock file.
func Release(path string) {
	_ = os.Remove(path)
}
