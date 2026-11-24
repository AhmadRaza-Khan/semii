package gen

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Strategies in canonical order.
var authStrategies = []string{"session", "jwt", "apikey"}

var strategyCtors = map[string]string{
	"session": "newSessionStrategy", "jwt": "newJWTStrategy", "apikey": "newAPIKeyStrategy",
}

// strategy files: Go source, its test, and the table it needs.
var strategyFiles = map[string]struct{ table, migration, queries string }{
	"session": {"sessions", "sessions", "sessions"},
	"jwt":     {"refresh_tokens", "refresh", "refresh"},
	"apikey":  {"api_keys", "apikeys", "apikeys"},
}

// AuthData is the template data for `semii generate auth`.
type AuthData struct {
	Version    string
	Module     string
	Semii      string
	ID         string
	Strategies []string // every generated strategy after this run
}

// UUID reports UUID primary keys.
func (d AuthData) UUID() bool { return d.ID != "bigint" }

// IDGo is the Go type of user ids.
func (d AuthData) IDGo() string {
	if d.UUID() {
		return "uuid.UUID"
	}
	return "int64"
}

// IDSQL is the column type of user id foreign keys.
func (d AuthData) IDSQL() string {
	if d.UUID() {
		return "UUID"
	}
	return "BIGINT"
}

// Has reports whether strategy is generated.
func (d AuthData) Has(strategy string) bool { return slices.Contains(d.Strategies, strategy) }

// AuthOptions are the inputs of PlanAuth.
type AuthOptions struct {
	Project    Project
	Strategies []string // strategies to add
	Version    string
	Now        time.Time
}

