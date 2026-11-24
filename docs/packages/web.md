# web

`github.com/AhmadRaza-Khan/semii/web`: a thin layer over `http.ServeMux`
with groups, error-returning handlers and deny-by-default policies, plus
RFC 9457 problem responses, JSON helpers, pagination and middleware. It
depends only on the standard library, `validate`, `logx` and google/uuid.

## Router

```go
r := web.NewRouter(web.Options{Logger: log, Env: cfg.Env})
r.HandleFunc("GET /healthz", health).Public()

api := r.Group("/v1", rateLimit)            // prefix + group middleware
orders := api.Group("/orders").Require(auth.Authenticated())
orders.HandleFunc("GET /", h.List)          // GET /v1/orders
orders.HandleFunc("GET /{id}", h.Get)       // r.PathValue("id")
orders.HandleFunc("DELETE /{id}", h.Delete).Require(auth.Role("admin"))

if err := r.Verify(); err != nil {         // fails on any route without a policy
	return err
}
```

- Patterns use ServeMux syntax; the group prefix is inserted after the
  method. `"GET /"` inside a group is the group path itself.
- Every route needs `Public()` or `Require(...)`, on the route or a group
  (route beats nearest group). `Verify()` lists offenders; requests to
  such a route are refused with 403 even if `Verify` was skipped.
- 404 and 405 (with `Allow`) are problem responses.
- `Routes()` lists routes with policy names (used by `semii routes`).
- `Mount(prefix, h)` serves an `http.Handler` under a prefix.

Handlers return errors:

```go
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathUUID(r, "id")   // 400 on a malformed id
	if err != nil {
		return err
	}
	o, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return err                     // db.ErrNotFound -> 404
	}
	return web.JSON(w, http.StatusOK, o)
}
```

## Errors

| Returned error | Response |
|----------------|----------|
| `*web.Error` | as-is (`Status`, `Code`, `Detail`, `Fields`, extra `Header`) |
| `validate.Errors` | 422 `validation_failed` with field errors |
| `db.ErrNotFound` / `ErrConflict` / `ErrForeignKey` | 404 / 409 / 409 |
| `web.ErrUnauthenticated` / `ErrForbidden` / `ErrInvalidCredentials` | 401 / 403 / 401 |
| `context.DeadlineExceeded` | 504 |
| `context.Canceled` | nothing (client left) |
| anything else | 500 `internal_error`, logged; detail only in development |

Constructors: `BadRequest`, `Unauthorized`, `Forbidden`, `NotFound`,
`Conflict`, `Unprocessable(detail, fields...)`, `TooManyRequests(retry)`.
Map your own errors with `r.MapError(ErrOutOfStock, 409, "out_of_stock")`.

```json
{"type":"about:blank","title":"Unprocessable Entity","status":422,
 "detail":"request validation failed","code":"validation_failed",
 "request_id":"01J…","errors":[{"field":"email","code":"email","message":"must be a valid email"}]}
```

## JSON

- `Decode[T](r)`: requires `application/json` (415), rejects unknown
  fields and trailing data (400), oversize bodies (413), then validates
  (422).
- `DecodeQuery[T](r)`: binds `query:"name"` fields (strings, numbers,
  bools, `encoding.TextUnmarshaler` such as `time.Time`, `uuid.UUID`,
  `web.Date`, pointers and repeated keys as slices), then validates.
- `JSON(w, status, v)`, `Created(w, location, v)`, `NoContent(w)`.
- `Optional[T]` distinguishes an absent field from an explicit `null`
  (PATCH). `Date` is a date-only `"2006-01-02"` value.

## Pagination

```go
p, err := web.ParseCursor(r, 20, 100)          // ?limit=&cursor=
rows := fetch(p.Limit + 1, p.Cursor)           // p.Cursor.Scan(&createdAt, &id)
page := web.NewCursorPage(rows, p.Limit, func(o Order) []any { return []any{o.CreatedAt, o.ID} })
return web.JSON(w, http.StatusOK, page)        // {"data": [...], "next_cursor": "..."|null}
```

`ParseOffset` / `NewOffsetPage` provide `?page=&per_page=` pagination.

## Middleware

In the order generated apps use (outermost first):

| Middleware | Behaviour |
|------------|-----------|
| `RequestID()` | accepts a safe `X-Request-ID` or generates a ULID |
| `RealIP(trusted)` | honours `X-Forwarded-For` only from trusted proxies; read with `ClientIP(r)` |
| `Logger(log)` | one line per request (route pattern, status, duration, principal); skips health checks; stores a request logger for `logx.FromContext` |
| `Recover(log)` | panics become 500 problems; `http.ErrAbortHandler` passes through |
| `SecurityHeaders(env)` | nosniff, frame denial, no-referrer, CSP; HSTS in production |
| `CORS(cfg)` | exact origins, preflight 204; `"*"` with credentials is refused |
| `BodyLimit(n)` | 1 MiB default; an inner limit replaces an outer one |
| `Timeout(d)` | context deadline (30 s default); handlers must honour ctx |
| `Gzip()` | JSON/text responses of at least 1 KiB |

`Chain(h, mw...)` applies them, first outermost.
