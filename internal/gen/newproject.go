package gen

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"
)

// ProjectData is the template data for `semii new`.
type ProjectData struct {
	Name         string // directory and binary name
	Module       string
	Version      string // semii CLI version
	Semii        string // framework module path
	SemiiVersion string // version required in go.mod
	LocalSemii   string // replace target for a local framework checkout
	DBName       string
	ID           string
}

// NewOptions are the inputs of PlanNew.
type NewOptions struct {
	Name, Module, DB, ID string
	Docker               bool
	Version              string
	LocalSemii           string
	Now                  time.Time
}

// projectFiles maps templates under postgres/project to output paths.
var projectFiles = map[string]string{
	"go.mod.tmpl":                          "go.mod",
	"gitignore.tmpl":                       ".gitignore",
	"env.example.tmpl":                     ".env.example",
	"Makefile.tmpl":                        "Makefile",
	"sqlc.yaml.tmpl":                       "sqlc.yaml",
	"README.md.tmpl":                       "README.md",
	"cmd/api/main.go.tmpl":                 "cmd/api/main.go",
	"internal/app/app.go.tmpl":             "internal/app/app.go",
	"internal/app/routes.go.tmpl":          "internal/app/routes.go",
	"internal/app/apptest/apptest.go.tmpl": "internal/app/apptest/apptest.go",
	"internal/config/config.go.tmpl":       "internal/config/config.go",
	"internal/factory/factory.go.tmpl":     "internal/factory/factory.go",
	"db/embed.go.tmpl":                     "db/embed.go",
	"db/seeds/seed.go.tmpl":                "db/seeds/seed.go",
}

// PlanNew builds the plan for a new project rooted at root.
func PlanNew(rd Renderer, root string, o NewOptions) (*Plan, Project, error) {
	if o.DB != "postgres" {
		return nil, Project{}, fmt.Errorf("--db %s: templates for %s arrive in milestone M5; use --db postgres", o.DB, o.DB)
	}
	// Auth strategies are added afterwards by PlanAuth, which records them.
	proj := Project{
		Root: root, Semii: o.Version, Template: TemplateVersion,
		Module: o.Module, DB: o.DB, ID: o.ID,
	}
	if err := proj.Validate(); err != nil {
		return nil, Project{}, err
	}
	data := ProjectData{
		Name: o.Name, Module: o.Module, Version: o.Version, Semii: SemiiModule,
		SemiiVersion: requireVersion(o.Version), LocalSemii: o.LocalSemii,
		DBName: Snake(o.Name), ID: o.ID,
	}
	if data.DBName == "" {
		data.DBName = "app"
	}

	plan := &Plan{Root: root}
	plan.Add(ProjectFile, proj.Marshal())
	for _, tmpl := range sortedKeys(projectFiles) {
		target := projectFiles[tmpl]
		out, err := rd.Render(path.Join("postgres/project", tmpl), path.Join(root, target), data)
		if err != nil {
			return nil, Project{}, err
		}
		plan.Add(target, out)
	}
	if o.Docker {
		out, err := rd.Render("postgres/project/docker-compose.yml.tmpl", "docker-compose.yml", data)
		if err != nil {
			return nil, Project{}, err
		}
		plan.Add("docker-compose.yml", out)
	}
	initSQL, err := rd.Render("postgres/project/db/migrations/init.sql.tmpl", "init.sql", data)
	if err != nil {
		return nil, Project{}, err
	}
	plan.Add("db/migrations/"+o.Now.UTC().Format("20060102150405")+"_init.sql", initSQL)
	plan.Add("db/queries/.gitkeep", []byte{})
	return plan, proj, nil
}

// requireVersion is the framework version written to go.mod: the CLI's own
// version when it is a release, otherwise a placeholder that a replace
// directive (or the first release) resolves.
func requireVersion(v string) string {
	if strings.HasPrefix(v, "v") && !strings.Contains(v, "-dev") {
		return v
	}
	return "v0.0.0-00010101000000-000000000000"
}

func sortedKeys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }
