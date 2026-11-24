// Package sqldb opens database/sql pools for MySQL and SQLite, runs
// transactions and maps driver errors to the semii db sentinels.
//
// Register the driver in your program: _ "github.com/go-sql-driver/mysql"
// for MySQL, _ "modernc.org/sqlite" (pure Go, CGO_ENABLED=0) for SQLite.
package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/AhmadRaza-Khan/semii/db"
	"github.com/go-sql-driver/mysql"
)

// Drivers.
const (
	MySQL  = "mysql"
	SQLite = "sqlite"
)

// Options tune the pool. Zero values use the defaults below.
type Options struct {
	MaxOpenConns    int           // default 4 * GOMAXPROCS
	MaxIdleConns    int           // default MaxOpenConns
	ConnMaxLifetime time.Duration // default 1h
	ConnMaxIdleTime time.Duration // default 30m
	// NoPing skips the connectivity check Open performs by default.
	NoPing bool
}

// Open opens driver with dsn, applying semii's required settings:
//
//	mysql:  parseTime=true (DATETIME scans into time.Time), loc=UTC
//	sqlite: _pragma=foreign_keys(1), journal_mode(WAL), busy_timeout(5000)
//
// Errors never include the DSN, which usually carries a password.
func Open(ctx context.Context, driver, dsn string, o Options) (*sql.DB, error) {
	var err error
	switch driver {
	case MySQL:
		dsn, err = mysqlDSN(dsn)
	case SQLite:
		dsn = SQLiteDSN(dsn)
	default:
		return nil, fmt.Errorf("sqldb: unsupported driver %q (mysql, sqlite)", driver)
	}
	if err != nil {
		return nil, err
	}
	sdb, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqldb: open %s: %w", driver, err)
	}
	if o.MaxOpenConns <= 0 {
		o.MaxOpenConns = 4 * runtime.GOMAXPROCS(0)
	}
	if o.MaxIdleConns <= 0 {
		o.MaxIdleConns = o.MaxOpenConns
	}
	if o.ConnMaxLifetime <= 0 {
		o.ConnMaxLifetime = time.Hour
	}
	if o.ConnMaxIdleTime <= 0 {
		o.ConnMaxIdleTime = 30 * time.Minute
	}
	sdb.SetMaxOpenConns(o.MaxOpenConns)
	sdb.SetMaxIdleConns(o.MaxIdleConns)
	sdb.SetConnMaxLifetime(o.ConnMaxLifetime)
	sdb.SetConnMaxIdleTime(o.ConnMaxIdleTime)
	if !o.NoPing {
		if err := sdb.PingContext(ctx); err != nil {
			sdb.Close()
			return nil, fmt.Errorf("sqldb: ping %s: %w", driver, redact(err))
		}
	}
	return sdb, nil
}

func mysqlDSN(dsn string) (string, error) {
	// Accept URLs (mysql://user:pass@host:3306/db) as well as native DSNs.
	if strings.HasPrefix(dsn, "mysql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", errors.New("sqldb: invalid MySQL URL")
		}
		cfg := mysql.NewConfig()
		cfg.Net, cfg.Addr, cfg.DBName = "tcp", u.Host, strings.TrimPrefix(u.Path, "/")
		if u.User != nil {
			cfg.User = u.User.Username()
			cfg.Passwd, _ = u.User.Password()
		}
		for k, vs := range u.Query() {
			if cfg.Params == nil {
				cfg.Params = map[string]string{}
			}
			cfg.Params[k] = vs[0]
		}
		dsn = cfg.FormatDSN()
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", errors.New("sqldb: invalid MySQL DSN")
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	return cfg.FormatDSN(), nil
}

// SQLiteDSN adds the required pragmas to a SQLite path or file: URI.
// ":memory:" yields a private in-memory database per connection; use
// "file:name?mode=memory&cache=shared" to share one between connections.
func SQLiteDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	var add []string
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)"} {
		name := p[:strings.IndexByte(p, '(')]
		if !strings.Contains(dsn, "_pragma="+name) {
			add = append(add, "_pragma="+p)
		}
	}
	if !strings.Contains(dsn, "_time_format=") {
		add = append(add, "_time_format=sqlite")
	}
	if len(add) == 0 {
		return dsn
	}
	return dsn + sep + strings.Join(add, "&")
}

func redact(err error) error {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return fmt.Errorf("mysql error %d", me.Number)
	}
	return err
}

// WithTx runs fn in a transaction: it commits when fn returns nil and rolls
// back when fn returns an error or panics (the panic is re-raised).
func WithTx(ctx context.Context, sdb *sql.DB, fn func(*sql.Tx) error, opts *sql.TxOptions) error {
	tx, err := sdb.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("sqldb: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("sqldb: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqldb: commit: %w", err)
	}
	return nil
}

// SQLite extended result codes for constraint violations.
const (
	sqliteConstraintForeignKey = 787
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

// MapError translates driver errors into db sentinels, keeping the
// original in the chain:
//
//	sql.ErrNoRows                         -> db.ErrNotFound
//	MySQL 1062, SQLite UNIQUE/PRIMARY KEY -> db.ErrConflict
//	MySQL 1451/1452, SQLite FOREIGN KEY   -> db.ErrForeignKey
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", db.ErrNotFound, err)
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1062:
			return fmt.Errorf("%w: %w", db.ErrConflict, err)
		case 1451, 1452:
			return fmt.Errorf("%w: %w", db.ErrForeignKey, err)
		}
		return err
	}
	// modernc.org/sqlite errors expose Code() without sqldb importing the
	// driver.
	var se interface{ Code() int }
	if errors.As(err, &se) {
		switch se.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return fmt.Errorf("%w: %w", db.ErrConflict, err)
		case sqliteConstraintForeignKey:
			return fmt.Errorf("%w: %w", db.ErrForeignKey, err)
		}
	}
	return err
}

// JSON is a JSON document column for MySQL. It is sent as text (MySQL
// rejects JSON in binary strings) and scans from text or bytes; nil is
// NULL. Convert to and from json.RawMessage with a type conversion.
type JSON []byte

// Value implements driver.Valuer.
func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner.
func (j *JSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append(JSON(nil), v...)
	case string:
		*j = JSON(v)
	default:
		return fmt.Errorf("sqldb: cannot scan %T into JSON", src)
	}
	return nil
}
