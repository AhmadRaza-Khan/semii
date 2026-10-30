package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/semii/semii/config"
	"github.com/semii/semii/logx"
	"github.com/semii/semii/server"
	"github.com/semii/semii/web"
)

type Config struct {
	Env         string        `env:"SEMII_ENV" default:"development" validate:"oneof=development test production"`
	LogLevel    string        `env:"LOG_LEVEL" default:"info" validate:"oneof=debug info warn error"`
	DatabaseURL config.Secret `env:"DATABASE_URL" validate:"required"`
	HTTP        HTTPConfig    `envPrefix:"HTTP_"`
}

type HTTPConfig struct {
	Addr           string         `env:"ADDR" default:":8080" validate:"hostname_port"`
	MaxBodyBytes   int64          `env:"MAX_BODY_BYTES" default:"1048576"`
	HandlerTimeout time.Duration  `env:"HANDLER_TIMEOUT" default:"30s"`
	TrustedProxies []netip.Prefix `env:"TRUSTED_PROXIES"`
	CORS           web.CORSConfig `envPrefix:"CORS_"`
}

// Example_main mirrors the generated cmd/api/main.go: load config, build
// the logger and router, then run the server until SIGINT/SIGTERM.
func Example_main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	if err := cfg.HTTP.CORS.Validate(); err != nil {
		return err
	}
	log := logx.New(cfg.Env, cfg.LogLevel)

	r := web.NewRouter(web.Options{Logger: log, Env: cfg.Env})
	r.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) error {
		logx.FromContext(r.Context()).Info("saying hello")
		return web.JSON(w, http.StatusOK, map[string]string{"hello": "world"})
	}).Public()
	if err := r.Verify(); err != nil {
		return err
	}
	h := web.Chain(r,
		web.RequestID(), web.RealIP(cfg.HTTP.TrustedProxies),
		web.Logger(log), web.Recover(log),
		web.SecurityHeaders(cfg.Env), web.CORS(cfg.HTTP.CORS),
		web.BodyLimit(cfg.HTTP.MaxBodyBytes), web.Timeout(cfg.HTTP.HandlerTimeout),
		web.Gzip(),
	)

	return server.Run(context.Background(), server.Config{
		Addr: cfg.HTTP.Addr, Handler: h, Logger: log,
		Health: server.Health{Checks: map[string]server.Check{
			"db": func(ctx context.Context) error { return nil }, // e.g. pool.Ping
		}},
	})
}
