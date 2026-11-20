//go:build !windows

package main

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

func terminalWidth() int {
	if v, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && v > 0 {
		return v
	}
	var ws struct{ Row, Col, X, Y uint16 }
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if e == 0 {
		return int(ws.Col)
	}
	return 0
}

func enableColor() {}
