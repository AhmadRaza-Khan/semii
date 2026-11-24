# db

semii does not abstract databases: the database is a generator choice and
the swap point is each resource's `Repository` interface. The packages
below open connections, run transactions and translate errors into the
shared sentinels in package `db`:

| Sentinel | Meaning | HTTP (default mapping) |
|----------|---------|------------------------|
| `db.ErrNotFound` | no row / document | 404 |
| `db.ErrConflict` | unique violation | 409 |
| `db.ErrForeignKey` | foreign-key violation | 409 |

## db/pgxdb (Postgres, pgx)

`Open(ctx, url, Options{MaxConns, MinConns, MaxConnLifetime,
MaxConnIdleTime, NoPing})` returns a `*pgxpool.Pool` that scans
timestamps in UTC. `WithTx(ctx, pool, fn)` commits on nil and rolls back
on error or panic. `MapError` maps `pgx.ErrNoRows`, 23505 and 23503.

## db/sqldb (MySQL, SQLite)

`Open(ctx, sqldb.MySQL|sqldb.SQLite, dsn, Options{...})`. MySQL accepts
`mysql://user:pass@host:3306/db` URLs and always sets `parseTime` and
UTC; SQLite gets `foreign_keys(1)`, `journal_mode(WAL)` and
`busy_timeout(5000)`. Register the driver in your program
(`_ "github.com/go-sql-driver/mysql"` or `_ "modernc.org/sqlite"`, pure
Go). `WithTx`, `MapError` (MySQL 1062/1451/1452, SQLite constraint
codes) and `JSON`, a column type sent as text.

## db/mongodb (MongoDB driver v2)

`Open(ctx, uri, database, Options{})` returns the client and database.
`WithTx(ctx, client, fn)` needs a replica set and says so clearly on a
standalone server. `MapError` handles not-found and duplicate keys;
`EnsureIndexes` creates indexes declared per collection.

## db/migrate (goose)

```go
applied, err := migrate.Up(ctx, sqlDB, appdb.Migrations(), migrate.WithDialect(migrate.Postgres))
```

`Up`, `Down` (one step), `To(version)`, `List`, `Pending`. Postgres runs
take an advisory lock so concurrent deploys wait. Migrations are
timestamped goose files embedded in the binary.
