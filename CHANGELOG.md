# Changelog

All notable changes to semii are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
semantic versioning. Public APIs of web, config, auth, validate, db/*,
server, logx and testkit are frozen from v1.0.0 until v2.

## [Unreleased]

### Changed

- Go 1.26 is the minimum version; dependencies updated (govulncheck clean).
- `semii new` from a development build fetches the framework with
  `go get …/semii@latest` instead of requiring `SEMII_LOCAL_PATH`.
- `web.Decode` locates malformed `Date` fields itself, since Go 1.27's
  `encoding/json` no longer reports their path.
- Generated code always imports `github.com/google/uuid`; on Go 1.27
  goimports would otherwise pick the new standard library `uuid` package.

### Added

- `web`: ServeMux router with groups, error-returning handlers and
  deny-by-default policies; RFC 9457 problems; `Decode`, `DecodeQuery`,
  `Optional`, `Date`; cursor and offset pagination; RequestID, RealIP,
  Logger, Recover, SecurityHeaders, CORS, BodyLimit, Timeout and Gzip
  middleware.
- `validate`, `config` (typed loader, `.env` layering, `Secret`), `server`
  (graceful shutdown, `/healthz`, `/readyz`) and `logx`.
- `auth` and `auth/password`: principals, policies, JWT (HS256/EdDSA)
  with rotating refresh families, server-side sessions with CSRF
  protection, API keys, argon2id passwords, rate limiting and lockout.
- `db/pgxdb`, `db/sqldb` (MySQL, SQLite), `db/mongodb`, `db/migrate`.
- `testkit`: in-process HTTP client and per-test Postgres, MySQL, SQLite
  and MongoDB databases (testcontainers).
- `semii` CLI: `new`, `generate resource|auth|migration|middleware`, `dev`,
  `routes`, `migrate`, `db seed`, `doctor`, `version`, for Postgres,
  MySQL, SQLite and MongoDB.
- Generated projects include a Dockerfile, docker-compose, GitHub Actions
  CI, an optional SSH deploy workflow, systemd, PM2 and Nginx files, and
  `api migrate`.
- Documentation in `docs/` and two example apps in `examples/`.
