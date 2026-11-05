package pgxdb_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/semii/semii/db"
	"github.com/semii/semii/db/pgxdb"
	"github.com/semii/semii/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestOpen(t *testing.T) {
	ctx := context.Background()
	if _, err := pgxdb.Open(ctx, "://bad", pgxdb.Options{}); err == nil || strings.Contains(err.Error(), "bad") {
		t.Errorf("invalid URL: %v", err)
	}
	_, err := pgxdb.Open(ctx, "postgres://u:secretpw@127.0.0.1:1/x?connect_timeout=1", pgxdb.Options{})
	if err == nil || strings.Contains(err.Error(), "secretpw") {
		t.Errorf("unreachable: %v", err)
	}
	url := os.Getenv(testkit.EnvDatabaseURL)
	if url == "" {
		t.Skip("no database")
	}
	pool, err := pgxdb.Open(ctx, url, pgxdb.Options{MaxConns: 2, NoPing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Config().MaxConns != 2 {
		t.Errorf("MaxConns %d", pool.Config().MaxConns)
	}
}

func TestWithTxAndMapError(t *testing.T) {
	pool := testkit.Postgres(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE p (id INT PRIMARY KEY); CREATE TABLE c (id INT PRIMARY KEY, p_id INT REFERENCES p (id))`)
	if err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM p").Scan(&n)
		return n
	}

	if err := pgxdb.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO p VALUES (1)")
		return err
	}); err != nil || count() != 1 {
		t.Fatalf("commit: %v (%d rows)", err, count())
	}

	boom := errors.New("boom")
	if err := pgxdb.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, "INSERT INTO p VALUES (2)")
		return boom
	}); !errors.Is(err, boom) || count() != 1 {
		t.Fatalf("rollback: %v (%d rows)", err, count())
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic not re-raised")
			}
		}()
		_ = pgxdb.WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, "INSERT INTO p VALUES (3)")
			panic("kaboom")
		}, pgx.TxOptions{IsoLevel: pgx.Serializable})
	}()
	if count() != 1 {
		t.Errorf("panic did not roll back (%d rows)", count())
	}

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"no rows", func() error { var n int; return pool.QueryRow(ctx, "SELECT id FROM p WHERE id = 99").Scan(&n) }, db.ErrNotFound},
		{"unique", func() error { _, err := pool.Exec(ctx, "INSERT INTO p VALUES (1)"); return err }, db.ErrConflict},
		{"fk", func() error { _, err := pool.Exec(ctx, "INSERT INTO c VALUES (1, 42)"); return err }, db.ErrForeignKey},
	}
	for _, c := range cases {
		raw := c.run()
		err := pgxdb.MapError(fmt.Errorf("repo: %w", raw))
		if !errors.Is(err, c.want) || !errors.Is(err, raw) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	other := errors.New("other")
	if pgxdb.MapError(other) != other || pgxdb.MapError(nil) != nil {
		t.Error("unmapped errors must pass through")
	}
	if _, err := pool.Exec(ctx, "SELEC"); errors.Is(pgxdb.MapError(err), db.ErrConflict) {
		t.Error("syntax error mapped")
	}
}