// PlanAuth plans the auth flows for the requested strategies. On a project
// without auth it generates internal/auth and wires it in; afterwards it
// only adds the missing strategies. The returned project carries the new
// strategy list for .semii.yaml.
func PlanAuth(rd Renderer, o AuthOptions) (*Plan, Project, error) {
	p := o.Project
	if p.DB != "postgres" {
		return nil, Project{}, fmt.Errorf("db %q: auth templates for %s arrive in milestone M5; only postgres is available", p.DB, p.DB)
	}
	var add []string
	for _, s := range o.Strategies {
		if !slices.Contains(authStrategies, s) {
			return nil, Project{}, fmt.Errorf("unknown strategy %q (session, jwt, apikey)", s)
		}
		if !slices.Contains(p.Auth, s) && !slices.Contains(add, s) {
			add = append(add, s)
		}
	}
	if len(add) == 0 {
		return nil, Project{}, fmt.Errorf("nothing to add: strategies %v are already generated", p.Auth)
	}
	final := append(slices.Clone(p.Auth), add...)
	if slices.Contains(final, "apikey") && !slices.Contains(final, "session") && !slices.Contains(final, "jwt") {
		return nil, Project{}, fmt.Errorf("apikey needs session or jwt so users can sign in to manage keys")
	}
	fresh := len(p.Auth) == 0
	if _, err := os.Stat(filepath.Join(p.Root, "internal", "auth", "auth.go")); fresh && err == nil {
		return nil, Project{}, fmt.Errorf("internal/auth already exists but .semii.yaml lists no strategies; fix .semii.yaml")
	}

	data := AuthData{Version: o.Version, Module: p.Module, Semii: SemiiModule, ID: p.ID, Strategies: final}
	plan := &Plan{Root: p.Root}
	render := func(tmpl, target string) error {
		out, err := rd.Render("postgres/auth/"+tmpl, filepath.Join(p.Root, target), data)
		if err != nil {
			return err
		}
		plan.Add(target, out)
		return nil
	}

	versions := migrationVersions(p.Root, o.Now, len(add)+1)
	if fresh {
		for _, f := range []string{"auth", "model", "service", "handler", "routes", "mailer", "seed"} {
			if err := render(f+".go.tmpl", "internal/auth/"+f+".go"); err != nil {
				return nil, Project{}, err
			}
		}
		for _, f := range []string{"helpers", "auth"} {
			if err := render(f+"_test.go.tmpl", "internal/auth/"+f+"_test.go"); err != nil {
				return nil, Project{}, err
			}
		}
		if err := render("users.migration.sql.tmpl", "db/migrations/"+versions[0]+"_create_users.sql"); err != nil {
			return nil, Project{}, err
		}
		if err := render("users.queries.sql.tmpl", "db/queries/auth_users.sql"); err != nil {
			return nil, Project{}, err
		}
		if err := render("factory_user.go.tmpl", "internal/factory/user.go"); err != nil {
			return nil, Project{}, err
		}
	}
	for i, s := range add {
		f := strategyFiles[s]
		if err := render(s+".go.tmpl", "internal/auth/"+s+".go"); err != nil {
			return nil, Project{}, err
		}
		if err := render(s+"_test.go.tmpl", "internal/auth/"+s+"_test.go"); err != nil {
			return nil, Project{}, err
		}
		if err := render(f.migration+".migration.sql.tmpl", "db/migrations/"+versions[i+1]+"_create_"+f.table+".sql"); err != nil {
			return nil, Project{}, err
		}
		if err := render(f.queries+".queries.sql.tmpl", "db/queries/auth_"+f.table+".sql"); err != nil {
			return nil, Project{}, err
		}
		if s == "jwt" {
			if err := render("factory_token.go.tmpl", "internal/factory/token.go"); err != nil {
				return nil, Project{}, err
			}
		}
	}

	strategiesValue := strings.Join(final, ",")
	semiiAuth := "semiiauth " + SemiiModule + "/auth"
	authPkg := p.Module + "/internal/auth"
	if fresh {
		edits := []fileEdit{
			{"internal/config/config.go", []markerLine{
				{"config", "Auth semiiauth.Config `envPrefix:\"AUTH_\"`"},
				{"config", "SMTP semiiauth.SMTPConfig `envPrefix:\"SMTP_\"`"},
			}, []string{semiiAuth}},
			{"internal/app/app.go", []markerLine{
				{"wiring", "authH, authN, authErr := auth.Wire(cfg, pool, log)"},
				{"wiring", "d.Auth = authH"},
				{"wiring", "mw = append(mw, semiiauth.Middleware(authN...))"},
				{"wiring", "problems = append(problems, authErr, cfg.Auth.Validate())"},
				{"wiring", "strictRoutes = cfg.Auth.StrictRoutes"},
			}, []string{authPkg, semiiAuth}},
			{"internal/app/routes.go", []markerLine{
				{"deps", "Auth *auth.Handler"},
				{"routes", "auth.Register(r, d.Auth)"},
			}, []string{authPkg}},
			{"internal/app/apptest/apptest.go", []markerLine{
				{"testenv", `"AUTH_STRATEGIES": "` + strategiesValue + `",`},
				{"testenv", `"AUTH_JWT_SECRET": "test-secret-test-secret-test-secret-0123",`},
				{"testenv", `"AUTH_APP_URL": "https://app.test",`},
			}, nil},
			{"db/seeds/seed.go", []markerLine{
				{"seed", seedAdminSnippet},
			}, []string{authPkg, "semiiconfig " + SemiiModule + "/config"}},
		}
		for _, e := range edits {
			if err := planEdit(plan, p.Root, e); err != nil {
				return nil, Project{}, err
			}
		}
	} else {
		var lines []markerLine
		for _, s := range add {
			lines = append(lines, markerLine{"strategies", fmt.Sprintf("all = append(all, %s(svc))", strategyCtors[s])})
		}
		if err := planEdit(plan, p.Root, fileEdit{"internal/auth/auth.go", lines, nil}); err != nil {
			return nil, Project{}, err
		}
		for _, f := range []string{"internal/app/apptest/apptest.go", "internal/auth/helpers_test.go"} {
			planReplace(plan, p.Root, f, authStrategiesGo, `"AUTH_STRATEGIES": "`+strategiesValue+`",`)
		}
	}
	if err := planEnv(plan, p.Root, ".env.example", final, false); err != nil {
		return nil, Project{}, err
	}
	if _, err := os.Stat(filepath.Join(p.Root, ".env")); err == nil {
		if err := planEnv(plan, p.Root, ".env", final, true); err != nil {
			return nil, Project{}, err
		}
	}
	p.Auth = final
	plan.Edit(ProjectFile, p.Marshal(), "")
	return plan, p, nil
}

