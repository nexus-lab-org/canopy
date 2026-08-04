package pool

import (
	"os"
	"syscall"
)

// isAlive reports whether pid refers to a currently running process, by
// sending it signal 0 (no-op signal used purely for existence/permission
// checks; this is the standard Unix liveness probe and works on both
// macOS and Linux, canopy's only supported platforms).
func isAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but we can't signal it (e.g. owned
	// by another user) — that still counts as alive.
	return err == syscall.EPERM
}
