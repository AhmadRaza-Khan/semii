package web

import (
	"encoding"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// DecodeQuery binds URL query parameters into a T (a struct) using
// `query:"name"` field tags, then validates it.
//
// Supported field types are strings, integers, floats, bools, any
// encoding.TextUnmarshaler (time.Time as RFC 3339, uuid.UUID,
// decimal.Decimal), pointers to those (nil when absent) and slices of
// those (from repeated keys). Untagged fields and unknown parameters are
// ignored; embedded structs are bound recursively. Unparsable values return
// a 400 invalid_query error listing each offending parameter.
func DecodeQuery[T any](r *http.Request) (T, error) {
	var v T
	rv := reflect.ValueOf(&v).Elem()
	if rv.Kind() != reflect.Struct {
		panic(fmt.Sprintf("web: DecodeQuery target %T must be a struct", v))
	}
	var fields []FieldError
	bindQuery(rv, r.URL.Query(), &fields)
	if len(fields) > 0 {
		e := NewError(http.StatusBadRequest, "invalid_query", "invalid query parameters")
		e.Fields = fields
		return v, e
	}
	if err := validatorFrom(r.Context()).Struct(r.Context(), &v); err != nil {
		return v, err
	}
	return v, nil
}

func bindQuery(rv reflect.Value, q url.Values, errs *[]FieldError) {
	rt := rv.Type()
	for i := range rt.NumField() {
		sf := rt.Field(i)
		fv := rv.Field(i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct && sf.Tag.Get("query") == "" {
			bindQuery(fv, q, errs)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("query"), ",")
		if name == "" || name == "-" {
			continue
		}
		vals, ok := q[name]
		if !ok || len(vals) == 0 {
			continue
		}
		if err := setField(fv, vals); err != nil {
			*errs = append(*errs, FieldError{Field: name, Code: "invalid_query", Message: err.Error()})
		}
	}
}

func setField(fv reflect.Value, vals []string) error {
	if fv.Kind() == reflect.Slice && !fv.Type().Implements(textUnmarshalerType) &&
		!reflect.PointerTo(fv.Type()).Implements(textUnmarshalerType) {
		out := reflect.MakeSlice(fv.Type(), 0, len(vals))
		for _, s := range vals {
			ev := reflect.New(fv.Type().Elem()).Elem()
			if err := setScalar(ev, s); err != nil {
				return err
			}
			out = reflect.Append(out, ev)
		}
		fv.Set(out)
		return nil
	}
	return setScalar(fv, vals[len(vals)-1])
}

func setScalar(fv reflect.Value, s string) error {
	if fv.Kind() == reflect.Pointer {
		pv := reflect.New(fv.Type().Elem())
		if err := setScalar(pv.Elem(), s); err != nil {
			return err
		}
		fv.Set(pv)
		return nil
	}
	if fv.CanAddr() {
		if tu, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
			if err := tu.UnmarshalText([]byte(s)); err != nil {
				return fmt.Errorf("must be a valid %s", fv.Type().String())
			}
			return nil
		}
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("must be a boolean")
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("must be an integer")
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("must be a non-negative integer")
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("must be a number")
		}
		fv.SetFloat(f)
	default:
		panic(fmt.Sprintf("web: DecodeQuery: unsupported field type %s", fv.Type()))
	}
	return nil
}
