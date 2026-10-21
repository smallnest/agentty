//go:build unix

package terminal

import "syscall"

// killProcessGroup kills the PTY child and everything it spawned. pty.Start
// makes the child a session leader, so the negative PID targets its group.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
