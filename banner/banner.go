// Package banner prints the semii leaf in a terminal.
//
// The logo is drawn with half-block characters, two pixels per cell, in
// true colour or the 256-colour palette depending on the terminal. It is
// only printed to an interactive terminal with colour enabled, so pipes,
// log files and CI output never contain it.
//
//	banner.Print(os.Stderr, "shop · development", "built with semii")
package banner

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
)

//go:embed leaf.png
var leafPNG []byte

// Mode is a terminal colour depth.
type Mode int

const (
	// None disables the banner.
	None Mode = iota
	// Color256 uses the xterm 256-colour palette.
	Color256
	// TrueColor uses 24-bit colour.
	TrueColor
)

// Print writes the leaf followed by lines to w and reports whether it did.
// It does nothing unless w is a terminal and colour is enabled (see
// DetectMode).
func Print(w io.Writer, lines ...string) bool {
	if !IsTerminal(w) {
		return false
	}
	mode := DetectMode(os.Getenv)
	if mode == None {
		return false
	}
	_, err := io.WriteString(w, Render(mode, lines...))
	return err == nil
}

// DetectMode picks the colour depth from the environment: None when
// NO_COLOR is set or TERM is "dumb", TrueColor when COLORTERM says so or
// in Windows Terminal, otherwise Color256.
func DetectMode(getenv func(string) string) Mode {
	switch {
	case getenv("NO_COLOR") != "", getenv("TERM") == "dumb":
		return None
	case getenv("COLORTERM") == "truecolor", getenv("COLORTERM") == "24bit", getenv("WT_SESSION") != "":
		return TrueColor
	}
	return Color256
}

// IsTerminal reports whether w is an interactive terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Render returns the leaf drawn for mode, followed by lines (the first in
// bold). It returns "" for None.
func Render(mode Mode, lines ...string) string {
	if mode == None {
		return ""
	}
	img, err := png.Decode(bytes.NewReader(leafPNG))
	if err != nil {
		return "" // the embedded image is checked by tests
	}
	var b strings.Builder
	b.WriteByte('\n')
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		row := cells(img, y, mode)
		if row == "" {
			continue
		}
		b.WriteString("  ")
		b.WriteString(row)
		b.WriteString("\x1b[0m\n")
	}
	for i, l := range lines {
		if i == 0 {
			fmt.Fprintf(&b, "  \x1b[1m%s\x1b[0m\n", l)
		} else {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	b.WriteByte('\n')
	return b.String()
}

// cells renders pixel rows y and y+1 as one line of half blocks, without
// trailing blank cells.
func cells(img image.Image, y int, mode Mode) string {
	bounds := img.Bounds()
	var b strings.Builder
	blanks := 0
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		top, topOK := pixel(img, x, y)
		bot, botOK := pixel(img, x, y+1)
		if !topOK && !botOK {
			blanks++
			continue
		}
		if blanks > 0 {
			b.WriteString("\x1b[0m")
			b.WriteString(strings.Repeat(" ", blanks))
			blanks = 0
		}
		switch {
		case topOK && botOK:
			b.WriteString("\x1b[0m" + fg(top, mode) + bg(bot, mode) + "▀")
		case topOK:
			b.WriteString("\x1b[0m" + fg(top, mode) + "▀")
		default:
			b.WriteString("\x1b[0m" + fg(bot, mode) + "▄")
		}
	}
	return b.String()
}

// pixel returns the colour at x, y and whether it is opaque enough to draw.
func pixel(img image.Image, x, y int) (color.NRGBA, bool) {
	if !(image.Point{x, y}.In(img.Bounds())) {
		return color.NRGBA{}, false
	}
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	return c, c.A >= 128
}

func fg(c color.NRGBA, mode Mode) string { return sgr(38, c, mode) }
func bg(c color.NRGBA, mode Mode) string { return sgr(48, c, mode) }

func sgr(base int, c color.NRGBA, mode Mode) string {
	if mode == TrueColor {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", base, c.R, c.G, c.B)
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", base, xterm256(c))
}

// xterm256 maps a colour to the nearest entry of the 6x6x6 cube or the
// grey ramp of the xterm palette.
func xterm256(c color.NRGBA) int {
	level := func(v uint8) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return int(v-35) / 40
	}
	r, g, b := level(c.R), level(c.G), level(c.B)
	cube := 16 + 36*r + 6*g + b
	if r == g && g == b {
		avg := (int(c.R) + int(c.G) + int(c.B)) / 3
		if avg > 8 && avg < 238 {
			return 232 + (avg-8)/10
		}
	}
	return cube
}
