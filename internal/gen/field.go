package gen

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// typeInfo describes one field type for Postgres.
type typeInfo struct {
	sql     string // column type
	cast    string // cast used on sqlc.narg parameters
	goType  string // model and input type
	numeric bool   // min/max compare values, not lengths
}

var types = map[string]typeInfo{
	"string":  {"VARCHAR(255)", "varchar", "string", false},
	"text":    {"TEXT", "text", "string", false},
	"int":     {"INTEGER", "integer", "int32", true},
	"int64":   {"BIGINT", "bigint", "int64", true},
	"bool":    {"BOOLEAN", "boolean", "bool", false},
	"float":   {"DOUBLE PRECISION", "double precision", "float64", true},
	"decimal": {"NUMERIC(18,4)", "numeric", "decimal.Decimal", true},
	"time":    {"TIMESTAMPTZ", "timestamptz", "time.Time", false},
	"date":    {"DATE", "date", "web.Date", false},
	"uuid":    {"UUID", "uuid", "uuid.UUID", false},
	"json":    {"JSONB", "jsonb", "json.RawMessage", false},
}

// Field is one user-declared column, parsed from "name:type[:modifier...]".
type Field struct {
	Col        string // snake column and JSON name: customer_id
	Type       string // one of types, or "ref"
	Ref        *Names // referenced resource, for ref fields
	Null       bool
	Unique     bool
	Index      bool
	Default    string // raw default value, "" if none
	HasDefault bool
	Min, Max   string

	idType string // project id kind, for ref fields: uuid | bigint | objectid
	db     string // postgres | mysql | sqlite | mongo
	info   typeInfo
}

// dialectSQL maps field types to column types for MySQL and SQLite. The
// type names double as sqlc override keys (see the sqlc.yaml templates):
// SQLite uses UUID and DECIMAL_TEXT (TEXT affinity, exact decimals).
var dialectSQL = map[string]map[string]string{
	"mysql": {
		"string": "VARCHAR(255)", "text": "TEXT", "int": "INT", "int64": "BIGINT", "bool": "BOOLEAN",
		"float": "DOUBLE", "decimal": "DECIMAL(18,4)", "time": "DATETIME(6)", "date": "DATE",
		"uuid": "CHAR(36)", "json": "JSON",
	},
	"sqlite": {
		"string": "VARCHAR(255)", "text": "TEXT", "int": "INT", "int64": "BIGINT", "bool": "BOOLEAN",
		"float": "DOUBLE", "decimal": "DECIMAL_TEXT", "time": "DATETIME", "date": "DATE",
		"uuid": "UUID", "json": "JSON",
	},
}

// SetDialect adapts a parsed field to the project's database. Postgres is
// the default ParseField already produces.
func (f *Field) SetDialect(db string) error {
	f.db = db
	switch db {
	case "postgres", "":
		return nil
	case "mongo":
		if f.Ref != nil {
			f.info = typeInfo{goType: "bson.ObjectID"}
		}
		return nil
	}
	types, ok := dialectSQL[db]
	if !ok {
		return fmt.Errorf("unsupported database %q", db)
	}
	switch {
	case f.Ref != nil && f.idType == "bigint":
		f.info.sql = "BIGINT"
	case f.Ref != nil:
		f.info.sql = types["uuid"]
	case f.Type == "string" && f.Max != "":
		f.info.sql = "VARCHAR(" + f.Max + ")"
	default:
		f.info.sql = types[f.Type]
	}
	if db == "mysql" && f.Type == "text" && (f.Index || f.Unique) {
		return fmt.Errorf("field %q: MySQL cannot index text columns; use string (VARCHAR) instead", f.Col)
	}
	return nil
}

