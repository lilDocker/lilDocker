package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"lildocker/image"
	"lildocker/parser"
	"lildocker/proc"
	"lildocker/registry"
)

type RunOptions struct {
	Name       string
	WorkDir    string
	Entrypoint string
	Restart    string
	Hostname   string
	Env        []string
	Volumes    []string
	Ports      []string
	Args       []string
	Detach     bool
	Remove     bool
	Isolate    bool
	Stdout     io.Writer
}

const defaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func Run(img string, o RunOptions) (int, error) {
	c, err := Create(img, o)
	if err != nil {
		return 1, err
	}
	out := o.Stdout
	if out == nil {
		out = os.Stdout
	}
	if o.Detach {
		if err := spawn(c); err != nil {
			return 1, err
		}
		fmt.Fprintln(out, c.ID)
		return 0, nil
	}
	return c.execute(out, os.Stdin)
}

// Start runs an existing container again, in the background unless attach is set.
func Start(ref string, attach bool, out io.Writer) (int, error) {
	c, err := Find(ref)
	if err != nil {
		return 1, err
	}
	if c.Active() {
		return 0, nil
	}
	if attach {
		return c.execute(out, os.Stdin)
	}
	c.Status = StatusCreated
	if err := c.save(); err != nil {
		return 1, err
	}
	return 0, spawn(c)
}

// Supervise is what a background container's helper process runs.
func Supervise(id string) error {
	c, err := Find(id)
	if err != nil {
		return err
	}
	_, err = c.execute(nil, nil)
	return err
}

func resolveImage(ref string) (*image.Manifest, error) {
	m, err := image.Load(ref)
	if err == nil || !errors.Is(err, image.ErrNotFound) {
		return m, err
	}
	fmt.Fprintf(os.Stderr, "Unable to find image %q locally\n", ref)
	m, perr := registry.Pull(ref, os.Stderr)
	if perr != nil {
		return nil, fmt.Errorf("%w (pulling it failed: %v)", err, perr)
	}
	return m, nil
}

var restartPolicy = func(p string) bool {
	switch p {
	case "", "no", "always", "unless-stopped", "on-failure":
		return true
	}
	if n, ok := strings.CutPrefix(p, "on-failure:"); ok {
		_, err := strconv.Atoi(n)
		return err == nil
	}
	return false
}

func Create(img string, o RunOptions) (*Container, error) {
	m, err := resolveImage(img)
	if err != nil {
		return nil, err
	}
	if m.OS != "" {
		if goruntime.GOOS != "linux" {
			return nil, fmt.Errorf("%s is a linux image and can only run on linux", m.Ref())
		}
		o.Isolate = true
	}
	if !restartPolicy(o.Restart) {
		return nil, fmt.Errorf("invalid restart policy %q (no, always, unless-stopped, on-failure[:N])", o.Restart)
	}
	for _, p := range o.Ports {
		if _, err := parsePort(p); err != nil {
			return nil, err
		}
	}

	im := *m
	if o.Entrypoint != "" {
		im.Entrypoint = o.Entrypoint
		if !strings.HasPrefix(strings.TrimSpace(o.Entrypoint), "[") {
			b, _ := json.Marshal([]string{o.Entrypoint})
			im.Entrypoint = string(b)
		}
	}
	argv := resolveCommand(&im, o.Args)
	if len(argv) == 0 {
		return nil, fmt.Errorf("image %s has no command, pass one to run", m.Ref())
	}
	name, err := uniqueName(o.Name)
	if err != nil {
		return nil, err
	}

	c := &Container{
		ID:         newID(),
		Name:       name,
		Image:      img,
		ImageID:    m.ID,
		Command:    argv,
		Isolated:   o.Isolate,
		AutoRemove: o.Remove,
		Ports:      o.Ports,
		Restart:    o.Restart,
		StopSignal: m.StopSignal,
		Status:     StatusCreated,
		Created:    time.Now(),
	}
	c.Hostname = o.Hostname
	if c.Hostname == "" {
		c.Hostname = c.ID
	}
	c.Env = append(append([]string(nil), m.Env...), o.Env...)
	c.Env = append(c.Env, "HOSTNAME="+c.Hostname)

	wd := m.WorkDir
	if o.WorkDir != "" {
		wd = o.WorkDir
		if !strings.HasPrefix(wd, "/") {
			wd = filepath.ToSlash(filepath.Join(m.WorkDir, wd))
		}
	}
	c.WorkDir = "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Clean(wd)), "/")

	fail := func(err error) (*Container, error) {
		os.RemoveAll(c.dir())
		return nil, err
	}
	if err := os.MkdirAll(c.Rootfs(), 0o755); err != nil {
		return fail(err)
	}
	if err := image.Extract(m, c.Rootfs()); err != nil {
		return fail(err)
	}

	covered := map[string]bool{}
	specs := append([]string(nil), o.Volumes...)
	for _, sp := range specs {
		parts := splitSpec(sp)
		t := parts[0]
		if len(parts) > 1 {
			t = parts[1]
		}
		covered[filepath.ToSlash(filepath.Clean(t))] = true
	}
	for _, v := range m.Volumes {
		if !covered[v] {
			specs = append(specs, v)
		}
	}
	for i, sp := range specs {
		mt, err := parseMount(sp, c.dir(), i)
		if err != nil {
			return fail(err)
		}
		c.Mounts = append(c.Mounts, mt)
	}

	if real, err := image.ResolveInRoot(c.Rootfs(), c.WorkDir); err == nil {
		os.MkdirAll(real, 0o755)
	}
	if err := c.save(); err != nil {
		return fail(err)
	}
	return c, nil
}

