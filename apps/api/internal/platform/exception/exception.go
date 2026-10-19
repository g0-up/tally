// Package exception records and consumes exception confirmations: the owner's explicit "I
// confirm …" for an action a rule would otherwise refuse (credit limit, below-minimum price,
// period reopen …). A confirmation is bound to one kind, one document version and the SHA-256 of
// the exact command payload the owner saw; it expires after ConfirmationTTL and can be consumed
// once. Domain commands call Guard.Consume inside their own transaction, so the use is
// committed together with the action it allowed.
package exception

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// ConfirmationTTL is how long a confirmation stays usable.
const ConfirmationTTL = 30 * time.Minute

// ConfirmationField is the request body field that references a confirmation; it is left out
// of the payload hash because the payload is hashed before the confirmation exists.
const ConfirmationField = "exception_confirmation_id"

// Kind is an exception kind (exception_confirmations.kind).
type Kind string

// Exception kinds.
const (
	CreditLimitExceeded Kind = "credit_limit_exceeded" //nolint:gosec // an exception kind name, not a credential
	BelowMinPrice       Kind = "below_min_price"
	StockDisposal       Kind = "stock_disposal"
	CountVariance       Kind = "count_variance"
	DebtWriteOff        Kind = "debt_write_off"
	ExpiredLotSale      Kind = "expired_lot_sale"
	QuarantineRelease   Kind = "quarantine_release"
	PeriodReopen        Kind = "period_reopen"
)

// Kinds lists every kind.
func Kinds() []Kind {
	return []Kind{CreditLimitExceeded, BelowMinPrice, StockDisposal, CountVariance, DebtWriteOff,
		ExpiredLotSale, QuarantineRelease, PeriodReopen}
}

// Valid reports a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds(), k) }

// PayloadHash returns the SHA-256 of the canonical form of a JSON command body, the form the web
// client builds with JSON.stringify (shared/lib/canonical-hash.ts): object keys sorted by UTF-16
// code units, no insignificant whitespace, numbers kept exactly as sent, strings escaped as
// JSON.stringify escapes them (only '"', '\\' and control characters; '<', '>', '&', U+2028 and
// U+2029 stay literal), and the top-level ConfirmationField removed. A client computes the same
// digest over the body it is about to send.
func PayloadHash(body []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, apperr.NewValidation("invalid_payload", "The payload must be a JSON document", nil)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, apperr.NewValidation("invalid_payload", "The payload must be a single JSON document", nil)
	}
	if obj, ok := v.(map[string]any); ok {
		delete(obj, ConfirmationField)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return sum[:], nil
}

// writeCanonical writes a value decoded with UseNumber in canonical form.
func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	case json.Number:
		buf.WriteString(t.String())
	case string:
		writeJSString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, compareUTF16)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("exception: canonical payload: unexpected %T", v)
	}
	return nil
}

// writeJSString quotes s the way JSON.stringify does.
func writeJSString(buf *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hex[r>>4])
				buf.WriteByte(hex[r&0xf])
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
}

// compareUTF16 orders strings by UTF-16 code units, like JavaScript's < on strings. It differs
// from byte order only between characters above U+FFFF and those in U+E000..U+FFFF.
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// Confirmation is a stored exception confirmation.
type Confirmation struct {
	ID          uuid.UUID       `db:"id"`
	TenantID    uuid.UUID       `db:"tenant_id"`
	Kind        Kind            `db:"kind"`
	DocType     string          `db:"doc_type"`
	DocID       uuid.UUID       `db:"doc_id"`
	DocVersion  int             `db:"doc_version"`
	PayloadHash []byte          `db:"payload_hash"`
	Impact      json.RawMessage `db:"impact"`
	Note        *string         `db:"note"`
	ConfirmedBy uuid.UUID       `db:"confirmed_by"`
	ConfirmedAt time.Time       `db:"confirmed_at"`
}

// ExpiresAt is the end of the confirmation's validity.
func (c Confirmation) ExpiresAt() time.Time { return c.ConfirmedAt.Add(ConfirmationTTL) }

// NewConfirmation is what the owner confirmed.
type NewConfirmation struct {
	TenantID    uuid.UUID
	Kind        Kind
	DocType     string
	DocID       uuid.UUID
	DocVersion  int
	PayloadHash []byte
	Impact      json.RawMessage
	Note        string
	ConfirmedBy uuid.UUID
}

