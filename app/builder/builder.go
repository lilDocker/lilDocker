package builder

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"lildocker/config"
	"lildocker/image"
	"lildocker/parser"
	"lildocker/proc"
	"lildocker/registry"
)

type Options struct {
	Context string
	File    string
	Tags    []string
	Args    map[string]string
	Target  string
}

type stage struct {
	name string
	root string
	m    image.Manifest
	args map[string]string
}

type build struct {
	opts   Options
	src    string
	home   string
	skip   []string
	tmp    string
	stages []*stage
	global map[string]string
}

func BuildImage(srcDir, ref string) error {
	return Build(Options{Context: srcDir, Tags: []string{ref}})
}

func Build(o Options) error {
	if len(o.Tags) == 0 {
		return errors.New("an image name is required")
	}
	for _, t := range o.Tags {
		if _, _, err := image.ParseRef(t); err != nil {
			return err
		}
	}
	src, err := filepath.Abs(o.Context)
	if err != nil {
		return err
	}
	file := o.File
	if file == "" {
		file = filepath.Join(src, "lilDockfile")
	}
	steps, err := parser.ParseLilDockfile(file)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		return errors.New("lilDockfile is empty")
	}

	os.MkdirAll(config.Dir("temp"), 0o755)
	tmp, err := os.MkdirTemp(config.Dir("temp"), "build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	home, _ := filepath.Abs(config.Home())
	b := &build{opts: o, src: src, home: home, skip: readIgnore(src), tmp: tmp, global: map[string]string{}}

	var cur *stage
	firstReal := firstNonArg(steps)
	if firstReal >= 0 && steps[firstReal].Command != "FROM" {
		if cur, err = b.newStage("scratch", ""); err != nil {
			return err
		}
		if !has(steps, "COPY") && !has(steps, "ADD") {
			if err := b.copyContext(src, cur.root); err != nil {
				return err
			}
		}
	}

	for i, s := range steps {
		if s.Command == "FROM" && cur != nil && o.Target != "" && cur.name == o.Target {
			break
		}
		fmt.Printf("Step %d/%d : %s %s\n", i+1, len(steps), s.Command, s.Value)
		var err error
		switch {
		case s.Command == "FROM":
			cur, err = b.from(s.Value)
		case s.Command == "ARG" && cur == nil:
			k, v := b.argValue(s.Value, b.global)
			b.global[k] = v
		default:
			err = b.apply(cur, s)
		}
		if err != nil {
			return fmt.Errorf("line %d (%s): %w", s.Line, s.Command, err)
		}
	}
	if cur == nil {
		return errors.New("lilDockfile has no instructions to build")
	}
	final := cur
	if o.Target != "" {
		final = nil
		for _, st := range b.stages {
			if st.name == o.Target {
				final = st
			}
		}
		if final == nil {
			return fmt.Errorf("no stage named %q", o.Target)
		}
	}

	name, tag, _ := image.ParseRef(o.Tags[0])
	final.m.Name, final.m.Tag = name, tag
	if err := image.Commit(&final.m, final.root, nil); err != nil {
		return err
	}
	for _, t := range o.Tags[1:] {
		if err := image.Tag(o.Tags[0], t); err != nil {
			return err
		}
	}
	fmt.Printf("Successfully built %s (%s)\n", final.m.Ref(), final.m.ID)
	return nil
}

func has(steps []parser.Instruction, cmd string) bool {
	for _, s := range steps {
		if s.Command == cmd {
			return true
		}
	}
	return false
}

func firstNonArg(steps []parser.Instruction) int {
	for i, s := range steps {
		if s.Command != "ARG" {
			return i
		}
	}
	return -1
}

