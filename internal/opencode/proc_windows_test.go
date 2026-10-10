//go:build windows

package opencode

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPresenceProcessIdentity(t *testing.T) {
	pid := os.Getpid()
	var created, exited, kernel, user windows.Filetime
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	born := created.Nanoseconds() / 1e9
	if ProcessAliveAt(pid, born-1) {
		t.Fatal("accepted heartbeat belonging to an earlier process")
	}
	// Even a long, silent turn is valid when the process predates its heartbeat.
	if !ProcessAliveAt(pid, born) || !ProcessAliveAt(pid, time.Now().Unix()) {
		t.Fatal("discarded the current process")
	}
}
