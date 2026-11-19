//go:build !windows

package proc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func Configure(cmd *exec.Cmd, o Options) error {
	a := &syscall.SysProcAttr{}
	if o.Detach {
		a.Setsid = true
	} else if o.Group {
		a.Setpgid = true
	}
	if o.Chroot != "" {
		if err := isolate(a, o.Chroot, o.Namespaces); err != nil {
			return err
		}
	}
	cmd.SysProcAttr = a
	return nil
}

var signals = map[string]syscall.Signal{
	"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
	"TERM": syscall.SIGTERM, "CONT": syscall.SIGCONT, "STOP": syscall.SIGSTOP,
}

// Signal sends a signal to a process group, by name ("TERM", "SIGKILL") or number.
func Signal(pid int, name string) error {
	sig, err := parseSignal(name)
	if err != nil {
		return err
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}

func parseSignal(name string) (syscall.Signal, error) {
	n := strings.TrimPrefix(strings.ToUpper(name), "SIG")
	if s, ok := signals[n]; ok {
		return s, nil
	}
	if v, err := strconv.Atoi(n); err == nil && v > 0 && v < 65 {
		return syscall.Signal(v), nil
	}
	return 0, fmt.Errorf("unknown signal %q", name)
}

func Terminate(pid int) error { return Signal(pid, "TERM") }

func Kill(pid int) error { return Signal(pid, "KILL") }

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func ExitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
