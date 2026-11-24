package sqldb_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AhmadRaza-Khan/semii/db"
	"github.com/AhmadRaza-Khan/semii/db/sqldb"
	"github.com/AhmadRaza-Khan/semii/testkit"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) { testkit.Main(m) }

const schema = `CREATE TABLE p (id INT PRIMARY KEY, name VARCHAR(20) UNIQUE);
CREATE TABLE c (id INT PRIMARY KEY, p_id INT, FOREIGN KEY (p_id) REFERENCES p (id))`

func exercise(t *testing.T, sdb *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range strings.Split(schema, ";") {
		if _, err := sdb.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (n int) {
		_ = sdb.QueryRowContext(ctx, "SELECT count(*) FROM p").Scan(&n)
		return n
	}
	if err := sqldb.WithTx(ctx, sdb, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO p VALUES (1, 'a')")
		return err
	}, nil); err != nil || count() != 1 {
		t.Fatalf("commit: %v", err)
	}
	boom := errors.New("boom")
	if err := sqldb.WithTx(ctx, sdb, func(tx *sql.Tx) error {
		_, _ = tx.ExecContext(ctx, "INSERT INTO p VALUES (2, 'b')")
		return boom
	}, nil); !errors.Is(err, boom) || count() != 1 {
		t.Fatalf("rollback: %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		_ = sqldb.WithTx(ctx, sdb, func(tx *sql.Tx) error {
			_, _ = tx.ExecContext(ctx, "INSERT INTO p VALUES (3, 'c')")
			panic("kaboom")
		}, nil)
	}()
	if count() != 1 {
		t.Error("panic did not roll back")
	}

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"no rows", func() error { var n int; return sdb.QueryRowContext(ctx, "SELECT id FROM p WHERE id = 99").Scan(&n) }, db.ErrNotFound},
		{"unique", func() error { _, err := sdb.ExecContext(ctx, "INSERT INTO p VALUES (5, 'a')"); return err }, db.ErrConflict},
		{"primary key", func() error { _, err := sdb.ExecContext(ctx, "INSERT INTO p VALUES (1, 'z')"); return err }, db.ErrConflict},
		{"fk insert", func() error { _, err := sdb.ExecContext(ctx, "INSERT INTO c VALUES (1, 42)"); return err }, db.ErrForeignKey},
	}
	for _, c := range cases {
		raw := c.run()
		if err := sqldb.MapError(fmt.Errorf("repo: %w", raw)); !errors.Is(err, c.want) || !errors.Is(err, raw) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	_, _ = sdb.ExecContext(ctx, "INSERT INTO c VALUES (1, 1)")
	_, err := sdb.ExecContext(ctx, "DELETE FROM p WHERE id = 1")
	if !errors.Is(sqldb.MapError(err), db.ErrForeignKey) {
		t.Errorf("fk delete: %v", err)
	}
	other := errors.New("other")
	if sqldb.MapError(other) != other || sqldb.MapError(nil) != nil {
		t.Error("passthrough")
	}
}

func TestSQLite(t *testing.T) {
	exercise(t, testkit.SQLite(t))
}

func TestMySQL(t *testing.T) {
	sdb := testkit.MySQL(t)
	exercise(t, sdb)
	if _, err := sdb.ExecContext(context.Background(), "SELEC"); sqldb.MapError(err) != err {
		t.Error("syntax error should pass through")
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := sqldb.Open(ctx, "oracle", "x", sqldb.Options{}); err == nil {
		t.Error("unknown driver accepted")
	}
	if _, err := sqldb.Open(ctx, sqldb.MySQL, "::bad::", sqldb.Options{}); err == nil {
		t.Error("bad DSN accepted")
	}
	_, err := sqldb.Open(ctx, sqldb.MySQL, "mysql://u:secretpw@127.0.0.1:1/x?timeout=1s", sqldb.Options{})
	if err == nil || strings.Contains(err.Error(), "secretpw") {
		t.Errorf("unreachable: %v", err)
	}
	if _, err := sqldb.Open(ctx, sqldb.MySQL, "mysql://%zz", sqldb.Options{}); err == nil {
		t.Error("bad URL accepted")
	}
	sdb, err := sqldb.Open(ctx, sqldb.MySQL, "mysql://u:p@127.0.0.1:1/x?charset=utf8mb4", sqldb.Options{NoPing: true, MaxOpenConns: 3})
	if err != nil || sdb.Stats().MaxOpenConnections != 3 {
		t.Fatalf("%v", err)
	}
	sdb.Close()
}

func TestSQLiteDSN(t *testing.T) {
	got := sqldb.SQLiteDSN("app.db")
	for _, want := range []string{"app.db?", "_pragma=foreign_keys(1)", "_pragma=journal_mode(WAL)", "_pragma=busy_timeout(5000)", "_time_format=sqlite"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing %s", got, want)
		}
	}
	if again := sqldb.SQLiteDSN(got); again != got {
		t.Errorf("not idempotent: %s", again)
	}
	if d := sqldb.SQLiteDSN("file:x?mode=memory"); !strings.HasPrefix(d, "file:x?mode=memory&_pragma") {
		t.Error(d)
	}
}

func TestJSON(t *testing.T) {
	if v, _ := sqldb.JSON(`{"a":1}`).Value(); v != `{"a":1}` {
		t.Errorf("%v", v)
	}
	if v, _ := sqldb.JSON(nil).Value(); v != nil {
		t.Errorf("nil: %v", v)
	}
	var j sqldb.JSON
	for _, src := range []any{[]byte(`{"b":2}`), `{"b":2}`} {
		if err := j.Scan(src); err != nil || string(j) != `{"b":2}` {
			t.Errorf("%T: %s %v", src, j, err)
		}
	}
	if err := j.Scan(nil); err != nil || j != nil {
		t.Error("nil scan")
	}
	if err := j.Scan(5); err == nil {
		t.Error("int scanned")
	}
}
