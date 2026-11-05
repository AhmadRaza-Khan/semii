// Package gen is semii's code generator: naming rules, field parsing,
// template rendering and safe file writing.
package gen

import (
	"fmt"
	"go/token"
	"strings"
	"unicode"
)

// initialisms are upper-cased in Go identifiers (customer_id -> CustomerID).
var initialisms = map[string]bool{
	"id": true, "url": true, "uri": true, "api": true, "http": true, "https": true,
	"json": true, "uuid": true, "ip": true, "sql": true, "html": true, "xml": true,
	"cpu": true, "os": true, "tls": true, "ttl": true, "utc": true, "sku": true,
}

// words splits an identifier in any common style into lower-case words:
// "OrderItem", "order_item", "order-item", "orderItem", "HTTPServer".
func words(s string) []string {
	var out []string
	var cur []rune
	rs := []rune(s)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			flush()
		case unicode.IsUpper(r):
			// Split before an upper-case rune that starts a new word: after
			// a lower-case rune or digit, or before "Xy" in "HTTPServer".
			if i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]) ||
				(unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

func capitalize(w string) string {
	if w == "" {
		return w
	}
	return strings.ToUpper(w[:1]) + w[1:]
}

// Pascal returns the exported Go name: "customer_id" -> "CustomerID".
func Pascal(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		if initialisms[w] {
			b.WriteString(strings.ToUpper(w))
		} else {
			b.WriteString(capitalize(w))
		}
	}
	return b.String()
}

// Camel returns the unexported Go name: "order_item" -> "orderItem",
// "id" -> "id", "url_path" -> "urlPath".
func Camel(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return ""
	}
	rest := Pascal(strings.Join(ws[1:], "_"))
	return ws[0] + rest
}

// Snake returns "order_item".
func Snake(s string) string { return strings.Join(words(s), "_") }

// Kebab returns "order-item".
func Kebab(s string) string { return strings.Join(words(s), "-") }

// Lower returns the package name: "order_item" -> "orderitem".
func Lower(s string) string { return strings.Join(words(s), "") }

// SqlcName mirrors sqlc's struct and field naming: words capitalized, with
// only "id" treated as an initialism ("api_url" -> "ApiUrl").
func SqlcName(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		if w == "id" {
			b.WriteString("ID")
		} else {
			b.WriteString(capitalize(w))
		}
	}
	return b.String()
}

var irregularPlurals = map[string]string{
	"person": "people", "man": "men", "woman": "women", "child": "children",
	"mouse": "mice", "goose": "geese", "foot": "feet", "tooth": "teeth",
	"ox": "oxen", "datum": "data", "index": "indices", "quiz": "quizzes",
}

var uncountable = map[string]bool{
	"equipment": true, "information": true, "rice": true, "money": true,
	"species": true, "series": true, "fish": true, "sheep": true, "news": true,
	"metadata": true, "data": true,
}

// pluralWord pluralizes one lower-case English word, consistently with the
// inflection rules sqlc uses to singularize table names.
func pluralWord(w string) string {
	if p, ok := irregularPlurals[w]; ok {
		return p
	}
	if uncountable[w] {
		return w
	}
	switch {
	case strings.HasSuffix(w, "y") && len(w) > 1 && !strings.ContainsRune("aeiou", rune(w[len(w)-2])):
		return w[:len(w)-1] + "ies"
	case strings.HasSuffix(w, "s"), strings.HasSuffix(w, "x"), strings.HasSuffix(w, "z"),
		strings.HasSuffix(w, "ch"), strings.HasSuffix(w, "sh"):
		return w + "es"
	}
	return w + "s"
}

// Plural pluralizes the last word: "order_item" -> "order_items".
func Plural(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return ""
	}
	ws[len(ws)-1] = pluralWord(ws[len(ws)-1])
	return strings.Join(ws, "_")
}

// reservedPackages are package names a resource may not take because
// generated files import packages with these names.
var reservedPackages = map[string]bool{
	"app": true, "apptest": true, "auth": true, "bytes": true, "config": true,
	"context": true, "db": true, "decimal": true, "errors": true, "factory": true,
	"fmt": true, "http": true, "json": true, "logx": true, "main": true,
	"middleware": true, "migrate": true, "pgx": true, "pgxdb": true, "pgxpool": true,
	"seeds": true, "server": true, "slog": true, "sort": true, "sqlc": true,
	"strings": true, "sync": true, "testing": true, "testkit": true, "time": true,
	"uuid": true, "validate": true, "web": true,
}

// Names holds every spelling of a resource name used by templates.
type Names struct {
	Name       string // Order, OrderItem
	Var        string // order, orderItem
	Plural     string // Orders, OrderItems
	PluralVar  string // orders, orderItems
	Pkg        string // order, orderitem
	Table      string // orders, order_items
	Route      string // /orders, /order-items
	SqlcStruct string // sqlc's row struct: Order, OrderItem (sqlc naming)
}

// ResourceNames validates a singular resource name in any case style.
func ResourceNames(input string) (Names, error) {
	ws := words(input)
	if len(ws) == 0 {
		return Names{}, fmt.Errorf("resource name is empty")
	}
	for _, w := range ws {
		for _, r := range w {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				return Names{}, fmt.Errorf("resource name %q: only letters and digits are allowed", input)
			}
		}
	}
	if unicode.IsDigit(rune(ws[0][0])) {
		return Names{}, fmt.Errorf("resource name %q must start with a letter", input)
	}
	snake := strings.Join(ws, "_")
	if pl := Plural(snake); pl == snake {
		return Names{}, fmt.Errorf("resource name %q has no distinct plural; pick a countable noun", input)
	}
	pkg := Lower(snake)
	if token.Lookup(pkg).IsKeyword() || reservedPackages[pkg] {
		return Names{}, fmt.Errorf("resource name %q would create package %q, which clashes with a Go keyword or a package semii imports", input, pkg)
	}
	plural := Plural(snake)
	return Names{
		Name: Pascal(snake), Var: Camel(snake),
		Plural: Pascal(plural), PluralVar: Camel(plural),
		Pkg: pkg, Table: plural, Route: "/" + Kebab(plural),
		SqlcStruct: SqlcName(snake),
	}, nil
}
