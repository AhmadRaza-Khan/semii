package validate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

type address struct {
	City string `json:"city" validate:"required"`
}

type item struct {
	Qty int `json:"qty" validate:"gte=1"`
}

type input struct {
	Email    string           `json:"email" validate:"required,email"`
	Name     string           `json:"name" validate:"notblank,max=5"`
	Total    decimal.Decimal  `json:"total" validate:"decimal_gte=0"`
	Discount *decimal.Decimal `json:"discount" validate:"omitempty,decimal_lte=100"`
	Password string           `json:"password" validate:"password"`
	Status   string           `json:"status" validate:"oneof=open closed" msg:"pick open or closed"`
	Address  address          `json:"address"`
	Items    []item           `json:"items" validate:"dive"`
	Ignored  string           `json:"-" validate:"required"`
	NoTag    string           `validate:"required"`
}

func valid() input {
	return input{
		Email: "a@b.co", Name: "bob", Total: decimal.NewFromInt(1),
		Password: "longenough", Status: "open", Address: address{City: "x"},
		Items: []item{{Qty: 1}}, Ignored: "x", NoTag: "x",
	}
}

func fields(t *testing.T, err error) map[string]FieldError {
	t.Helper()
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("want Errors, got %T %v", err, err)
	}
	out := map[string]FieldError{}
	for _, fe := range errs {
		out[fe.Field] = fe
	}
	return out
}

