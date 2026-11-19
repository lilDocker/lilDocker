package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"lildocker/image"
	"lildocker/runtime"
)

type runFlags struct {
	opts   runtime.RunOptions
	env    listFlag
	files  listFlag
	vols   listFlag
	ports  listFlag
	ignore bool
}

func newRunFlags(fs *flag.FlagSet, withRun bool) *runFlags {
	r := &runFlags{}
	o := &r.opts
	strFlag(fs, &o.Name, "give the container a name", "name")
	listVar(fs, &r.env, "set an environment variable (KEY=VALUE)", "e", "env")
	listVar(fs, &r.files, "read environment variables from a file", "env-file")
	strFlag(fs, &o.WorkDir, "working directory inside the container", "w", "workdir")
	listVar(fs, &r.vols, "mount a volume: [source:]target[:ro]", "v", "volume")
	listVar(fs, &r.ports, "publish a port: [ip:]hostPort:containerPort", "p", "publish")
	strFlag(fs, &o.Restart, "restart policy: no, always, unless-stopped, on-failure[:N]", "restart")
	strFlag(fs, &o.Entrypoint, "replace the image's entrypoint", "entrypoint")
	strFlag(fs, &o.Hostname, "container hostname (sets $HOSTNAME)", "h", "hostname")
	boolFlag(fs, &o.Isolate, "chroot and namespaces (linux, needs root)", "isolate")
	if withRun {
		boolFlag(fs, &o.Detach, "run in the background and print the container id", "d", "detach")
		boolFlag(fs, &o.Remove, "remove the container when it exits", "rm")
		boolFlag(fs, &r.ignore, "accepted for docker compatibility, ignored", "i", "interactive")
		boolFlag(fs, &r.ignore, "accepted for docker compatibility, ignored", "t", "tty")
	}
	return r
}

func (r *runFlags) build() (runtime.RunOptions, error) {
	o := r.opts
	for _, f := range r.files {
		vars, err := readEnvFile(f)
		if err != nil {
			return o, err
		}
		o.Env = append(o.Env, vars...)
	}
	for _, e := range r.env {
		if !strings.Contains(e, "=") {
			e += "=" + os.Getenv(e)
		}
		o.Env = append(o.Env, e)
	}
	o.Volumes, o.Ports = r.vols, r.ports
	return o, nil
}

func readEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			line += "=" + os.Getenv(line)
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

func cmdRun(args []string) int {
	fs := newFlags("run")
	rf := newRunFlags(fs, true)
	if code, done := parse("run", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("run")
	}
	o, err := rf.build()
	if err != nil {
		return fail(err)
	}
	o.Args = fs.Args()[1:]
	code, err := runtime.Run(fs.Arg(0), o)
	if err != nil {
		return fail(err)
	}
	return code
}

func cmdCreate(args []string) int {
	fs := newFlags("create")
	rf := newRunFlags(fs, false)
	if code, done := parse("create", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("create")
	}
	o, err := rf.build()
	if err != nil {
		return fail(err)
	}
	o.Args = fs.Args()[1:]
	c, err := runtime.Create(fs.Arg(0), o)
	if err != nil {
		return fail(err)
	}
	fmt.Println(c.ID)
	return 0
}

func cmdStart(args []string) int {
	fs := newFlags("start")
	var attach bool
	boolFlag(fs, &attach, "attach to the container's output", "a", "attach")
	if code, done := parse("start", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("start")
	}
	code := 0
	for _, ref := range fs.Args() {
		exit, err := runtime.Start(ref, attach, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lildocker:", err)
			code = 1
			continue
		}
		if attach {
			return exit
		}
		fmt.Println(ref)
	}
	return code
}

func eachContainer(cmd string, args []string, fn func(string) error) int {
	code := 0
	for _, ref := range args {
		if err := fn(ref); err != nil {
			fmt.Fprintln(os.Stderr, "lildocker:", err)
			code = 1
			continue
		}
		fmt.Println(ref)
	}
	return code
}

