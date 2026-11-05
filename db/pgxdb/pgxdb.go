// Package pgxdb opens pgx connection pools, runs transactions and maps
// Postgres errors to the semii db sentinels.
package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/AhmadRaza-Khan/semii/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options tune the pool. Zero values use the defaults below.
type Options struct {
	MaxConns        int32         // default 4 * GOMAXPROCS
	MinConns        int32         // default 0
	MaxConnLifetime time.Duration // default 1h
	MaxConnIdleTime time.Duration // default 30m
	// NoPing skips the connectivity check Open performs by default.
	NoPing bool
}

// Open parses url, creates a pool and, unless o.NoPing, pings the server.
// Errors never include the URL, which usually carries a password.
func Open(ctx context.Context, url string, o Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("pgxdb: invalid database URL")
	}
	cfg.MaxConns = o.MaxConns
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = int32(4 * runtime.GOMAXPROCS(0))
	}
	cfg.MinConns = o.MinConns
	cfg.MaxConnLifetime = o.MaxConnLifetime
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = time.Hour
	}
	cfg.MaxConnIdleTime = o.MaxConnIdleTime
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 30 * time.Minute
	}
	cfg.AfterConnect = ScanUTC
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxdb: open: %w", err)
	}
	if !o.NoPing {
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			return nil, fmt.Errorf("pgxdb: ping %s:%d/%s: %w",
				cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Database, err)
		}
	}
	return pool, nil
}

// ScanUTC makes conn return timestamptz values in UTC instead of the
// process's local time zone. Open installs it as the pool's AfterConnect
// hook; call it from your own hook if you replace that.
func ScanUTC(_ context.Context, conn *pgx.Conn) error {
	conn.TypeMap().RegisterType(&pgtype.Type{
		Name: "timestamptz", OID: pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	})
	return nil
}

// WithTx runs fn in a transaction: it commits when fn returns nil and rolls
// back when fn returns an error or panics (the panic is re-raised).
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error, opts ...pgx.TxOptions) (err error) {
	var txo pgx.TxOptions
	if len(opts) > 0 {
		txo = opts[0]
	}
	tx, err := pool.BeginTx(ctx, txo)
	if err != nil {
		return fmt.Errorf("pgxdb: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("pgxdb: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgxdb: commit: %w", err)
	}
	return nil
}

// Postgres SQLSTATE codes mapped by MapError.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
)

// MapError translates pgx errors into db sentinels while keeping the
// original error in the chain:
//
//	pgx.ErrNoRows        -> db.ErrNotFound
//	unique violation     -> db.ErrConflict
//	foreign key violation -> db.ErrForeignKey
//
// Other errors are returned unchanged.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", db.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case codeUniqueViolation:
			return fmt.Errorf("%w: %w", db.ErrConflict, err)
		case codeForeignKeyViolation:
			return fmt.Errorf("%w: %w", db.ErrForeignKey, err)
		}
	}
	return err
}
