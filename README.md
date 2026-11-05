# semii

Rails-speed scaffolding for Go backend APIs. Conventions are delivered
through code generation, never runtime magic. The spec lives in
`semii-v1-spec/`.

## Status

| Milestone | Scope | State |
|-----------|-------|-------|
| M1 | web core: router, groups, error handlers, problem+json, JSON, middleware, pagination | done |
| M2 | config, server, logx | done |
| M3 | generator engine + Postgres (`semii new`, `generate resource`, …) | done |
| M4 | auth | — |
| M5 | testkit + MySQL/SQLite/Mongo | — |
| M6 | ops, docs, examples | — |

## Quick start (from a local checkout)

semii is not published yet, so generated projects point at this checkout
with a `replace` directive. sqlc must be on PATH.

```sh
go install ./cmd/semii
export SEMII_LOCAL_PATH=$PWD
cd /tmp && semii new shop --auth none
cd shop && cp .env.example .env && docker compose up -d db
semii migrate up
semii g resource Customer name:string email:string:unique
semii g resource Order customer:ref:Customer total:decimal:min=0 notes:text:null --soft-delete
semii routes
semii dev
```

## CLI (M3)

`new`, `generate resource|migration|middleware`, `dev`, `routes`,
`migrate up|down|status|redo|create`, `db seed`, `doctor`, `version`, with
the global `--dry-run --force --verbose --no-color` and exit codes 0/1/2/3.
`generate auth` and `--auth`/`--db` values other than `none`/`postgres`
report the milestone that adds them (M4, M5).

## Packages so far

- `web` — router over `http.ServeMux`, `HandlerFunc` returning `error`,
  RFC 9457 problems, `Decode`/`DecodeQuery`/`JSON`, cursor and offset
  pagination, `Optional[T]`, and the built-in middleware (RequestID, RealIP,
  Logger, Recover, SecurityHeaders, CORS, BodyLimit, Timeout, Gzip).
- `validate` — wrapper over go-playground/validator with JSON-named field
  errors, default messages, `msg:"..."` overrides and the `notblank`,
  `decimal_gte`/`decimal_lte` and `password` rules (needed by `web.Decode`).
- `config` — `Load[T]` from default tags, `.env`, `.env.<SEMII_ENV>`,
  `.env.local` and the process environment, reporting every problem in one
  error; `config.Secret` never prints its value.
- `server` — `Run` with production timeouts, `/healthz` and `/readyz`
  in front of the app, and drain → shutdown → close-in-reverse on
  SIGINT/SIGTERM (second signal exits immediately).
- `logx` — `New(env, level)` (colored text in development, JSON
  otherwise) and `FromContext`, which returns the request-scoped logger
  stored by `web.Logger`.
- `db` — driver-independent sentinels `ErrNotFound`, `ErrConflict` and
  `ErrForeignKey`, which the router maps to 404/409/409.
- `db/pgxdb` — `Open` (pool defaults, ping, UTC timestamps), `WithTx`,
  `MapError`.
- `db/migrate` — goose as a library: `Up`, `Down`, `To`, `List`, `Pending`,
  with a Postgres advisory lock.
- `testkit` — in-process HTTP client with `Expect*` assertions and a cookie
  jar; `Postgres(t)` clones a migrated template database per test.
  (Pulled forward from M5 because generated tests need it; MySQL, SQLite,
  Mongo and testcontainers remain M5.)
- `web.Date` — date-only JSON (`"2006-01-02"`) for `date` fields.

## Design decisions (not spelled out in the spec)

- **`web.Policy` is an interface** (`Authorize(*http.Request) error`), not
  `auth.Policy`. The spec has web reference `auth.Policy` while auth depends
  on web, which would be an import cycle and would break P5 (web usable on
  its own). In M4 `auth.Policy` must implement `web.Policy`; a policy that
  implements `fmt.Stringer` is listed by that name in `semii routes`.
- **Auth sentinels live in web** (`web.ErrUnauthenticated`, `ErrForbidden`,
  `ErrInvalidCredentials`) so `NewRouter` can map them. M4 re-exports them
  as `auth.Err*`.
- **`"GET /"` inside a prefixed group is the group path itself**
  (`Group("/orders")` + `"GET /"` gives `GET /orders`), not a ServeMux
  subtree. Use `/{$}` or a trailing slash if you want the ServeMux meanings.
- **`web.Error` has a `Header` field** for extra response headers
  (`Retry-After` now, `WWW-Authenticate` in M4).
- **Raising the body limit on one route:** an inner `BodyLimit` replaces an
  outer one, e.g. `r.Group("/uploads", web.BodyLimit(10<<20))`.
- **`CORS` panics on invalid config** (`"*"` with credentials), because its
  signature has no error return. Call `CORSConfig.Validate()` at config load
  (M2) to report the error cleanly.
- **web imports logx** so `web.Logger` can store the request logger for
  `logx.FromContext`. logx is stdlib-only, so web's dependency footprint is
  unchanged; logx does not import web.
- **`web.CORSConfig` carries `env` tags** so generated config can embed it
  under `envPrefix:"CORS_"`.
- **Config details:** empty values count as unset (the default applies);
  `WithPrefix` skips names starting with `SEMII_`; `SEMII_ENV` may come
  from `.env` itself when the process environment does not set it.
- **`/readyz` reports `ok`, `timeout` or `error` per check**; error
  details go to the log only, since the endpoint is public.
- **Reflection** is used in `DecodeQuery` (query binding) and once at
  registration time to name handlers for `Routes()`, plus in the config
  loader and validate as the spec allows.

### M3

- **Generated layout additions:** `app.Build(cfg, log, pool)` (used by
  tests) and `app.Routes()` (used by `semii routes`, no database needed);
  `internal/app/apptest.New(t)` replaces the spec's
  `internal/app/testapp_test.go`, because `_test.go` helpers cannot be
  imported by the per-resource test packages.
- **Routes are kebab-case** (`OrderItem` → `/order-items`); tables stay snake
  plural (`order_items`). The spec only shows a one-word example.
- **Without auth, generated resources are `Public()`** with a comment; with
  auth configured they will use `Require(auth.Authenticated())` (M4).
- **`migrate` returns results:** `Up` returns `([]Result, error)` and the
  status call is `List` (a `Status` type exists), so the CLI can print what
  ran.
- **`pgxdb.Options.NoPing`** replaces `PingOnOpen` (whose default `true`
  can't be a Go zero value).
- **`semii routes`, `db seed` and part of `doctor`** compile a throwaway
  `main` under `.semii/tmp/` with `go run`, so they use the project's own
  code without any runtime loading.
- **`semii dev`** sets `HTTP_DRAIN_DELAY=-1s` (no drain wait between reloads)
  and runs the app in its own process group with a parent-death signal on
  Linux, so Ctrl+C stops it once and a killed watcher never orphans it.
- **Dockerfile, CI workflow and deploy files** are M6; `semii new` writes
  `docker-compose.yml` (database only) now and accepts `--no-ci`.

## Development

```sh
go test -race -cover ./...
go test -run x -bench . ./web        # router overhead vs plain ServeMux

# Integration tests need a Postgres server:
export TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
# Full CLI workflow (new → generate → sqlc → vet → staticcheck → tests), needs sqlc:
SEMII_E2E=1 go test -run EndToEnd -v ./internal/cli
```
