//go:build !windows

package app

import "os/exec"

func hideCommand(cmd *exec.Cmd) {}
