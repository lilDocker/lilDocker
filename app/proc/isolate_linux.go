//go:build linux

package proc

import "syscall"

func isolate(a *syscall.SysProcAttr, root string, namespaces bool) error {
	a.Chroot = root
	if namespaces {
		a.Cloneflags = syscall.CLONE_NEWPID | syscall.CLONE_NEWNS |
			syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC
	}
	return nil
}