func TestStructValid(t *testing.T) {
	in := valid()
	if err := Default.Struct(context.Background(), &in); err != nil {
		t.Fatal(err)
	}
	if err := Default.Struct(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestStructErrors(t *testing.T) {
	neg := decimal.NewFromInt(101)
	in := valid()
	in.Email = "nope"
	in.Name = "   "
	in.Total = decimal.NewFromInt(-1)
	in.Discount = &neg
	in.Password = "short"
	in.Status = "x"
	in.Address.City = ""
	in.Items = []item{{Qty: 1}, {Qty: 0}}
	in.NoTag = ""

	got := fields(t, Default.Struct(context.Background(), in))
	want := map[string]struct{ code, msg string }{
		"email":        {"email", "must be a valid email"},
		"name":         {"notblank", "must not be blank"},
		"total":        {"decimal_gte", "must be greater than or equal to 0"},
		"discount":     {"decimal_lte", "must be less than or equal to 100"},
		"password":     {"password", "must be between 8 and 128 characters"},
		"status":       {"oneof", "pick open or closed"},
		"address.city": {"required", "is required"},
		"items[1].qty": {"gte", "must be greater than or equal to 1"},
		"NoTag":        {"required", "is required"},
	}
	for f, w := range want {
		fe, ok := got[f]
		if !ok {
			t.Errorf("missing error for %q (got %v)", f, got)
			continue
		}
		if fe.Code != w.code || fe.Message != w.msg {
			t.Errorf("%s: got (%s, %q) want (%s, %q)", f, fe.Code, fe.Message, w.code, w.msg)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d errors, want %d: %v", len(got), len(want), got)
	}
}

func TestQueryTagName(t *testing.T) {
	type q struct {
		Limit int `query:"limit" json:"lim" validate:"max=10"`
	}
	got := fields(t, Default.Struct(context.Background(), q{Limit: 11}))
	if fe, ok := got["limit"]; !ok || fe.Message != "must be at most 10" || fe.Param != "10" {
		t.Fatalf("got %v", got)
	}
}

func TestSizedMessages(t *testing.T) {
	type s struct {
		Name string   `json:"name" validate:"min=3"`
		Tags []string `json:"tags" validate:"max=1"`
	}
	got := fields(t, Default.Struct(context.Background(), s{Name: "a", Tags: []string{"a", "b"}}))
	if got["name"].Message != "must be at least 3 characters" {
		t.Errorf("name: %q", got["name"].Message)
	}
	if got["tags"].Message != "must be at most 1 items" {
		t.Errorf("tags: %q", got["tags"].Message)
	}
}

func TestNonStruct(t *testing.T) {
	var nilPtr *input
	for _, v := range []any{[]int{1}, 3, nilPtr, map[string]int{}} {
		if err := Default.Struct(context.Background(), v); err != nil {
			t.Errorf("%T: %v", v, err)
		}
	}
}

func TestRegister(t *testing.T) {
	v := New(WithPasswordLength(4, 6))
	err := v.Register("even", func(_ context.Context, value any, _ string) bool {
		n, ok := value.(int)
		return ok && n%2 == 0
	}, "must be even, like {param}")
	if err != nil {
		t.Fatal(err)
	}
	type s struct {
		N  int    `json:"n" validate:"even=2"`
		PW string `json:"pw" validate:"password"`
	}
	got := fields(t, v.Struct(context.Background(), s{N: 3, PW: "abcdefg"}))
	if got["n"].Message != "must be even, like 2" {
		t.Errorf("n: %q", got["n"].Message)
	}
	if got["pw"].Message != "must be between 4 and 6 characters" {
		t.Errorf("pw: %q", got["pw"].Message)
	}
	if err := v.Register("", nil, ""); err == nil {
		t.Error("want error for empty registration")
	}
}

func TestErrorsString(t *testing.T) {
	e := Errors{{Field: "a", Message: "is required"}, {Field: "b", Message: "x"}}
	if s := e.Error(); !strings.Contains(s, "a: is required; b: x") {
		t.Error(s)
	}
	if (Errors{}).Error() != "validation failed" {
		t.Error("empty")
	}
}

func TestBuiltinMessages(t *testing.T) {
	type s struct {
		URL   string   `json:"url" validate:"url"`
		ID    string   `json:"id" validate:"uuid"`
		Min   int      `json:"min" validate:"min=5"`
		Max   int      `json:"max" validate:"max=1"`
		Len   string   `json:"len" validate:"len=2"`
		LenN  int      `json:"len_n" validate:"len=2"`
		Gte   string   `json:"gte" validate:"gte=3"`
		Lte   int      `json:"lte" validate:"lte=1"`
		LteS  []int    `json:"lte_s" validate:"lte=1"`
		Gt    int      `json:"gt" validate:"gt=1"`
		GtS   string   `json:"gt_s" validate:"gt=1"`
		Lt    int      `json:"lt" validate:"lt=1"`
		LtS   string   `json:"lt_s" validate:"lt=1"`
		Alnum string   `json:"alnum" validate:"alphanum"`
		Phone string   `json:"phone" validate:"e164"`
		Date  string   `json:"date" validate:"datetime=2006-01-02"`
		Tags  []string `json:"tags" validate:"unique"`
		Hex   string   `json:"hex" validate:"hexadecimal"`
		Dec   string   `json:"dec" validate:"decimal_gte=x"`
		DecI  int      `json:"dec_i" validate:"decimal_gte=0"`
		DecS  string   `json:"dec_s" validate:"decimal_gte=0"`
		DecB  string   `json:"dec_b" validate:"decimal_lte=0"`
		Blank int      `json:"blank" validate:"notblank"`
		PW    int      `json:"pw" validate:"password"`
		Gte2  int      `json:"gte2" validate:"gte=3"`
		Min2  string   `json:"min2" validate:"min=2"`
		Max2  string   `json:"max2" validate:"max=1"`
	}
	in := s{
		URL: "nope", ID: "x", Min: 1, Max: 2, Len: "a", LenN: 3, Gte: "a", Lte: 2, LteS: []int{1, 2},
		Gt: 1, GtS: "a", Lt: 1, LtS: "ab", Alnum: "a-b", Phone: "123", Date: "2020", Tags: []string{"a", "a"},
		Hex: "zz", Dec: "1", DecS: "-1", DecB: "1.5.5", Min2: "a", Max2: "ab",
	}
	want := map[string]string{
		"url": "must be a valid URL", "id": "must be a valid UUID",
		"min": "must be at least 5", "max": "must be at most 1",
		"len": "must be exactly 2 characters", "len_n": "must equal 2",
		"gte": "must be at least 3 characters", "lte": "must be less than or equal to 1",
		"lte_s": "must be at most 1 items", "gt": "must be greater than 1",
		"gt_s": "must be more than 1 characters", "lt": "must be less than 1",
		"lt_s": "must be fewer than 1 characters", "alnum": "must contain only letters and digits",
		"phone": "must be a phone number in E.164 format", "date": "must be a datetime in format 2006-01-02",
		"tags": "must not contain duplicate items", "hex": "failed rule hexadecimal",
		"dec": "must be greater than or equal to x", "dec_i": "must be greater than or equal to 0",
		"dec_s": "must be greater than or equal to 0", "dec_b": "must be less than or equal to 0",
		"blank": "must not be blank", "pw": "must be between 8 and 128 characters",
		"gte2": "must be greater than or equal to 3", "min2": "must be at least 2 characters",
		"max2": "must be at most 1 characters",
	}
	got := fields(t, Default.Struct(context.Background(), in))
	for f, msg := range want {
		if got[f].Message != msg {
			t.Errorf("%s: got %q want %q", f, got[f].Message, msg)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d errors want %d", len(got), len(want))
	}
}
