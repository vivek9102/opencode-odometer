//go:build windows

package opencode

import "golang.org/x/sys/windows"

// processAlive reports whether a PID belongs to a running process.
//
// os.FindProcess always succeeds on Windows and Signal(0) is not supported, so
// liveness has to be asked of the OS directly. A pointer written by a plugin
// that has since exited names a port nobody is listening on, so trusting it
// keeps the odometer pointed at a dead address.
func processAlive(pid int) bool {
	return processAliveAt(pid, 0)
}

func processAliveAt(pid int, heartbeat int64) bool {
	const stillActive = 259 // STILL_ACTIVE

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	if heartbeat > 0 {
		var created, exited, kernel, user windows.Filetime
		if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil && created.Nanoseconds()/1e6 > heartbeat*1000+999 {
			return false
		}
	}

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// The handle opened, so something exists; treat it as alive rather
		// than discarding a usable pointer on an unexpected query failure.
		return true
	}
	return code == stillActive
}