// ParseField parses "total:decimal:min=0", "customer:ref:Customer",
// "notes:text:null". idType is the project's id kind (uuid or bigint),
// used for ref columns.
func ParseField(spec, idType string) (Field, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Field{}, fmt.Errorf("field %q: expected name:type[:modifier...]", spec)
	}
	f := Field{Col: Snake(parts[0]), Type: parts[1], idType: idType}
	if f.Col == "" || !validIdent(f.Col) {
		return Field{}, fmt.Errorf("field %q: invalid name", spec)
	}
	switch f.Col {
	case "id", "created_at", "updated_at", "deleted_at":
		return Field{}, fmt.Errorf("field %q: %s is added automatically", spec, f.Col)
	}
	mods := parts[2:]
	if f.Type == "ref" {
		if len(mods) == 0 || mods[0] == "" {
			return Field{}, fmt.Errorf("field %q: ref needs a resource, e.g. customer:ref:Customer", spec)
		}
		n, err := ResourceNames(mods[0])
		if err != nil {
			return Field{}, fmt.Errorf("field %q: %w", spec, err)
		}
		f.Ref = &n
		mods = mods[1:]
		if !strings.HasSuffix(f.Col, "_id") {
			f.Col += "_id"
		}
		f.Index = true
		f.info = typeInfo{sql: "UUID", cast: "uuid", goType: "uuid.UUID"}
		if idType == "bigint" {
			f.info = typeInfo{sql: "BIGINT", cast: "bigint", goType: "int64", numeric: true}
		}
	} else if strings.HasPrefix(f.Type, "ref") {
		return Field{}, fmt.Errorf("field %q: write ref as name:ref:Resource", spec)
	} else {
		info, ok := types[f.Type]
		if !ok {
			return Field{}, fmt.Errorf("field %q: unknown type %q (string, text, int, int64, bool, float, decimal, time, date, uuid, json, ref)", spec, f.Type)
		}
		f.info = info
	}

	for _, m := range mods {
		key, val, hasVal := strings.Cut(m, "=")
		switch {
		case key == "null" && !hasVal:
			f.Null = true
		case key == "unique" && !hasVal:
			f.Unique = true
		case key == "index" && !hasVal:
			f.Index = true
		case key == "default" && hasVal:
			f.Default, f.HasDefault = val, true
		case (key == "min" || key == "max") && hasVal:
			if _, ok := new(big.Rat).SetString(val); !ok {
				return Field{}, fmt.Errorf("field %q: %s must be a number", spec, key)
			}
			if key == "min" {
				f.Min = val
			} else {
				f.Max = val
			}
		default:
			return Field{}, fmt.Errorf("field %q: unknown modifier %q (null, unique, index, default=X, min=N, max=N)", spec, m)
		}
	}
	if err := f.check(); err != nil {
		return Field{}, fmt.Errorf("field %q: %w", spec, err)
	}
	return f, nil
}