const seedAdminSnippet = `admin, err := semiiconfig.Load[struct {
	Email    string             ` + "`env:\"SEED_ADMIN_EMAIL\"`" + `
	Password semiiconfig.Secret ` + "`env:\"SEED_ADMIN_PASSWORD\"`" + `
}]()
if err != nil {
	return err
}
if admin.Email != "" {
	if err := auth.SeedAdmin(ctx, d.Pool, admin.Email, admin.Password.Reveal()); err != nil {
		return err
	}
	d.Log.Info("admin user seeded", "email", admin.Email)
}`

var authStrategiesGo = regexp.MustCompile(`"AUTH_STRATEGIES":\s*"[^"]*",`)

// markerLine is one line to insert between the named markers.
type markerLine struct{ marker, line string }

// fileEdit inserts lines between markers in file and adds imports.
type fileEdit struct {
	file    string
	lines   []markerLine
	imports []string
}

// planEdit applies e to the file on disk (G3); when the file or a marker
// is missing it plans a manual note with the snippet instead.
func planEdit(plan *Plan, root string, e fileEdit) error {
	full := filepath.Join(root, e.file)
	var manual []string
	for _, l := range e.lines {
		manual = append(manual, fmt.Sprintf("// between semii:%s markers:\n%s", l.marker, l.line))
	}
	if len(e.imports) > 0 {
		manual = append(manual, "// imports:\n"+strings.Join(e.imports, "\n"))
	}
	note := strings.Join(manual, "\n")
	src, err := os.ReadFile(full)
	if err != nil {
		plan.Edit(e.file, nil, note)
		return nil
	}
	for _, l := range e.lines {
		if src, err = InsertBetweenMarkers(src, l.marker, l.line); err != nil {
			plan.Edit(e.file, nil, note)
			return nil
		}
	}
	if strings.HasSuffix(e.file, ".go") {
		if src, err = AddImports(full, src, e.imports...); err != nil {
			return err
		}
	}
	plan.Edit(e.file, src, "")
	return nil
}

// planReplace rewrites the first match of re in file, if both exist.
func planReplace(plan *Plan, root, file string, re *regexp.Regexp, repl string) {
	src, err := os.ReadFile(filepath.Join(root, file))
	if err != nil || !re.Match(src) {
		return
	}
	plan.Edit(file, re.ReplaceAll(src, []byte(repl)), "")
}

// planEnv sets AUTH_STRATEGIES and appends the auth and mail settings to
// an env file. secrets puts a random JWT secret in .env; .env.example gets
// a placeholder.
func planEnv(plan *Plan, root, file string, strategies []string, secrets bool) error {
	src, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil
	}
	line := regexp.MustCompile(`(?m)^AUTH_STRATEGIES=.*$`)
	value := "AUTH_STRATEGIES=" + strings.Join(strategies, ",")
	if line.Match(src) {
		src = line.ReplaceAll(src, []byte(value))
	} else {
		secret := "dev-only-secret-change-me-in-production"
		if secrets {
			b := make([]byte, 36)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			secret = base64.RawURLEncoding.EncodeToString(b)
		}
		block := fmt.Sprintf(`
# Auth (see semii/auth.Config). Strategies: session, jwt, apikey.
%s
AUTH_APP_URL=http://localhost:3000
# HS256 signing key, at least 32 bytes. Use a unique random value per environment.
AUTH_JWT_SECRET=%s
# AUTH_REQUIRE_VERIFIED_EMAIL=false

# Account emails. While SMTP_HOST is empty, links are logged (development
# only); production refuses to start without it.
# SMTP_HOST=
# SMTP_PORT=587
# SMTP_USERNAME=
# SMTP_PASSWORD=
# SMTP_FROM=no-reply@example.com

# semii db seed creates this admin account when set.
# SEED_ADMIN_EMAIL=admin@example.com
# SEED_ADMIN_PASSWORD=
`, value, secret)
		src = append(src, block...)
	}
	plan.Edit(file, src, "")
	return nil
}

// migrationVersions returns n consecutive unused migration versions, so
// tables are created in dependency order.
func migrationVersions(root string, now time.Time, n int) []string {
	out := make([]string, 0, n)
	t := now.UTC()
	for len(out) < n {
		v := MigrationVersion(root, t)
		out = append(out, v)
		parsed, _ := time.Parse("20060102150405", v)
		t = parsed.Add(time.Second)
	}
	return out
}