func (b *build) newStage(ref, name string) (*stage, error) {
	st := &stage{
		name: name,
		root: filepath.Join(b.tmp, fmt.Sprintf("stage%d", len(b.stages)), "rootfs"),
		m:    image.Manifest{WorkDir: "/"},
		args: map[string]string{},
	}
	for k, v := range b.global {
		st.args[k] = v
	}
	if err := os.MkdirAll(st.root, 0o755); err != nil {
		return nil, err
	}
	if ref != "scratch" {
		base, err := image.Load(ref)
		if errors.Is(err, image.ErrNotFound) {
			if base, err = registry.Pull(ref, os.Stdout); err != nil {
				return nil, fmt.Errorf("base image %q: %w", ref, err)
			}
		} else if err != nil {
			return nil, err
		}
		if err := image.Extract(base, st.root); err != nil {
			return nil, err
		}
		st.m = *base
		st.m.Base = ref
		st.m.Env = append([]string(nil), base.Env...)
		st.m.Expose = append([]string(nil), base.Expose...)
		st.m.Volumes = append([]string(nil), base.Volumes...)
		st.m.History = append([]string(nil), base.History...)
		st.m.Labels = map[string]string{}
		for k, v := range base.Labels {
			st.m.Labels[k] = v
		}
	}
	b.stages = append(b.stages, st)
	return st, nil
}

func (b *build) from(value string) (*stage, error) {
	f := strings.Fields(b.expand(value, b.global, nil))
	switch {
	case len(f) == 1:
		return b.newStage(f[0], "")
	case len(f) == 3 && strings.EqualFold(f[1], "AS"):
		return b.newStage(f[0], f[2])
	}
	return nil, errors.New("expected: FROM <image> [AS <name>]")
}

func (b *build) vars(st *stage) map[string]string {
	v := map[string]string{}
	if st == nil {
		return v
	}
	for k, val := range st.args {
		v[k] = val
	}
	for _, e := range st.m.Env {
		if k, val, ok := strings.Cut(e, "="); ok {
			v[k] = val
		}
	}
	return v
}

func (b *build) expand(s string, vars map[string]string, st *stage) string {
	if st != nil {
		vars = b.vars(st)
	}
	return os.Expand(s, func(k string) string { return vars[k] })
}

func (b *build) argValue(value string, into map[string]string) (string, string) {
	k, v, hasDefault := strings.Cut(strings.TrimSpace(value), "=")
	v = strings.Trim(v, `"'`)
	if over, ok := b.opts.Args[k]; ok {
		v = over
	} else if !hasDefault {
		v = into[k]
	}
	return k, v
}

func (b *build) apply(st *stage, s parser.Instruction) error {
	if st == nil {
		return errors.New("the first instruction must be FROM")
	}
	val := s.Value
	switch s.Command {
	case "RUN", "CMD", "ENTRYPOINT":
	default:
		val = b.expand(val, nil, st)
	}
	st.m.History = append(st.m.History, s.Command+" "+s.Value)

	switch s.Command {
	case "WORKDIR":
		wd := val
		if !strings.HasPrefix(wd, "/") {
			wd = path.Join(st.m.WorkDir, wd)
		}
		st.m.WorkDir = path.Clean(wd)
		return os.MkdirAll(st.path(st.m.WorkDir), 0o755)

	case "ENV":
		for _, kv := range envPairs(val) {
			k, v, err := parser.ParseKeyValue(kv)
			if err != nil {
				return err
			}
			st.setEnv(k, v)
		}

	case "ARG":
		k, v := b.argValue(val, st.args)
		st.args[k] = v

	case "LABEL":
		if st.m.Labels == nil {
			st.m.Labels = map[string]string{}
		}
		for _, kv := range parser.SplitArgs(val) {
			k, v, err := parser.ParseKeyValue(kv)
			if err != nil {
				return err
			}
			st.m.Labels[k] = v
		}

	case "COPY", "ADD":
		return b.copy(st, val, s.Command == "ADD")

	case "RUN":
		return b.run(st, val)

	case "EXPOSE":
		st.m.Expose = append(st.m.Expose, strings.Fields(val)...)

	case "VOLUME":
		for _, v := range parser.SplitArgs(val) {
			st.m.Volumes = append(st.m.Volumes, path.Clean("/"+v))
		}

	case "USER":
		st.m.User = val

	case "STOPSIGNAL":
		st.m.StopSignal = val

	case "CMD":
		st.m.Cmd = val

	case "ENTRYPOINT":
		st.m.Entrypoint = val
	}
	return nil
}

func envPairs(v string) []string {
	if strings.Contains(v, "=") {
		first := strings.Index(v, "=")
		if sp := strings.IndexAny(v, " \t"); sp < 0 || first < sp {
			return parser.SplitArgs(v)
		}
	}
	return []string{v}
}

