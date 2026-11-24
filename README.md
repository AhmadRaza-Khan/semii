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
| M4 | auth | done |
| M5 | testkit + MySQL/SQLite/Mongo | done |
| M6 | ops, docs, examples | — |

## Quick start (from a local checkout)

semii is not published yet, so generated projects point at this checkout
with a `replace` directive. sqlc must be on PATH.

```sh
go install ./cmd/semii
export SEMII_LOCAL_PATH=$PWD
cd /tmp && semii new shop --auth jwt,session   # or --auth none
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
`generate auth [--strategy session,jwt,apikey]` adds account flows and can
be re-run to add strategies. `--db postgres|mysql|sqlite|mongo` works for
every command (Mongo has no SQL migrations: `migrate up` ensures the
indexes declared in `db/mongo`).

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
- `db/sqldb` — MySQL and SQLite over database/sql: `Open` (MySQL URLs,
  parseTime, UTC; SQLite foreign keys, WAL, busy timeout), `WithTx`,
  `MapError`, and `JSON` (a column type sent as text).
- `db/mongodb` — `Open`, `WithTx` (replica set, with a clear error on a
  standalone server), `MapError`, `EnsureIndexes` and Decimal128/JSON helpers.
- `db/migrate` — goose as a library: `Up`, `Down`, `To`, `List`, `Pending`,
  for Postgres (advisory lock), MySQL and SQLite.
- `testkit` — in-process HTTP client with `Expect*` assertions, a cookie
  jar and `As(principal)`; `Postgres(t)` (template clones), `MySQL(t)`,
  `SQLite(t)` (a temporary file) and `Mongo(t)` (replica set) per test.
  Servers come from `TEST_<ENGINE>_URL`, a matching `TEST_DATABASE_URL`, or
  one container per test process (testcontainers); without Docker the tests
  skip.
- `auth` — `Principal`, `Middleware`, named policies (`Authenticated`,
  `Role`, `Scope`, `Kind`, `Any`, `All`), JWT (HS256/EdDSA, pinned
  algorithm, iss/aud, 30s leeway), server-side sessions with CSRF checks,
  API keys (`sk_<env>_<prefix>_<secret>`, constant-time compare), one-time
  tokens, an in-memory rate limiter, lockout backoff, and LogSender /
  SMTPSender (STARTTLS required) / MemorySender.
- `auth/password` — argon2id PHC hashes, rehash detection, a bounded
  `Hasher`, the length + common-password policy (SecLists top 10k).
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
### M4

- **`auth.Policy` is a small struct** (name + check), not a bare func type,
  so `semii routes` can print `authenticated`, `role:admin`, … It
  implements `web.Policy`; build custom ones with `auth.NewPolicy`.
- **`ErrTokenExpired`, `ErrTokenReused`, `ErrCSRF` are `*web.Error`
  values** (401 `token_expired`, 401 `token_reused`, 403 `csrf_rejected`),
  so they render correctly without extra router setup.
- **Stale session cookies count as absent**, not as a 401: otherwise a
  browser with an expired cookie could not reach `/auth/login`. Protected
  routes still answer 401, and CSRF is checked before any lookup.
- **Login mode:** with both session and jwt enabled, `/auth/login` takes
  `"mode": "session" | "token"` (default: first in `AUTH_STRATEGIES`).
- **Register always answers 201 with a message** (never the user) and
  forgot always 202; mail work runs in the background so neither the body
  nor the timing reveals whether an email has an account.
- **API keys** survive password resets (they belong to integrations) and
  cannot create or list keys (`Kind(user)` on `/auth/api-keys`). Key env
  tags are `live` (production), `test`, `dev`.
- **`apikey` alone is rejected**: users need session or jwt to sign in and
  manage keys.
- **Logout with JWT** revokes the refresh family given in the body; access
  tokens stay valid until they expire (stateless, 15 minutes).
- **Generated layout:** each strategy is its own file in `internal/auth`
  registered between `semii:strategies` markers, which is what makes
  `generate auth --strategy` re-runnable. The project templates gained
  markers for config, the test environment and seeds, and `app.go` builds
  everything in one `newApp` (template v1 is not yet published, so it was
  amended rather than versioned).
- **auth imports config, db and logx** (for `config.Secret`, `db.ErrNotFound`
  and the request logger) in addition to web; none of them adds a
  third-party dependency beyond what web already has, plus golang-jwt and
  x/crypto.
- **Session rotation on privilege change** has no trigger yet: v1 exposes
  no endpoint that changes roles.

### M5

- **One template tree, per-database parts:** `templates/common` holds what
  every database shares (models, DTOs, services, handlers, the in-memory
  fake, factories, tests, project files with small conditionals, all auth
  flows); `postgres/`, `sql/` (MySQL and SQLite) and `mongo/` hold
  repositories, queries, migrations and auth stores.
- **Same Go types everywhere:** sqlc overrides give MySQL and SQLite the
  Postgres types (uuid.UUID, decimal.Decimal, pointers for nullable
  columns). SQLite columns use the type names `UUID` and `DECIMAL_TEXT`
  (TEXT affinity keeps decimals exact). MySQL stores UUIDs as CHAR(36).
- **MySQL/SQLite PATCH is read-modify-write** in a transaction (`SELECT …
  FOR UPDATE` on MySQL), sharing `UpdateInput.apply` with the fake; list
  filters use typed `has_*` flags because sqlc cannot type optional
  parameters on those engines.
- **Mongo:** ObjectID ids (JSON hex), decimals as Decimal128, UUIDs and JSON
  fields as strings, millisecond timestamps, PATCH as read-replace.
  Foreign keys are not enforced, and unique indexes on soft-deleted
  collections still count deleted documents (Mongo partial indexes cannot
  express "not deleted").
- **Auth goes through a per-database `store`** whose strategy methods live
  in `<strategy>_store.go`; transactions travel in the context, so the
  password reset still revokes sessions and refresh tokens atomically on
  every database.
- **Generated go.mod pins three OpenTelemetry modules** for Go 1.24:
  testcontainers' Docker dependency would otherwise pull versions needing
  Go 1.26 during `go mod tidy`. Remove the pins once on Go 1.26+.

- **Dockerfile, CI workflow and deploy files** are M6; `semii new` writes
  `docker-compose.yml` (database only) now and accepts `--no-ci`.

## Development

```sh
go test -race -cover ./...
go test -run x -bench . ./web        # router overhead vs plain ServeMux

# Database tests start Postgres, MySQL and Mongo containers (Docker), or use
# TEST_POSTGRES_URL / TEST_MYSQL_URL / TEST_MONGO_URL when set.
# Full CLI workflow on every database (needs sqlc and Docker):
SEMII_E2E=1 go test -timeout 40m -run EndToEnd -v ./internal/cli
SEMII_E2E=1 SEMII_E2E_ONLY=sqlite go test -run EndToEnd -v ./internal/cli
```
