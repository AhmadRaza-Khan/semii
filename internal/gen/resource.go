package gen

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SemiiModule is the framework's module path, imported by generated code.
const SemiiModule = "github.com/semii/semii"

// Resource is the template data for `semii generate resource`.
type Resource struct {
	Names
	Version    string
	Module     string
	Semii      string
	Fields     []Field
	ID         string // uuid | bigint
	SoftDelete bool
	Public     bool
	Timestamp  string // migration version
}

// IDGo is the Go type of the primary key.
func (r Resource) IDGo() string {
	if r.UUID() {
		return "uuid.UUID"
	}
	return "int64"
}

// IDCast is the Postgres type of the primary key.
func (r Resource) IDCast() string {
	if r.UUID() {
		return "uuid"
	}
	return "bigint"
}

// UUID reports UUID primary keys.
func (r Resource) UUID() bool { return r.ID != "bigint" }

// PathID is the web helper that parses the {id} path value.
func (r Resource) PathID() string {
	if r.UUID() {
		return "PathUUID"
	}
	return "PathInt64"
}

// MissingID is a Go expression for an id that does not exist.
func (r Resource) MissingID() string {
	if r.UUID() {
		return "uuid.NewString()"
	}
	return `"999999999"`
}

func (r Resource) filter(keep func(Field) bool) []Field {
	var out []Field
	for _, f := range r.Fields {
		if keep(f) {
			out = append(out, f)
		}
	}
	return out
}

// Filters are the fields ListFilter exposes (indexed, unique and refs).
func (r Resource) Filters() []Field { return r.filter(Field.Filterable) }

// Uniques are the unique fields.
func (r Resource) Uniques() []Field { return r.filter(func(f Field) bool { return f.Unique }) }

// Refs are the ref fields.
func (r Resource) Refs() []Field { return r.filter(Field.IsRef) }

// HasNull reports whether any field is nullable.
func (r Resource) HasNull() bool { return len(r.filter(func(f Field) bool { return f.Null })) > 0 }

// UsesSeq reports whether factories need a sequence number.
func (r Resource) UsesSeq() bool {
	return len(r.filter(func(f Field) bool { return f.Unique && !f.IsRef() })) > 0
}

// RequiredField is the first field CreateInput requires, or nil.
func (r Resource) RequiredField() *Field {
	for _, f := range r.Fields {
		if f.Required() {
			return &f
		}
	}
	return nil
}

// PatchField is a field update tests can change and observe, or nil.
func (r Resource) PatchField() *Field {
	for _, f := range r.Fields {
		if f.Patchable() && !f.IsRef() {
			return &f
		}
	}
	return nil
}

// InsertColumns lists the columns of the insert query.
func (r Resource) InsertColumns() string {
	var cols []string
	if r.UUID() {
		cols = append(cols, "id")
	}
	for _, f := range r.Fields {
		cols = append(cols, f.Col)
	}
	return strings.Join(append(cols, "created_at", "updated_at"), ", ")
}

