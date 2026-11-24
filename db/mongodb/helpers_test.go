package mongodb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AhmadRaza-Khan/semii/db/mongodb"
	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecimal128RoundTrip(t *testing.T) {
	for _, s := range []string{"0", "12.5", "-0.0001", "123456789012345678901234567890.1234"} {
		d := decimal.RequireFromString(s)
		if got := mongodb.Decimal(mongodb.Decimal128(d)); !got.Equal(d) {
			t.Errorf("%s -> %s", s, got)
		}
	}
	// More than 34 significant digits does not fit Decimal128.
	huge := decimal.RequireFromString(strings.Repeat("9", 40))
	if v := mongodb.Decimal128(huge); v.String() != "NaN" {
		t.Errorf("overflow: %s", v)
	}
	nan, _ := bson.ParseDecimal128("NaN")
	if !mongodb.Decimal(nan).IsZero() {
		t.Error("NaN should become zero")
	}
}

func TestMapPtrAndJSON(t *testing.T) {
	if mongodb.MapPtr[int, string](nil, func(int) string { return "x" }) != nil {
		t.Error("nil not preserved")
	}
	n := 2
	if got := mongodb.MapPtr(&n, func(i int) int { return i * 3 }); *got != 6 {
		t.Errorf("%d", *got)
	}
	raw := json.RawMessage(`{"a":1}`)
	if s := mongodb.JSONString(raw); s != `{"a":1}` || string(mongodb.RawJSON(s)) != `{"a":1}` {
		t.Errorf("%q", s)
	}
	if mongodb.RawJSON("") != nil || mongodb.JSONString(nil) != "" {
		t.Error("empty handling")
	}
}