func resolveCommand(m *image.Manifest, args []string) []string {
	if m.Entrypoint != "" {
		ep, shell := parser.ParseCommand(m.Entrypoint)
		if shell {
			return proc.ShellArgs(m.Entrypoint)
		}
		if len(args) == 0 && m.Cmd != "" {
			args = cmdArgs(m.Cmd)
		}
		return append(ep, args...)
	}
	if len(args) > 0 {
		return args
	}
	if m.Cmd == "" {
		return nil
	}
	return cmdArgs(m.Cmd)
}

func cmdArgs(v string) []string {
	argv, shell := parser.ParseCommand(v)
	if shell {
		return proc.ShellArgs(v)
	}
	return argv
}

func (c *Container) command(argv []string, namespaces bool, extraEnv []string, workdir string) (*exec.Cmd, error) {
	root := c.Rootfs()
	if workdir == "" {
		workdir = c.WorkDir
	}

	if !c.Isolated {
		bin := argv[0]
		if strings.HasPrefix(bin, "/") {
			if inside, err := image.ResolveInRoot(root, bin); err == nil {
				if st, err := os.Stat(inside); err == nil && !st.IsDir() {
					bin = inside
				}
			}
		}
		cmd := exec.Command(bin, argv[1:]...)
		cmd.Dir = filepath.Join(root, filepath.FromSlash(workdir))
		cmd.Env = append(append(os.Environ(), c.Env...), extraEnv...)
		return cmd, proc.Configure(cmd, proc.Options{Group: true})
	}

	env := append(append([]string(nil), c.Env...), extraEnv...)
	if !hasPath(env) {
		env = append(env, "PATH="+defaultPath)
	}
	bin, err := lookInRoot(root, argv[0], env)
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{Path: bin, Args: argv, Env: env, Dir: workdir}
	err = proc.Configure(cmd, proc.Options{Group: true, Chroot: root, Namespaces: namespaces})
	return cmd, err
}

func hasPath(env []string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			return true
		}
	}
	return false
}

func lookInRoot(root, name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	dirs := defaultPath
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			dirs = v
		}
	}
	for _, d := range filepath.SplitList(dirs) {
		p := "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(d, name)), "/")
		if real, err := image.ResolveInRoot(root, p); err == nil {
			if st, err := os.Stat(real); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%q not found inside the container", name)
}

func (c *Container) execute(stdout io.Writer, stdin io.Reader) (int, error) {
	logFile, err := os.OpenFile(c.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 1, err
	}
	defer logFile.Close()
	var out io.Writer = logFile
	if stdout != nil {
		out = io.MultiWriter(quiet{stdout}, logFile)
	}
	fail := func(err error) (int, error) {
		fmt.Fprintln(logFile, "lilDocker:", err)
		c.finish(127)
		return 127, err
	}

	os.Remove(c.stopMarker())
	undoMounts, err := c.applyMounts()
	if err != nil {
		return fail(err)
	}
	stopFwd, err := startForwards(c.Ports)
	if err != nil {
		undoMounts()
		return fail(err)
	}

	var current atomic.Int32
	var interrupted atomic.Bool
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sigs {
			interrupted.Store(true)
			if p := current.Load(); p > 0 {
				proc.Terminate(int(p))
			}
		}
	}()

	code, runErr := 0, error(nil)
	for {
		code, runErr = c.runOnce(out, stdin, &current)
		if runErr != nil {
			fmt.Fprintln(logFile, "lilDocker:", runErr)
			break
		}
		if !c.shouldRestart(code, interrupted.Load()) {
			break
		}
		c.RestartCount++
		c.Status, c.ExitCode = StatusRestarting, code
		c.persist()
		if !c.pause(&interrupted, time.Second) {
			break
		}
	}

	signal.Stop(sigs)
	close(sigs)
	stopFwd()
	undoMounts()
	c.finish(code)
	return code, runErr
}

