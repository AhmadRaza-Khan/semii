package gen

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semii/semii/templates"
)

func TestNaming(t *testing.T) {
	cases := []struct{ in, pascal, camel, snake, kebab, sqlc string }{
		{"customer_id", "CustomerID", "customerID", "customer_id", "customer-id", "CustomerID"},
		{"OrderItem", "OrderItem", "orderItem", "order_item", "order-item", "OrderItem"},
		{"api_url", "APIURL", "apiURL", "api_url", "api-url", "ApiUrl"},
		{"HTTPServer", "HTTPServer", "httpServer", "http_server", "http-server", "HttpServer"},
		{"line2Total", "Line2Total", "line2Total", "line2_total", "line2-total", "Line2Total"},
	}
	for _, c := range cases {
		if got := Pascal(c.in); got != c.pascal {
			t.Errorf("Pascal(%q) = %q", c.in, got)
		}
		if got := Camel(c.in); got != c.camel {
			t.Errorf("Camel(%q) = %q", c.in, got)
		}
		if got := Snake(c.in); got != c.snake {
			t.Errorf("Snake(%q) = %q", c.in, got)
		}
		if got := Kebab(c.in); got != c.kebab {
			t.Errorf("Kebab(%q) = %q", c.in, got)
		}
		if got := SqlcName(c.in); got != c.sqlc {
			t.Errorf("SqlcName(%q) = %q", c.in, got)
		}
	}
	if Camel("") != "" || Plural("") != "" {
		t.Error("empty input")
	}
	plurals := map[string]string{
		"order": "orders", "category": "categories", "day": "days", "box": "boxes", "address": "addresses",
		"batch": "batches", "person": "people", "order_item": "order_items", "line_person": "line_people",
	}
	for in, want := range plurals {
		if got := Plural(in); got != want {
			t.Errorf("Plural(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResourceNames(t *testing.T) {
	n, err := ResourceNames("order_item")
	if err != nil {
		t.Fatal(err)
	}
	want := Names{Name: "OrderItem", Var: "orderItem", Plural: "OrderItems", PluralVar: "orderItems",
		Pkg: "orderitem", Table: "order_items", Route: "/order-items", SqlcStruct: "OrderItem"}
	if n != want {
		t.Errorf("%+v", n)
	}
	for _, bad := range []string{"", "Type", "Web", "Config", "9lives", "Sheep", "Ord€r"} {
		if _, err := ResourceNames(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseField(t *testing.T) {
	f, err := ParseField("customer:ref:Customer:null", "uuid")
	if err != nil || f.Col != "customer_id" || !f.Index || !f.Null || f.SQLType() != "UUID" || f.Ref.Table != "customers" {
		t.Fatalf("%+v %v", f, err)
	}
	if !strings.Contains(f.ColumnDef(), "REFERENCES customers (id) ON DELETE RESTRICT") || strings.Contains(f.ColumnDef(), "NOT NULL") {
		t.Errorf("%s", f.ColumnDef())
	}
	f, _ = ParseField("owner:ref:User", "bigint")
	if f.GoType() != "int64" || f.SQLType() != "BIGINT" || f.Sample("n") != "int64(1)" {
		t.Errorf("bigint ref: %s %s", f.GoType(), f.SQLType())
	}
	f, err = ParseField("title:string:max=80:min=3", "uuid")
	if err != nil || f.SQLType() != "VARCHAR(80)" || f.CreateValidate() != "required,min=3,max=80" || f.UpdateValidate() != "omitempty,min=3,max=80" {
		t.Errorf("%s %s %s %v", f.SQLType(), f.CreateValidate(), f.UpdateValidate(), err)
	}
	f, _ = ParseField("total:decimal:min=0:max=10", "uuid")
	if f.CreateValidate() != "decimal_gte=0,decimal_lte=10" || f.Required() {
		t.Errorf("%s", f.CreateValidate())
	}
	f, _ = ParseField("status:string:default=it's", "uuid")
	if f.CreateType() != "*string" || f.CreateValidate() != "omitempty,max=255" || !strings.HasSuffix(f.ColumnDef(), "DEFAULT 'it''s'") || f.DefaultGo() != `"it's"` {
		t.Errorf("%s %s %s", f.CreateType(), f.CreateValidate(), f.ColumnDef())
	}
	f, _ = ParseField("notes:json:null", "uuid")
	if f.GoType() != "json.RawMessage" || f.UpdateType() != "web.Optional[json.RawMessage]" || f.UpdateParam() != "in.Notes.Value" {
		t.Errorf("%s %s", f.GoType(), f.UpdateType())
	}
	f, _ = ParseField("due:date:null", "uuid")
	if f.FromSqlc("row.Due") != "web.DateFromPtr(row.Due)" || f.UpdateParam() != "in.Due.Ptr().TimePtr()" {
		t.Errorf("%s", f.FromSqlc("row.Due"))
	}

	bad := []string{
		"x", ":string", "name:", "id:string", "created_at:time", "n:nope", "n:string:wat",
		"n:bool:unique", "m:json:index", "n:bool:min=1", "n:string:max=x", "n:string:max=0",
		"n:int:default=x", "n:float:default=x", "n:decimal:default=x", "n:bool:default=yes",
		"n:time:default=now", "n:string:null:default=a", "c:ref", "c:refx:Customer", "c:ref:Type", "Bad-Name!:string",
	}
	for _, spec := range bad {
		if _, err := ParseField(spec, "uuid"); err == nil {
			t.Errorf("%q accepted", spec)
		}
	}
}

func TestSamples(t *testing.T) {
	f, _ := ParseField("code:string:unique:max=12", "uuid")
	if got := f.Sample("n"); got != `fmt.Sprintf("s-%d", n)` {
		t.Errorf("short unique: %s", got)
	}
	f, _ = ParseField("code:string:max=4:min=2", "uuid")
	if got := f.Sample("n"); got != `"ss"` {
		t.Errorf("tiny: %s", got)
	}
	f, _ = ParseField("qty:int:min=5:max=9:unique", "uuid")
	if got := f.Sample("n"); got != "int32(5 + n)" {
		t.Errorf("int: %s", got)
	}
	f, _ = ParseField("price:decimal:max=3", "uuid")
	if got := f.Sample("n"); got != `decimal.RequireFromString("3.0000")` {
		t.Errorf("decimal: %s", got)
	}
	f, _ = ParseField("name:text:min=30", "uuid")
	if got := f.Sample("n"); len(got) < 32 {
		t.Errorf("min length: %s", got)
	}
}

// TestRenderMatrix renders every template for many field combinations and
// both id kinds; FormatGo fails on any template that emits invalid Go.
func TestRenderMatrix(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "uuid")
	if err := os.MkdirAll(filepath.Join(root, "internal", "customer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "customer", "repo_sql.go"), []byte("package customer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := Renderer{FS: templates.FS, Version: "vtest"}
	fieldSets := [][]string{
		nil,
		{"name:string"},
		{"customer:ref:Customer", "customer2:ref:Customer:null", "total:decimal:min=0", "discount:decimal:null",
			"qty:int:min=1:max=9", "big:int64:null:unique", "paid:bool:default=true", "score:float:null:index",
			"due:date", "due_on:date:null:index", "at:time:unique", "at2:time:null", "meta:json", "meta2:json:null",
			"ref:uuid:null:index", "status:string:default=open:index", "notes:text:null", "code:string:unique",
			"amount:decimal:default=1.5", "level:int:default=3", "ratio:float:default=0.5", "body:text:min=10"},
	}
	for _, id := range []string{"uuid", "bigint"} {
		p, err := ReadProject(writeProject(t, root, id))
		if err != nil {
			t.Fatal(err)
		}
		p.Root = root
		for i, fields := range fieldSets {
			for _, soft := range []bool{false, true} {
				plan, res, err := PlanResource(rd, ResourceOptions{
					Project: p, Name: "Order", Fields: fields, SoftDelete: soft, Version: "vtest", Now: time.Unix(0, 0),
				})
				if err != nil {
					t.Fatalf("%s set %d soft=%v: %v", id, i, soft, err)
				}
				if len(plan.Changes) != 15 || res.Route != "/orders" {
					t.Fatalf("%d changes", len(plan.Changes))
				}
				for _, c := range plan.Changes {
					if c.Action == Modify && c.Content == nil {
						t.Errorf("%s: markers not applied: %s", c.Path, c.Note)
					}
				}
			}
		}
	}

	p, _ := ReadProject(filepath.Join(root, ProjectFile))
	p.Root = root
	plan, _, err := PlanResource(rd, ResourceOptions{Project: p, Name: "Order", Only: []string{"model", "migration"}, Now: time.Unix(0, 0)})
	if err != nil || len(plan.Changes) != 3 {
		t.Fatalf("--only: %d changes, %v", len(plan.Changes), err)
	}
	if _, _, err := PlanResource(rd, ResourceOptions{Project: p, Name: "Order", Only: []string{"nope"}}); err == nil {
		t.Error("unknown --only part accepted")
	}
	for name, mut := range map[string]func(*Project){
		"mysql": func(p *Project) { p.DB = "mysql" },
		"auth":  func(p *Project) { p.Auth = []string{"jwt"} },
	} {
		q := p
		mut(&q)
		if _, _, err := PlanResource(rd, ResourceOptions{Project: q, Name: "Order"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := PlanResource(rd, ResourceOptions{Project: p, Name: "Order", Fields: []string{"a:string", "a:text"}}); err == nil {
		t.Error("duplicate field accepted")
	}
}

// writeProject lays down the files resource generation edits.
func writeProject(t *testing.T, root, id string) string {
	t.Helper()
	rd := Renderer{FS: templates.FS, Version: "vtest"}
	plan, _, err := PlanNew(rd, root, NewOptions{Name: "shop", Module: "example.com/shop", DB: "postgres", ID: id, Docker: true, Version: "vtest", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := plan.Apply(&out, true, false); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, ProjectFile)
}

func TestPlanNew(t *testing.T) {
	rd := Renderer{FS: templates.FS, Version: "v1.2.3"}
	root := t.TempDir()
	plan, proj, err := PlanNew(rd, root, NewOptions{Name: "my-shop", Module: "x/y", DB: "postgres", ID: "uuid", Version: "v1.2.3", LocalSemii: "/src/semii", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if proj.Template != TemplateVersion || proj.Module != "x/y" {
		t.Errorf("%+v", proj)
	}
	files := map[string]string{}
	for _, c := range plan.Changes {
		files[c.Path] = string(c.Content)
	}
	if _, ok := files["docker-compose.yml"]; ok {
		t.Error("docker-compose.yml written without Docker")
	}
	if !strings.Contains(files["go.mod"], "require github.com/semii/semii v1.2.3") || !strings.Contains(files["go.mod"], "replace github.com/semii/semii => /src/semii") {
		t.Errorf("go.mod:\n%s", files["go.mod"])
	}
	if !strings.Contains(files[".env.example"], "/my_shop?") {
		t.Errorf(".env.example db name")
	}
	if !strings.HasPrefix(files["internal/app/app.go"], "// Code generated by semii v1.2.3. You own this file; edit freely.") {
		t.Error("missing header")
	}
	if _, _, err := PlanNew(rd, root, NewOptions{Name: "a", Module: "a", DB: "mongo", ID: "uuid"}); err == nil {
		t.Error("mongo accepted in M3")
	}
	if _, _, err := PlanNew(rd, root, NewOptions{Name: "a", Module: "a", DB: "postgres", ID: "uuid", Auth: []string{"jwt"}}); err == nil {
		t.Error("auth accepted in M3")
	}
	if _, _, err := PlanNew(rd, root, NewOptions{Name: "a", Module: "a", DB: "postgres", ID: "int"}); err == nil {
		t.Error("bad id accepted")
	}
	if requireVersion("v0.3.0-dev") != "v0.0.0-00010101000000-000000000000" {
		t.Error("dev version")
	}
}

func TestProjectFile(t *testing.T) {
	dir := t.TempDir()
	p := Project{Semii: "v1", Template: 1, Module: "m", DB: "postgres", Auth: []string{"jwt", "apikey"}, ID: "uuid"}
	path := filepath.Join(dir, ProjectFile)
	if err := os.WriteFile(path, p.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "a", "b")
	_ = os.MkdirAll(sub, 0o755)
	got, err := FindProject(sub)
	if err != nil || got.Root != dir || len(got.Auth) != 2 || !got.HasAuth() || got.Module != "m" {
		t.Fatalf("%+v %v", got, err)
	}
	none := Project{Semii: "v1", Template: 1, Module: "m", DB: "postgres", ID: "uuid"}
	_ = os.WriteFile(path, none.Marshal(), 0o644)
	if got, _ := ReadProject(path); got.HasAuth() {
		t.Error("auth: [] parsed as configured")
	}
	for _, bad := range []string{"nope", "semii: v1\nwat: 1\n", "template: x\n", "module: m\ndb: oracle\nid: uuid\ntemplate: 1\n", "module: m\ndb: postgres\nid: uuid\ntemplate: 1\nauth: [ldap]\n", "db: postgres\n"} {
		_ = os.WriteFile(path, []byte(bad), 0o644)
		if _, err := ReadProject(path); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := FindProject(t.TempDir()); err == nil {
		t.Error("found a project in an empty dir")
	}
}

func TestInsertBetweenMarkers(t *testing.T) {
	src := []byte("type d struct {\n\t// semii:deps:start\n\tA    *a.Handler\n\t// semii:deps:end\n}\n")
	out, err := InsertBetweenMarkers(src, "deps", "B *b.Handler")
	if err != nil || !strings.Contains(string(out), "\tA    *a.Handler\n\tB *b.Handler\n\t// semii:deps:end") {
		t.Fatalf("%s %v", out, err)
	}
	again, _ := InsertBetweenMarkers(out, "deps", "A *a.Handler")
	if !bytes.Equal(again, out) {
		t.Error("re-insert of a gofmt-aligned line duplicated it")
	}
	if _, err := InsertBetweenMarkers(src, "routes", "x"); !errors.Is(err, ErrMarkersMissing) {
		t.Errorf("%v", err)
	}
}

func TestPlanApply(t *testing.T) {
	root := t.TempDir()
	plan := &Plan{Root: root}
	plan.Add("a/b.txt", []byte("1"))
	plan.Edit("c.go", nil, "add this line")
	var out bytes.Buffer
	if err := plan.Apply(&out, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b.txt")); err == nil {
		t.Error("dry run wrote a file")
	}
	if err := plan.Apply(&out, false, false); err != nil {
		t.Fatal(err)
	}
	var ce *ConflictError
	if err := plan.Apply(&out, false, false); !errors.As(err, &ce) || ce.Paths[0] != "a/b.txt" {
		t.Fatalf("%v", err)
	}
	if err := plan.Apply(&out, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "overwrite a/b.txt") || !strings.Contains(out.String(), "manual") {
		t.Errorf("%s", out.String())
	}
}

func TestMarkersMissingGivesManualNote(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "uuid")
	if err := os.WriteFile(filepath.Join(root, "internal", "app", "routes.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := ReadProject(filepath.Join(root, ProjectFile))
	p.Root = root
	plan, _, err := PlanResource(Renderer{FS: templates.FS, Version: "v"}, ResourceOptions{Project: p, Name: "Note", Fields: []string{"title:string"}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Changes {
		if c.Path == "internal/app/routes.go" {
			if c.Content != nil || !strings.Contains(c.Note, "note.Register(r, d.Notes)") {
				t.Errorf("%+v", c)
			}
			return
		}
	}
	t.Error("no change for routes.go")
}

func TestFormatGoError(t *testing.T) {
	if _, err := FormatGo("x.go", []byte("package x\nfunc (")); err == nil || !strings.Contains(err.Error(), "   2  func (") {
		t.Errorf("%v", err)
	}
	if _, err := (Renderer{FS: templates.FS}).Render("missing.tmpl", "x", nil); err == nil {
		t.Error("missing template rendered")
	}
}
