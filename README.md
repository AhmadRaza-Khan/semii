# semii

Rails-speed scaffolding for Go backend APIs. Conventions are delivered
through code generation, never runtime magic. The spec lives in
`semii-v1-spec/`.

## Status

| Milestone | Scope | State |
|-----------|-------|-------|
| M1 | web core: router, groups, error handlers, problem+json, JSON, middleware, pagination | done |
| M2 | config, server, logx | — |
| M3 | generator engine + Postgres (`semii new`, `generate resource`, …) | — |
| M4 | auth | — |
| M5 | testkit + MySQL/SQLite/Mongo | — |
| M6 | ops, docs, examples | — |

## Packages so far

- `web` — router over `http.ServeMux`, `HandlerFunc` returning `error`,
  RFC 9457 problems, `Decode`/`DecodeQuery`/`JSON`, cursor and offset
  pagination, `Optional[T]`, and the built-in middleware (RequestID, RealIP,
  Logger, Recover, SecurityHeaders, CORS, BodyLimit, Timeout, Gzip).
- `validate` — wrapper over go-playground/validator with JSON-named field
  errors, default messages, `msg:"..."` overrides and the `notblank`,
  `decimal_gte`/`decimal_lte` and `password` rules (needed by `web.Decode`).
- `db` — driver-independent sentinels `ErrNotFound` and `ErrConflict`,
  which the router maps by default.

## Design decisions made in M1 (not spelled out in the spec)

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
- **Reflection** is used in `DecodeQuery` (query binding) and once at
  registration time to name handlers for `Routes()`.

## Development

```sh
go test -race -cover ./...
go test -run x -bench . ./web        # router overhead vs plain ServeMux
```
