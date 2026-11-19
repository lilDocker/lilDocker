//go:build linux

package proc

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"lildocker/image"
)

func BindMount(src, dst string, readOnly bool) error {
	if err := syscall.Mount(src, dst, "", syscall.MS_BIND, ""); err != nil {
		return err
	}
	if readOnly {
		err := syscall.Mount("", dst, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, "")
		if err != nil {
			Unmount(dst)
			return err
		}
	}
	return nil
}

func Unmount(dst string) error {
	return syscall.Unmount(dst, syscall.MNT_DETACH)
}

// MountedUnder reports whether anything is mounted at or below dir.
func MountedUnder(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		mp := strings.ReplaceAll(fields[4], `\040`, " ")
		if mp == abs || strings.HasPrefix(mp, abs+"/") {
			return true
		}
	}
	return false
}

// Prepare gives a chrooted rootfs the bits most programs expect: a few
// device files and a resolv.conf. The returned func undoes the mounts.
func Prepare(root string) (func(), error) {
	var mounted []string
	undo := func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			Unmount(mounted[i])
		}
	}

	if _, err := os.Stat(filepath.Join(root, "etc", "resolv.conf")); err != nil {
		if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
			os.MkdirAll(filepath.Join(root, "etc"), 0o755)
			os.WriteFile(filepath.Join(root, "etc", "resolv.conf"), data, 0o644)
		}
	}

	dev, err := image.ResolveInRoot(root, "/dev")
	if err != nil {
		return undo, err
	}
	os.MkdirAll(dev, 0o755)
	for _, name := range []string{"null", "zero", "random", "urandom", "tty"} {
		dst := filepath.Join(dev, name)
		if mp, err := os.Lstat(dst); err == nil && mp.Mode()&os.ModeDevice == 0 && mp.Size() > 0 {
			continue
		}
		if f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Close()
		}
		if err := BindMount("/dev/"+name, dst, false); err != nil {
			undo()
			return func() {}, err
		}
		mounted = append(mounted, dst)
	}
	return undo, nil
}
