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
	DB           string // postgres | mysql | sqlite | mongo
	ID           string
}

// Database predicates for templates.
func (d ProjectData) Postgres() bool { return d.DB == "postgres" }
func (d ProjectData) MySQL() bool    { return d.DB == "mysql" }
func (d ProjectData) SQLite() bool   { return d.DB == "sqlite" }
func (d ProjectData) Mongo() bool    { return d.DB == "mongo" }

// NewOptions are the inputs of PlanNew.
type NewOptions struct {
	Name, Module, DB, ID string
	Docker               bool
	CI                   bool
	Deploy               string // "" or "ssh": add .github/workflows/deploy.yml
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
	"README.md.tmpl":                       "README.md",
	"cmd/api/main.go.tmpl":                 "cmd/api/main.go",
	"internal/app/app.go.tmpl":             "internal/app/app.go",
	"internal/app/routes.go.tmpl":          "internal/app/routes.go",
	"internal/app/apptest/apptest.go.tmpl": "internal/app/apptest/apptest.go",
	"internal/config/config.go.tmpl":       "internal/config/config.go",
	"internal/factory/factory.go.tmpl":     "internal/factory/factory.go",
	"db/seeds/seed.go.tmpl":                "db/seeds/seed.go",
}

// PlanNew builds the plan for a new project rooted at root.
func PlanNew(rd Renderer, root string, o NewOptions) (*Plan, Project, error) {
	if o.DB == "mongo" {
		if o.ID != "" && o.ID != "uuid" && o.ID != "objectid" {
			return nil, Project{}, fmt.Errorf("--id %s: mongo projects use ObjectID ids", o.ID)
		}
		o.ID = "objectid"
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
		DBName: Snake(o.Name), DB: o.DB, ID: o.ID,
	}
	if data.DBName == "" {
		data.DBName = "app"
	}

	plan := &Plan{Root: root}
	plan.Add(ProjectFile, proj.Marshal())
	files := maps.Clone(projectFiles)
	if o.DB == "mongo" {
		files["db/mongo/indexes.go.tmpl"] = "db/mongo/indexes.go"
	} else {
		files["sqlc.yaml.tmpl"] = "sqlc.yaml"
		files["db/embed.go.tmpl"] = "db/embed.go"
		files["db/migrations/init.sql.tmpl"] = "db/migrations/" + o.Now.UTC().Format("20060102150405") + "_init.sql"
	}
	if o.Docker {
		files["docker-compose.yml.tmpl"] = "docker-compose.yml"
		files["Dockerfile.tmpl"] = "Dockerfile"
		files["dockerignore.tmpl"] = ".dockerignore"
	}
	if o.CI {
		files["github/workflows/ci.yml.tmpl"] = ".github/workflows/ci.yml"
	}
	switch o.Deploy {
	case "":
	case "ssh":
		files["github/workflows/deploy.yml.tmpl"] = ".github/workflows/deploy.yml"
	default:
		return nil, Project{}, fmt.Errorf("--deploy %q: only ssh is supported", o.Deploy)
	}
	files["deploy/systemd/service.tmpl"] = "deploy/systemd/" + o.Name + ".service"
	files["deploy/pm2/ecosystem.config.js.tmpl"] = "deploy/pm2/ecosystem.config.js"
	files["deploy/nginx/site.conf.tmpl"] = "deploy/nginx/" + o.Name + ".conf"
	for _, tmpl := range sortedKeys(files) {
		target := files[tmpl]
		out, err := rd.Render(path.Join("common/project", tmpl), path.Join(root, target), data)
		if err != nil {
			return nil, Project{}, err
		}
		plan.Add(target, out)
	}
	if o.DB != "mongo" {
		plan.Add("db/queries/.gitkeep", []byte{})
	}
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
