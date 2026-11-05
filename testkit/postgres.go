package testkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/semii/semii/db/migrate"
	"github.com/semii/semii/db/pgxdb"
)

// EnvDatabaseURL names the variable pointing tests at a database server,
// e.g. postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable.
// Container-backed databases (testcontainers) arrive with M5; until then
// tests needing a database are skipped when it is unset.
const EnvDatabaseURL = "TEST_DATABASE_URL"

// DBOption configures a test database.
type DBOption func(*dbOptions)

type dbOptions struct {
	migrations fs.FS
}

// WithMigrations applies the migrations in fsys (files at its root).
func WithMigrations(fsys fs.FS) DBOption { return func(o *dbOptions) { o.migrations = fsys } }

// Process-wide registry of template databases. Templates are expensive to
// build, so each test process migrates one per migration set and clones it
// per test; Main drops them on exit. This is the only shared state in
// testkit, and it is test-only.
var (
	tplMu     sync.Mutex
	templates = map[string]string{} // migration hash -> template db name
	dbSeq     atomic.Int64
)

// Postgres returns a pool on a fresh database cloned from a migrated
// template (CREATE DATABASE ... TEMPLATE), dropped when the test ends.
// Tests are skipped in -short mode or when TEST_DATABASE_URL is unset.
func Postgres(t testing.TB, opts ...DBOption) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("testkit: skipping database test in -short mode")
	}
	adminURL := os.Getenv(EnvDatabaseURL)
	if adminURL == "" {
		t.Skipf("testkit: %s not set; start Postgres (docker compose up -d db) and export it", EnvDatabaseURL)
	}
	var o dbOptions
	for _, opt := range opts {
		opt(&o)
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testkit: connect %s: %v", EnvDatabaseURL, err)
	}
	defer admin.Close(ctx)

	tplMu.Lock()
	tpl, err := ensureTemplate(ctx, admin, adminURL, o.migrations)
	if err != nil {
		tplMu.Unlock()
		t.Fatalf("testkit: %v", err)
	}
	name := fmt.Sprintf("test_%d_%d", os.Getpid(), dbSeq.Add(1))
	// Cloning is serialized: Postgres rejects concurrent use of a template.
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{tpl}.Sanitize())
	tplMu.Unlock()
	if err != nil {
		t.Fatalf("testkit: create database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatalf("testkit: parse %s: %v", EnvDatabaseURL, err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 4
	cfg.AfterConnect = pgxdb.ScanUTC
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testkit: open test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropDatabase(adminURL, name)
	})
	return pool
}

func ensureTemplate(ctx context.Context, admin *pgx.Conn, adminURL string, migrations fs.FS) (string, error) {
	key, err := hashFS(migrations)
	if err != nil {
		return "", err
	}
	if name, ok := templates[key]; ok {
		return name, nil
	}
	name := fmt.Sprintf("tpl_%d_%s", os.Getpid(), key[:12])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return "", fmt.Errorf("create template: %w", err)
	}
	templates[key] = name
	if migrations == nil {
		return name, nil
	}
	cfg, err := pgx.ParseConfig(adminURL)
	if err != nil {
		return "", err
	}
	cfg.Database = name
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()
	if _, err := migrate.Up(ctx, sqlDB, migrations); err != nil {
		return "", fmt.Errorf("migrate template: %w", err)
	}
	return name, nil
}

// hashFS fingerprints a migration set so each distinct set gets its own
// template.
func hashFS(fsys fs.FS) (string, error) {
	h := sha256.New()
	if fsys != nil {
		var paths []string
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				paths = append(paths, p)
			}
			return err
		})
		if err != nil {
			return "", fmt.Errorf("read migrations: %w", err)
		}
		sort.Strings(paths)
		for _, p := range paths {
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func dropDatabase(adminURL, name string) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return
	}
	defer admin.Close(ctx)
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
}

// Main runs the package's tests and then drops the template databases this
// process created. Call it from TestMain:
//
//	func TestMain(m *testing.M) { testkit.Main(m) }
func Main(m *testing.M) {
	code := m.Run()
	tplMu.Lock()
	if url := os.Getenv(EnvDatabaseURL); url != "" {
		for _, name := range templates {
			dropDatabase(url, name)
		}
	}
	tplMu.Unlock()
	os.Exit(code)
}
