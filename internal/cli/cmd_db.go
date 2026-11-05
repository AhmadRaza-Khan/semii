package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/AhmadRaza-Khan/semii/config"
	"github.com/AhmadRaza-Khan/semii/db/migrate"
	"github.com/AhmadRaza-Khan/semii/internal/gen"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// dbEnv is the slice of the project's configuration the CLI reads itself.
type dbEnv struct {
	Env         string        `env:"SEMII_ENV" default:"development"`
	DatabaseURL config.Secret `env:"DATABASE_URL" validate:"required"`
}

func loadDBEnv(root string) (dbEnv, error) {
	c, err := config.Load[dbEnv](config.WithDir(root))
	if err != nil {
		return dbEnv{}, userErr("%v", err)
	}
	return c, nil
}

func openSQL(ctx context.Context, url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, toolErr(errors.New("invalid DATABASE_URL"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, toolErr(fmt.Errorf("database unreachable: %v", err))
	}
	return db, nil
}

func runMigrate(e *Env, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	to := fs.String("to", "", "target version for up/down")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return userErr("usage: semii migrate up|down|status|redo|create <name> [--to=version]")
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	if pos[0] == "create" {
		if len(pos) != 2 {
			return userErr("usage: semii migrate create <name>")
		}
		return generateMigration(e, pos[1:])
	}
	if p.DB == "mongo" {
		return userErr("mongo index migrations arrive in milestone M5")
	}
	if e.DryRun {
		e.printf("migrate %s (dry run: nothing applied)\n", pos[0])
		return nil
	}
	cfg, err := loadDBEnv(p.Root)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := openSQL(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer db.Close()
	fsys := os.DirFS(filepath.Join(p.Root, "db", "migrations"))

	var version int64
	if *to != "" {
		if version, err = strconv.ParseInt(*to, 10, 64); err != nil {
			return userErr("--to must be a migration version, e.g. 20240101120000")
		}
	}
	printResults := func(verb string, rs []migrate.Result) {
		if len(rs) == 0 {
			e.printf("Nothing to %s.\n", verb)
		}
		for _, r := range rs {
			e.printf("  %-9s %s (%s)\n", verb, r.Name, r.Duration.Round(time.Millisecond))
		}
	}
	switch pos[0] {
	case "up":
		var rs []migrate.Result
		if *to != "" {
			rs, err = migrate.To(ctx, db, fsys, version)
		} else {
			rs, err = migrate.Up(ctx, db, fsys)
		}
		printResults("apply", rs)
	case "down":
		if *to != "" {
			var rs []migrate.Result
			rs, err = migrate.To(ctx, db, fsys, version)
			printResults("rollback", rs)
			break
		}
		var r *migrate.Result
		if r, err = migrate.Down(ctx, db, fsys); r != nil {
			printResults("rollback", []migrate.Result{*r})
		} else if err == nil {
			e.printf("Nothing to roll back.\n")
		}
	case "redo":
		var r *migrate.Result
		if r, err = migrate.Down(ctx, db, fsys); err == nil {
			if r == nil {
				return userErr("nothing to redo: no migration is applied")
			}
			printResults("rollback", []migrate.Result{*r})
			var rs []migrate.Result
			rs, err = migrate.To(ctx, db, fsys, r.Version)
			printResults("apply", rs)
		}
	case "status":
		var ss []migrate.Status
		if ss, err = migrate.List(ctx, db, fsys); err == nil {
			tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
			for _, s := range ss {
				state := "pending"
				if s.Applied {
					state = "applied " + s.AppliedAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(tw, "  %s\t%s\n", s.Name, state)
			}
			_ = tw.Flush()
		}
	default:
		return userErr("unknown migrate command %q (up, down, status, redo, create)", pos[0])
	}
	if err != nil {
		return toolErr(err)
	}
	return nil
}

func runDB(e *Env, args []string) error {
	fs := flag.NewFlagSet("db seed", flag.ContinueOnError)
	allowProd := fs.Bool("allow-production", false, "allow seeding when SEMII_ENV=production")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "seed" {
		return userErr("usage: semii db seed [--allow-production]")
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	cfg, err := loadDBEnv(p.Root)
	if err != nil {
		return err
	}
	if cfg.Env == "production" && !*allowProd {
		return userErr("refusing to seed a production database (pass --allow-production)")
	}
	if e.DryRun {
		e.printf("db seed (dry run: nothing run)\n")
		return nil
	}
	out, err := e.goRun(p.Root, "seed", seedMain(p))
	if err != nil {
		return err
	}
	e.printf("%s", out)
	return nil
}

func seedMain(p gen.Project) string {
	return strings.NewReplacer("MODULE", p.Module, "SEMII", gen.SemiiModule).Replace(`package main

import (
	"context"
	"fmt"
	"os"

	"MODULE/db/seeds"
	"MODULE/internal/config"
	"SEMII/db/pgxdb"
	"SEMII/logx"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxdb.Open(ctx, cfg.DatabaseURL.Reveal(), pgxdb.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := seeds.Seed(ctx, seeds.Deps{Pool: pool, Log: logx.New(cfg.Env, cfg.LogLevel)}); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Println("Seed complete.")
}
`)
}
