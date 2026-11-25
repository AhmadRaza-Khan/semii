# auth

`github.com/AhmadRaza-Khan/semii/auth` authenticates requests and
authorizes routes. Generated apps add the account flows in
`internal/auth` with `semii generate auth`.

## Principals and middleware

```go
h := web.Chain(router, ..., auth.Middleware(jwtAuth, apiKeys, sessions))
```

Authenticators run in order; the first principal wins and is stored in
the request context (`auth.FromContext`). Invalid credentials answer 401
immediately; missing credentials continue anonymously and the route's
policy decides.

```go
type Principal struct {
	ID     string
	Kind   string            // auth.KindUser or auth.KindAPIKey
	Roles  []string
	Scopes []string
	Claims map[string]any
}
```

## Policies

`Authenticated()`, `Role(roles...)` (any of), `Scope(scopes...)` (all
of), `Kind(k)`, `Any(...)`, `All(...)`, and custom ones with
`NewPolicy(name, fn)`. A missing principal is 401 with
`WWW-Authenticate`; a failed policy is 403. Policy names appear in
`semii routes`.

## Strategies

| Strategy | Credential | Storage |
|----------|-----------|---------|
| JWT (`NewJWT`) | `Authorization: Bearer <jwt>`, 15 min, HS256 or EdDSA | refresh tokens: hashed, single use, rotating families |
| Sessions (`NewSessions`) | `semii_session` cookie: HttpOnly, Secure (not in development), SameSite=Lax | sha256 of the id; idle 7 d, absolute 30 d |
| API keys (`NewAPIKeys`) | `X-API-Key: sk_<env>_<prefix>_<secret>` or a bearer `sk_` key | prefix + sha256 of the secret |

JWT verification pins the configured algorithm (no `alg` trust), checks
`iss`, `aud`, `exp`, `nbf` and `iat` with 30 s leeway. Reusing a consumed
refresh token revokes its whole family (`token_reused`). Cookie-
authenticated unsafe requests need `Sec-Fetch-Site: same-origin|none` or
an allowed `Origin`, else 403 `csrf_rejected`.

Passwords (`auth/password`): argon2id (64 MiB, 1 pass, 4 lanes) as PHC
strings, transparent rehash, a bounded `Hasher`, and `Check`: 8–128
characters, not in the 10,000 most common passwords.

## Generated endpoints

| Endpoint | |
|----------|--|
| `POST /auth/register` | 201 always (no account enumeration) + verification mail |
| `POST /auth/login` | session cookie or `{access_token, refresh_token, expires_in}`; `"mode": "session"\|"token"` when both are enabled |
| `POST /auth/logout` | revokes the session, or the refresh family in the body |
| `POST /auth/refresh` | new token pair (jwt) |
| `POST /auth/password/forgot` | 202 always |
| `POST /auth/password/reset` | token + password; revokes every session and refresh token, and every other outstanding reset link |
| `POST /auth/email/verify`, `/email/resend` | one-time verification links |
| `GET /auth/me` | the current user |
| `POST/GET /auth/api-keys`, `DELETE /auth/api-keys/{id}` | users manage their keys; keys cannot manage keys |

Login is rate limited per IP (20/min) and locks an account after 5
failures (1 m, 5 m, 15 m, then 1 h), always answering
`invalid_credentials`. Unknown emails still cost a password hash.
Every `/auth` response is sent with `Cache-Control: no-store`. Access
tokens stay valid until they expire (15 minutes) even after a logout or
password reset; refresh tokens and sessions are revoked immediately.

## Per-user data

Policies decide who may call a route, not which records they may touch.
Generated resources are shared: an `Authenticated()` route lets every
user reach every record by id. For data that belongs to a user, add an
owner column of the project's id type (`uuid` or `int64`) and filter by
it in every query:

```sh
semii g resource Note title:string body:text:null owner_id:uuid
```

Then remove `owner_id` from the generated create and update input types,
so clients cannot set it. Set it from the principal on create, and check
it on every read, update and delete:

```go
p, _ := auth.FromContext(r.Context()) // create: OwnerID comes from p.ID
note, err := h.svc.Get(ctx, id)
if err != nil || note.OwnerID.String() != p.ID {
	return web.NotFound("note not found") // 404, not 403: do not confirm the id exists
}
```

For lists, filter in the query (`WHERE owner_id = $1`) rather than after
fetching. `Role("admin")` routes can skip the check.

## Configuration (`AUTH_`)

`AUTH_STRATEGIES`, `AUTH_APP_URL` (links in mail, allowed CSRF origin),
`AUTH_JWT_SECRET` (≥ 32 bytes) or `AUTH_JWT_ALGORITHM=EdDSA` with
`AUTH_JWT_PRIVATE_KEY`, `AUTH_JWT_ISSUER`, `AUTH_JWT_AUDIENCE`,
`AUTH_ACCESS_TTL` (15m), `AUTH_REFRESH_TTL` (720h), `AUTH_SESSION_IDLE`
(168h), `AUTH_SESSION_ABSOLUTE` (720h), `AUTH_REQUIRE_VERIFIED_EMAIL`,
`AUTH_STRICT_ROUTES`, `AUTH_CHECK_COMMON_PASSWORDS`. Mail uses `SMTP_*`
(STARTTLS required); without `SMTP_HOST` links are logged, which
production refuses.
