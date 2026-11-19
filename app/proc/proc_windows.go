//go:build windows

package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
	stillActive           = 259
)

func Configure(cmd *exec.Cmd, o Options) error {
	if o.Chroot != "" {
		return errors.New("--isolate is only supported on linux")
	}
	a := &syscall.SysProcAttr{}
	if o.Detach {
		a.CreationFlags = createNewProcessGroup | detachedProcess
	} else if o.Group {
		a.CreationFlags = createNewProcessGroup
	}
	cmd.SysProcAttr = a
	return nil
}

func Signal(pid int, name string) error {
	switch strings.TrimPrefix(strings.ToUpper(name), "SIG") {
	case "TERM", "INT":
		return Terminate(pid)
	case "KILL":
		return Kill(pid)
	}
	return fmt.Errorf("signal %q is not supported on windows", name)
}

func Terminate(pid int) error {
	return exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid)).Run()
}

func Kill(pid int) error {
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

func ExitCode(ps *os.ProcessState) int {
	return ps.ExitCode()
}
