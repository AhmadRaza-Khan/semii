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
)
