// Package cli implements the semii command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/AhmadRaza-Khan/semii/banner"
	"github.com/AhmadRaza-Khan/semii/internal/gen"
	"github.com/AhmadRaza-Khan/semii/templates"
)

// Version is the CLI version, stamped into generated files and .semii.yaml.
// Release builds set it with -ldflags "-X …/internal/cli.Version=v1.2.3";
// `go install …@v1.2.3` builds take it from the module version instead. A
// release version is also what generated go.mod files require.
var Version = moduleVersion("v1.0.1-dev", debug.ReadBuildInfo)

// moduleVersion returns v unless it is a development version and the
// binary was built by `go install module@vX.Y.Z`, whose build info then
// carries that release. Local builds report "(devel)", a VCS version such
// as "v1.0.0+dirty", or a pseudo-version; none of them is a published
// release, so they keep v.
func moduleVersion(v string, info func() (*debug.BuildInfo, bool)) string {
	if gen.IsRelease(v) {
		return v
	}
	if bi, ok := info(); ok && releaseRE.MatchString(bi.Main.Version) {
		return bi.Main.Version
	}
	return v
}

var releaseRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// Exit codes.
const (
	ExitOK       = 0
	ExitUser     = 1 // bad arguments, existing files, not a project
	ExitTool     = 2 // sqlc, go or goose failed
	ExitInternal = 3
)

// exitError carries an exit code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func userErr(format string, args ...any) error {
	return &exitError{ExitUser, fmt.Errorf(format, args...)}
}

func toolErr(err error) error { return &exitError{ExitTool, err} }

// Env is everything a command needs from the outside world.
type Env struct {
	Stdout, Stderr io.Writer
	Dir            string // working directory
	Getenv         func(string) string

	DryRun, Force, Verbose, NoColor bool
}

func (e *Env) printf(format string, args ...any) { fmt.Fprintf(e.Stdout, format, args...) }

func (e *Env) debugf(format string, args ...any) {
	if e.Verbose {
		fmt.Fprintf(e.Stderr, "debug: "+format+"\n", args...)
	}
}

// showBanner prints the semii leaf above line when stdout is a terminal
// and colour is enabled, and reports whether it did.
func (e *Env) showBanner(line string) bool {
	if e.NoColor {
		return false
	}
	return banner.Print(e.Stdout, line, "Rails-speed scaffolding for Go APIs")
}

func (e *Env) renderer() gen.Renderer {
	return gen.Renderer{FS: templates.FS, Version: Version}
}

func (e *Env) project() (gen.Project, error) {
	p, err := gen.FindProject(e.Dir)
	if err != nil {
		return gen.Project{}, userErr("%v", err)
	}
	return p, nil
}

type command struct {
	name    string
	aliases []string
	usage   string
	run     func(e *Env, args []string) error
}

func commands() []command {
	return []command{
		{"new", nil, "new <name> [--module path] [--db postgres|mysql|sqlite|mongo] [--auth jwt|session|apikey|none] [--id uuid|bigint] [--deploy ssh] [--no-docker] [--no-ci] [--no-git]", runNew},
		{"generate", []string{"g"}, "generate resource|migration|middleware|auth ...", runGenerate},
		{"dev", nil, "dev                 build, run and reload on changes", runDev},
		{"routes", nil, "routes [--json]     list routes and their policies", runRoutes},
		{"migrate", nil, "migrate up|down|status|redo|create <name> [--to=version]", runMigrate},
		{"db", nil, "db seed [--allow-production]", runDB},
		{"doctor", nil, "doctor              check the toolchain, config and database", runDoctor},
		{"version", nil, "version", runVersion},
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "semii %s: scaffolding for Go APIs\n\nUsage: semii <command> [flags]\n\nCommands:\n", Version)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %s\n", c.usage)
	}
	fmt.Fprint(w, "\nGlobal flags: --dry-run --force --verbose --no-color\n")
}

// Run executes the command line and returns the process exit code.
func Run(args []string, e *Env) int {
	rest := e.globalFlags(args)
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		e.showBanner("semii " + Version)
		usage(e.Stdout)
		if len(rest) == 0 {
			return ExitUser
		}
		return ExitOK
	}
	for _, c := range commands() {
		if c.name == rest[0] || contains(c.aliases, rest[0]) {
			err := c.run(e, rest[1:])
			return e.report(err)
		}
	}
	fmt.Fprintf(e.Stderr, "semii: unknown command %q\n\n", rest[0])
	usage(e.Stderr)
	return ExitUser
}

func (e *Env) report(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		fmt.Fprintf(e.Stderr, "semii: %v\n", ee.err)
		return ee.code
	}
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	var ce *gen.ConflictError
	if errors.As(err, &ce) {
		fmt.Fprintf(e.Stderr, "semii: %v\n", err)
		return ExitUser
	}
	fmt.Fprintf(e.Stderr, "semii: internal error: %v\n", err)
	return ExitInternal
}

// globalFlags strips the global flags from anywhere in args.
func (e *Env) globalFlags(args []string) []string {
	var rest []string
	for _, a := range args {
		switch a {
		case "--dry-run":
			e.DryRun = true
		case "--force":
			e.Force = true
		case "--verbose", "-v":
			e.Verbose = true
		case "--no-color":
			e.NoColor = true
		default:
			rest = append(rest, a)
		}
	}
	if os.Getenv("NO_COLOR") != "" {
		e.NoColor = true
	}
	return rest
}

// parse parses fs flags interleaved with positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, userErr("%s: %v", fs.Name(), err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
