# Thin aliases; the semii CLI does the work.
.PHONY: dev build test test-short lint migrate-up migrate-down seed docker-up docker-down

dev:
	semii dev

build:
	go build -trimpath -o bin/api ./cmd/api

# Loads .env so an optional TEST_DATABASE_URL reaches the integration tests.
test:
	@set -a; [ -f .env ] && . ./.env; set +a; go test -race -count=1 ./...

test-short:
	go test -short ./...

lint:
	go vet ./...
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed; skipping"

migrate-up:
	semii migrate up

migrate-down:
	semii migrate down

seed:
	semii db seed

docker-up:
	docker compose up -d db

docker-down:
	docker compose down
