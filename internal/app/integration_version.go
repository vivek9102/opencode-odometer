package app

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Injectable for installation tests, which must never launch the live CLI.
var probeOpenCodeVersion = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if path := os.Getenv("OPENCODE_EXECUTABLE"); path != "" {
		cmd = exec.CommandContext(ctx, path, "--version")
	} else if runtime.GOOS == "windows" {
		// npm installs a .cmd shim rather than an executable on PATH.
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "opencode --version")
	} else {
		cmd = exec.CommandContext(ctx, "opencode", "--version")
	}
	hideCommand(cmd)
	out, err := cmd.Output()
	return string(out), err
}
