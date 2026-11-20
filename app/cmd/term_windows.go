//go:build windows

package main

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

var (
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	getMode    = kernel32.NewProc("GetConsoleMode")
	setMode    = kernel32.NewProc("SetConsoleMode")
	bufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

func terminalWidth() int {
	if v, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && v > 0 {
		return v
	}
	var info struct {
		SizeX, SizeY     int16
		CursorX, CursorY int16
		Attr             uint16
		Left, Top        int16
		Right, Bottom    int16
		MaxX, MaxY       int16
	}
	if r, _, _ := bufferInfo.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info))); r != 0 {
		return int(info.Right-info.Left) + 1
	}
	return 0
}

// enableColor turns on ANSI colours in older Windows consoles.
func enableColor() {
	var mode uint32
	h := os.Stdout.Fd()
	if r, _, _ := getMode.Call(h, uintptr(unsafe.Pointer(&mode))); r != 0 {
		setMode.Call(h, uintptr(mode|0x0004))
	}
}
