// Package migrate applies SQL migrations with pressly/goose, used as a
// library. Migration files use goose's "-- +goose Up" / "-- +goose Down"
// sections and timestamped versions.
//
// fsys must hold the .sql files at its root; with an embedded directory use
// fs.Sub(files, "migrations").
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Dialect selects the SQL dialect.
type Dialect string

// Supported dialects.
const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
	SQLite   Dialect = "sqlite3"
)

// Option configures a migration run.
type Option func(*options)

type options struct {
	dialect Dialect
}

// WithDialect sets the dialect (default Postgres). Postgres runs take a
// session advisory lock so concurrent runners wait for each other.
func WithDialect(d Dialect) Option { return func(o *options) { o.dialect = d } }

// Result describes one migration applied or rolled back.
type Result struct {
	Version  int64
	Name     string
	Duration time.Duration
}

// Status describes one migration.
type Status struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time
}

func provider(db *sql.DB, fsys fs.FS, opts []Option) (*goose.Provider, error) {
	o := options{dialect: Postgres}
	for _, opt := range opts {
		opt(&o)
	}
	var popts []goose.ProviderOption
	if o.dialect == Postgres {
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
		popts = append(popts, goose.WithSessionLocker(locker))
	}
	p, err := goose.NewProvider(goose.Dialect(o.dialect), db, fsys, popts...)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return p, nil
}

func results(rs []*goose.MigrationResult) []Result {
	out := make([]Result, 0, len(rs))
	for _, r := range rs {
		out = append(out, Result{Version: r.Source.Version, Name: r.Source.Path, Duration: r.Duration})
	}
	return out
}

// Up applies all pending migrations.
func Up(ctx context.Context, db *sql.DB, fsys fs.FS, opts ...Option) ([]Result, error) {
	p, err := provider(db, fsys, opts)
	if err != nil {
		return nil, err
	}
	rs, err := p.Up(ctx)
	if err != nil {
		return results(rs), fmt.Errorf("migrate up: %w", err)
	}
	return results(rs), nil
}

// Down rolls back the most recently applied migration. It returns a nil
// Result when nothing is applied.
func Down(ctx context.Context, db *sql.DB, fsys fs.FS, opts ...Option) (*Result, error) {
	p, err := provider(db, fsys, opts)
	if err != nil {
		return nil, err
	}
	r, err := p.Down(ctx)
	if errors.Is(err, goose.ErrNoNextVersion) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrate down: %w", err)
	}
	return &results([]*goose.MigrationResult{r})[0], nil
}

// To migrates up or down until version is the latest applied migration.
// Version 0 rolls everything back.
func To(ctx context.Context, db *sql.DB, fsys fs.FS, version int64, opts ...Option) ([]Result, error) {
	p, err := provider(db, fsys, opts)
	if err != nil {
		return nil, err
	}
	current, err := p.GetDBVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: current version: %w", err)
	}
	var rs []*goose.MigrationResult
	if version >= current {
		rs, err = p.UpTo(ctx, version)
	} else {
		rs, err = p.DownTo(ctx, version)
	}
	if err != nil {
		return results(rs), fmt.Errorf("migrate to %d: %w", version, err)
	}
	return results(rs), nil
}

// List reports every migration in fsys and whether it is applied.
func List(ctx context.Context, db *sql.DB, fsys fs.FS, opts ...Option) ([]Status, error) {
	p, err := provider(db, fsys, opts)
	if err != nil {
		return nil, err
	}
	ss, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate status: %w", err)
	}
	out := make([]Status, 0, len(ss))
	for _, s := range ss {
		out = append(out, Status{
			Version: s.Source.Version, Name: s.Source.Path,
			Applied: s.State == goose.StateApplied, AppliedAt: s.AppliedAt,
		})
	}
	return out, nil
}

// Pending returns how many migrations are not yet applied.
func Pending(ctx context.Context, db *sql.DB, fsys fs.FS, opts ...Option) (int, error) {
	ss, err := List(ctx, db, fsys, opts...)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range ss {
		if !s.Applied {
			n++
		}
	}
	return n, nil
}
