# testkit

`github.com/AhmadRaza-Khan/semii/testkit` makes integration tests short.

## HTTP client

```go
c := testkit.NewClient(t, app.Handler)
var got order.Order
c.POST("/orders").Bearer(token).JSON(in).Do().
	ExpectStatus(http.StatusCreated).
	ExpectHeader("Content-Type", "application/json").
	Decode(&got)
c.GET("/orders/" + id).Do().ExpectProblem(http.StatusNotFound, "not_found")
c.POST("/orders").JSON(map[string]any{}).Do().ExpectFieldError("customer_id", "required")
c.GET("/orders").As(&auth.Principal{ID: "u1", Kind: auth.KindUser}).Do() // skip real credentials
```

Requests go straight to the handler (no network). A cookie jar keeps
sessions across requests. `ExpectJSON(subset)` matches objects partially.

## Databases

```go
func TestMain(m *testing.M) { testkit.Main(m) }

pool := testkit.Postgres(t, testkit.WithMigrations(appdb.Migrations())) // template clone per test
sdb := testkit.MySQL(t, testkit.WithMigrations(fsys))                  // database per test
sdb := testkit.SQLite(t, testkit.WithMigrations(fsys))                 // file per test
client, d := testkit.Mongo(t, testkit.WithMongoSetup(indexes.Ensure))  // replica set
```

Each test gets its own database, dropped afterwards. Servers come from
`TEST_POSTGRES_URL` / `TEST_MYSQL_URL` / `TEST_MONGO_URL`, from
`TEST_DATABASE_URL` when its scheme matches, or from one container per
test process started with testcontainers; without Docker the tests skip.
`-short` skips them too.

Generated projects also have `internal/app/apptest.New(t)` (the real app
on a fresh database), factories in `internal/factory`, and an in-memory
`FakeRepo` per resource with `FailNext(method, err)` for unit tests.
