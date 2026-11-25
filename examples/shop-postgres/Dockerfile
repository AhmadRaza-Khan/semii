# syntax=docker/dockerfile:1
# Build: docker build --build-arg VERSION=$(git describe --always) -t shop-postgres .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
ARG VERSION=dev
# Module and build caches survive between builds; vendor/ is used if present.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/AhmadRaza-Khan/semii/server.Version=${VERSION}" \
      -o /out/api ./cmd/api

# No shell, no package manager, not root. Migrations are embedded in the
# binary: run them with `api migrate`.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
ENV SEMII_ENV=production HTTP_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
