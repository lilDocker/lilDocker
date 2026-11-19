package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"lildocker/builder"
	"lildocker/image"
	"lildocker/registry"
	"lildocker/runtime"
)

func cmdBuild(args []string) int {
	fs := newFlags("build")
	var tags, buildArgs listFlag
	var file, target string
	listVar(fs, &tags, "name and optionally a tag (name:tag), repeatable", "t", "tag")
	strFlag(fs, &file, "path to the lilDockfile (default <path>/lilDockfile)", "f", "file")
	listVar(fs, &buildArgs, "set a build variable (KEY=VALUE)", "build-arg")
	strFlag(fs, &target, "build only up to this stage", "target")
	if code, done := parse("build", fs, args); done {
		return code
	}
	pos := fs.Args()
	if len(pos) == 2 && len(tags) == 0 {
		tags = listFlag{pos[1]}
		pos = pos[:1]
	}
	if len(pos) != 1 || len(tags) == 0 {
		fmt.Fprintln(os.Stderr, "Usage:  lildocker build -t <name[:tag]> <path>\n        lildocker build <path> <name[:tag]>")
		return 1
	}
	vars := map[string]string{}
	for _, a := range buildArgs {
		k, v, _ := strings.Cut(a, "=")
		if !strings.Contains(a, "=") {
			v = os.Getenv(k)
		}
		vars[k] = v
	}
	err := builder.Build(builder.Options{Context: pos[0], File: file, Tags: tags, Args: vars, Target: target})
	if err != nil {
		return fail(err)
	}
	return 0
}

type imageRow struct {
	Repository, Tag, ID, CreatedSince, Size string
}

func cmdImages(args []string) int {
	fs := newFlags("images")
	var all, quiet bool
	var format string
	boolFlag(fs, &all, "accepted for docker compatibility", "a", "all")
	boolFlag(fs, &quiet, "only show ids", "q", "quiet")
	strFlag(fs, &format, "Go template, e.g. '{{.Repository}}:{{.Tag}}'", "format")
	if code, done := parse("images", fs, args); done {
		return code
	}
	list, err := image.List()
	if err != nil {
		return fail(err)
	}
	var rows []image.Manifest
	for _, m := range list {
		if fs.NArg() == 0 || m.Name == fs.Arg(0) || m.Ref() == fs.Arg(0) {
			rows = append(rows, m)
		}
	}
	row := func(m image.Manifest) imageRow {
		return imageRow{m.Name, m.Tag, m.ID, ago(m.Created), size(m.Size)}
	}
	switch {
	case quiet:
		for _, m := range rows {
			fmt.Println(m.ID)
		}
	case format != "":
		for _, m := range rows {
			s, err := render(format, row(m))
			if err != nil {
				return fail(err)
			}
			fmt.Println(s)
		}
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "REPOSITORY\tTAG\tIMAGE ID\tCREATED\tSIZE")
		for _, m := range rows {
			r := row(m)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Repository, r.Tag, r.ID, r.CreatedSince, r.Size)
		}
		tw.Flush()
	}
	return 0
}

func cmdRmi(args []string) int {
	fs := newFlags("rmi")
	var force bool
	boolFlag(fs, &force, "remove even if containers use the image", "f", "force")
	if code, done := parse("rmi", fs, args); done {
		return code
	}
	if fs.NArg() < 1 {
		return usageErr("rmi")
	}
	code := 0
	for _, ref := range fs.Args() {
		m, err := image.Load(ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lildocker:", err)
			code = 1
			continue
		}
		if users := runtime.UsedBy(m.ID); len(users) > 0 && !force && lastTag(m, ref) {
			fmt.Fprintf(os.Stderr, "lildocker: %s is used by %s, remove them first or use -f\n", ref, strings.Join(users, ", "))
			code = 1
			continue
		}
		gone, err := image.Remove(ref)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lildocker:", err)
			code = 1
			continue
		}
		for _, g := range gone {
			fmt.Println("Untagged:", g)
		}
	}
	return code
}

// lastTag reports whether removing ref would leave the image id without any tag.
func lastTag(m *image.Manifest, ref string) bool {
	all, _ := image.List()
	n := 0
	for _, o := range all {
		if o.ID == m.ID {
			n++
		}
	}
	name, tag, err := image.ParseRef(ref)
	byTag := err == nil && name == m.Name && tag == m.Tag
	return !byTag || n <= 1
}

func cmdTag(args []string) int {
	if len(args) != 2 {
		return usageErr("tag")
	}
	if err := image.Tag(args[0], args[1]); err != nil {
		return fail(err)
	}
	return 0
}

func cmdHistory(args []string) int {
	if len(args) != 1 {
		return usageErr("history")
	}
	m, err := image.Load(args[0])
	if err != nil {
		return fail(err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "STEP\tCREATED BY")
	for i, h := range m.History {
		fmt.Fprintf(tw, "%d\t%s\n", i+1, clip(h, 90))
	}
	tw.Flush()
	return 0
}

func cmdSave(args []string) int {
	fs := newFlags("save")
	var out string
	strFlag(fs, &out, "write to a file instead of stdout", "o", "output")
	if code, done := parse("save", fs, args); done {
		return code
	}
	if fs.NArg() != 1 {
		return usageErr("save")
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
	if err := image.Archive(fs.Arg(0), w); err != nil {
		return fail(err)
	}
	return 0
}

func cmdLoad(args []string) int {
	fs := newFlags("load")
	var in string
	strFlag(fs, &in, "read from a file instead of stdin", "i", "input")
	if code, done := parse("load", fs, args); done {
		return code
	}
	var r io.Reader = os.Stdin
	if in != "" {
		f, err := os.Open(in)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		r = f
	}
	m, err := image.Unarchive(r)
	if err != nil {
		return fail(err)
	}
	fmt.Println("Loaded image:", m.Ref())
	return 0
}

func cmdImport(args []string) int {
	if len(args) != 2 {
		return usageErr("import")
	}
	var r io.Reader = os.Stdin
	if args[0] != "-" {
		f, err := os.Open(args[0])
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		r = f
	}
	m, err := image.Import(r, args[1])
	if err != nil {
		return fail(err)
	}
	fmt.Println(m.ID)
	return 0
}

func cmdPull(args []string) int {
	if len(args) != 1 {
		return usageErr("pull")
	}
	if _, err := registry.Pull(args[0], os.Stdout); err != nil {
		return fail(err)
	}
	return 0
}

func cmdImagePrune(args []string) int {
	fs := newFlags("prune")
	var all, force bool
	boolFlag(fs, &all, "remove every image no container uses", "a", "all")
	boolFlag(fs, &force, "do not ask for confirmation", "f", "force")
	if code, done := parse("prune", fs, args); done {
		return code
	}
	if !all {
		fmt.Println("nothing to do: lilDocker has no dangling images, use -a to remove unused ones")
		return 0
	}
	if !confirm("This will remove all images no container uses. Continue?", force) {
		return 0
	}
	list, _ := image.List()
	var freed int64
	for _, m := range list {
		if len(runtime.UsedBy(m.ID)) > 0 {
			continue
		}
		if _, err := image.Remove(m.ID); err == nil {
			freed += m.Size
			fmt.Println("Untagged:", m.Ref())
		}
	}
	fmt.Println("Total reclaimed space:", size(freed))
	return 0
}