func validIdent(s string) bool {
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (f *Field) check() error {
	if (f.Min != "" || f.Max != "") && !f.info.numeric && f.Type != "string" && f.Type != "text" {
		return fmt.Errorf("min/max apply to string, text and numeric types only")
	}
	if f.Type == "string" && f.Max != "" {
		n, err := strconv.Atoi(f.Max)
		if err != nil || n < 1 {
			return fmt.Errorf("max for string must be a positive integer")
		}
		f.info.sql = fmt.Sprintf("VARCHAR(%d)", n)
	}
	if (f.Type == "json" || f.Type == "bool") && (f.Index || f.Unique) {
		return fmt.Errorf("%s columns cannot be indexed or unique", f.Type)
	}
	if f.HasDefault {
		if f.Null {
			return fmt.Errorf("default and null cannot be combined")
		}
		if _, err := f.defaultGo(); err != nil {
			return err
		}
	}
	return nil
}

// --- names ---

// GoName is the model field name (CustomerID).
func (f Field) GoName() string { return Pascal(f.Col) }

// SqlcName is the sqlc struct field name (CustomerID, ApiUrl).
func (f Field) SqlcName() string { return SqlcName(f.Col) }

// IsDate reports date fields, whose model type differs from sqlc's.
func (f Field) IsDate() bool { return f.Type == "date" }

// IsJSON reports json fields, which are never pointers.
func (f Field) IsJSON() bool { return f.Type == "json" }

// IsRef reports ref fields.
func (f Field) IsRef() bool { return f.Ref != nil }

// Filterable fields appear in ListFilter.
func (f Field) Filterable() bool { return f.Index || f.Unique }

// --- Go types ---

// GoType is the model type.
func (f Field) GoType() string {
	if f.Null && !f.IsJSON() {
		return "*" + f.info.goType
	}
	return f.info.goType
}

// JSONTag is the model's json tag value.
func (f Field) JSONTag() string {
	if f.Null {
		return f.Col + ",omitempty"
	}
	return f.Col
}

// CreateType is the CreateInput field type: pointers for nullable fields
// and fields with a default (nil means "use the default").
func (f Field) CreateType() string {
	if (f.Null || f.HasDefault) && !f.IsJSON() {
		return "*" + f.info.goType
	}
	return f.info.goType
}

// UpdateType is the UpdateInput field type: nil means unchanged; nullable
// fields use web.Optional so clients can send an explicit null.
func (f Field) UpdateType() string {
	switch {
	case f.Null:
		return "web.Optional[" + f.info.goType + "]"
	case f.IsJSON():
		return "json.RawMessage"
	}
	return "*" + f.info.goType
}

// FilterType is the ListFilter field type.
func (f Field) FilterType() string { return "*" + f.info.goType }

// --- validation ---

func (f Field) rules() []string {
	var r []string
	if f.Type == "decimal" {
		if f.Min != "" {
			r = append(r, "decimal_gte="+f.Min)
		}
		if f.Max != "" {
			r = append(r, "decimal_lte="+f.Max)
		}
		return r
	}
	if f.Min != "" {
		r = append(r, "min="+f.Min)
	}
	switch {
	case f.Max != "":
		r = append(r, "max="+f.Max)
	case f.Type == "string":
		r = append(r, "max=255")
	}
	return r
}

// Required reports whether CreateInput requires the field.
func (f Field) Required() bool {
	if f.Null || f.HasDefault {
		return false
	}
	switch f.Type {
	case "string", "text", "time", "date", "uuid", "json", "ref":
		return true
	}
	return false
}

// CreateValidate is the CreateInput validate tag ("" for none).
func (f Field) CreateValidate() string {
	rules := f.rules()
	switch {
	case f.Required():
		rules = append([]string{"required"}, rules...)
	case (f.Null || f.HasDefault) && len(rules) > 0:
		rules = append([]string{"omitempty"}, rules...)
	}
	return strings.Join(rules, ",")
}

// UpdateValidate is the UpdateInput validate tag ("" for none). Optional
// (nullable) fields are not validated by tags.
func (f Field) UpdateValidate() string {
	if f.Null {
		return ""
	}
	rules := f.rules()
	if len(rules) == 0 {
		return ""
	}
	return "omitempty," + strings.Join(rules, ",")
}

// --- SQL ---

// SQLType is the column type.
func (f Field) SQLType() string { return f.info.sql }

// Cast is the cast applied to sqlc.narg parameters.
func (f Field) Cast() string { return f.info.cast }

// ColumnDef is the full column definition for CREATE TABLE.
// MySQL ignores inline REFERENCES, so its foreign keys are table
// constraints (see ForeignKeys); MySQL also rejects defaults on TEXT.
func (f Field) ColumnDef() string {
	def := f.Col + " " + f.info.sql
	if !f.Null {
		def += " NOT NULL"
	}
	if f.HasDefault && !(f.db == "mysql" && f.Type == "text") {
		def += " DEFAULT " + f.defaultSQL()
	}
	if f.Ref != nil && f.db != "mysql" {
		def += " REFERENCES " + f.Ref.Table + " (id) ON DELETE RESTRICT"
	}
	return def
}

func (f Field) defaultSQL() string {
	switch f.Type {
	case "string", "text":
		return "'" + strings.ReplaceAll(f.Default, "'", "''") + "'"
	case "decimal":
		if f.db == "sqlite" {
			return "'" + f.Default + "'" // TEXT affinity keeps it exact
		}
	case "bool":
		if f.db == "sqlite" {
			if f.Default == "true" {
				return "1"
			}
			return "0"
		}
	}
	return f.Default
}

// DefaultGo is the Go expression for the default value.
func (f Field) DefaultGo() string {
	s, _ := f.defaultGo()
	return s
}

func (f Field) defaultGo() (string, error) {
	v := f.Default
	switch f.Type {
	case "string", "text":
		return strconv.Quote(v), nil
	case "int", "int64":
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return "", fmt.Errorf("default %q is not an integer", v)
		}
		return f.info.goType + "(" + v + ")", nil // typed: an untyped literal is int
	case "float":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return "", fmt.Errorf("default %q is not a number", v)
		}
		return "float64(" + v + ")", nil
	case "decimal":
		if _, ok := new(big.Rat).SetString(v); !ok {
			return "", fmt.Errorf("default %q is not a decimal", v)
		}
		return fmt.Sprintf("decimal.RequireFromString(%q)", v), nil
	case "bool":
		if v != "true" && v != "false" {
			return "", fmt.Errorf("default %q must be true or false", v)
		}
		return v, nil
	}
	return "", fmt.Errorf("default is not supported for %s fields", f.Type)
}

