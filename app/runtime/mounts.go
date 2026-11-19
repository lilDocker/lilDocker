package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"lildocker/config"
	"lildocker/image"
	"lildocker/proc"
)

var volumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]+$`)

func splitSpec(s string) []string {
	parts := strings.Split(s, ":")
	if len(parts) > 1 && len(parts[0]) == 1 && (strings.HasPrefix(parts[1], `\`) || strings.HasPrefix(parts[1], "/")) {
		parts = append([]string{parts[0] + ":" + parts[1]}, parts[2:]...)
	}
	return parts
}

// parseMount reads "target", "source:target" or "source:target:ro".
func parseMount(spec, containerDir string, n int) (Mount, error) {
	parts := splitSpec(spec)
	var m Mount
	switch len(parts) {
	case 1:
		m.Target = parts[0]
		m.Anonymous = true
		m.Source = filepath.Join(containerDir, "volumes", fmt.Sprint(n))
	case 2, 3:
		src := parts[0]
		m.Target = parts[1]
		if len(parts) == 3 {
			for _, o := range strings.Split(parts[2], ",") {
				switch o {
				case "ro":
					m.ReadOnly = true
				case "rw", "":
				default:
					return m, fmt.Errorf("unknown mount option %q in %q", o, spec)
				}
			}
		}
		if filepath.IsAbs(src) || strings.HasPrefix(src, ".") || strings.ContainsAny(src, `/\`) {
			abs, err := filepath.Abs(src)
			if err != nil {
				return m, err
			}
			m.Source = abs
		} else if volumeName.MatchString(src) {
			p, err := CreateVolume(src)
			if err != nil {
				return m, err
			}
			m.Source = p
		} else {
			return m, fmt.Errorf("invalid volume name %q", src)
		}
	default:
		return m, fmt.Errorf("invalid volume spec %q", spec)
	}
	if !strings.HasPrefix(m.Target, "/") {
		return m, fmt.Errorf("volume target must be an absolute path, got %q", m.Target)
	}
	if _, err := os.Stat(m.Source); os.IsNotExist(err) {
		if err := os.MkdirAll(m.Source, 0o755); err != nil {
			return m, err
		}
	}
	return m, nil
}

// applyMounts wires volumes into the container folder. The returned func
// must be called once the container has stopped.
func (c *Container) applyMounts() (func(), error) {
	root := c.Rootfs()
	var undo []func()
	cleanup := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}

	if c.Isolated {
		prep, err := proc.Prepare(root)
		if err != nil {
			return func() {}, err
		}
		undo = append(undo, prep)
	}

	for _, m := range c.Mounts {
		target, err := image.ResolveParent(root, m.Target)
		if err != nil {
			cleanup()
			return func() {}, err
		}
		if proc.MountedUnder(target) {
			cleanup()
			return func() {}, fmt.Errorf("%s is still mounted from an earlier run", m.Target)
		}

		if c.Isolated && goruntime.GOOS == "linux" {
			st, err := os.Stat(m.Source)
			if err != nil {
				cleanup()
				return func() {}, err
			}
			if st.IsDir() {
				err = os.MkdirAll(target, 0o755)
			} else if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				var f *os.File
				if f, err = os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
					f.Close()
				}
			}
			if err == nil {
				err = proc.BindMount(m.Source, target, m.ReadOnly)
			}
			if err != nil {
				cleanup()
				return func() {}, fmt.Errorf("mounting %s: %w", m.Target, err)
			}
			undo = append(undo, func() { proc.Unmount(target) })
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return func() {}, err
		}
		os.RemoveAll(target)
		if err := os.Symlink(m.Source, target); err != nil {
			cleanup()
			return func() {}, fmt.Errorf("linking %s: %w (on Windows this needs developer mode or an admin shell)", m.Target, err)
		}
	}
	return cleanup, nil
}

func (c *Container) mountSkips() map[string]bool {
	skip := map[string]bool{}
	for _, m := range c.Mounts {
		skip[strings.TrimPrefix(filepath.ToSlash(filepath.Clean(m.Target)), "/")] = true
	}
	return skip
}

type Volume struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Created time.Time `json:"created"`
	UsedBy  []string  `json:"used_by,omitempty"`
}

func VolumePath(name string) string { return filepath.Join(config.Dir("volumes"), name) }

// CreateVolume makes a named volume. An empty name picks a random one.
func CreateVolume(name string) (string, error) {
	if name == "" {
		name = newID()
	}
	if !volumeName.MatchString(name) {
		return "", fmt.Errorf("invalid volume name %q", name)
	}
	p := VolumePath(name)
	return p, os.MkdirAll(p, 0o755)
}

func ListVolumes() ([]Volume, error) {
	entries, err := os.ReadDir(config.Dir("volumes"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	all, _ := List(true)
	var out []Volume
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v := Volume{Name: e.Name(), Path: VolumePath(e.Name())}
		if info, err := e.Info(); err == nil {
			v.Created = info.ModTime()
		}
		for _, c := range all {
			for _, m := range c.Mounts {
				if m.Source == v.Path {
					v.UsedBy = append(v.UsedBy, c.Name)
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func InspectVolume(name string) (*Volume, error) {
	vs, err := ListVolumes()
	if err != nil {
		return nil, err
	}
	for i := range vs {
		if vs[i].Name == name {
			return &vs[i], nil
		}
	}
	return nil, fmt.Errorf("no such volume: %s", name)
}

func RemoveVolume(name string, force bool) error {
	v, err := InspectVolume(name)
	if err != nil {
		return err
	}
	if len(v.UsedBy) > 0 && !force {
		return fmt.Errorf("volume %s is used by %s", name, strings.Join(v.UsedBy, ", "))
	}
	if proc.MountedUnder(v.Path) {
		return errors.New("volume is still mounted somewhere")
	}
	return os.RemoveAll(v.Path)
}

func PruneVolumes() ([]string, error) {
	vs, err := ListVolumes()
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, v := range vs {
		if len(v.UsedBy) == 0 {
			if err := RemoveVolume(v.Name, false); err != nil {
				return gone, err
			}
			gone = append(gone, v.Name)
		}
	}
	return gone, nil
}