func (st *stage) setEnv(k, v string) {
	for i, e := range st.m.Env {
		if strings.HasPrefix(e, k+"=") {
			st.m.Env[i] = k + "=" + v
			return
		}
	}
	st.m.Env = append(st.m.Env, k+"="+v)
}

// path maps a container path onto the stage folder, never leaving it.
func (st *stage) path(p string) string {
	real, _ := image.ResolveInRoot(st.root, path.Clean("/"+p))
	return real
}

func (b *build) run(st *stage, script string) error {
	env := append(os.Environ(), st.m.Env...)
	for k, v := range st.args {
		env = append(env, k+"="+v)
	}

	if st.m.OS == "" {
		cmd := proc.Shell(script)
		cmd.Dir = st.path(st.m.WorkDir)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}

	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("RUN on a pulled base image needs linux and root, since it runs inside the image")
	}
	cleanEnv := append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, st.m.Env...)
	for k, v := range st.args {
		cleanEnv = append(cleanEnv, k+"="+v)
	}
	undo, err := proc.Prepare(st.root)
	if err != nil {
		return err
	}
	defer undo()
	cmd := &exec.Cmd{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", script}, Dir: st.m.WorkDir, Env: cleanEnv}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := proc.Configure(cmd, proc.Options{Chroot: st.root}); err != nil {
		return err
	}
	return cmd.Run()
}

func (b *build) copy(st *stage, value string, isAdd bool) error {
	args := parser.SplitArgs(value)
	from := ""
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		if v, ok := strings.CutPrefix(args[0], "--from="); ok {
			from = v
		}
		args = args[1:]
	}
	if len(args) < 2 {
		return errors.New("needs at least one source and a destination")
	}
	dstArg, srcArgs := args[len(args)-1], args[:len(args)-1]

	base, fromStage := b.src, (*stage)(nil)
	if from != "" {
		if fromStage = b.findStage(from); fromStage == nil {
			return fmt.Errorf("--from=%s: no such stage", from)
		}
		base = fromStage.root
	}

	var matches []string
	for _, a := range srcArgs {
		pattern := filepath.Join(base, filepath.FromSlash(a))
		if fromStage != nil {
			pattern = filepath.Join(base, filepath.FromSlash(path.Clean("/"+a)))
		}
		found, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return fmt.Errorf("%s: no such file", a)
		}
		for _, m := range found {
			rel, err := filepath.Rel(base, m)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("%s: outside the build context", a)
			}
			if fromStage == nil && b.ignored(rel) {
				continue
			}
			matches = append(matches, m)
		}
	}

	dst := dstArg
	if !strings.HasPrefix(dst, "/") {
		dst = path.Join(st.m.WorkDir, dst)
	}
	target := st.path(dst)
	intoDir := strings.HasSuffix(dstArg, "/") || dstArg == "." || len(matches) > 1

	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := image.CopyTree(m, target, b.skipFunc(fromStage == nil)); err != nil {
				return err
			}
			continue
		}
		if isAdd && isArchive(m) {
			if err := extractArchive(m, target); err != nil {
				return err
			}
			continue
		}
		out := target
		if st, err := os.Stat(target); intoDir || (err == nil && st.IsDir()) {
			out = filepath.Join(target, filepath.Base(m))
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := image.CopyFile(m, out, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func (b *build) findStage(ref string) *stage {
	for i, st := range b.stages {
		if st.name == ref || fmt.Sprint(i) == ref {
			return st
		}
	}
	return nil
}

func isArchive(p string) bool {
	return strings.HasSuffix(p, ".tar") || strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz")
}

func extractArchive(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return image.ApplyLayer(f, dst)
}

func (b *build) copyContext(src, dst string) error {
	return image.CopyTree(src, dst, b.skipFunc(true))
}

func (b *build) skipFunc(useIgnore bool) func(string, fs.DirEntry) bool {
	return func(p string, d fs.DirEntry) bool {
		if p == b.home {
			return true
		}
		if !useIgnore {
			return false
		}
		rel, err := filepath.Rel(b.src, p)
		return err == nil && rel != "." && b.ignored(rel)
	}
}

func readIgnore(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, ".lildockerignore"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, strings.TrimSuffix(line, "/"))
		}
	}
	return out
}

func (b *build) ignored(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, pat := range b.skip {
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
		if ok, _ := path.Match(pat, path.Base(rel)); ok {
			return true
		}
	}
	return false
}
