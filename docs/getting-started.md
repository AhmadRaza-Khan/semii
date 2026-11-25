# Getting started

## Install

You need Go 1.26 or newer, [sqlc](https://docs.sqlc.dev) (for Postgres,
MySQL and SQLite projects) and Docker for the database and integration
tests.

```sh
go install github.com/AhmadRaza-Khan/semii/cmd/semii@latest
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
semii version
```

`go install` puts binaries in `$(go env GOPATH)/bin` (usually `~/go/bin`);
add it to PATH if `semii` is not found.

## Create an API

```sh
semii new shop --db postgres --auth jwt
cd shop
cp .env.example .env
docker compose up -d db
semii migrate up
```

`--db` accepts `postgres`, `mysql`, `sqlite` or `mongo`; `--auth` takes a
comma list of `session`, `jwt`, `apikey`, or `none`.

## Add resources

```sh
semii g resource Customer name:string:min=2 email:string:unique
semii g resource Order customer:ref:Customer total:decimal:min=0 notes:text:null --soft-delete
semii migrate up
semii routes
```

Each resource gets a model, input types with validation, a repository
interface with a database implementation and an in-memory fake, a
service, HTTP handlers, routes, a factory and tests.

Resources are shared tables. With auth set up, their routes require
`auth.Authenticated()`, so **any signed-in user can list, read, change and
delete every record**. For per-user data (notes, orders, carts) add an
owner column and scope the repository by the caller; see
[Per-user data](packages/auth.md#per-user-data).

## Run it

```sh
semii dev        # builds, runs and reloads on changes
```

```sh
curl -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","password":"correct-horse-battery"}'
TOKEN=$(curl -s -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","password":"correct-horse-battery"}' | jq -r .access_token)
curl -X POST localhost:8080/customers -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"Ada","email":"ada@example.com"}'
curl localhost:8080/customers -H "Authorization: Bearer $TOKEN"
```

## Test it

```sh
make test        # go test -race ./... with a database per test
semii doctor     # toolchain, config, database and migrations
```

Integration tests start a database container automatically. To use a
running server instead, set `TEST_DATABASE_URL`.

## Where things live

| Path | Purpose |
|------|---------|
| `cmd/api/main.go` | Entrypoint: `api` serves, `api migrate` migrates |
| `internal/app/app.go` | Explicit wiring between `semii:wiring` markers |
| `internal/app/routes.go` | Route registration |
| `internal/config/config.go` | Typed configuration |
| `internal/<resource>/` | One package per resource |
| `internal/auth/` | Account flows (when auth is enabled) |
| `db/migrations`, `db/queries`, `db/sqlc` | SQL schema, queries, sqlc output |
| `db/mongo/indexes.go` | MongoDB indexes (Mongo projects) |
| `deploy/` | systemd, PM2 and Nginx files |
