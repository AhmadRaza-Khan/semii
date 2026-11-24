# validate

`github.com/AhmadRaza-Khan/semii/validate` wraps go-playground/validator.
`web.Decode` and `web.DecodeQuery` call it.

```go
type CreateInput struct {
	Email string          `json:"email" validate:"required,email"`
	Total decimal.Decimal `json:"total" validate:"decimal_gte=0"`
	Tags  []string        `json:"tags" validate:"max=5,dive,notblank" msg:"up to five tags"`
}
err := validate.Default.Struct(ctx, in) // nil or validate.Errors
```

Errors use JSON (or `query`) names with dot and index paths
(`address.city`, `items[2].qty`) and English messages; `msg:"..."`
overrides one. Built-in extra rules: `notblank`, `decimal_gte=N`,
`decimal_lte=N`, `password` (8–128 characters).

Custom rules on your own validator (pass it in `web.Options.Validator`):

```go
v := validate.New()
_ = v.Register("sku", func(ctx context.Context, value any, param string) bool {
	s, _ := value.(string)
	return skuPattern.MatchString(s)
}, "must be a valid SKU")
```

Business rules belong in services: return `web.Unprocessable(detail,
fields...)` or a domain error mapped with `Router.MapError`.
