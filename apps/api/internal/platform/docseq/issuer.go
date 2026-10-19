// Package docseq issues gap-free document numbers per tenant and prefix ("PN-0001"). The
// counter row is locked by the UPDATE until the posting transaction ends, so a rolled-back
// posting releases its number and the next posting reuses it.
package docseq

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

var prefixPattern = regexp.MustCompile(`^[A-Z]{2,3}$`)

// ValidPrefix reports a prefix of two or three upper-case ASCII letters.
func ValidPrefix(prefix string) bool { return prefixPattern.MatchString(prefix) }

// Format renders a document number: PREFIX-NNNN, zero-padded to four digits and growing past 9999.
func Format(prefix string, n int64) string { return fmt.Sprintf("%s-%04d", prefix, n) }

// Issuer hands out document numbers inside the caller's transaction.
type Issuer struct{}

// NewIssuer returns an issuer.
func NewIssuer() *Issuer { return &Issuer{} }

// Next returns the next number for prefix. Call it inside the posting transaction, before any
// other row lock (doc_sequences comes first in the global lock order).
func (*Issuer) Next(ctx context.Context, q db.Querier, tenantID uuid.UUID, prefix string) (string, error) {
	if !ValidPrefix(prefix) {
		return "", apperr.NewValidation("invalid_doc_prefix", "Document prefix must be 2–3 upper-case letters", nil)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO doc_sequences (tenant_id, prefix, next_value) VALUES ($1, $2, 1)
		ON CONFLICT (tenant_id, prefix) DO NOTHING`, tenantID, prefix); err != nil {
		return "", fmt.Errorf("init doc sequence %s: %w", prefix, err)
	}
	var n int64
	if err := q.QueryRowxContext(ctx, `
		UPDATE doc_sequences SET next_value = next_value + 1
		WHERE tenant_id = $1 AND prefix = $2
		RETURNING next_value - 1`, tenantID, prefix).Scan(&n); err != nil {
		return "", fmt.Errorf("next doc number %s: %w", prefix, err)
	}
	return Format(prefix, n), nil
}
