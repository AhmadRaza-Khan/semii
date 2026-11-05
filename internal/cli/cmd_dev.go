package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	devDebounce    = 200 * time.Millisecond
	devStopTimeout = 5 * time.Second
)

// skipDirs are never watched: generated output, VCS data and build dirs.
var skipDirs = map[string]bool{
	".git": true, ".semii": true, "tmp": true, "bin": true, "node_modules": true, "vendor": true,
}

func runDev(e *Env, args []string) error {
	if len(args) > 0 {
		return userErr("usage: semii dev")
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	if e.DryRun {
		e.printf("dev (dry run: would build ./cmd/api and watch %s)\n", p.Root)
		return nil
	}
	d := &devServer{e: e, root: p.Root, bin: filepath.Join(p.Root, "tmp", "semii-dev", "api")}
	return d.loop()
}

type devServer struct {
	e    *Env
	root string
	bin  string
	proc *exec.Cmd
	done chan struct{} // closed when proc exits
}

func (d *devServer) loop() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("dev: watcher: %w", err)
	}
	defer w.Close()
	if err := d.watchTree(w, d.root); err != nil {
		return err
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	d.rebuild(true)
	var timer <-chan time.Time
	needSqlc := false
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			kind := d.classify(ev)
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = d.watchTree(w, ev.Name)
				}
			}
			if kind == "" {
				continue
			}
			if kind == "sql" {
				needSqlc = true
			}
			d.e.debugf("change %s (%s)", d.rel(ev.Name), kind)
			timer = time.After(devDebounce)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(d.e.Stderr, "dev: watch error: %v\n", err)
		case <-timer:
			timer = nil
			d.rebuild(needSqlc)
			needSqlc = false
		case <-sigs:
			d.e.printf("\nStopping.\n")
			d.stop()
			return nil
		}
	}
}

// classify returns "go", "sql", "env" or "" for changes to ignore.
func (d *devServer) classify(ev fsnotify.Event) string {
	if ev.Op == fsnotify.Chmod {
		return ""
	}
	rel := d.rel(ev.Name)
	base := filepath.Base(rel)
	switch {
	case strings.HasPrefix(rel, filepath.Join("db", "sqlc")+string(filepath.Separator)):
		return "" // our own sqlc output
	case strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go"):
		return "go"
	case strings.HasSuffix(base, ".sql") && (strings.HasPrefix(rel, filepath.Join("db", "queries")) || strings.HasPrefix(rel, filepath.Join("db", "migrations"))):
		return "sql"
	case base == ".env" || strings.HasPrefix(base, ".env.") && base != ".env.example":
		return "env"
	}
	return ""
}

func (d *devServer) rel(path string) string {
	if r, err := filepath.Rel(d.root, path); err == nil {
		return r
	}
	return path
}

func (d *devServer) watchTree(w *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
		if err != nil || !de.IsDir() {
			return nil
		}
		rel := d.rel(path)
		if path != d.root && (skipDirs[de.Name()] || rel == filepath.Join("db", "sqlc")) {
			return filepath.SkipDir
		}
		if err := w.Add(path); err != nil {
			return fmt.Errorf("dev: watch %s: %w", rel, err)
		}
		return nil
	})
}

// rebuild builds the binary and, on success, replaces the running process.
// On failure the previous process keeps running.
func (d *devServer) rebuild(sqlc bool) {
	start := time.Now()
	if sqlc {
		if err := d.e.sqlcGenerate(d.root); err != nil {
			fmt.Fprintf(d.e.Stderr, "%v\n", err)
			return
		}
	}
	cmd := exec.Command("go", "build", "-o", d.bin, "./cmd/api")
	cmd.Dir = d.root
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(d.e.Stderr, "build failed (previous version keeps running):\n%s\n", strings.TrimSpace(string(out)))
		return
	}
	d.e.printf("built in %s; restarting\n", time.Since(start).Round(time.Millisecond))
	d.stop()
	proc := exec.Command(d.bin)
	proc.Dir = d.root
	// No drain wait between reloads: nothing routes traffic away in dev.
	proc.Env = append(os.Environ(), "SEMII_ENV=development", "HTTP_DRAIN_DELAY=-1s")
	proc.SysProcAttr = childAttrs()
	proc.Stdout, proc.Stderr = d.e.Stdout, d.e.Stderr
	if err := proc.Start(); err != nil {
		fmt.Fprintf(d.e.Stderr, "start: %v\n", err)
		return
	}
	done := make(chan struct{})
	go func() {
		err := proc.Wait()
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			fmt.Fprintf(d.e.Stderr, "app: %v\n", err)
		}
		close(done)
	}()
	d.proc, d.done = proc, done
}

// stop sends SIGTERM and escalates to SIGKILL after devStopTimeout.
func (d *devServer) stop() {
	if d.proc == nil {
		return
	}
	select {
	case <-d.done:
	default:
		_ = d.proc.Process.Signal(syscall.SIGTERM)
		select {
		case <-d.done:
		case <-time.After(devStopTimeout):
			_ = d.proc.Process.Kill()
			<-d.done
		}
	}
	d.proc, d.done = nil, nil
}