func cmdStop(args []string) int {
	fs := newFlags("stop")
	secs := 10
	intFlag(fs, &secs, "seconds to wait before killing", "t", "time")
	if code, done := parse("stop", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("stop")
	}
	return eachContainer("stop", fs.Args(), func(ref string) error {
		_, err := runtime.Stop(ref, secs)
		return err
	})
}

func cmdRestart(args []string) int {
	fs := newFlags("restart")
	secs := 10
	intFlag(fs, &secs, "seconds to wait before killing", "t", "time")
	if code, done := parse("restart", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("restart")
	}
	return eachContainer("restart", fs.Args(), func(ref string) error { return runtime.Restart(ref, secs) })
}

func cmdKill(args []string) int {
	fs := newFlags("kill")
	sig := "KILL"
	strFlag(fs, &sig, "signal to send", "s", "signal")
	if code, done := parse("kill", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("kill")
	}
	return eachContainer("kill", fs.Args(), func(ref string) error { return runtime.Kill(ref, sig) })
}

func cmdPause(args []string) int {
	if len(args) < 1 {
		return usageErr("pause")
	}
	return eachContainer("pause", args, runtime.Pause)
}

func cmdUnpause(args []string) int {
	if len(args) < 1 {
		return usageErr("unpause")
	}
	return eachContainer("unpause", args, runtime.Unpause)
}

func cmdRm(args []string) int {
	fs := newFlags("rm")
	var force, vols bool
	boolFlag(fs, &force, "stop running containers first", "f", "force")
	boolFlag(fs, &vols, "accepted for docker compatibility, ignored", "v", "volumes")
	if code, done := parse("rm", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("rm")
	}
	return eachContainer("rm", fs.Args(), func(ref string) error { return runtime.Remove(ref, force) })
}

type psRow struct {
	ID, Image, Command, RunningFor, Status, State, Ports, Names, Name string
}

func statusText(c *runtime.Container) string {
	switch c.Status {
	case runtime.StatusRunning:
		return "Up " + since(c.Started)
	case runtime.StatusPaused:
		return "Up " + since(c.Started) + " (Paused)"
	case runtime.StatusRestarting:
		return fmt.Sprintf("Restarting (%d) %s", c.ExitCode, ago(c.Finished))
	case runtime.StatusExited:
		return fmt.Sprintf("Exited (%d) %s", c.ExitCode, ago(c.Finished))
	}
	return "Created"
}

func portText(c *runtime.Container) string {
	var out []string
	for _, p := range c.Ports {
		parts := strings.Split(strings.TrimSuffix(p, "/tcp"), ":")
		switch len(parts) {
		case 2:
			out = append(out, fmt.Sprintf("0.0.0.0:%s->%s/tcp", parts[0], parts[1]))
		case 3:
			out = append(out, fmt.Sprintf("%s:%s->%s/tcp", parts[0], parts[1], parts[2]))
		}
	}
	return strings.Join(out, ", ")
}

func display(argv []string) string {
	if len(argv) == 3 && (argv[0] == "sh" && argv[1] == "-c" || argv[0] == "cmd" && argv[1] == "/C") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

func matches(c *runtime.Container, filters []string) bool {
	for _, f := range filters {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "name":
			if !strings.Contains(c.Name, v) {
				return false
			}
		case "id":
			if !strings.HasPrefix(c.ID, v) {
				return false
			}
		case "status":
			if c.Status != v {
				return false
			}
		case "ancestor":
			if c.Image != v && c.ImageID != v {
				return false
			}
		}
	}
	return true
}

