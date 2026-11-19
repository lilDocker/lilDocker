package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

var known = map[string]bool{
	"FROM": true, "WORKDIR": true, "COPY": true, "ADD": true, "ENV": true, "RUN": true,
	"EXPOSE": true, "CMD": true, "ENTRYPOINT": true, "ARG": true, "LABEL": true,
	"USER": true, "VOLUME": true, "STOPSIGNAL": true,
}

type Instruction struct {
	Command string
	Value   string
	Line    int
}

func ParseLilDockfile(path string) ([]Instruction, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Parse(file)
}

func Parse(r io.Reader) ([]Instruction, error) {
	var out []Instruction
	var pending strings.Builder
	start, n := 0, 0

	flush := func() error {
		ins, err := parseLine(pending.String(), start)
		pending.Reset()
		if err != nil {
			return err
		}
		out = append(out, ins)
		return nil
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if pending.Len() == 0 {
			start = n
		}
		if strings.HasSuffix(line, "\\") {
			pending.WriteString(strings.TrimSpace(strings.TrimSuffix(line, "\\")) + " ")
			continue
		}
		pending.WriteString(line)
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if pending.Len() > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseLine(text string, line int) (Instruction, error) {
	text = strings.TrimSpace(text)
	cmd, val := text, ""
	if i := strings.IndexAny(text, " \t"); i >= 0 {
		cmd, val = text[:i], strings.TrimSpace(text[i+1:])
	}
	cmd = strings.ToUpper(cmd)
	if !known[cmd] {
		return Instruction{}, fmt.Errorf("line %d: unknown instruction %q", line, cmd)
	}
	if val == "" {
		return Instruction{}, fmt.Errorf("line %d: %s needs a value", line, cmd)
	}
	return Instruction{Command: cmd, Value: val, Line: line}, nil
}

// ParseKeyValue reads "KEY=value" or the older "KEY value" form.
func ParseKeyValue(v string) (string, string, error) {
	eq := strings.Index(v, "=")
	sp := strings.IndexAny(v, " \t")
	var k, val string
	if eq > 0 && (sp < 0 || eq < sp) {
		k, val = v[:eq], v[eq+1:]
	} else if sp > 0 {
		k, val = v[:sp], strings.TrimSpace(v[sp+1:])
	} else {
		return "", "", fmt.Errorf("expected KEY=value, got %q", v)
	}
	if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
		val = val[1 : len(val)-1]
	}
	return k, val, nil
}

// SplitArgs splits on whitespace, honouring quotes, or reads a JSON array.
func SplitArgs(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		var a []string
		if json.Unmarshal([]byte(v), &a) == nil {
			return a
		}
	}
	var args []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range v {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case unicode.IsSpace(r):
			if has || cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

// ParseCommand returns the argv for a JSON array, or the raw string and
// shell=true for the plain form.
func ParseCommand(v string) (argv []string, shell bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		var a []string
		if json.Unmarshal([]byte(v), &a) == nil && len(a) > 0 {
			return a, false
		}
	}
	return []string{v}, true
}
