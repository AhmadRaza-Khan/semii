// Package db defines the database-agnostic sentinel errors shared by the
// semii driver packages (db/pgxdb, db/sqldb, db/mongodb).
//
// Driver packages translate their native errors into these values so that
// services and the web layer can match them with errors.Is without
// depending on a particular driver.
package db

import "errors"

var (
	// ErrNotFound reports that a requested row or document does not exist.
	ErrNotFound = errors.New("not found")

	// ErrConflict reports a uniqueness or concurrency conflict.
	ErrConflict = errors.New("conflict")

	// ErrForeignKey reports a foreign-key violation: a referenced row is
	// missing, or a row is still referenced.
	ErrForeignKey = errors.New("foreign key violation")
)

// Tx marks a transaction handle. It carries no methods: each driver
// package exposes its concrete type (pgx.Tx, *sql.Tx, a mongo session
// context), and repositories accept that type in WithTx.
type Tx interface{}
