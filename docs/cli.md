# CLI and generators

```
semii <command> [flags]
```

Global flags, accepted anywhere: `--dry-run` (print what would change),
`--force` (overwrite existing files), `--verbose`, `--no-color`.

Exit codes: `0` success, `1` user error (bad arguments, existing files,
not a project), `2` an external tool failed (`go`, `sqlc`, a database),
`3` internal error.

Generators never overwrite a file without `--force`; a conflict lists
every file and exits 1 before writing anything. Edits to existing files
only happen between marker comments such as `// semii:routes:start` and
`// semii:routes:end`; when markers are missing the snippet to add is
printed instead. Re-running a generator does not duplicate marker lines.

## semii new &lt;name&gt;

| Flag | Default | |
|------|---------|--|
| `--module` | `<name>` | Go module path |
| `--db` | `postgres` | `postgres`, `mysql`, `sqlite` or `mongo` |
| `--auth` | `jwt` | comma list of `session`, `jwt`, `apikey`, or `none` |
| `--id` | `uuid` | `uuid` (v7) or `bigint`; Mongo always uses ObjectIDs |
| `--deploy` | | `ssh` adds `.github/workflows/deploy.yml` |
| `--no-docker` | | skip `Dockerfile` and `docker-compose.yml` |
| `--no-ci` | | skip `.github/workflows/ci.yml` |
| `--no-git` | | skip `git init` |

`apikey` needs `session` or `jwt` too, so users can sign in to manage
their keys.

## semii generate resource &lt;Name&gt; [field...]

Alias `semii g resource`. Name is singular (`Order`, `OrderItem`). Tables
are snake plural (`order_items`), routes kebab plural (`/order-items`),
packages lowercase (`internal/orderitem`).

Fields are `name:type[:modifier...]`:

| Type | Postgres | MySQL | SQLite | Go |
|------|----------|-------|--------|----|
| `string` | `VARCHAR(255)` | `VARCHAR(255)` | `VARCHAR(255)` | `string` |
| `text` | `TEXT` | `TEXT` | `TEXT` | `string` |
| `int` | `INTEGER` | `INT` | `INT` | `int32` |
| `int64` | `BIGINT` | `BIGINT` | `BIGINT` | `int64` |
| `bool` | `BOOLEAN` | `BOOLEAN` | `BOOLEAN` | `bool` |
| `float` | `DOUBLE PRECISION` | `DOUBLE` | `DOUBLE` | `float64` |
| `decimal` | `NUMERIC(18,4)` | `DECIMAL(18,4)` | `DECIMAL_TEXT` | `decimal.Decimal` |
| `time` | `TIMESTAMPTZ` | `DATETIME(6)` | `DATETIME` | `time.Time` |
| `date` | `DATE` | `DATE` | `DATE` | `web.Date` (`"2006-01-02"`) |
| `uuid` | `UUID` | `CHAR(36)` | `UUID` | `uuid.UUID` |
| `json` | `JSONB` | `JSON` | `JSON` | `json.RawMessage` |
| `ref:R` | FK to R, indexed | FK to R | FK to R | R's id type |

MongoDB stores the same Go types (decimals as Decimal128, UUIDs and JSON
as strings).

Modifiers: `null` (nullable; pointer in Go, omitted from JSON when nil;
PATCH accepts `null` to clear it), `unique`, `index`, `default=X`,
`min=N`, `max=N` (length for strings, value for numbers). Unique, indexed
and ref fields become list filters (`GET /orders?customer_id=…`).

Every resource gets `id`, `created_at` and `updated_at`.

| Flag | |
|------|--|
| `--soft-delete` | add `deleted_at`; deletes set it and lists hide the row |
| `--no-auth` | make the routes `Public()` in an auth project |
| `--only` | limit output: `model,repo,service,handler,migration,tests` |

Endpoints: `GET /orders` (cursor pagination: `?limit=&cursor=`, newest
first), `POST /orders` (201 + `Location`), `GET /orders/{id}`,
`PATCH /orders/{id}` (absent fields unchanged), `DELETE /orders/{id}`
(204). Errors are `application/problem+json`; unique and foreign-key
violations are 409.

Afterwards the CLI runs `sqlc generate`, `go mod tidy` and `go build ./...`.

## semii generate auth [--strategy s,...]

Adds account flows for the strategies (default `jwt`) and records them in
`.semii.yaml`. Run it again with `--strategy` to add more; each strategy
lives in its own files. See [auth](packages/auth.md) for the endpoints.

## semii generate migration &lt;name&gt;

An empty goose migration `db/migrations/<timestamp>_<name>.sql` (SQL
databases).

## semii generate middleware &lt;name&gt;

`internal/middleware/<name>.go` with a `func Name(next http.Handler)
http.Handler` skeleton and a test.

## semii dev

Builds `./cmd/api`, runs it and rebuilds on changes to Go files, queries
or `.env` (200 ms debounce). Query changes run `sqlc generate` first. A
failed build keeps the previous version running. The app runs in its own
process group and stops with the watcher.

## semii routes [--json]

Lists every route with its method, path, policy and handler. Exits 1 if a
route has no policy.

## semii migrate up|down|status|redo|create &lt;name&gt; [--to=version]

goose, used as a library, with `DATABASE_URL` from the environment or
`.env`. `down` rolls back one migration. On MongoDB `up` ensures the
indexes in `db/mongo`.

## semii db seed [--allow-production]

Runs `db/seeds.Seed`. With auth, setting `SEED_ADMIN_EMAIL` and
`SEED_ADMIN_PASSWORD` creates an admin account. Refuses production unless
`--allow-production`.

## semii doctor

Checks the Go version, sqlc, `.semii.yaml`, the app's configuration,
database connectivity and pending migrations. Prints PASS/WARN/FAIL; exits
1 on any FAIL.

## semii version

The CLI and Go versions, and the project's semii and template versions.
