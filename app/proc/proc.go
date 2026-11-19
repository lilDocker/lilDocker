package proc

import (
	"os/exec"
	"runtime"
)

type Options struct {
	Group      bool
	Detach     bool
	Chroot     string
	Namespaces bool
}

func ShellArgs(script string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", script}
	}
	return []string{"sh", "-c", script}
}

func Shell(script string) *exec.Cmd {
	a := ShellArgs(script)
	return exec.Command(a[0], a[1:]...)
}
