package opencode

// ProcessAlive checks local TUI presence without treating a quiet chat as closed.
func ProcessAlive(pid int) bool { return pid > 0 && processAlive(pid) }