func (c *Container) runOnce(out io.Writer, in io.Reader, current *atomic.Int32) (int, error) {
	cmd, err := c.command(c.Command, true, nil, "")
	if err != nil {
		return 127, err
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = out, out, in
	if err := cmd.Start(); err != nil {
		if c.Isolated {
			err = fmt.Errorf("%w (isolation needs linux and root)", err)
		}
		return 127, err
	}
	current.Store(int32(cmd.Process.Pid))
	c.Status, c.Pid, c.Supervisor, c.Started = StatusRunning, cmd.Process.Pid, os.Getpid(), time.Now()
	c.persist()

	cmd.Wait()
	current.Store(0)
	return proc.ExitCode(cmd.ProcessState), nil
}

func (c *Container) stopRequested(interrupted *atomic.Bool) bool {
	if interrupted.Load() {
		return true
	}
	_, err := os.Stat(c.stopMarker())
	return err == nil
}

func (c *Container) shouldRestart(code int, interrupted bool) bool {
	if interrupted {
		return false
	}
	if _, err := os.Stat(c.stopMarker()); err == nil {
		return false
	}
	switch {
	case c.Restart == "always" || c.Restart == "unless-stopped":
		return true
	case c.Restart == "on-failure":
		return code != 0
	case strings.HasPrefix(c.Restart, "on-failure:"):
		max, _ := strconv.Atoi(strings.TrimPrefix(c.Restart, "on-failure:"))
		return code != 0 && c.RestartCount < max
	}
	return false
}

// pause waits between restarts and reports whether to carry on.
func (c *Container) pause(interrupted *atomic.Bool, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if c.stopRequested(interrupted) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !c.stopRequested(interrupted)
}

func (c *Container) finish(code int) {
	if c.AutoRemove {
		c.removeFiles()
		return
	}
	c.Status, c.ExitCode, c.Finished = StatusExited, code, time.Now()
	c.persist()
}

func spawn(c *Container) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "_supervise", c.ID)
	if err := proc.Configure(cmd, proc.Options{Detach: true}); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()

	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if s, err := readState(c.dir()); err != nil || s.Status != StatusCreated {
			break
		}
	}
	return nil
}

// quiet swallows write errors, so a closed terminal or pipe never kills the log.
type quiet struct{ w io.Writer }

func (q quiet) Write(p []byte) (int, error) {
	q.w.Write(p)
	return len(p), nil
}

type ExecOptions struct {
	Env     []string
	WorkDir string
	Stdout  io.Writer
}

// Exec runs a command against a container's filesystem.
func Exec(ref string, argv []string, o ExecOptions) (int, error) {
	c, err := Find(ref)
	if err != nil {
		return 1, err
	}
	if _, err := os.Stat(c.Rootfs()); err != nil {
		return 1, fmt.Errorf("container %s has no filesystem", c.Name)
	}
	cmd, err := c.command(argv, false, o.Env, o.WorkDir)
	if err != nil {
		return 127, err
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if o.Stdout != nil {
		cmd.Stdout, cmd.Stderr = o.Stdout, o.Stdout
	}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return proc.ExitCode(ee.ProcessState), nil
		}
		return 127, err
	}
	return 0, nil
}

func Stop(ref string, timeoutSec int) (*Container, error) {
	c, err := Find(ref)
	if err != nil {
		return nil, err
	}
	if !c.Active() {
		return c, nil
	}
	os.WriteFile(c.stopMarker(), nil, 0o644)

	if c.Status == StatusPaused {
		proc.Signal(c.Pid, "CONT")
	}
	sig := c.StopSignal
	if sig == "" {
		sig = "TERM"
	}
	if err := proc.Signal(c.Pid, sig); err != nil {
		proc.Terminate(c.Pid)
	}
	if !waitDead(c.Pid, time.Duration(timeoutSec)*time.Second) {
		proc.Kill(c.Pid)
		waitDead(c.Pid, 3*time.Second)
	}

	for i := 0; i < 30; i++ {
		s, err := readState(c.dir())
		if err != nil || !s.Active() {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.Status, c.ExitCode, c.Finished = StatusExited, 137, time.Now()
	c.save()
	return c, nil
}

func waitDead(pid int, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if !proc.Alive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !proc.Alive(pid)
}

func Restart(ref string, timeoutSec int) error {
	if _, err := Stop(ref, timeoutSec); err != nil {
		return err
	}
	_, err := Start(ref, false, nil)
	return err
}

func Kill(ref, sig string) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	if !c.Active() {
		return fmt.Errorf("%s is not running", c.Name)
	}
	if sig == "" {
		sig = "KILL"
	}
	return proc.Signal(c.Pid, sig)
}

func Pause(ref string) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	if c.Status != StatusRunning {
		return fmt.Errorf("%s is not running", c.Name)
	}
	if err := proc.Signal(c.Pid, "STOP"); err != nil {
		return err
	}
	c.Status = StatusPaused
	return c.save()
}

func Unpause(ref string) error {
	c, err := Find(ref)
	if err != nil {
		return err
	}
	if c.Status != StatusPaused {
		return fmt.Errorf("%s is not paused", c.Name)
	}
	if err := proc.Signal(c.Pid, "CONT"); err != nil {
		return err
	}
	c.Status = StatusRunning
	return c.save()
}

// Update changes the restart policy of an existing container.
func Update(ref, restart string) error {
	if !restartPolicy(restart) {
		return fmt.Errorf("invalid restart policy %q", restart)
	}
	c, err := Find(ref)
	if err != nil {
		return err
	}
	c.Restart = restart
	return c.save()
}

// Wait blocks until the container stops and returns its exit code.
func Wait(ref string) (int, error) {
	for {
		c, err := Find(ref)
		if err != nil {
			return 1, err
		}
		if !c.Active() && c.Status != StatusCreated {
			return c.ExitCode, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}
