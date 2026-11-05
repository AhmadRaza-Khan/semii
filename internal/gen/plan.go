package gen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Action is what a Plan does to one file.
type Action int

// Plan actions.
const (
	Create Action = iota // new file; conflicts unless --force
	Modify               // existing file edited between markers
)

// Change is one planned file write.
type Change struct {
	Path    string // relative to the plan root
	Action  Action
	Content []byte
	// Note is printed instead of writing when a marker edit could not be
	// placed (G3): the snippet the user must add by hand.
	Note string
}

// Plan collects file changes so they can be checked and applied as a unit:
// nothing is written if any created file already exists (G2).
type Plan struct {
	Root    string
	Changes []Change
}

// Add plans a new file.
func (p *Plan) Add(path string, content []byte) {
	p.Changes = append(p.Changes, Change{Path: path, Action: Create, Content: content})
}

// Edit plans a marker edit of an existing file. When content is nil, note
// explains the manual change instead.
func (p *Plan) Edit(path string, content []byte, note string) {
	p.Changes = append(p.Changes, Change{Path: path, Action: Modify, Content: content, Note: note})
}

// ConflictError lists files that already exist.
type ConflictError struct{ Paths []string }

func (e *ConflictError) Error() string {
	return "refusing to overwrite existing files (use --force):\n  " + strings.Join(e.Paths, "\n  ")
}

// Conflicts returns the planned new files that already exist.
func (p *Plan) Conflicts() []string {
	var out []string
	for _, c := range p.Changes {
		if c.Action != Create {
			continue
		}
		if _, err := os.Stat(filepath.Join(p.Root, c.Path)); err == nil {
			out = append(out, c.Path)
		}
	}
	sort.Strings(out)
	return out
}

// Apply writes the plan. Without force it fails before writing anything if
// a created file exists. With dryRun it only reports. Progress lines are
// written to out.
func (p *Plan) Apply(out io.Writer, force, dryRun bool) error {
	if conflicts := p.Conflicts(); len(conflicts) > 0 && !force {
		return &ConflictError{Paths: conflicts}
	}
	for _, c := range p.Changes {
		full := filepath.Join(p.Root, c.Path)
		verb := "create"
		if c.Action == Modify {
			verb = "update"
		} else if _, err := os.Stat(full); err == nil {
			verb = "overwrite"
		}
		if c.Content == nil {
			fmt.Fprintf(out, "  %-9s %s\n%s\n", "manual", c.Path, indent(c.Note))
			continue
		}
		if dryRun {
			fmt.Fprintf(out, "  %-9s %s (dry run)\n", verb, c.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, c.Content, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "  %-9s %s\n", verb, c.Path)
	}
	return nil
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "      " + l
	}
	return strings.Join(lines, "\n")
}

// ErrMarkersMissing reports that a file lacks the semii markers needed for
// an automatic edit.
var ErrMarkersMissing = errors.New("markers not found")

// InsertBetweenMarkers inserts line before "// semii:<name>:end", keeping
// the end marker's indentation. It is a no-op if an identical line already
// sits between the markers, so generators are re-runnable.
func InsertBetweenMarkers(src []byte, name, line string) ([]byte, error) {
	start := []byte("// semii:" + name + ":start")
	end := []byte("// semii:" + name + ":end")
	si := bytes.Index(src, start)
	ei := bytes.Index(src, end)
	if si < 0 || ei < 0 || ei < si {
		return nil, fmt.Errorf("semii:%s: %w", name, ErrMarkersMissing)
	}
	// gofmt may have re-aligned earlier inserts, so compare with all
	// whitespace runs collapsed.
	want := strings.Join(strings.Fields(line), " ")
	for _, l := range strings.Split(string(src[si:ei]), "\n") {
		if strings.Join(strings.Fields(l), " ") == want {
			return src, nil
		}
	}
	lineStart := bytes.LastIndexByte(src[:ei], '\n') + 1
	ind := src[lineStart:ei]
	var b bytes.Buffer
	b.Write(src[:lineStart])
	b.Write(ind)
	b.WriteString(line)
	b.WriteByte('\n')
	b.Write(src[lineStart:])
	return b.Bytes(), nil
}
