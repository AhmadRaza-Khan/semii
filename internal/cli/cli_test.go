package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, &Env{
		Stdout: &out, Stderr: &errOut, Dir: dir,
		Getenv: func(k string) string { return env[k] },
	})
	return code, out.String(), errOut.String()
}

func TestUsageAndUnknown(t *testing.T) {
	if code, out, _ := run(t, t.TempDir(), nil); code != ExitUser || !strings.Contains(out, "Commands:") {
		t.Errorf("no args: %d %q", code, out)
	}
	if code, _, _ := run(t, t.TempDir(), nil, "help"); code != ExitOK {
		t.Errorf("help: %d", code)
	}
	if code, _, errOut := run(t, t.TempDir(), nil, "frobnicate"); code != ExitUser || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown: %d %q", code, errOut)
	}
	if code, out, _ := run(t, t.TempDir(), nil, "version"); code != ExitOK || !strings.Contains(out, Version) {
		t.Errorf("version: %d %q", code, out)
	}
}

func TestUserErrors(t *testing.T) {
	dir := t.TempDir()
	cases := [][]string{
		{"new"},
		{"new", "a/b"},
		{"new", "shop", "--auth", "ldap"},
		{"new", "shop2", "--auth", "apikey"},
		{"new", "shop", "--db", "oracle", "--auth", "none"},
		{"new", "shop", "--bogus"},
		{"generate"},
		{"g", "wat"},
		{"g", "auth"},
		{"g", "resource"},
		{"g", "resource", "Order"}, // not in a project
		{"routes"},
		{"migrate"},
		{"db", "seed"},
		{"dev"},
	}
	for _, args := range cases {
		if code, _, _ := run(t, dir, nil, args...); code != ExitUser {
			t.Errorf("%v: exit %d, want %d", args, code, ExitUser)
		}
	}
}

func TestNewDryRunAndConflicts(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := run(t, dir, nil, "new", "shop", "--auth", "none", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "internal/app/app.go (dry run)") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "shop")); err == nil {
		t.Error("dry run created the project")
	}
	if err := os.MkdirAll(filepath.Join(dir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "taken", "x"), nil, 0o644)
	if code, _, errOut := run(t, dir, nil, "new", "taken", "--auth", "none"); code != ExitUser || !strings.Contains(errOut, "not empty") {
		t.Errorf("%d %s", code, errOut)
	}
}

func TestParseInterleaved(t *testing.T) {
	e := &Env{}
	rest := e.globalFlags([]string{"g", "--verbose", "resource", "Order", "--dry-run", "a:string", "--no-color", "--force"})
	if !e.Verbose || !e.DryRun || !e.NoColor || !e.Force || strings.Join(rest, " ") != "g resource Order a:string" {
		t.Errorf("%+v %v", e, rest)
	}
}

func TestGoAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"go1.26.3": true, "go1.27": true, "go2.0": true, "go1.25.9": false, "go1.26rc1": true, "devel": false} {
		if goAtLeast(v, 1, 26) != want {
			t.Errorf("%s", v)
		}
	}
}

