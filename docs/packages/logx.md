# logx

`github.com/AhmadRaza-Khan/semii/logx` builds the application logger.

```go
log := logx.New(cfg.Env, cfg.LogLevel) // colored text in development, JSON otherwise
```

`web.Logger` stores a logger enriched with `request_id` in each request;
handlers use `logx.FromContext(r.Context())`. There is no global logger.
`config.Secret` values log as `[redacted]`, and request headers are never
logged.
