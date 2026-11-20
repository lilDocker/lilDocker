package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/png"
	"io"
	"os"
	"strings"
)

//go:embed logo/lilDocker.png
var logoPNG []byte

const (
	logoCols = 94
	logoRows = 30
	ramp     = " .,:;i1tfLCG08@"
)

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func colorWanted() bool {
	return isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// showLogo draws the embedded logo as ASCII art, shrunk to fit narrow terminals.
func showLogo(w io.Writer, color bool) error {
	cols := logoCols
	if tw := terminalWidth(); tw > 0 && tw < cols {
		cols = tw
	}
	if cols < 24 {
		return nil
	}
	rows := cols * logoRows / logoCols

	img, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return err
	}
	b := img.Bounds()

	var sb strings.Builder
	for cy := 0; cy < rows; cy++ {
		y0 := b.Min.Y + cy*b.Dy()/rows
		y1 := max(b.Min.Y+(cy+1)*b.Dy()/rows, y0+1)
		last := ""
		for cx := 0; cx < cols; cx++ {
			x0 := b.Min.X + cx*b.Dx()/cols
			x1 := max(b.Min.X+(cx+1)*b.Dx()/cols, x0+1)

			var sr, sg, sb2, sa, n float64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, bl, a := img.At(x, y).RGBA()
					sr, sg, sb2, sa, n = sr+float64(r), sg+float64(g), sb2+float64(bl), sa+float64(a), n+1
				}
			}
			cover := sa / (n * 65535)
			if cover < 0.2 {
				sb.WriteByte(' ')
				continue
			}
			r, g, bl := sr/sa*255, sg/sa*255, sb2/sa*255
			idx := int((r+g+bl)/3/255*cover*float64(len(ramp)-1) + 0.5)
			if idx == 0 {
				sb.WriteByte(' ')
				continue
			}
			if color {
				if c := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(r), int(g), int(bl)); c != last {
					sb.WriteString(c)
					last = c
				}
			}
			sb.WriteByte(ramp[idx])
		}
		if color {
			sb.WriteString("\x1b[0m")
		}
		sb.WriteByte('\n')
	}
	_, err = io.WriteString(w, sb.String())
	return err
}

// splash is shown when lilDocker is started with no arguments.
func splash() {
	if os.Getenv("LILDOCKER_NO_LOGO") != "" || !isTerminal(os.Stdout) {
		return
	}
	enableColor()
	showLogo(os.Stdout, colorWanted())
	fmt.Println()
}

func cmdLogo(args []string) int {
	fs := newFlags("logo")
	var force bool
	boolFlag(fs, &force, "colour the logo even when output is not a terminal", "color")
	if code, done := parse("logo", fs, args); done {
		return code
	}
	enableColor()
	if err := showLogo(os.Stdout, force || colorWanted()); err != nil {
		return fail(err)
	}
	return 0
}