// Expectation is what a command requires of the confirmation it consumes.
type Expectation struct {
	ConfirmationID uuid.UUID
	Kind           Kind
	DocType        string
	DocID          uuid.UUID
	DocVersion     int
	PayloadHash    []byte
	// UsedByDocType and UsedByDocID name the document the confirmation is spent on.
	UsedByDocType string
	UsedByDocID   uuid.UUID
}

// Guard creates and consumes confirmations.
type Guard struct {
	clock clock.Clock
}

// NewGuard returns a guard stamping rows with clk.
func NewGuard(clk clock.Clock) *Guard { return &Guard{clock: clk} }

// Create stores a confirmation inside the caller's tenant transaction.
func (g *Guard) Create(ctx context.Context, q db.Querier, in NewConfirmation) (Confirmation, error) {
	fields := map[string]string{}
	if !in.Kind.Valid() {
		fields["kind"] = "invalid"
	}
	if in.DocType == "" || len(in.DocType) > 64 {
		fields["doc_type"] = "invalid"
	}
	if in.DocID == uuid.Nil {
		fields["doc_id"] = "required"
	}
	if in.DocVersion < 0 {
		fields["doc_version"] = "invalid"
	}
	if len(in.PayloadHash) != sha256.Size {
		fields["payload_hash"] = "invalid"
	}
	if len(in.Note) > 1000 {
		fields["note"] = "too_long"
	}
	impact := in.Impact
	if len(impact) == 0 {
		impact = json.RawMessage(`{}`)
	} else if !json.Valid(impact) {
		fields["impact"] = "invalid"
	}
	if len(fields) > 0 {
		return Confirmation{}, apperr.NewValidation("validation_failed", "The confirmation is invalid", fields)
	}
	c := Confirmation{
		ID: ids.New(), TenantID: in.TenantID, Kind: in.Kind, DocType: in.DocType, DocID: in.DocID,
		DocVersion: in.DocVersion, PayloadHash: in.PayloadHash, Impact: impact,
		ConfirmedBy: in.ConfirmedBy, ConfirmedAt: g.clock.Now().UTC(),
	}
	if in.Note != "" {
		note := in.Note
		c.Note = &note
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO exception_confirmations
		  (id, tenant_id, kind, doc_type, doc_id, doc_version, payload_hash, impact, note, confirmed_by, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		c.ID, c.TenantID, c.Kind, c.DocType, c.DocID, c.DocVersion, c.PayloadHash, []byte(c.Impact), c.Note,
		c.ConfirmedBy, c.ConfirmedAt); err != nil {
		return Confirmation{}, fmt.Errorf("exception: insert confirmation: %w", err)
	}
	return c, nil
}

// Consume spends the confirmation for want inside the caller's tenant transaction. A missing,
// expired or mismatched confirmation answers exception_required (409) for want.Kind, so the
// client asks the owner again; a confirmation that was already used answers 409
// exception_confirmation_used.
func (g *Guard) Consume(ctx context.Context, q db.Querier, tenantID uuid.UUID, want Expectation) error {
	required := apperr.NewExceptionRequired(string(want.Kind), "This action needs the owner's confirmation")
	if want.ConfirmationID == uuid.Nil {
		return required
	}
	var c Confirmation
	err := q.QueryRowxContext(ctx, `
		SELECT id, tenant_id, kind, doc_type, doc_id, doc_version, payload_hash, impact, note, confirmed_by, confirmed_at
		FROM exception_confirmations WHERE tenant_id = $1 AND id = $2`, tenantID, want.ConfirmationID).StructScan(&c)
	if db.IsNoRows(err) {
		return required
	}
	if err != nil {
		return fmt.Errorf("exception: read confirmation: %w", err)
	}
	if c.Kind != want.Kind || c.DocType != want.DocType || c.DocID != want.DocID || c.DocVersion != want.DocVersion ||
		subtle.ConstantTimeCompare(c.PayloadHash, want.PayloadHash) != 1 || !g.clock.Now().Before(c.ExpiresAt()) {
		return required
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO exception_confirmation_uses (tenant_id, confirmation_id, used_by_doc_type, used_by_doc_id, used_at)
		VALUES ($1, $2, $3, $4, $5)`, tenantID, c.ID, want.UsedByDocType, want.UsedByDocID, g.clock.Now().UTC()); err != nil {
		if db.IsUniqueViolation(err, "pk_exception_confirmation_uses") {
			return apperr.NewConflict("exception_confirmation_used", "This confirmation was already used; confirm again")
		}
		return fmt.Errorf("exception: record use: %w", err)
	}
	return nil
}
