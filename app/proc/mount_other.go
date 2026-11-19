//go:build !linux

package proc

import "errors"

var errMounts = errors.New("bind mounts are only supported on linux")

func BindMount(src, dst string, readOnly bool) error { return errMounts }

func Unmount(dst string) error { return errMounts }

func MountedUnder(dir string) bool { return false }

func Prepare(root string) (func(), error) { return func() {}, nil }
