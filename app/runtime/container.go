package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lildocker/config"
	"lildocker/proc"
)

const (
	StatusCreated    = "created"
	StatusRunning    = "running"
	StatusPaused     = "paused"
	StatusRestarting = "restarting"
	StatusExited     = "exited"
)

type Mount struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"read_only,omitempty"`
	Anonymous bool   `json:"anonymous,omitempty"`
}

type Container struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Image        string    `json:"image"`
	ImageID      string    `json:"image_id"`
	Command      []string  `json:"command"`
	Env          []string  `json:"env,omitempty"`
	WorkDir      string    `json:"workdir"`
	Hostname     string    `json:"hostname"`
	Isolated     bool      `json:"isolated"`
	AutoRemove   bool      `json:"auto_remove"`
	Ports        []string  `json:"ports,omitempty"`
	Mounts       []Mount   `json:"mounts,omitempty"`
	Restart      string    `json:"restart,omitempty"`
	RestartCount int       `json:"restart_count"`
	StopSignal   string    `json:"stop_signal,omitempty"`
	Status       string    `json:"status"`
	Pid          int       `json:"pid"`
	Supervisor   int       `json:"supervisor"`
	ExitCode     int       `json:"exit_code"`
	Created      time.Time `json:"created"`
	Started      time.Time `json:"started"`
	Finished     time.Time `json:"finished"`
}

func containersDir() string { return config.Dir("containers") }

func (c *Container) dir() string        { return filepath.Join(containersDir(), c.ID) }
func (c *Container) Rootfs() string     { return filepath.Join(c.dir(), "rootfs") }
func (c *Container) logPath() string    { return filepath.Join(c.dir(), "container.log") }
func (c *Container) statePath() string  { return filepath.Join(c.dir(), "state.json") }
func (c *Container) stopMarker() string { return filepath.Join(c.dir(), "stopped") }

func (c *Container) save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.statePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.statePath())
}

// persist is save for the supervising process: it first picks up fields other
// commands may have changed meanwhile (rename, update --restart).
func (c *Container) persist() error {
	if d, err := readState(c.dir()); err == nil {
		c.Name, c.Restart = d.Name, d.Restart
	}
	return c.save()
}

func readState(dir string) (*Container, error) {
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return nil, err
	}
	var c Container
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Container) Active() bool {
	return c.Status == StatusRunning || c.Status == StatusPaused || c.Status == StatusRestarting
}

// a container whose process vanished without recording its exit shows as exited
func (c *Container) view() *Container {
	switch c.Status {
	case StatusRunning, StatusPaused:
		if !proc.Alive(c.Pid) {
			c.Status = StatusExited
		}
	case StatusRestarting:
		if !proc.Alive(c.Supervisor) {
			c.Status = StatusExited
		}
	}
	return c
}

func List(all bool) ([]*Container, error) {
	entries, err := os.ReadDir(containersDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Container
	for _, e := range entries {
		c, err := readState(filepath.Join(containersDir(), e.Name()))
		if err != nil {
			continue
		}
		c.view()
		if all || c.Active() {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// Find looks a container up by name, full id or unique id prefix.
func Find(ref string) (*Container, error) {
	if ref == "" {
		return nil, errors.New("no container given")
	}
	all, err := List(true)
	if err != nil {
		return nil, err
	}
	var prefix []*Container
	for _, c := range all {
		if c.Name == ref || c.ID == ref {
			return c, nil
		}
		if strings.HasPrefix(c.ID, ref) {
			prefix = append(prefix, c)
		}
	}
	switch len(prefix) {
	case 0:
		return nil, fmt.Errorf("no such container: %s", ref)
	case 1:
		return prefix[0], nil
	}
	return nil, fmt.Errorf("%q matches more than one container", ref)
}

func (c *Container) removeFiles() error {
	if proc.MountedUnder(c.dir()) {
		return fmt.Errorf("%s still has mounted folders, refusing to delete it", c.Name)
	}
	return os.RemoveAll(c.dir())
}

func Remove(ref string, force bool) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	if c.Active() {
		if !force {
			return fmt.Errorf("%s is running, stop it first or use -f", c.Name)
		}
		if _, err := Stop(ref, 10); err != nil {
			return err
		}
	}
	return c.removeFiles()
}

func Prune() ([]string, error) {
	all, err := List(true)
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, c := range all {
		if c.Active() {
			continue
		}
		if err := c.removeFiles(); err != nil {
			return gone, err
		}
		gone = append(gone, c.ID)
	}
	return gone, nil
}

// UsedBy lists the containers created from an image id.
func UsedBy(imageID string) []string {
	all, _ := List(true)
	var names []string
	for _, c := range all {
		if c.ImageID == imageID {
			names = append(names, c.Name)
		}
	}
	return names
}

func Rename(ref, name string) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	if name == c.Name {
		return nil
	}
	if _, err := uniqueName(name); err != nil {
		return err
	}
	c.Name = name
	return c.save()
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var (
	adjectives = []string{"brave", "calm", "eager", "fuzzy", "gentle", "happy", "jolly", "quiet", "silly", "witty"}
	animals    = []string{"otter", "panda", "falcon", "gecko", "koala", "lemur", "badger", "heron", "marmot", "newt"}
)

func pick(list []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		return list[0]
	}
	return list[n.Int64()]
}

func uniqueName(want string) (string, error) {
	all, err := List(true)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, c := range all {
		taken[c.Name] = true
	}
	if want != "" {
		if taken[want] {
			return "", fmt.Errorf("the name %q is already in use", want)
		}
		return want, nil
	}
	for {
		n := pick(adjectives) + "_" + pick(animals)
		if !taken[n] {
			return n, nil
		}
	}
}
