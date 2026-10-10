package opencode

// ProcessAlive checks local TUI presence without treating a quiet chat as closed.
func ProcessAlive(pid int) bool { return pid > 0 && processAlive(pid) }

// ProcessAliveAt also rejects a Windows PID reused after the last heartbeat.
// A quiet TUI remains valid: its process was created before that heartbeat.
func ProcessAliveAt(pid int, heartbeat int64) bool {
	return pid > 0 && processAliveAt(pid, heartbeat)
}
