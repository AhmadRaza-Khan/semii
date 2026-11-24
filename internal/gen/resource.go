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
const SemiiModule = "github.com/AhmadRaza-Khan/semii"

// Resource is the template data for `semii generate resource`.
type Resource struct {
	Names
	Version    string
	Module     string
	Semii      string
	DB         string // postgres | mysql | sqlite | mongo
	Fields     []Field
	ID         string // uuid | bigint | objectid
	SoftDelete bool
	Public     bool
	Timestamp  string // migration version
}

// IDGo is the Go type of the primary key.
func (r Resource) IDGo() string {
	switch r.ID {
	case "bigint":
		return "int64"
	case "objectid":
		return "bson.ObjectID"
	}
	return "uuid.UUID"
}

// Database predicates for templates.
func (r Resource) Postgres() bool { return r.DB == "postgres" }
func (r Resource) MySQL() bool    { return r.DB == "mysql" }
func (r Resource) SQLite() bool   { return r.DB == "sqlite" }
func (r Resource) Mongo() bool    { return r.DB == "mongo" }

// Flag is the SQL test "the optional parameter name is unset" for MySQL
// and SQLite queries; sqlc types the parameter from the cast.
func (r Resource) Flag(name string) string {
	if r.MySQL() {
		return "CAST(sqlc.arg('" + name + "') AS SIGNED) = 0"
	}
	return "CAST(sqlc.arg('" + name + "') AS BOOLEAN) = 0"
}

// FlagOn is the Go value setting a Flag parameter.
func (r Resource) FlagOn() string {
	if r.MySQL() {
		return "1"
	}
	return "true"
}

// IDSQLType is the primary key column type for MySQL and SQLite.
func (r Resource) IDSQLType() string {
	switch {
	case r.SeqID() && r.MySQL():
		return "BIGINT AUTO_INCREMENT PRIMARY KEY"
	case r.SeqID():
		return "INTEGER PRIMARY KEY AUTOINCREMENT"
	case r.MySQL():
		return "CHAR(36) PRIMARY KEY"
	}
	return "UUID PRIMARY KEY"
}

// TimeSQL is the timestamp column type for MySQL and SQLite.
func (r Resource) TimeSQL() string {
	if r.MySQL() {
		return "DATETIME(6)"
	}
	return "DATETIME"
}

// LimitType is sqlc's Go type for LIMIT ? on MySQL and SQLite.
func (r Resource) LimitType() string {
	if r.MySQL() {
		return "int32"
	}
	return "int64"
}

// SeqID reports database-assigned integer ids.
func (r Resource) SeqID() bool { return r.ID == "bigint" }

// ObjectID reports MongoDB ObjectID ids.
func (r Resource) ObjectID() bool { return r.ID == "objectid" }

// NewIDExpr generates an id in Go (not used for bigint).
func (r Resource) NewIDExpr() string {
	if r.ObjectID() {
		return "bson.NewObjectID()"
	}
	return "uuid.Must(uuid.NewV7())"
}

// DocConvertible reports that the Mongo document has exactly the model's
// fields and types, so toDoc can be a plain conversion.
func (r Resource) DocConvertible() bool {
	if r.SoftDelete {
		return false
	}
	for _, f := range r.Fields {
		if !f.docIdentity() {
			return false
		}
	}
	return true
}

// FactoryType is the database handle factories and repositories take.
func (r Resource) FactoryType() string {
	switch r.DB {
	case "postgres":
		return "*sqlc.Queries"
	case "mongo":
		return "*mongo.Database"
	}
	return "*sql.DB"
}

// RepoCtor builds the database repository from arg (a FactoryType).
func (r Resource) RepoCtor(arg string) string {
	if r.Mongo() {
		return "NewMongoRepo(" + arg + ")"
	}
	return "NewSQLRepo(" + arg + ")"
}

// FactoryArg is the FactoryType expression handler tests pass, given the
// app a.
func (r Resource) FactoryArg() string {
	if r.Postgres() {
		return "sqlc.New(a.DB)"
	}
	return "a.DB"
}

// IDCast is the Postgres type of the primary key.
func (r Resource) IDCast() string {
	if r.UUID() {
		return "uuid"
	}
	return "bigint"
}

// UUID reports UUID primary keys.
func (r Resource) UUID() bool { return r.ID == "uuid" || r.ID == "" }

