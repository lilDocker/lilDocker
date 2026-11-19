package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func boolFlag(fs *flag.FlagSet, p *bool, usage string, names ...string) {
	for i, n := range names {
		u := usage
		if i > 0 {
			u = "same as -" + names[0]
		}
		fs.BoolVar(p, n, *p, u)
	}
}

func strFlag(fs *flag.FlagSet, p *string, usage string, names ...string) {
	for i, n := range names {
		u := usage
		if i > 0 {
			u = "same as -" + names[0]
		}
		fs.StringVar(p, n, *p, u)
	}
}

func intFlag(fs *flag.FlagSet, p *int, usage string, names ...string) {
	for i, n := range names {
		u := usage
		if i > 0 {
			u = "same as -" + names[0]
		}
		fs.IntVar(p, n, *p, u)
	}
}

func listVar(fs *flag.FlagSet, p *listFlag, usage string, names ...string) {
	for i, n := range names {
		u := usage
		if i > 0 {
			u = "same as -" + names[0]
		}
		fs.Var(p, n, u)
	}
}

// expandShort turns "-it" into "-i -t" while the letters are all boolean flags.
func expandShort(fs *flag.FlagSet, args []string) []string {
	isBool := func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		return ok && b.IsBoolFlag()
	}
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			return append(out, args[i:]...)
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			out = append(out, a)
			continue
		}
		f := fs.Lookup(name)
		switch {
		case f != nil && isBool(name):
			out = append(out, a)
		case f != nil:
			out = append(out, a)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case len(name) > 1 && !strings.HasPrefix(a, "--") && allBool(name, isBool):
			for _, r := range name {
				out = append(out, "-"+string(r))
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

func allBool(s string, isBool func(string) bool) bool {
	for _, r := range s {
		if !isBool(string(r)) {
			return false
		}
	}
	return true
}

// parse reads the flags. done is true when the command should stop with code.
func parse(cmd string, fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(expandShort(fs, args))
	if err == nil {
		return 0, false
	}
	if errors.Is(err, flag.ErrHelp) {
		commandHelp(cmd, fs)
		return 0, true
	}
	fmt.Fprintf(os.Stderr, "lildocker: %v\nSee 'lildocker %s --help'.\n", err, cmd)
	return 1, true
}

func usageErr(cmd string) int {
	if c, ok := commands[cmd]; ok {
		fmt.Fprintf(os.Stderr, "Usage:  lildocker %s\n\nSee 'lildocker %s --help'.\n", c.usage, cmd)
	}
	return 1
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "lildocker:", err)
	return 1
}

func confirm(msg string, force bool) bool {
	if force {
		return true
	}
	fmt.Print(msg + " [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
}

func render(format string, v any) (string, error) {
	format = strings.NewReplacer(`\t`, "\t", `\n`, "\n").Replace(format)
	t, err := template.New("format").Parse(format)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, v); err != nil {
		return "", err
	}
	return sb.String(), nil
}
