package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AhmadRaza-Khan/semii/db/migrate"
	"github.com/AhmadRaza-Khan/semii/db/mongodb"
	"github.com/AhmadRaza-Khan/semii/internal/gen"
)

type checkResult struct {
	status string // PASS, WARN, FAIL
	name   string
	detail string
}

func (e *Env) paint(status string) string {
	if e.NoColor {
		return status
	}
	color := map[string]string{"PASS": "32", "WARN": "33", "FAIL": "31"}[status]
	return "\x1b[" + color + "m" + status + "\x1b[0m"
}

func runDoctor(e *Env, args []string) error {
	var results []checkResult
	add := func(status, name, detail string) {
		results = append(results, checkResult{status, name, detail})
		e.printf("%s  %-22s %s\n", e.paint(status), name, detail)
	}

	// Go toolchain.
	if out, err := exec.Command("go", "env", "GOVERSION").Output(); err != nil {
		add("FAIL", "go", "go not found on PATH"+installHint("go"))
	} else if v := strings.TrimSpace(string(out)); goAtLeast(v, 1, 24) {
		add("PASS", "go", v)
	} else {
		add("FAIL", "go", v+" (need go1.24 or newer)")
	}

	p, err := gen.FindProject(e.Dir)
	if err != nil {
		add("FAIL", ".semii.yaml", err.Error())
		return summarize(results)
	}
	add("PASS", ".semii.yaml", fmt.Sprintf("%s, db %s, template v%d", p.Module, p.DB, p.Template))

	if p.DB != "mongo" {
		if _, err := exec.LookPath("sqlc"); err != nil {
			add("FAIL", "sqlc", "not installed"+installHint("sqlc"))
		} else {
			add("PASS", "sqlc", "installed")
		}
	}

	// The app's own config loader reports missing or invalid variables.
	if out, err := e.goRun(p.Root, "doctor", doctorMain(p)); err != nil && out == nil {
		add("FAIL", "config", "could not build the config check: "+err.Error())
	} else {
		var res struct{ Problems []string }
		_ = json.Unmarshal(out, &res)
		if len(res.Problems) == 0 {
			add("PASS", "config", "all required variables set")
		} else {
			add("FAIL", "config", strings.Join(res.Problems, "; "))
		}
	}

	cfg, err := loadDBEnv(p.Root)
	if err != nil {
		add("FAIL", "database", "DATABASE_URL is not set")
		return summarize(results)
	}
	ctx := context.Background()
	if p.DB == "mongo" {
		client, _, err := mongodb.Open(ctx, cfg.DatabaseURL.Reveal(), "admin", mongodb.Options{Timeout: 5 * time.Second})
		if err != nil {
			add("FAIL", "database", err.Error())
			return summarize(results)
		}
		_ = client.Disconnect(ctx)
		add("PASS", "database", "reachable")
		add("PASS", "indexes", "ensured by `semii migrate up` (not checked)")
		return summarize(results)
	}
	db, err := openSQL(ctx, p, cfg.DatabaseURL.Reveal())
	if err != nil {
		add("FAIL", "database", err.Error())
		return summarize(results)
	}
	defer db.Close()
	add("PASS", "database", "reachable")

	n, err := migrate.Pending(ctx, db, os.DirFS(filepath.Join(p.Root, "db", "migrations")), dialect(p.DB))
	switch {
	case err != nil:
		add("FAIL", "migrations", err.Error())
	case n > 0:
		add("WARN", "migrations", fmt.Sprintf("%d pending; run semii migrate up", n))
	default:
		add("PASS", "migrations", "up to date")
	}
	return summarize(results)
}

func summarize(results []checkResult) error {
	for _, r := range results {
		if r.status == "FAIL" {
			return &exitError{ExitUser, fmt.Errorf("doctor found problems")}
		}
	}
	return nil
}

// goAtLeast parses "go1.24.3" style versions.
func goAtLeast(v string, major, minor int) bool {
	v = strings.TrimPrefix(v, "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	digits := parts[1]
	if i := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits = digits[:i] // "24rc1" -> "24"
	}
	mi, err2 := strconv.Atoi(digits)
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > major || ma == major && mi >= minor
}

func doctorMain(p gen.Project) string {
	return strings.NewReplacer("MODULE", p.Module, "SEMII", gen.SemiiModule).Replace(`package main

import (
	"encoding/json"
	"errors"
	"os"

	"MODULE/internal/config"
	semiiconfig "SEMII/config"
)

func main() {
	var out struct{ Problems []string }
	if _, err := config.Load(); err != nil {
		var ce *semiiconfig.Error
		if errors.As(err, &ce) {
			for _, p := range ce.Problems {
				out.Problems = append(out.Problems, p.Name+": "+p.Message)
			}
		} else {
			out.Problems = append(out.Problems, err.Error())
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
`)
}