// --- conversions between model and sqlc types (only date differs) ---

// ToSqlc converts a model/input expression of this field's type (as a value
// of GoType, CreateType when ptr) into the sqlc parameter type.
func (f Field) ToSqlc(expr string, ptr bool) string {
	if f.IsJSON() && (f.db == "mysql" || f.db == "sqlite") {
		// sqldb.JSON is sent as text (MySQL JSON rejects binary strings)
		// and scans NULL, which json.RawMessage cannot.
		return "sqldb.JSON(" + expr + ")"
	}
	if !f.IsDate() {
		return expr
	}
	if ptr {
		return expr + ".TimePtr()"
	}
	return expr + ".Time"
}

// FromSqlc converts a sqlc row field into the model type.
func (f Field) FromSqlc(expr string) string {
	if f.IsJSON() && (f.db == "mysql" || f.db == "sqlite") {
		return "json.RawMessage(" + expr + ")"
	}
	if !f.IsDate() {
		return expr
	}
	if f.Null {
		return "web.DateFromPtr(" + expr + ")"
	}
	return "web.Date{Time: " + expr + "}"
}

// --- MongoDB documents ---

// DocType is the field's type in the MongoDB document struct.
func (f Field) DocType() string {
	base := f.info.goType
	switch f.Type {
	case "decimal":
		base = "bson.Decimal128"
	case "uuid":
		base = "string"
	case "date":
		base = "time.Time"
	case "json":
		return "string"
	}
	if f.Null {
		return "*" + base
	}
	return base
}

// BSONTag is the document field's bson tag.
func (f Field) BSONTag() string {
	if f.Null || f.IsJSON() {
		return f.Col + ",omitempty"
	}
	return f.Col
}

// ToDoc converts a model value (pointer when ptr) to DocType.
func (f Field) ToDoc(expr string, ptr bool) string {
	conv := map[string][2]string{
		"decimal": {"mongodb.Decimal128", "mongodb.MapPtr(%s, mongodb.Decimal128)"},
		"uuid":    {"%s.String()", "mongodb.MapPtr(%s, uuid.UUID.String)"},
		"date":    {"%s.Time", "%s.TimePtr()"},
		"json":    {"mongodb.JSONString", "mongodb.JSONString"},
	}
	c, ok := conv[f.Type]
	if !ok || f.IsRef() {
		return expr
	}
	tmpl := c[0]
	if ptr {
		tmpl = c[1]
	}
	if !strings.Contains(tmpl, "%s") {
		return tmpl + "(" + expr + ")"
	}
	return fmt.Sprintf(tmpl, expr)
}

// docIdentity reports that the document stores the model value as is.
func (f Field) docIdentity() bool {
	return f.ToDoc("x", f.Null) == "x"
}