// TestEndToEnd drives the real workflow for every database: new,
// generate, sqlc, build, vet, staticcheck (when installed), routes and the
// generated tests (against containers testkit starts, so Docker is needed).
// It needs sqlc on PATH and runs only with SEMII_E2E=1; SEMII_E2E_ONLY=x
// runs the cases whose name contains x.
func TestEndToEnd(t *testing.T) {
	if os.Getenv("SEMII_E2E") != "1" {
		t.Skip("set SEMII_E2E=1 (with sqlc on PATH and Docker available) to run")
	}
	if _, err := exec.LookPath("sqlc"); err != nil {
		t.Fatal("sqlc not on PATH")
	}
	_, file, _, _ := runtime.Caller(0)
	framework := filepath.Join(filepath.Dir(file), "..", "..")
	env := map[string]string{EnvLocalSemii: framework}
	dir := t.TempDir()

	must := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		code, out, errOut := run(t, dir, env, args...)
		if code != ExitOK {
			t.Fatalf("semii %s: exit %d\n%s\n%s", strings.Join(args, " "), code, out, errOut)
		}
		return out
	}
	cases := []struct {
		name, db, id, auth, addLater string
	}{
		{"pg-uuid-noauth", "postgres", "uuid", "none", ""},
		{"pg-bigint-allauth", "postgres", "bigint", "jwt,session,apikey", ""},
		{"pg-uuid-jwt-then-more", "postgres", "uuid", "jwt", "session,apikey"},
		{"pg-uuid-session-only", "postgres", "uuid", "session", ""},
		{"mysql-uuid-allauth", "mysql", "uuid", "jwt,session,apikey", ""},
		{"mysql-bigint-noauth", "mysql", "bigint", "none", ""},
		{"sqlite-uuid-allauth", "sqlite", "uuid", "jwt,session,apikey", ""},
		{"sqlite-bigint-session-then-more", "sqlite", "bigint", "session", "jwt,apikey"},
		{"mongo-allauth", "mongo", "objectid", "jwt,session,apikey", ""},
	}
	if only := os.Getenv("SEMII_E2E_ONLY"); only != "" {
		var keep []struct{ name, db, id, auth, addLater string }
		for _, tc := range cases {
			if strings.Contains(tc.name, only) {
				keep = append(keep, tc)
			}
		}
		cases = keep
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := strings.ReplaceAll(tc.name, "-", "")
			must(t, dir, "new", name, "--db", tc.db, "--auth", tc.auth, "--id", tc.id, "--no-git")
			root := filepath.Join(dir, name)
			if tc.addLater != "" {
				must(t, root, "g", "auth", "--strategy", tc.addLater)
			}
			must(t, root, "g", "resource", "Customer", "name:string:min=2", "email:string:unique", "tier:string:default=basic:index")
			must(t, root, "g", "resource", "Order",
				"customer:ref:Customer", "backup:ref:Customer:null", "total:decimal:min=0", "discount:decimal:null",
				"qty:int:min=1:max=100", "big:int64:null:unique", "paid:bool:default=false", "score:float:null:index",
				"due:date", "due_on:date:null:index", "placed_at:time", "at2:time:null", "meta:json", "meta2:json:null",
				"ref:uuid:null:index", "status:string:max=20:default=open", "notes:text:null", "--soft-delete")
			must(t, root, "g", "resource", "Tag", "label:string", "--no-auth")
			must(t, root, "g", "middleware", "audit")
			out := must(t, root, "routes")
			if !strings.Contains(out, "order.(*Handler).List") || !strings.Contains(out, "tag.(*Handler).List") {
				t.Errorf("routes:\n%s", out)
			}
			if tc.auth != "none" && !strings.Contains(out, "/auth/login") {
				t.Errorf("auth routes missing:\n%s", out)
			}
			checks := [][]string{{"go", "vet", "./..."}, {"go", "test", "-race", "-count=1", "./..."}}
			if _, err := exec.LookPath("staticcheck"); err == nil {
				checks = append(checks, []string{"staticcheck", "./..."})
			}
			for _, args := range checks {
				cmd := exec.Command(args[0], args[1:]...)
				cmd.Dir = root
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
				}
			}
		})
	}
}

func TestModuleVersion(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, true }
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	for _, c := range []struct {
		stamped string
		info    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"v1.2.3", info("v9.9.9"), "v1.2.3"},          // ldflags release wins
		{"v1.0.1-dev", info("v1.0.0"), "v1.0.0"},      // go install module@v1.0.0
		{"v1.0.1-dev", info("(devel)"), "v1.0.1-dev"}, // local build
		{"v1.0.1-dev", info("v1.0.0+dirty"), "v1.0.1-dev"},
		{"v1.0.1-dev", info("v1.0.1-0.20261125120000-5a526c62d55f"), "v1.0.1-dev"},
		{"v1.0.1-dev", none, "v1.0.1-dev"},
	} {
		if got := moduleVersion(c.stamped, c.info); got != c.want {
			t.Errorf("moduleVersion(%q) = %q, want %q", c.stamped, got, c.want)
		}
	}
}

func TestBuildRef(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, true }
	}
	for v, want := range map[string]string{
		"v1.0.3-0.20261125134551-3e62e2d3a6fd": "v1.0.3-0.20261125134551-3e62e2d3a6fd", // go install @main
		"v1.0.2":                               "v1.0.2",
		"(devel)":                              "latest",
		"v1.0.2+dirty":                         "latest",
	} {
		if got := buildRef(info(v)); got != want {
			t.Errorf("buildRef(%q) = %q, want %q", v, got, want)
		}
	}
	if got := buildRef(func() (*debug.BuildInfo, bool) { return nil, false }); got != "latest" {
		t.Errorf("no build info: %q", got)
	}
}
