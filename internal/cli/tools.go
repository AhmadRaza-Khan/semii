package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runTool runs an external program in dir, streaming its output in
// verbose mode and including it in the error otherwise. Failures map to
// exit code 2.
func (e *Env) runTool(dir, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return toolErr(fmt.Errorf("%s not found on PATH%s", name, installHint(name)))
	}
	e.debugf("run %s %s (in %s)", name, strings.Join(args, " "), dir)
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if e.Verbose {
		cmd.Stdout, cmd.Stderr = e.Stderr, e.Stderr
	}
	if err := cmd.Run(); err != nil {
		return toolErr(fmt.Errorf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String())))
	}
	return nil
}

func installHint(name string) string {
	switch name {
	case "sqlc":
		return "; install it: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest (or see https://docs.sqlc.dev)"
	case "go":
		return "; install Go 1.24 or newer from https://go.dev/dl"
	case "git":
		return "; install git or pass --no-git"
	}
	return ""
}

// hasQueries reports whether db/queries holds any .sql file; sqlc refuses
// to run without one.
func hasQueries(root string) bool {
	m, _ := filepath.Glob(filepath.Join(root, "db", "queries", "*.sql"))
	return len(m) > 0
}

// sqlcGenerate runs sqlc when there are queries to compile.
func (e *Env) sqlcGenerate(root string) error {
	if !hasQueries(root) {
		e.debugf("no queries yet; skipping sqlc generate")
		return nil
	}
	e.printf("  run       sqlc generate\n")
	return e.runTool(root, "sqlc", "generate")
}

// goRun compiles and runs a throwaway main package inside the project, so
// it can import the project's internal packages. The program's stdout is
// returned; its stderr is passed through.
func (e *Env) goRun(root, name, src string, args ...string) ([]byte, error) {
	dir := filepath.Join(root, ".semii", "tmp", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command("go", append([]string{"run", "./.semii/tmp/" + name}, args...)...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	e.debugf("go run ./.semii/tmp/%s %s", name, strings.Join(args, " "))
	err := cmd.Run()
	if stderr.Len() > 0 && (err != nil || e.Verbose) {
		fmt.Fprint(e.Stderr, stderr.String())
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok && stdout.Len() > 0 {
			return stdout.Bytes(), err // program ran and reported through stdout
		}
		return nil, toolErr(fmt.Errorf("go run failed: %v", err))
	}
	return stdout.Bytes(), nil
}
