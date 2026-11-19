package runtime

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"time"

	"lildocker/image"
	"lildocker/proc"
)

func Logs(ref string, follow bool, tail int, w io.Writer) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(c.logPath())
	if err != nil {
		return err
	}
	offset := int64(len(data))
	if tail >= 0 {
		data = lastLines(data, tail)
	}
	w.Write(data)
	if !follow {
		return nil
	}

	f, err := os.Open(c.logPath())
	if err != nil {
		return err
	}
	defer f.Close()
	f.Seek(offset, io.SeekStart)
	for {
		n, err := io.Copy(w, f)
		if err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if s, err := readState(c.dir()); err != nil || !s.view().Active() {
			_, err := io.Copy(w, f)
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func lastLines(data []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

// Top shows the container's main process.
func Top(ref string) (string, error) {
	c, err := Find(ref)
	if err != nil {
		return "", err
	}
	if !c.Active() {
		return "", fmt.Errorf("%s is not running", c.Name)
	}
	var cmd *exec.Cmd
	if goruntime.GOOS == "windows" {
		cmd = exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(c.Pid))
	} else {
		cmd = exec.Command("ps", "-o", "pid,ppid,etime,args", "-p", strconv.Itoa(c.Pid))
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func baseManifest(c *Container) image.Manifest {
	if m, err := image.Load(c.ImageID); err == nil {
		return *m
	}
	return image.Manifest{WorkDir: c.WorkDir}
}

func Diff(ref string) ([]image.Change, error) {
	c, err := Find(ref)
	if err != nil {
		return nil, err
	}
	m, err := image.Load(c.ImageID)
	if err != nil {
		return nil, fmt.Errorf("the image of %s no longer exists", c.Name)
	}
	return image.Diff(m, c.Rootfs(), c.mountSkips())
}

// Commit saves a container's filesystem as a new image.
func Commit(ref, target string) (*image.Manifest, error) {
	c, err := Find(ref)
	if err != nil {
		return nil, err
	}
	name, tag, err := image.ParseRef(target)
	if err != nil {
		return nil, err
	}
	m := baseManifest(c)
	m.Name, m.Tag = name, tag
	m.History = append(append([]string(nil), m.History...), "commit "+c.Name)
	return &m, image.Commit(&m, c.Rootfs(), c.mountSkips())
}

func Export(ref string, w io.Writer) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	return image.PackSkip(c.Rootfs(), w, c.mountSkips())
}

// CopyFrom copies a file or folder out of a container.
func CopyFrom(ref, containerPath, dest string) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	src, err := image.ResolveInRoot(c.Rootfs(), containerPath)
	if err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("%s: no such file in container", containerPath)
	}
	return copyInto(src, st, dest, filepath.Base(src))
}

// CopyTo copies a file or folder into a container.
func CopyTo(ref, hostPath, containerPath string) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	st, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	dest, err := image.ResolveInRoot(c.Rootfs(), containerPath)
	if err != nil {
		return err
	}
	return copyInto(hostPath, st, dest, filepath.Base(hostPath))
}

func copyInto(src string, st os.FileInfo, dest, name string) error {
	if d, err := os.Stat(dest); err == nil && d.IsDir() {
		dest = filepath.Join(dest, name)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if st.IsDir() {
		os.MkdirAll(dest, 0o755)
		return image.CopyTree(src, dest, nil)
	}
	return image.CopyFile(src, dest, st.Mode())
}

func PortMappings(ref string) ([]string, error) {
	c, err := Find(ref)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range c.Ports {
		f, err := parsePort(p)
		if err != nil {
			continue
		}
		ip := f.ip
		if ip == "" {
			ip = "0.0.0.0"
		}
		out = append(out, fmt.Sprintf("%s/tcp -> %s:%s", f.target, ip, f.host))
	}
	return out, nil
}

// DirSize adds up the files under a folder, leaving mounts alone.
func DirSize(dir string) int64 {
	if proc.MountedUnder(dir) {
		return 0
	}
	var n int64
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func (c *Container) Size() int64 { return DirSize(c.Rootfs()) }

type Counts struct {
	Total, Running, Paused, Stopped int
}

func Count() Counts {
	all, _ := List(true)
	var n Counts
	for _, c := range all {
		n.Total++
		switch c.Status {
		case StatusRunning, StatusRestarting:
			n.Running++
		case StatusPaused:
			n.Paused++
		default:
			n.Stopped++
		}
	}
	return n
}

func SortedNames(cs []*Container) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}
