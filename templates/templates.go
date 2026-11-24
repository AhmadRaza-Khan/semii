// Package templates embeds the generator templates: common/ holds files
// shared by every database, postgres/, sql/ (MySQL and SQLite) and mongo/
// the database-specific ones, each split by kind (project/, resource/,
// auth/).
package templates

import "embed"

// FS holds every template. Files ending in .tmpl are text/template
// sources; generated Go is formatted with goimports afterwards.
//
//go:embed all:common all:postgres all:sql all:mongo
var FS embed.FS
