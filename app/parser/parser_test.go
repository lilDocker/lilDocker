package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	src := `
# comment
from scratch
ENV A=1
RUN echo one && \
    echo two
CMD ["node", "app.js"]
`
	got, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 instructions, got %d", len(got))
	}
	if got[0].Command != "FROM" || got[0].Line != 3 {
		t.Errorf("bad first instruction: %+v", got[0])
	}
	if got[2].Value != "echo one && echo two" {
		t.Errorf("continuation not joined: %q", got[2].Value)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"NOPE x", "CMD", "RUN   "} {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}

func TestKeyValue(t *testing.T) {
	cases := map[string][2]string{
		"A=1":         {"A", "1"},
		`A="x y"`:     {"A", "x y"},
		"A some text": {"A", "some text"},
	}
	for in, want := range cases {
		k, v, err := ParseKeyValue(in)
		if err != nil || k != want[0] || v != want[1] {
			t.Errorf("%q -> %q %q %v", in, k, v, err)
		}
	}
	if _, _, err := ParseKeyValue("lonely"); err == nil {
		t.Error("expected error")
	}
}

func TestSplitArgs(t *testing.T) {
	got := SplitArgs(`a "b c" 'd' e`)
	if want := []string{"a", "b c", "d", "e"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	got = SplitArgs(`["x", "y z"]`)
	if want := []string{"x", "y z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestParseCommand(t *testing.T) {
	argv, shell := ParseCommand(`["a","b"]`)
	if shell || len(argv) != 2 {
		t.Errorf("exec form: %v %v", argv, shell)
	}
	argv, shell = ParseCommand("echo hi")
	if !shell || argv[0] != "echo hi" {
		t.Errorf("shell form: %v %v", argv, shell)
	}
}

func TestNewInstructionsParse(t *testing.T) {
	src := "ARG X=1\nLABEL a=b\nADD f.tar /\nUSER bob\nVOLUME /data\nSTOPSIGNAL SIGINT\n"
	got, err := Parse(strings.NewReader(src))
	if err != nil || len(got) != 6 {
		t.Fatalf("%v %v", got, err)
	}
}
