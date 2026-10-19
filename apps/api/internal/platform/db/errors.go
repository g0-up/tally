package db

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATE codes the application reacts to.
const (
	codeUniqueViolation       = "23505"
	codeForeignKeyViolation   = "23503"
	codeCheckViolation        = "23514"
	codeSerializationFailure  = "40001"
	codeDeadlockDetected      = "40P01"
	codeReadOnlyTransaction   = "25006"
	codeInsufficientPrivilege = "42501"
)

func pgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr, true
	}
	return nil, false
}

func hasCode(err error, code, constraint string) bool {
	pgErr, ok := pgError(err)
	return ok && pgErr.Code == code && (constraint == "" || pgErr.ConstraintName == constraint)
}

// IsUniqueViolation reports a unique violation on the named constraint ("" matches any), e.g.
// IsUniqueViolation(err, "ux_tenants__slug"). Every constraint is named in the migrations
// (pk_<table>, fk_|ux_|ck_<table>__<rule>), so these names are stable.
func IsUniqueViolation(err error, constraint string) bool {
	return hasCode(err, codeUniqueViolation, constraint)
}

// IsForeignKeyViolation reports a foreign key violation on the named constraint ("" matches any).
func IsForeignKeyViolation(err error, constraint string) bool {
	return hasCode(err, codeForeignKeyViolation, constraint)
}

// IsCheckViolation reports a CHECK violation on the named constraint ("" matches any).
func IsCheckViolation(err error, constraint string) bool {
	return hasCode(err, codeCheckViolation, constraint)
}

// IsRetryable reports a deadlock or serialization failure: the whole transaction may be retried.
func IsRetryable(err error) bool {
	return hasCode(err, codeDeadlockDetected, "") || hasCode(err, codeSerializationFailure, "")
}

// IsReadOnlyViolation reports a write attempted inside a READ ONLY transaction or without the
// privilege to write (support sessions run as tally_readonly).
func IsReadOnlyViolation(err error) bool {
	return hasCode(err, codeReadOnlyTransaction, "") || hasCode(err, codeInsufficientPrivilege, "")
}

// IsNoRows reports sql.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
