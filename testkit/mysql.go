package testkit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AhmadRaza-Khan/semii/db/migrate"
	"github.com/AhmadRaza-Khan/semii/db/sqldb"
)

// MySQL returns a connection pool on a fresh, migrated database, dropped
// when the test ends. Each test migrates its own database (MySQL has no
// template databases), which is slower than Postgres. The "mysql" driver
// must be registered (generated apps import it).
func MySQL(t testing.TB, opts ...DBOption) *sql.DB {
	t.Helper()
	adminURL := serverURL(t, "mysql")
	var o dbOptions
	for _, opt := range opts {
		opt(&o)
	}
	ctx := context.Background()
	admin, err := sqldb.Open(ctx, sqldb.MySQL, adminURL, sqldb.Options{MaxOpenConns: 2})
	if err != nil {
		t.Fatalf("testkit: connect mysql: %v", err)
	}
	defer admin.Close()
	name := fmt.Sprintf("test_%d_%d", os.Getpid(), dbSeq.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatalf("testkit: create database: %v", err)
	}
	t.Cleanup(func() {
		if a, err := sqldb.Open(context.Background(), sqldb.MySQL, adminURL, sqldb.Options{MaxOpenConns: 1}); err == nil {
			_, _ = a.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+name+"`")
			a.Close()
		}
	})
	sdb, err := sqldb.Open(ctx, sqldb.MySQL, withDatabase(adminURL, name), sqldb.Options{MaxOpenConns: 4})
	if err != nil {
		t.Fatalf("testkit: open test database: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	if o.migrations != nil {
		if _, err := migrate.Up(ctx, sdb, o.migrations, migrate.WithDialect(migrate.MySQL)); err != nil {
			t.Fatalf("testkit: migrate: %v", err)
		}
	}
	return sdb
}

// withDatabase swaps the database of a mysql:// URL.
func withDatabase(url, name string) string {
	rest := strings.TrimPrefix(url, "mysql://")
	query := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, query = rest[:i], rest[i:]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return "mysql://" + rest + "/" + name + query
}

// SQLite returns a database file in the test's temporary directory with
// migrations applied, closed (and removed) when the test ends. A file, not
// a shared in-memory database, so concurrent writers wait on the busy
// timeout as in production. The "sqlite" driver (modernc.org/sqlite) must
// be registered; generated apps import it.
func SQLite(t testing.TB, opts ...DBOption) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("testkit: skipping database test in -short mode")
	}
	var o dbOptions
	for _, opt := range opts {
		opt(&o)
	}
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), fmt.Sprintf("test_%d.db", dbSeq.Add(1)))
	sdb, err := sqldb.Open(ctx, sqldb.SQLite, dsn, sqldb.Options{})
	if err != nil {
		t.Fatalf("testkit: open sqlite (is modernc.org/sqlite imported?): %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	if o.migrations != nil {
		if _, err := migrate.Up(ctx, sdb, o.migrations, migrate.WithDialect(migrate.SQLite)); err != nil {
			t.Fatalf("testkit: migrate: %v", err)
		}
	}
	return sdb
}
