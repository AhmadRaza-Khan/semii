package validate

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"
)

type rule struct {
	tag string
	fn  Func
	msg string
}

func builtinRules(o options) []rule {
	return []rule{
		{"notblank", notBlank, "must not be blank"},
		{"decimal_gte", decimalCmp(func(c int) bool { return c >= 0 }), "must be greater than or equal to {param}"},
		{"decimal_lte", decimalCmp(func(c int) bool { return c <= 0 }), "must be less than or equal to {param}"},
		{"password", password(o.passwordMin, o.passwordMax),
			fmt.Sprintf("must be between %d and %d characters", o.passwordMin, o.passwordMax)},
	}
}

func notBlank(_ context.Context, value any, _ string) bool {
	s, ok := value.(string)
	return ok && strings.TrimSpace(s) != ""
}

// password enforces the auth password policy: length bounds only, no
// composition rules (NIST SP 800-63B).
func password(min, max int) Func {
	return func(_ context.Context, value any, _ string) bool {
		s, ok := value.(string)
		if !ok {
			return false
		}
		n := utf8.RuneCountInString(s)
		return n >= min && n <= max
	}
}

// decimalCmp compares a decimal value against the tag parameter. It accepts
// strings and any value whose String method yields an exact decimal, such
// as shopspring decimal.Decimal, so validate does not depend on a decimal
// library.
func decimalCmp(ok func(cmp int) bool) Func {
	return func(_ context.Context, value any, param string) bool {
		limit, valid := new(big.Rat).SetString(param)
		if !valid {
			return false
		}
		var s string
		switch v := value.(type) {
		case string:
			s = v
		case fmt.Stringer:
			s = v.String()
		default:
			return false
		}
		x, valid := new(big.Rat).SetString(s)
		if !valid {
			return false
		}
		return ok(x.Cmp(limit))
	}
}
