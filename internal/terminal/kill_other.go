//go:build !unix

package terminal

// killProcessGroup is a no-op on platforms without POSIX process groups.
func killProcessGroup(pid int) {}
