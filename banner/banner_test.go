package banner

import (
	"bytes"
	"image/color"
	"os"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	for _, mode := range []Mode{TrueColor, Color256} {
		out := Render(mode, "semii v1.2.3", "built with semii")
		rows := strings.Count(out, "▀") + strings.Count(out, "▄")
		if rows < 100 {
			t.Errorf("mode %d: only %d half blocks drawn", mode, rows)
		}
		if !strings.Contains(out, "\x1b[1msemii v1.2.3\x1b[0m") || !strings.Contains(out, "  built with semii\n") {
			t.Errorf("mode %d: lines missing:\n%s", mode, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasSuffix(l, " ") {
				t.Errorf("mode %d: trailing blank cells in %q", mode, l)
			}
		}
	}
	if !strings.Contains(Render(TrueColor), "\x1b[38;2;") || strings.Contains(Render(Color256), ";2;") {
		t.Error("colour sequences do not match the mode")
	}
	if Render(None, "x") != "" {
		t.Error("None should render nothing")
	}
}

func TestDetectMode(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want Mode
	}{
		{map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, None},
		{map[string]string{"TERM": "dumb"}, None},
		{map[string]string{"COLORTERM": "truecolor"}, TrueColor},
		{map[string]string{"COLORTERM": "24bit"}, TrueColor},
		{map[string]string{"WT_SESSION": "x"}, TrueColor},
		{map[string]string{"TERM": "xterm-256color"}, Color256},
	} {
		if got := DetectMode(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v: got %d, want %d", c.env, got, c.want)
		}
	}
}

func TestPrintSkipsNonTerminals(t *testing.T) {
	var buf bytes.Buffer
	if Print(&buf, "x") || buf.Len() != 0 {
		t.Error("printed to a buffer")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) || Print(f, "x") {
		t.Error("a regular file is not a terminal")
	}
}

func TestXterm256(t *testing.T) {
	for c, want := range map[color.NRGBA]int{
		{0, 0, 0, 255}:       16,
		{255, 255, 255, 255}: 231,
		{255, 0, 0, 255}:     196,
		{128, 128, 128, 255}: 244,
		{255, 135, 0, 255}:   208,
	} {
		if got := xterm256(c); got != want {
			t.Errorf("xterm256(%v) = %d, want %d", c, got, want)
		}
	}
}