func cmdPs(args []string) int {
	fs := newFlags("ps")
	var all, quiet bool
	var last int
	var filters listFlag
	var format string
	boolFlag(fs, &all, "show stopped containers too", "a", "all")
	boolFlag(fs, &quiet, "only show ids", "q", "quiet")
	intFlag(fs, &last, "show the n most recently created", "n", "last")
	listVar(fs, &filters, "filter by name=, id=, status= or ancestor=", "f", "filter")
	strFlag(fs, &format, "Go template, e.g. '{{.ID}} {{.Names}}'", "format")
	if code, done := parse("ps", fs, args); done {
		return code
	}
	list, err := runtime.List(all || last > 0)
	if err != nil {
		return fail(err)
	}
	var rows []*runtime.Container
	for _, c := range list {
		if matches(c, filters) {
			rows = append(rows, c)
		}
	}
	if last > 0 && len(rows) > last {
		rows = rows[len(rows)-last:]
	}
	// newest first, like docker
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	if quiet {
		for _, c := range rows {
			fmt.Println(c.ID)
		}
		return 0
	}
	row := func(c *runtime.Container) psRow {
		return psRow{c.ID, c.Image, clip(display(c.Command), 28), since(c.Created) + " ago", statusText(c), c.Status, portText(c), c.Name, c.Name}
	}
	if format != "" {
		for _, c := range rows {
			s, err := render(format, row(c))
			if err != nil {
				return fail(err)
			}
			fmt.Println(s)
		}
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "CONTAINER ID\tIMAGE\tCOMMAND\tCREATED\tSTATUS\tPORTS\tNAMES")
	for _, c := range rows {
		r := row(c)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Image, r.Command, r.RunningFor, r.Status, r.Ports, r.Names)
	}
	tw.Flush()
	return 0
}

func cmdLogs(args []string) int {
	fs := newFlags("logs")
	var follow bool
	tail := -1
	boolFlag(fs, &follow, "keep following the output", "f", "follow")
	intFlag(fs, &tail, "show only the last n lines", "n", "tail")
	if code, done := parse("logs", fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		return usageErr("logs")
	}
	if err := runtime.Logs(fs.Arg(0), follow, tail, os.Stdout); err != nil {
		return fail(err)
	}
	return 0
}

func cmdAttach(args []string) int {
	if len(args) != 1 {
		return usageErr("attach")
	}
	if err := runtime.Logs(args[0], true, -1, os.Stdout); err != nil {
		return fail(err)
	}
	return 0
}

func cmdExec(args []string) int {
	fs := newFlags("exec")
	var env listFlag
	var wd string
	var ignore bool
	listVar(fs, &env, "set an environment variable", "e", "env")
	strFlag(fs, &wd, "working directory", "w", "workdir")
	boolFlag(fs, &ignore, "accepted for docker compatibility, ignored", "i", "interactive")
	boolFlag(fs, &ignore, "accepted for docker compatibility, ignored", "t", "tty")
	if code, done := parse("exec", fs, args); done {
		return code
	}
	if fs.NArg() < 2 {
		return usageErr("exec")
	}
	code, err := runtime.Exec(fs.Arg(0), fs.Args()[1:], runtime.ExecOptions{Env: env, WorkDir: wd})
	if err != nil {
		return fail(err)
	}
	return code
}

func cmdInspect(args []string) int {
	fs := newFlags("inspect")
	var format string
	strFlag(fs, &format, "Go template applied to each object", "f", "format")
	if code, done := parse("inspect", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("inspect")
	}
	var objs []any
	code := 0
	for _, ref := range fs.Args() {
		if m, err := image.Load(ref); err == nil {
			objs = append(objs, m)
		} else if c, err := runtime.Find(ref); err == nil {
			objs = append(objs, c)
		} else if v, err := runtime.InspectVolume(ref); err == nil {
			objs = append(objs, v)
		} else {
			fmt.Fprintf(os.Stderr, "lildocker: no such image, container or volume: %s\n", ref)
			code = 1
		}
	}
	if format != "" {
		for _, o := range objs {
			s, err := render(format, o)
			if err != nil {
				return fail(err)
			}
			fmt.Println(s)
		}
		return code
	}
	data, _ := json.MarshalIndent(objs, "", "  ")
	fmt.Println(string(data))
	return code
}