// InsertValues lists $1..$n for the insert query.
func (r Resource) InsertValues() string {
	n := len(strings.Split(r.InsertColumns(), ", "))
	vals := make([]string, n)
	for i := range vals {
		vals[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(vals, ", ")
}

// SetClause is the UPDATE assignment for f: COALESCE for required
// columns, an explicit set flag for nullable ones so null can be written.
func (f Field) SetClause() string {
	narg := fmt.Sprintf("sqlc.narg('%s')::%s", f.Col, f.Cast())
	if f.Null {
		return fmt.Sprintf("%s = CASE WHEN sqlc.arg('set_%s')::boolean THEN %s ELSE %s END", f.Col, f.Col, narg, f.Col)
	}
	return fmt.Sprintf("%s = COALESCE(%s, %s)", f.Col, narg, f.Col)
}

// CreateParam is the expression for f in the sqlc create params.
func (f Field) CreateParam() string {
	if f.HasDefault {
		return "def" + f.GoName()
	}
	return f.ToSqlc("in."+f.GoName(), f.Null)
}

// UpdateParam is the expression for f in the sqlc update params.
func (f Field) UpdateParam() string {
	switch {
	case f.Null && f.IsJSON():
		return "in." + f.GoName() + ".Value" // nil when absent or null
	case f.Null:
		return f.ToSqlc("in."+f.GoName()+".Ptr()", true)
	}
	return f.ToSqlc("in."+f.GoName(), true)
}

// SampleFor is Sample converted to CreateType, wrapping pointer types with
// the named generic helper (ptr in tests, Ptr in package factory).
func (f Field) SampleFor(seq, ptrFunc string) string {
	if strings.HasPrefix(f.CreateType(), "*") {
		return ptrFunc + "(" + f.Sample(seq) + ")"
	}
	return f.Sample(seq)
}

// NeedsPtr reports whether the unit tests use the ptr helper.
func (r Resource) NeedsPtr() bool {
	if r.PatchField() != nil {
		return true
	}
	for _, f := range r.Fields {
		if strings.HasPrefix(f.CreateType(), "*") {
			return true
		}
	}
	return false
}

// eq compares two values of the base type.
func (f Field) eq(a, b string) string {
	switch f.Type {
	case "decimal", "time":
		return a + ".Equal(" + b + ")"
	case "date":
		return a + ".Time.Equal(" + b + ".Time)"
	}
	return "(" + a + " == " + b + ")"
}

// FakeSame compares two model values of f (for unique checks).
func (f Field) FakeSame(a, b string) string {
	if f.Null {
		return fmt.Sprintf("(%s != nil && %s != nil && %s)", a, b, f.eq("(*"+a+")", "(*"+b+")"))
	}
	return f.eq(a, b)
}

// FakeMatch tests a model value against a dereferenced filter value.
func (f Field) FakeMatch(row, want string) string {
	if f.Null {
		return fmt.Sprintf("(%s != nil && %s)", row, f.eq("(*"+row+")", want))
	}
	return f.eq(row, want)
}

// Paths of the files a resource generates, relative to the project root.
func (r Resource) files() map[string]string {
	pkg := path.Join("internal", r.Pkg)
	return map[string]string{
		"model.go":        pkg + "/model.go",
		"dto.go":          pkg + "/dto.go",
		"repo.go":         pkg + "/repo.go",
		"repo_sql.go":     pkg + "/repo_sql.go",
		"service.go":      pkg + "/service.go",
		"handler.go":      pkg + "/handler.go",
		"routes.go":       pkg + "/routes.go",
		"fake_repo.go":    pkg + "/fake_repo.go",
		"service_test.go": pkg + "/service_test.go",
		"handler_test.go": pkg + "/handler_test.go",
		"factory.go":      "internal/factory/" + r.Pkg + ".go",
		"queries.sql":     "db/queries/" + r.Table + ".sql",
		"migration.sql":   "db/migrations/" + r.Timestamp + "_create_" + r.Table + ".sql",
	}
}

// Parts selectable with --only.
var resourceParts = map[string][]string{
	"model":     {"model.go", "dto.go"},
	"repo":      {"repo.go", "repo_sql.go", "fake_repo.go", "queries.sql"},
	"service":   {"service.go"},
	"handler":   {"handler.go", "routes.go"},
	"migration": {"migration.sql"},
	"tests":     {"service_test.go", "handler_test.go", "factory.go"},
}

// ResourceOptions are the inputs of PlanResource.
type ResourceOptions struct {
	Project    Project
	Name       string
	Fields     []string
	SoftDelete bool
	NoAuth     bool
	Only       []string // subset of model, repo, service, handler, migration, tests
	Version    string
	Now        time.Time
}

// PlanResource builds the plan for one resource on Postgres.
func PlanResource(rd Renderer, o ResourceOptions) (*Plan, Resource, error) {
	p := o.Project
	if p.DB != "postgres" {
		return nil, Resource{}, fmt.Errorf("db %q: resource templates for %s arrive in milestone M5; only postgres is available", p.DB, p.DB)
	}
	names, err := ResourceNames(o.Name)
	if err != nil {
		return nil, Resource{}, err
	}
	res := Resource{
		Names: names, Version: o.Version, Module: p.Module, Semii: SemiiModule,
		ID: p.ID, SoftDelete: o.SoftDelete, Public: o.NoAuth || !p.HasAuth(),
	}
	seen := map[string]bool{}
	for _, spec := range o.Fields {
		f, err := ParseField(spec, p.ID)
		if err != nil {
			return nil, Resource{}, err
		}
		if seen[f.Col] {
			return nil, Resource{}, fmt.Errorf("field %q is declared twice", f.Col)
		}
		seen[f.Col] = true
		if f.Ref != nil {
			if _, err := os.Stat(filepath.Join(p.Root, "internal", f.Ref.Pkg, "repo_sql.go")); err != nil {
				return nil, Resource{}, fmt.Errorf("field %q references %s, which does not exist yet; generate it first", spec, f.Ref.Name)
			}
		}
		res.Fields = append(res.Fields, f)
	}
	if p.HasAuth() && !o.NoAuth {
		return nil, Resource{}, fmt.Errorf("authenticated resources need the auth package (milestone M4); pass --no-auth for now")
	}
	res.Timestamp = MigrationVersion(p.Root, o.Now)
	// Re-running the generator targets the existing migration instead of
	// adding a second CREATE TABLE.
	if m, _ := filepath.Glob(filepath.Join(p.Root, "db", "migrations", "*_create_"+res.Table+".sql")); len(m) > 0 {
		res.Timestamp = strings.SplitN(filepath.Base(m[0]), "_", 2)[0]
	}

	selected := map[string]bool{}
	if len(o.Only) == 0 {
		for _, files := range resourceParts {
			for _, f := range files {
				selected[f] = true
			}
		}
	}
	for _, part := range o.Only {
		files, ok := resourceParts[part]
		if !ok {
			return nil, Resource{}, fmt.Errorf("--only: unknown part %q (model, repo, service, handler, migration, tests)", part)
		}
		for _, f := range files {
			selected[f] = true
		}
	}

	plan := &Plan{Root: p.Root}
	files := res.files()
	for _, tmpl := range sortedKeys(files) {
		if !selected[tmpl] {
			continue
		}
		target := files[tmpl]
		out, err := rd.Render("postgres/resource/"+tmpl+".tmpl", filepath.Join(p.Root, target), res)
		if err != nil {
			return nil, Resource{}, err
		}
		plan.Add(target, out)
	}
	if selected["routes.go"] {
		if err := planWiring(plan, p, res); err != nil {
			return nil, Resource{}, err
		}
	}
	return plan, res, nil
}

// planWiring registers the resource in internal/app (G3).
func planWiring(plan *Plan, p Project, r Resource) error {
	pkgImport := p.Module + "/internal/" + r.Pkg
	sqlcImport := p.Module + "/db/sqlc"
	edits := []struct {
		file    string
		markers []struct{ name, line string }
		imports []string
	}{
		{"internal/app/routes.go", []struct{ name, line string }{
			{"deps", fmt.Sprintf("%s *%s.Handler", r.Plural, r.Pkg)},
			{"routes", fmt.Sprintf("%s.Register(r, d.%s)", r.Pkg, r.Plural)},
		}, []string{pkgImport}},
		{"internal/app/app.go", []struct{ name, line string }{
			{"wiring", fmt.Sprintf("d.%s = %s.NewHandler(%s.NewService(%s.NewSQLRepo(sqlc.New(pool))))", r.Plural, r.Pkg, r.Pkg, r.Pkg)},
		}, []string{pkgImport, sqlcImport}},
	}
	for _, e := range edits {
		full := filepath.Join(p.Root, e.file)
		src, err := os.ReadFile(full)
		var manual []string
		for _, m := range e.markers {
			manual = append(manual, fmt.Sprintf("// between semii:%s markers:\n%s", m.name, m.line))
		}
		manual = append(manual, "// imports:\n\""+strings.Join(e.imports, "\"\n\"")+"\"")
		note := strings.Join(manual, "\n")
		if err != nil {
			plan.Edit(e.file, nil, note)
			continue
		}
		ok := true
		for _, m := range e.markers {
			next, err := InsertBetweenMarkers(src, m.name, m.line)
			if err != nil {
				ok = false
				break
			}
			src = next
		}
		if !ok {
			plan.Edit(e.file, nil, note)
			continue
		}
		out, err := AddImports(full, src, e.imports...)
		if err != nil {
			return err
		}
		plan.Edit(e.file, out, "")
	}
	return nil
}

// MigrationVersion returns a YYYYMMDDHHMMSS version not used by an
// existing migration, bumping by a second on collision.
func MigrationVersion(root string, now time.Time) string {
	t := now.UTC()
	for {
		v := t.Format("20060102150405")
		matches, _ := filepath.Glob(filepath.Join(root, "db", "migrations", v+"_*.sql"))
		if len(matches) == 0 {
			return v
		}
		t = t.Add(time.Second)
	}
}