// FromDoc converts a document field to the model type.
func (f Field) FromDoc(expr string) string {
	if f.IsRef() {
		return expr
	}
	switch f.Type {
	case "decimal":
		if f.Null {
			return "mongodb.MapPtr(" + expr + ", mongodb.Decimal)"
		}
		return "mongodb.Decimal(" + expr + ")"
	case "uuid":
		if f.Null {
			return "mongodb.MapPtr(" + expr + ", uuid.MustParse)"
		}
		return "uuid.MustParse(" + expr + ")"
	case "date":
		if f.Null {
			return "web.DateFromPtr(" + expr + ")"
		}
		return "web.Date{Time: " + expr + "}"
	case "json":
		return "mongodb.RawJSON(" + expr + ")"
	}
	return expr
}

// --- sample values for factories and tests ---

// Sample returns a valid Go expression of the field's base type. seq is
// an int64 expression that makes values unique when the field is unique.
func (f Field) Sample(seq string) string {
	switch f.Type {
	case "string", "text":
		return f.sampleString(seq, "")
	case "int", "int64":
		base := f.sampleNumber("1")
		if f.Unique {
			return fmt.Sprintf("%s(%s + %s)", f.info.goType, base, seq)
		}
		return fmt.Sprintf("%s(%s)", f.info.goType, base)
	case "float":
		return f.sampleNumber("1.5")
	case "decimal":
		return fmt.Sprintf("decimal.RequireFromString(%q)", f.sampleNumber("12.5"))
	case "bool":
		return "true"
	case "time":
		return "time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)"
	case "date":
		return "web.NewDate(2024, time.January, 2)"
	case "uuid":
		return "uuid.New()"
	case "json":
		return "json.RawMessage(`{\"key\":\"value\"}`)"
	}
	switch f.idType {
	case "bigint":
		return "int64(1)"
	case "objectid":
		return "bson.NewObjectID()"
	}
	return "uuid.New()"
}

// AltSample returns a different valid value, used by update tests.
func (f Field) AltSample(seq string) string {
	switch f.Type {
	case "string", "text":
		return f.sampleString(seq, "u")
	case "bool":
		return "false"
	}
	return f.Sample(seq)
}

// Patchable reports whether update tests can change this field to
// AltSample and observe it.
func (f Field) Patchable() bool {
	return (f.Type == "string" || f.Type == "text") && !f.Null
}

func (f Field) sampleString(seq, tag string) string {
	prefix := f.Col + tag
	minLen, maxLen := 0, 1<<30
	if f.Min != "" {
		minLen, _ = strconv.Atoi(f.Min)
	}
	if f.Max != "" {
		maxLen, _ = strconv.Atoi(f.Max)
	} else if f.Type == "string" {
		maxLen = 255
	}
	// Room for "-" and up to 9 digits of seq.
	if len(prefix)+10 > maxLen {
		prefix = strings.Repeat("s", max(1, min(maxLen-10, minLen)))
		if maxLen < 11 {
			// Too short for a sequence: a fixed value of valid length.
			return strconv.Quote(strings.Repeat("s", max(1, min(maxLen, max(minLen, 1)))))
		}
	}
	if !f.Unique {
		for len(prefix) < minLen {
			prefix += "x"
		}
		return strconv.Quote(prefix)
	}
	for len(prefix)+2 < minLen { // "-" and at least one digit follow
		prefix += "x"
	}
	return fmt.Sprintf("fmt.Sprintf(%q, %s)", prefix+"-%d", seq)
}

// sampleNumber picks a value within [min, max], preferring def.
func (f Field) sampleNumber(def string) string {
	v, _ := new(big.Rat).SetString(def)
	if f.Min != "" {
		if lo, _ := new(big.Rat).SetString(f.Min); v.Cmp(lo) < 0 {
			v = lo
		}
	}
	if f.Max != "" {
		if hi, _ := new(big.Rat).SetString(f.Max); v.Cmp(hi) > 0 {
			v = hi
		}
	}
	if f.Type == "int" || f.Type == "int64" {
		return new(big.Int).Quo(v.Num(), v.Denom()).String()
	}
	return v.FloatString(4)
}