func cmdTop(args []string) int {
	if len(args) != 1 {
		return usageErr("top")
	}
	out, err := runtime.Top(args[0])
	fmt.Print(out)
	if err != nil {
		return fail(err)
	}
	return 0
}

func cmdPort(args []string) int {
	if len(args) != 1 {
		return usageErr("port")
	}
	ports, err := runtime.PortMappings(args[0])
	if err != nil {
		return fail(err)
	}
	for _, p := range ports {
		fmt.Println(p)
	}
	return 0
}

func cmdRename(args []string) int {
	if len(args) != 2 {
		return usageErr("rename")
	}
	if err := runtime.Rename(args[0], args[1]); err != nil {
		return fail(err)
	}
	return 0
}

func cmdWait(args []string) int {
	if len(args) < 1 {
		return usageErr("wait")
	}
	code := 0
	for _, ref := range args {
		exit, err := runtime.Wait(ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lildocker:", err)
			code = 1
			continue
		}
		fmt.Println(exit)
	}
	return code
}

// containerPath splits "name:/path", leaving Windows drive letters alone.
func containerPath(s string) (string, string, bool) {
	i := strings.Index(s, ":")
	if i < 2 {
		return "", s, false
	}
	return s[:i], s[i+1:], true
}

func cmdCp(args []string) int {
	if len(args) != 2 {
		return usageErr("cp")
	}
	srcC, srcP, fromC := containerPath(args[0])
	dstC, dstP, toC := containerPath(args[1])
	switch {
	case fromC && !toC:
		if err := runtime.CopyFrom(srcC, srcP, dstP); err != nil {
			return fail(err)
		}
	case toC && !fromC:
		if err := runtime.CopyTo(dstC, srcP, dstP); err != nil {
			return fail(err)
		}
	default:
		fmt.Fprintln(os.Stderr, "lildocker: one side of cp must be <container>:<path>")
		return 1
	}
	return 0
}

func cmdDiff(args []string) int {
	if len(args) != 1 {
		return usageErr("diff")
	}
	changes, err := runtime.Diff(args[0])
	if err != nil {
		return fail(err)
	}
	for _, c := range changes {
		fmt.Println(c.Kind, c.Path)
	}
	return 0
}

func cmdCommit(args []string) int {
	if len(args) != 2 {
		return usageErr("commit")
	}
	m, err := runtime.Commit(args[0], args[1])
	if err != nil {
		return fail(err)
	}
	fmt.Println(m.ID)
	return 0
}

func cmdExport(args []string) int {
	fs := newFlags("export")
	var out string
	strFlag(fs, &out, "write to a file instead of stdout", "o", "output")
	if code, done := parse("export", fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		return usageErr("export")
	}
	var w io.Writer = os.Stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		w = f
	}
	if err := runtime.Export(fs.Arg(0), w); err != nil {
		return fail(err)
	}
	return 0
}

func cmdUpdate(args []string) int {
	fs := newFlags("update")
	var restart string
	strFlag(fs, &restart, "new restart policy", "restart")
	if code, done := parse("update", fs, args); done {
		return code
	}
	if fs.NArg() < 1 || restart == "" {
		return usageErr("update")
	}
	return eachContainer("update", fs.Args(), func(ref string) error { return runtime.Update(ref, restart) })
}

func cmdContainerPrune(args []string) int {
	fs := newFlags("prune")
	var force bool
	boolFlag(fs, &force, "do not ask for confirmation", "f", "force")
	if code, done := parse("prune", fs, args); done {
		return code
	}
	if !confirm("This will remove all stopped containers. Continue?", force) {
		return 0
	}
	gone, err := runtime.Prune()
	if err != nil {
		return fail(err)
	}
	for _, id := range gone {
		fmt.Println(id)
	}
	fmt.Printf("removed %d container(s)\n", len(gone))
	return 0
}
