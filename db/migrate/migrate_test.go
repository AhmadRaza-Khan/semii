package migrate_test

import (
	"context"
	"os"
	"testing"

	"github.com/AhmadRaza-Khan/semii/db/migrate"
	"github.com/AhmadRaza-Khan/semii/testkit"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestUpDownToList(t *testing.T) {
	pool := testkit.Postgres(t) // empty database
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	ctx := context.Background()
	fsys := os.DirFS("testdata")

	rs, err := migrate.Up(ctx, sqlDB, fsys)
	if err != nil || len(rs) != 2 || rs[1].Version != 20240101000001 {
		t.Fatalf("up: %+v %v", rs, err)
	}
	if n, err := migrate.Pending(ctx, sqlDB, fsys); err != nil || n != 0 {
		t.Fatalf("pending %d %v", n, err)
	}
	r, err := migrate.Down(ctx, sqlDB, fsys)
	if err != nil || r == nil || r.Version != 20240101000001 {
		t.Fatalf("down: %+v %v", r, err)
	}
	ss, err := migrate.List(ctx, sqlDB, fsys)
	if err != nil || len(ss) != 2 || !ss[0].Applied || ss[1].Applied || ss[0].AppliedAt.IsZero() {
		t.Fatalf("list: %+v %v", ss, err)
	}
	if rs, err := migrate.To(ctx, sqlDB, fsys, 20240101000001); err != nil || len(rs) != 1 {
		t.Fatalf("to up: %+v %v", rs, err)
	}
	if rs, err := migrate.To(ctx, sqlDB, fsys, 0); err != nil || len(rs) != 2 {
		t.Fatalf("to 0: %+v %v", rs, err)
	}
	if r, err := migrate.Down(ctx, sqlDB, fsys); err != nil || r != nil {
		t.Fatalf("down with nothing applied: %+v %v", r, err)
	}
	if _, err := migrate.Up(ctx, sqlDB, os.DirFS("missing")); err == nil {
		t.Error("want error for missing dir")
	}
}

func TestDialectOptionAndErrors(t *testing.T) {
	pool := testkit.Postgres(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	ctx := context.Background()
	missing := os.DirFS("missing")
	if _, err := migrate.Down(ctx, sqlDB, missing); err == nil {
		t.Error("down: want error for missing dir")
	}
	if _, err := migrate.To(ctx, sqlDB, missing, 1); err == nil {
		t.Error("to: want error for missing dir")
	}
	if _, err := migrate.List(ctx, sqlDB, missing); err == nil {
		t.Error("list: want error for missing dir")
	}
	if _, err := migrate.Pending(ctx, sqlDB, missing); err == nil {
		t.Error("pending: want error for missing dir")
	}
	// Without the Postgres session lock (any non-postgres dialect skips it),
	// against a closed DB every call fails cleanly.
	closed := stdlib.OpenDBFromPool(pool)
	closed.Close()
	if _, err := migrate.Up(ctx, closed, os.DirFS("testdata"), migrate.WithDialect(migrate.SQLite)); err == nil {
		t.Error("up on closed db succeeded")
	}
	if _, err := migrate.To(ctx, closed, os.DirFS("testdata"), 1, migrate.WithDialect(migrate.SQLite)); err == nil {
		t.Error("to on closed db succeeded")
	}
}
