package lock

import (
	"os"
	"os/exec"
	"testing"
)

func TestExitedProcessWithOpenHandleIsNotAlive(t *testing.T) {
	cmd := exec.Command(os.Getenv("COMSPEC"), "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h, _, err := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(cmd.Process.Pid))
	if h == 0 {
		t.Fatal(err)
	}
	defer procCloseHandle.Call(h)
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if pidAlive(cmd.Process.Pid) {
		t.Fatal("an exited process with a retained handle must not block Odometer restart")
	}
}
