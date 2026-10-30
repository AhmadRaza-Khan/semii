package validate

import (
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// message returns the English message for a failed rule.
func (v *Validator) message(fe validator.FieldError) string {
	p := fe.Param()
	sized := isSized(fe.Kind())
	unit := func() string {
		if fe.Kind() == reflect.String {
			return " characters"
		}
		if sized {
			return " items"
		}
		return ""
	}

	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "url":
		return "must be a valid URL"
	case "uuid", "uuid4", "uuid7":
		return "must be a valid UUID"
	case "min":
		if sized {
			return "must be at least " + p + unit()
		}
		return "must be at least " + p
	case "max":
		if sized {
			return "must be at most " + p + unit()
		}
		return "must be at most " + p
	case "len":
		if sized {
			return "must be exactly " + p + unit()
		}
		return "must equal " + p
	case "gte":
		if sized {
			return "must be at least " + p + unit()
		}
		return "must be greater than or equal to " + p
	case "lte":
		if sized {
			return "must be at most " + p + unit()
		}
		return "must be less than or equal to " + p
	case "gt":
		if sized {
			return "must be more than " + p + unit()
		}
		return "must be greater than " + p
	case "lt":
		if sized {
			return "must be fewer than " + p + unit()
		}
		return "must be less than " + p
	case "oneof":
		return "must be one of: " + strings.Join(strings.Fields(p), ", ")
	case "alphanum":
		return "must contain only letters and digits"
	case "e164":
		return "must be a phone number in E.164 format"
	case "datetime":
		return "must be a datetime in format " + p
	case "unique":
		return "must not contain duplicate items"
	}
	if msg, ok := v.messages[fe.Tag()]; ok && msg != "" {
		return strings.ReplaceAll(msg, "{param}", p)
	}
	return "failed rule " + fe.Tag()
}

func isSized(k reflect.Kind) bool {
	switch k {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return true
	}
	return false
}
