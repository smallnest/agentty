//go:build unix

package sessionhost

import "syscall"

// detachAttributes puts the host in its own session so a daemon exit does not
// deliver SIGHUP to it.
func detachAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// killGroup kills the host's PTY child and everything it spawned.
func killGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
