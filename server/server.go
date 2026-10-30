// Package server runs an http.Handler with production timeouts, health
// endpoints and a graceful shutdown sequence.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"
)

// Version is reported by /healthz. Set it at build time with
//
//	-ldflags "-X github.com/semii/semii/server.Version=v1.2.3"
//
// When empty, the VCS revision from the build info is used.
var Version string

// Config configures Run. Zero durations use the documented defaults; a
// negative DrainDelay disables the drain wait.
type Config struct {
	Addr    string
	Handler http.Handler
	Logger  *slog.Logger // default: discard
	Health  Health
	// Closers are closed in reverse order after the server stops.
	Closers  []io.Closer
	Timeouts Timeouts
	// DrainDelay is how long /readyz reports 503 before connections are
	// shut down, so load balancers stop routing new traffic. Default 5s.
	DrainDelay time.Duration
	// ShutdownTimeout bounds http.Server.Shutdown. Default 30s.
	ShutdownTimeout time.Duration
}

// Timeouts are the http.Server timeouts. Defaults: ReadHeader 10s,
// Read 30s, Write 60s, Idle 120s.
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

func (c *Config) setDefaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	def(&c.Timeouts.ReadHeader, 10*time.Second)
	def(&c.Timeouts.Read, 30*time.Second)
	def(&c.Timeouts.Write, 60*time.Second)
	def(&c.Timeouts.Idle, 120*time.Second)
	def(&c.DrainDelay, 5*time.Second)
	def(&c.ShutdownTimeout, 30*time.Second)
	def(&c.Health.Timeout, 2*time.Second)
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
}

// Run serves c.Handler on c.Addr until ctx is canceled or the process
// receives SIGINT or SIGTERM, then shuts down:
//
//  1. /readyz starts returning 503
//  2. wait DrainDelay
//  3. http.Server.Shutdown, bounded by ShutdownTimeout
//  4. close Closers in reverse order
//
// A second signal during shutdown exits the process with status 1. Run
// returns listen errors and all shutdown and close errors, joined.
// /healthz and /readyz are served in front of c.Handler, so they bypass
// the application's middleware.
func Run(ctx context.Context, c Config) error {
	ln, err := net.Listen("tcp", c.Addr)
	if err != nil {
		return errors.Join(fmt.Errorf("server: listen %s: %w", c.Addr, err), closeAll(c.Closers))
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	return serve(ctx, c, ln, sigs, func() { os.Exit(1) })
}

func serve(ctx context.Context, c Config, ln net.Listener, sigs <-chan os.Signal, exit func()) error {
	if c.Handler == nil {
		_ = ln.Close()
		return errors.Join(errors.New("server: nil Handler"), closeAll(c.Closers))
	}
	c.setDefaults()
	log := c.Logger
	version := buildVersion()

	var draining atomic.Bool
	srv := &http.Server{
		Handler:           &healthHandler{health: c.Health, next: c.Handler, draining: &draining, version: version, log: log},
		ReadHeaderTimeout: c.Timeouts.ReadHeader,
		ReadTimeout:       c.Timeouts.Read,
		WriteTimeout:      c.Timeouts.Write,
		IdleTimeout:       c.Timeouts.Idle,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("server started", "addr", ln.Addr().String(), "version", version)

	var errs []error
	select {
	case err := <-serveErr:
		// Serve failed before any shutdown was requested.
		return errors.Join(fmt.Errorf("server: %w", err), closeAll(c.Closers))
	case <-ctx.Done():
		log.Info("shutting down", "reason", "context canceled")
	case sig := <-sigs:
		log.Info("shutting down", "signal", sig.String())
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case sig := <-sigs:
			log.Error("second signal received, exiting immediately", "signal", sig.String())
			exit()
		case <-done:
		}
	}()

	draining.Store(true)
	if c.DrainDelay > 0 {
		time.Sleep(c.DrainDelay)
	}

	sctx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		errs = append(errs, fmt.Errorf("server: shutdown: %w", err))
		_ = srv.Close() // force-close connections still in flight
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("server: %w", err))
	}
	errs = append(errs, closeAll(c.Closers))
	err := errors.Join(errs...)
	if err != nil {
		log.Error("shutdown finished with errors", "error", err)
	} else {
		log.Info("shutdown complete")
	}
	return err
}

// closeAll closes cs in reverse order, joining their errors.
func closeAll(cs []io.Closer) error {
	var errs []error
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i] == nil {
			continue
		}
		if err := cs[i].Close(); err != nil {
			errs = append(errs, fmt.Errorf("server: close %T: %w", cs[i], err))
		}
	}
	return errors.Join(errs...)
}

// buildVersion returns Version, else the VCS revision (12 chars, "-dirty"
// when modified), else the module version, else "dev".
func buildVersion() string {
	if Version != "" {
		return Version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if modified == "true" {
			rev += "-dirty"
		}
		return rev
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return "dev"
}
