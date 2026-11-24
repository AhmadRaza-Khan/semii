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

	"github.com/AhmadRaza-Khan/semii/db/migrate"
	"github.com/AhmadRaza-Khan/semii/db/pgxdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// DBOption configures a test database.
type DBOption func(*dbOptions)

type dbOptions struct {
	migrations fs.FS
	mongoSetup func(ctx context.Context, d *mongo.Database) error
}

// WithMigrations applies the SQL migrations in fsys (files at its root).
func WithMigrations(fsys fs.FS) DBOption { return func(o *dbOptions) { o.migrations = fsys } }

// WithMongoSetup runs fn on each new Mongo test database, typically to
// ensure indexes.
func WithMongoSetup(fn func(ctx context.Context, d *mongo.Database) error) DBOption {
	return func(o *dbOptions) { o.mongoSetup = fn }
}

// Process-wide registry of template databases. Templates are expensive to
// build, so each test process migrates one per migration set and clones it
// per test; Main drops them on exit. This is the only shared state in
// testkit, and it is test-only.
var (
	tplMu     sync.Mutex
	templates = map[string]template{} // migration hash + server -> template
	dbSeq     atomic.Int64
)

// Postgres returns a pool on a fresh database cloned from a migrated
// template (CREATE DATABASE ... TEMPLATE), dropped when the test ends.
// Tests are skipped in -short mode or when no server is available (see
// EnvDatabaseURL).
func Postgres(t testing.TB, opts ...DBOption) *pgxpool.Pool {
	t.Helper()
	adminURL := serverURL(t, "postgres")
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

type template struct{ adminURL, name string }

func ensureTemplate(ctx context.Context, admin *pgx.Conn, adminURL string, migrations fs.FS) (string, error) {
	hash, err := hashFS(migrations)
	if err != nil {
		return "", err
	}
	key := hash + "\x00" + adminURL
	if t, ok := templates[key]; ok {
		return t.name, nil
	}
	name := fmt.Sprintf("tpl_%d_%s", os.Getpid(), hash[:12])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return "", fmt.Errorf("create template: %w", err)
	}
	templates[key] = template{adminURL, name}
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

// Main runs the package's tests, then drops the template databases and
// stops the containers this process created. Call it from TestMain:
//
//	func TestMain(m *testing.M) { testkit.Main(m) }
func Main(m *testing.M) {
	code := m.Run()
	tplMu.Lock()
	for _, t := range templates {
		dropDatabase(t.adminURL, t.name)
	}
	clear(templates)
	tplMu.Unlock()
	stopServers()
	os.Exit(code)
}
