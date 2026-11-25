# Changelog

All notable changes to semii are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
semantic versioning. Public APIs of web, config, auth, validate, db/*,
server, logx and testkit are frozen from v1.0.0 until v2.

## [Unreleased]

## [1.0.2] - 2026-11-25

### Added

- MIT license (`LICENSE`), and the SecLists license for the bundled
  common-password list (`auth/password/LICENSE`). pkg.go.dev shows the
  documentation from this version on.

## [1.0.1] - 2026-11-25

### Fixed

- `semii` installed with `go install …/cmd/semii@vX.Y.Z` reported
  `v0.6.0-dev` and stamped it into `.semii.yaml` and generated files; it
  now reports the installed release. Release archives were not affected.

## [1.0.0] - 2026-11-25

### Security

Fixes from a manual pentest of a generated app (all strategies, Postgres):

- Password reset now also invalidates every other outstanding reset link
  for the account; an older link could previously change the password
  again after a reset.
- `/auth` responses are sent with `Cache-Control: no-store` (new
  `web.NoStore` middleware), so tokens, session cookies and API keys are
  never cached (RFC 6749 section 5.1).
- `/healthz` and `/readyz` send the same security headers as
  `web.SecurityHeaders`.
- Documented that generated resources are shared tables reachable by any
  signed-in user, with a per-user ownership recipe; generated `routes.go`
  says so too.

### Changed

- Go 1.26 is the minimum version; dependencies updated (govulncheck clean).
- `semii new` from a development build fetches the framework with
  `go get …/semii@latest` instead of requiring `SEMII_LOCAL_PATH`.
- `web.Decode` locates malformed `Date` fields itself, since Go 1.27's
  `encoding/json` no longer reports their path.
- CI installs the newest patch of Go 1.26 for tests (plus `stable`), and
  `stable` for govulncheck and release builds; `go-version-file` pinned
  Go 1.26.0 and its fixed standard library vulnerabilities. Generated
  projects build with `stable`. Actions moved to checkout/setup-go v7 and
  goreleaser-action v7 (Node 24).
- Router benchmarks compare against ServeMux both bare and behind the
  generated app's middleware (RequestID, Logger, Recover), writing through
  a discard writer instead of a growing recorder.
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

[Unreleased]: https://github.com/AhmadRaza-Khan/semii/compare/v1.0.2...HEAD
[1.0.2]: https://github.com/AhmadRaza-Khan/semii/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/AhmadRaza-Khan/semii/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/AhmadRaza-Khan/semii/releases/tag/v1.0.0
