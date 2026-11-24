# server

`github.com/AhmadRaza-Khan/semii/server` runs the HTTP server.

```go
err := server.Run(ctx, server.Config{
	Addr:    cfg.HTTP.Addr,
	Handler: a.Handler,
	Logger:  log,
	Health:  server.Health{Checks: map[string]server.Check{"db": a.Ping}},
	Closers: []io.Closer{a},
})
```

- Timeouts default to ReadHeader 10 s, Read 30 s, Write 60 s, Idle 120 s.
- `GET /healthz` is always 200 `{"status":"ok","version":"…"}`;
  `GET /readyz` runs the checks concurrently (2 s timeout) and answers
  503 with `ok`/`timeout`/`error` per check. Both bypass your middleware.
- On SIGINT/SIGTERM (or ctx cancel): `/readyz` turns 503, the server
  waits `DrainDelay` (5 s) so load balancers stop routing, shuts down
  within `ShutdownTimeout` (30 s) and closes `Closers` in reverse order. A
  second signal exits immediately.
- The version comes from `-ldflags "-X …/server.Version=v1.2.3"` or the
  VCS revision.
