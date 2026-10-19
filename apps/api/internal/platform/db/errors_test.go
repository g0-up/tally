package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestErrorClassification(t *testing.T) {
	wrap := func(code, constraint string) error {
		return fmt.Errorf("insert: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"unique named", IsUniqueViolation(wrap("23505", "ux_tenants__slug"), "ux_tenants__slug"), true},
		{"unique any", IsUniqueViolation(wrap("23505", "x"), ""), true},
		{"unique other constraint", IsUniqueViolation(wrap("23505", "x"), "ux_tenants__slug"), false},
		{"unique wrong code", IsUniqueViolation(wrap("23503", "x"), ""), false},
		{"fk", IsForeignKeyViolation(wrap("23503", "fk_a__b"), "fk_a__b"), true},
		{"check", IsCheckViolation(wrap("23514", "ck_x"), ""), true},
		{"deadlock", IsRetryable(wrap("40P01", "")), true},
		{"serialization", IsRetryable(wrap("40001", "")), true},
		{"not retryable", IsRetryable(wrap("23505", "")), false},
		{"read only tx", IsReadOnlyViolation(wrap("25006", "")), true},
		{"insufficient privilege", IsReadOnlyViolation(wrap("42501", "")), true},
		{"plain error", IsUniqueViolation(fmt.Errorf("boom"), ""), false},
		{"no rows", IsNoRows(fmt.Errorf("get: %w", sql.ErrNoRows)), true},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestUpdateWithVersionRejectsUnsafeIdentifiers(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	cases := []struct {
		table string
		sets  []Set
		want  string
	}{
		{"skus; drop table x", []Set{{"name", "a"}}, "invalid table"},
		{"skus", nil, "no columns"},
		{"skus", []Set{{"Name", "a"}}, "invalid column"},
		{"skus", []Set{{"version", 3}}, "invalid column"},
		{"skus", []Set{{"updated_at", now}}, "invalid column"},
	}
	for _, tc := range cases {
		_, err := UpdateWithVersion(ctx, nil, tc.table, uuid.New(), 1, now, tc.sets...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("UpdateWithVersion(%q, %v) error = %v, want %q", tc.table, tc.sets, err, tc.want)
		}
	}
}

func TestOpenHidesConnectionString(t *testing.T) {
	_, err := Open(context.Background(), "postgres://user:s3cret@[bad", Options{})
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("Open error = %v", err)
	}
}
