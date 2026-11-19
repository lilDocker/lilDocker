//go:build !linux && !windows

package proc

import (
	"errors"
	"syscall"
)

func isolate(a *syscall.SysProcAttr, root string, namespaces bool) error {
	return errors.New("--isolate is only supported on linux")
}
