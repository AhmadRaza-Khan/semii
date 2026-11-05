// Package templates embeds the generator templates, organised by database
// (postgres/, later mysql/, sqlite/, mongo/) and kind (project/, resource/).
package templates

import "embed"

// FS holds every template. Files ending in .tmpl are text/template
// sources; generated Go is formatted with goimports afterwards.
//
//go:embed all:postgres
var FS embed.FS
