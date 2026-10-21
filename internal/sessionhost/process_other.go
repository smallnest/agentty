//go:build !unix

package sessionhost

import "syscall"

func detachAttributes() *syscall.SysProcAttr {
	return nil
}

func killGroup(pid int) {}
