package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/semii/semii/internal/gen"
)

func runGenerate(e *Env, args []string) error {
	if len(args) == 0 {
		return userErr("usage: semii generate resource|migration|middleware|auth ...")
	}
	switch args[0] {
	case "resource":
		return generateResource(e, args[1:])
	case "migration":
		return generateMigration(e, args[1:])
	case "middleware":
		return generateMiddleware(e, args[1:])
	case "auth":
		return userErr("generate auth arrives in milestone M4")
	}
	return userErr("unknown generator %q (resource, migration, middleware, auth)", args[0])
}

func generateResource(e *Env, args []string) error {
	fs := flag.NewFlagSet("generate resource", flag.ContinueOnError)
	soft := fs.Bool("soft-delete", false, "add deleted_at and filter it")
	noAuth := fs.Bool("no-auth", false, "make the routes Public()")
	only := fs.String("only", "", "limit output: model,repo,service,handler,migration,tests")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return userErr("usage: semii generate resource <Name> [field:type[:modifier...]...]")
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	plan, res, err := gen.PlanResource(e.renderer(), gen.ResourceOptions{
		Project: p, Name: pos[0], Fields: pos[1:], SoftDelete: *soft, NoAuth: *noAuth,
		Only: splitList(*only), Version: Version, Now: time.Now(),
	})
	if err != nil {
		return userErr("%v", err)
	}
	e.printf("Generating resource %s (%s)\n", res.Name, res.Route)
	if err := plan.Apply(e.Stdout, e.Force, e.DryRun); err != nil {
		return err
	}
	if e.DryRun {
		return nil
	}
	return e.refresh(p.Root)
}

// refresh regenerates sqlc code, tidies modules and builds everything as a
// sanity check after a generator ran.
func (e *Env) refresh(root string) error {
	if err := e.sqlcGenerate(root); err != nil {
		return err
	}
	e.printf("  run       go mod tidy\n")
	if err := e.runTool(root, "go", "mod", "tidy"); err != nil {
		return err
	}
	e.printf("  run       go build ./...\n")
	return e.runTool(root, "go", "build", "./...")
}

func generateMigration(e *Env, args []string) error {
	if len(args) != 1 {
		return userErr("usage: semii generate migration <name>")
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	if p.DB == "mongo" {
		return userErr("mongo projects have no SQL migrations; declare indexes in db/mongo/indexes.go")
	}
	path, err := newMigration(e, p.Root, args[0])
	if err != nil {
		return err
	}
	e.printf("  create    %s\n", path)
	return nil
}

// newMigration writes an empty goose migration and returns its path
// relative to root.
func newMigration(e *Env, root, name string) (string, error) {
	slug := gen.Snake(name)
	if slug == "" {
		return "", userErr("invalid migration name %q", name)
	}
	version := gen.MigrationVersion(root, time.Now())
	rel := filepath.Join("db", "migrations", version+"_"+slug+".sql")
	body := fmt.Sprintf("-- %s\n-- +goose Up\n\n\n-- +goose Down\n\n", slug)
	plan := &gen.Plan{Root: root}
	plan.Add(rel, []byte(body))
	if err := plan.Apply(discard{}, e.Force, e.DryRun); err != nil {
		return "", err
	}
	return rel, nil
}

type discard struct{}

func (discard) Write(b []byte) (int, error) { return len(b), nil }

func generateMiddleware(e *Env, args []string) error {
	if len(args) != 1 {
		return userErr("usage: semii generate middleware <name>")
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	name := gen.Pascal(args[0])
	if name == "" {
		return userErr("invalid middleware name %q", args[0])
	}
	file := gen.Snake(args[0])
	data := struct{ Name, Version string }{name, Version}
	plan := &gen.Plan{Root: p.Root}
	for _, f := range []struct{ tmpl, target string }{
		{"middleware.go.tmpl", "internal/middleware/" + file + ".go"},
		{"middleware_test.go.tmpl", "internal/middleware/" + file + "_test.go"},
	} {
		tmpl, target := f.tmpl, f.target
		out, err := e.renderer().Render("postgres/middleware/"+tmpl, filepath.Join(p.Root, target), data)
		if err != nil {
			return err
		}
		plan.Add(target, out)
	}
	if err := plan.Apply(e.Stdout, e.Force, e.DryRun); err != nil {
		return err
	}
	if !e.DryRun {
		e.printf("Add it to a group or the chain in internal/app/app.go, e.g. r.Group(\"/admin\", middleware.%s)\n", name)
	}
	return nil
}
