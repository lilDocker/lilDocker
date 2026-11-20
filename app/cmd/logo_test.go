package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogoRenders(t *testing.T) {
	t.Setenv("COLUMNS", "200")
	var plain bytes.Buffer
	if err := showLogo(&plain, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(plain.String(), "\n"), "\n")
	if len(lines) != logoRows {
		t.Fatalf("want %d rows, got %d", logoRows, len(lines))
	}
	for i, l := range lines {
		if len(l) != logoCols {
			t.Fatalf("row %d is %d wide, want %d", i, len(l), logoCols)
		}
	}
	if strings.Count(plain.String(), " ") == len(plain.String())-logoRows {
		t.Fatal("logo is empty")
	}
	if strings.Contains(plain.String(), "\x1b") {
		t.Error("plain output must not contain escape codes")
	}

	var colored bytes.Buffer
	showLogo(&colored, true)
	if !strings.Contains(colored.String(), "\x1b[38;2;") || !strings.HasSuffix(strings.Split(colored.String(), "\n")[0], "\x1b[0m") {
		t.Error("coloured output should use 24-bit colour and reset each line")
	}
}

func TestLogoShrinksForNarrowTerminals(t *testing.T) {
	t.Setenv("COLUMNS", "60")
	var buf bytes.Buffer
	showLogo(&buf, false)
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if len(l) > 60 {
			t.Fatalf("line wider than the terminal: %d", len(l))
		}
	}
	t.Setenv("COLUMNS", "10")
	buf.Reset()
	showLogo(&buf, false)
	if buf.Len() != 0 {
		t.Error("a tiny terminal should skip the logo")
	}
}