// MissingID is a Go expression for an id that does not exist.
func (r Resource) MissingID() string {
	switch r.ID {
	case "bigint":
		return `"999999999"`
	case "objectid":
		return "bson.NewObjectID().Hex()"
	}
	return "uuid.NewString()"
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

// InsertPlaceholders lists ? placeholders for the MySQL/SQLite insert.
func (r Resource) InsertPlaceholders() string {
	n := len(strings.Split(r.InsertColumns(), ", "))
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
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

// dbDir is the template directory for the project's database.
func dbDir(db string) string {
	if db == "mysql" || db == "sqlite" {
		return "sql"
	}
	return db
}

// files maps template paths to output paths, relative to the project.
func (r Resource) files() map[string]string {
	pkg := path.Join("internal", r.Pkg)
	out := map[string]string{"common/resource/factory.go.tmpl": "internal/factory/" + r.Pkg + ".go"}
	for _, f := range []string{"model", "dto", "repo", "service", "handler", "routes", "fake_repo", "service_test", "handler_test"} {
		out["common/resource/"+f+".go.tmpl"] = pkg + "/" + f + ".go"
	}
	dir := dbDir(r.DB) + "/resource/"
	if r.Mongo() {
		out[dir+"repo_mongo.go.tmpl"] = pkg + "/repo_mongo.go"
		return out
	}
	out[dir+"repo_sql.go.tmpl"] = pkg + "/repo_sql.go"
	out[dir+"queries.sql.tmpl"] = "db/queries/" + r.Table + ".sql"
	out[dir+"migration.sql.tmpl"] = "db/migrations/" + r.Timestamp + "_create_" + r.Table + ".sql"
	return out
}

// Parts selectable with --only, by output file name.
var resourceParts = map[string][]string{
	"model":     {"model.go", "dto.go"},
	"repo":      {"repo.go", "repo_sql.go", "repo_mongo.go", "fake_repo.go", "queries.sql"},
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

// PlanResource builds the plan for one resource.
func PlanResource(rd Renderer, o ResourceOptions) (*Plan, Resource, error) {
	p := o.Project
	names, err := ResourceNames(o.Name)
	if err != nil {
		return nil, Resource{}, err
	}
	res := Resource{
		Names: names, Version: o.Version, Module: p.Module, Semii: SemiiModule,
		DB: p.DB, ID: p.ID, SoftDelete: o.SoftDelete, Public: o.NoAuth || !p.HasAuth(),
	}
	seen := map[string]bool{}
	for _, spec := range o.Fields {
		f, err := ParseField(spec, p.ID)
		if err != nil {
			return nil, Resource{}, err
		}
		if err := f.SetDialect(p.DB); err != nil {
			return nil, Resource{}, err
		}
		if seen[f.Col] {
			return nil, Resource{}, fmt.Errorf("field %q is declared twice", f.Col)
		}
		seen[f.Col] = true
		if f.Ref != nil {
			if _, err := os.Stat(filepath.Join(p.Root, "internal", f.Ref.Pkg, "repo.go")); err != nil {
				return nil, Resource{}, fmt.Errorf("field %q references %s, which does not exist yet; generate it first", spec, f.Ref.Name)
			}
		}
		res.Fields = append(res.Fields, f)
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
		target := files[tmpl]
		if !selected[path.Base(strings.TrimSuffix(tmpl, ".tmpl"))] {
			continue
		}
		out, err := rd.Render(tmpl, filepath.Join(p.Root, target), res)
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
	if err := planEdit(plan, p.Root, fileEdit{"internal/app/routes.go", []markerLine{
		{"deps", fmt.Sprintf("%s *%s.Handler", r.Plural, r.Pkg)},
		{"routes", fmt.Sprintf("%s.Register(r, d.%s)", r.Pkg, r.Plural)},
	}, []string{pkgImport}}); err != nil {
		return err
	}
	repo, imports := r.Pkg+".NewSQLRepo(sqlDB)", []string{pkgImport}
	switch r.DB {
	case "postgres":
		repo = r.Pkg + ".NewSQLRepo(sqlc.New(pool))"
		imports = append(imports, p.Module+"/db/sqlc")
	case "mongo":
		repo = r.Pkg + ".NewMongoRepo(mdb)"
		if err := planEdit(plan, p.Root, fileEdit{"db/mongo/indexes.go", []markerLine{
			{"indexes", fmt.Sprintf("%q: %s.Indexes(),", r.Table, r.Pkg)},
		}, []string{pkgImport}}); err != nil {
			return err
		}
	}
	return planEdit(plan, p.Root, fileEdit{"internal/app/app.go", []markerLine{
		{"wiring", fmt.Sprintf("d.%s = %s.NewHandler(%s.NewService(%s))", r.Plural, r.Pkg, r.Pkg, repo)},
	}, imports})
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
