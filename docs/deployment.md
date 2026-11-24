# Deployment

A generated app is one static binary with its migrations embedded. It
reads configuration from the environment (production reads no `.env`
file), logs JSON to stdout, serves `/healthz` and `/readyz`, and shuts
down gracefully on SIGTERM.

## Build

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X github.com/AhmadRaza-Khan/semii/server.Version=$(git describe --always)" \
  -o bin/api ./cmd/api
```

SQLite uses a pure-Go driver, so `CGO_ENABLED=0` works for every database.

## Migrations

Run them before starting a new version:

```sh
api migrate          # SQL: apply pending migrations; Mongo: ensure indexes
```

Postgres migrations take an advisory lock, so two deploys cannot run them
at once. `DB_AUTO_MIGRATE=true` migrates on boot instead (refused in
production unless `DB_AUTO_MIGRATE_PRODUCTION=true`).

## Docker

The generated `Dockerfile` builds with Go 1.24 and runs on
`gcr.io/distroless/static-debian12:nonroot`: no shell, not root, about
25 MB.

```sh
docker build --build-arg VERSION=$(git describe --always) -t shop .
docker run --env-file prod.env -p 8080:8080 shop
docker run --env-file prod.env shop migrate
```

`docker compose up --build` runs the app and its database locally, with
migrations on boot. SQLite projects keep the database on the `/data`
volume.

Until semii is published, projects created with `SEMII_LOCAL_PATH` point
at a local checkout through a `replace` directive outside the build
context: run `go mod vendor` before `docker build`.

## systemd

`deploy/systemd/<app>.service` runs `/opt/<app>/api` as a dedicated user
with `EnvironmentFile=/etc/<app>/env`, restart on failure,
`TimeoutStopSec=40` (longer than drain plus shutdown) and sandboxing
(`ProtectSystem=strict`, `NoNewPrivileges`, `PrivateTmp`, …).

```sh
sudo useradd --system --home /var/lib/shop shop
sudo install -d -o shop /var/lib/shop /opt/shop /etc/shop
sudo install -m 0755 bin/api /opt/shop/api
sudo install -m 0600 prod.env /etc/shop/env
sudo cp deploy/systemd/shop.service /etc/systemd/system/
sudo systemctl enable --now shop
```

## PM2

`deploy/pm2/ecosystem.config.js` runs the binary as a single fork-mode
process with `kill_timeout: 40000`, loading the same `/etc/<app>/env`:

```sh
pm2 start deploy/pm2/ecosystem.config.js && pm2 save
```

## Nginx

`deploy/nginx/<app>.conf` proxies to `127.0.0.1:8080` with
`X-Forwarded-For`, `X-Forwarded-Proto` and `X-Request-ID`, matching body
size and timeouts, and an ACME challenge location for certbot:

```sh
sudo cp deploy/nginx/shop.conf /etc/nginx/sites-available/
sudo ln -s /etc/nginx/sites-available/shop.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d api.example.com
```

Set `HTTP_TRUSTED_PROXIES=127.0.0.1/32` so the app takes client IPs from
Nginx (it ignores `X-Forwarded-For` from anyone else).

## CI and continuous deployment

`.github/workflows/ci.yml` checks formatting, runs `go vet`, staticcheck,
govulncheck, the tests with `-race` (Postgres and MySQL as service
containers; Mongo and SQLite need none) and builds the Docker image on
`main`.

`semii new --deploy ssh` adds `.github/workflows/deploy.yml`: on `main`
it builds the binary, copies it to the server, runs `api migrate` as the
service user with the service's environment, swaps the binary and
restarts the unit. Secrets: `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_KEY`,
`DEPLOY_KNOWN_HOSTS`.

## Production checklist

- `SEMII_ENV=production`: HSTS on, error details hidden, `.env` ignored.
- A unique `AUTH_JWT_SECRET` (≥ 32 bytes) and `SMTP_*` when auth is on.
- `AUTH_APP_URL` set to the real frontend; CORS origins exact.
- TLS at the proxy; `HTTP_TRUSTED_PROXIES` set to the proxy only.
- Logs shipped from stdout; `/readyz` wired to the load balancer.
