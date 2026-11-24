package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/AhmadRaza-Khan/semii/internal/gen"
)

// EnvLocalSemii points generated projects at a local framework checkout
// via a replace directive, until semii is published.
const EnvLocalSemii = "SEMII_LOCAL_PATH"

func runNew(e *Env, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	module := fs.String("module", "", "Go module path (default: <name>)")
	db := fs.String("db", "postgres", "postgres | mysql | sqlite | mongo")
	authList := fs.String("auth", "jwt", "comma list of session,jwt,apikey, or none")
	id := fs.String("id", "uuid", "uuid | bigint")
	noDocker := fs.Bool("no-docker", false, "skip docker-compose.yml")
	noCI := fs.Bool("no-ci", false, "skip .github/workflows/ci.yml")
	deploy := fs.String("deploy", "", "ssh: add a GitHub Actions deploy workflow (build, upload, migrate, restart)")
	noGit := fs.Bool("no-git", false, "skip git init")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return userErr("usage: semii new <name> [flags]")
	}
	name := pos[0]
	if gen.Snake(name) == "" || filepath.Base(name) != name {
		return userErr("invalid project name %q: use a plain directory name", name)
	}
	if *module == "" {
		*module = name
	}
	var auth []string
	if *authList != "none" {
		auth = splitList(*authList)
	}

	root := filepath.Join(e.Dir, name)
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 && !e.Force {
		return userErr("directory %s already exists and is not empty (use --force to write into it)", name)
	}
	local := e.Getenv(EnvLocalSemii)
	if local != "" {
		if local, err = filepath.Abs(local); err != nil {
			return userErr("%s: %v", EnvLocalSemii, err)
		}
	}

	for _, s := range auth {
		if s != "session" && s != "jwt" && s != "apikey" {
			return userErr("--auth: unknown strategy %q (session, jwt, apikey or none)", s)
		}
	}
	if slices.Contains(auth, "apikey") && !slices.Contains(auth, "session") && !slices.Contains(auth, "jwt") {
		return userErr("--auth: apikey needs session or jwt so users can sign in to manage keys")
	}
	plan, proj, err := gen.PlanNew(e.renderer(), root, gen.NewOptions{
		Name: name, Module: *module, DB: *db, ID: *id,
		Docker: !*noDocker, CI: !*noCI, Deploy: *deploy, Version: Version, LocalSemii: local, Now: time.Now(),
	})
	if err != nil {
		return userErr("%v", err)
	}
	e.printf("Creating %s\n", name)
	if err := plan.Apply(e.Stdout, e.Force, e.DryRun); err != nil {
		return err
	}
	if e.DryRun {
		if len(auth) > 0 {
			e.printf("  then      semii generate auth --strategy %s (dry run)\n", strings.Join(auth, ","))
		}
		return nil
	}
	if len(auth) > 0 {
		if err := e.applyAuth(proj, auth); err != nil {
			return err
		}
	}

	// sqlc first: generated code (auth) imports db/sqlc, which tidy must see.
	if err := e.sqlcGenerate(root); err != nil {
		return err
	}
	e.printf("  run       go mod tidy\n")
	if err := e.runTool(root, "go", "mod", "tidy"); err != nil {
		if local == "" {
			return toolErr(fmt.Errorf("%w\nhint: semii is not published yet; set %s to a local checkout of the framework", err, EnvLocalSemii))
		}
		return err
	}
	if !*noGit {
		if _, err := exec.LookPath("git"); err == nil {
			e.printf("  run       git init\n")
			if err := e.runTool(root, "git", "init", "-q"); err != nil {
				return err
			}
		} else {
			e.printf("  skip      git init (git not found)\n")
		}
	}

	e.printf("\nDone. Next steps:\n  cd %s\n  cp .env.example .env\n", name)
	if !*noDocker {
		e.printf("  docker compose up -d db\n")
	}
	e.printf("  semii migrate up\n  semii g resource Note title:string body:text:null\n  semii dev\n")
	return nil
}
