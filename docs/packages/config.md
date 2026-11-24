# config

`github.com/AhmadRaza-Khan/semii/config` loads a typed struct.

```go
type Config struct {
	Env         string        `env:"SEMII_ENV" default:"development" validate:"oneof=development test production"`
	DatabaseURL config.Secret `env:"DATABASE_URL" validate:"required"`
	HTTP        HTTPConfig    `envPrefix:"HTTP_"`
}
cfg, err := config.Load[Config]()
```

Sources, lowest to highest: `default` tags, `.env`, `.env.<SEMII_ENV>`,
`.env.local` (not in test), the process environment. Production reads no
`.env` file unless `SEMII_DOTENV=1`. Empty values count as unset.

Tags: `env:"NAME"`, `default:"value"`, `validate:"..."`,
`envPrefix:"HTTP_"` on nested structs, `sep:","` for slices. Supported
types: strings, bools, ints, uints, floats, `time.Duration`, `time.Time`,
`url.URL`, `netip.Prefix`, `netip.Addr`, `config.Secret`, any
`encoding.TextUnmarshaler`, pointers and slices of these.

`Load` returns one `*config.Error` listing every problem:

```
config: 2 errors
  DATABASE_URL: required
  HTTP_ADDR: must be host:port, got "8080"
```

`config.Secret` prints `[redacted]` in fmt, JSON, text and slog output;
use `Reveal()` for the value. Options: `WithPrefix`, `WithDir`,
`WithEnv`, `WithLookup` (tests).
