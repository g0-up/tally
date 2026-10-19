package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Set is one column assignment of a versioned update.
type Set struct {
	Column string
	Value  any
}

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// UpdateWithVersion applies sets to the row id of table only if its version still equals
// expected (the ETag the client saw), bumping version and updated_at. It returns the new version.
// No matching id → NotFound; a different version → PreconditionFailed (412). Table and column
// names are code constants and are validated as plain identifiers.
func UpdateWithVersion(ctx context.Context, q Querier, table string, id uuid.UUID, expected int, now time.Time, sets ...Set) (int, error) {
	if !identifier.MatchString(table) {
		return 0, fmt.Errorf("versioned update: invalid table name %q", table)
	}
	if len(sets) == 0 {
		return 0, fmt.Errorf("versioned update of %s: no columns to set", table)
	}
	parts := make([]string, 0, len(sets)+2)
	args := make([]any, 0, len(sets)+3)
	for _, s := range sets {
		if !identifier.MatchString(s.Column) || s.Column == "version" || s.Column == "updated_at" || s.Column == "id" {
			return 0, fmt.Errorf("versioned update of %s: invalid column %q", table, s.Column)
		}
		args = append(args, s.Value)
		parts = append(parts, fmt.Sprintf("%s = $%d", s.Column, len(args)))
	}
	args = append(args, now, id, expected)
	n := len(args)
	parts = append(parts, "version = version + 1", fmt.Sprintf("updated_at = $%d", n-2))
	query := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d AND version = $%d RETURNING version",
		table, strings.Join(parts, ", "), n-1, n)

	var version int
	err := q.QueryRowxContext(ctx, query, args...).Scan(&version)
	if err == nil {
		return version, nil
	}
	if !IsNoRows(err) {
		return 0, fmt.Errorf("versioned update of %s: %w", table, err)
	}
	var exists bool
	existsQuery := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)", table)
	if err := q.QueryRowxContext(ctx, existsQuery, id).Scan(&exists); err != nil {
		return 0, fmt.Errorf("versioned update of %s: %w", table, err)
	}
	if !exists {
		return 0, apperr.NewNotFound("not_found", "The record does not exist")
	}
	return 0, apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
}
