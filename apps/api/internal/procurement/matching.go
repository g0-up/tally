package procurement

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// MaxDocumentNo bounds the supplier's own invoice number.
const MaxDocumentNo = 64

// DocumentInput records a supplier's invoice. TotalAmount is goods + tax when not given;
// ReceiptIDs are matched at once.
type DocumentInput struct {
	SupplierPartyID uuid.UUID
	DocumentNo      string
	DocumentDate    *time.Time
	GoodsAmount     money.Money
	TaxAmount       money.Money
	TotalAmount     *money.Money
	Note            *string
	ReceiptIDs      []uuid.UUID
}

// DocumentDetail is a supplier document with its matched receipts and photos.
type DocumentDetail struct {
	SupplierDocument
	Receipts    []MatchedReceipt
	Attachments []Attachment
}

func validateDocument(in DocumentInput) map[string]string {
	fields := map[string]string{}
	if in.SupplierPartyID == uuid.Nil {
		fields["supplier_party_id"] = "required"
	}
	switch no := strings.TrimSpace(in.DocumentNo); {
	case no == "":
		fields["document_no"] = "required"
	case len([]rune(no)) > MaxDocumentNo:
		fields["document_no"] = "too_long"
	}
	if in.DocumentDate == nil {
		fields["document_date"] = "required"
	}
	if in.GoodsAmount.IsNegative() {
		fields["goods_amount"] = "must_not_be_negative"
	}
	if in.TaxAmount.IsNegative() {
		fields["tax_amount"] = "must_not_be_negative"
	}
	if in.TotalAmount != nil && !in.TotalAmount.Equal(in.GoodsAmount.Add(in.TaxAmount)) {
		fields["total_amount"] = "total_mismatch"
	}
	validText(fields, "note", in.Note)
	validateReceiptIDs(fields, "receipt_ids", in.ReceiptIDs)
	return fields
}

func validateReceiptIDs(fields map[string]string, name string, receiptIDs []uuid.UUID) {
	if len(receiptIDs) > MaxLines {
		fields[name] = "too_many"
		return
	}
	seen := map[uuid.UUID]bool{}
	for i, id := range receiptIDs {
		switch {
		case id == uuid.Nil:
			fields[fmt.Sprintf("%s.%d", name, i)] = "required"
		case seen[id]:
			fields[fmt.Sprintf("%s.%d", name, i)] = "duplicate"
		}
		seen[id] = true
	}
}

// CreateDocument records a supplier document. It only matches receipts: the payable of each
// receipt was recorded when it was posted, a document never adds one. With a key it runs once per
// Idempotency-Key: a retry of the same request returns the stored document (replayed true).
func (s *Service) CreateDocument(ctx context.Context, actor audit.Actor, key *idempotency.Key, in DocumentInput) (DocumentDetail, bool, error) {
	if fields := validateDocument(in); len(fields) > 0 {
		return DocumentDetail{}, false, errInvalid(fields)
	}
	var out DocumentDetail
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeDocumentCreate, key, http.StatusCreated, &out, func() error {
			if err := checkSupplier(ctx, q, in.SupplierPartyID, "supplier_party_id"); err != nil {
				return err
			}
			if err := lockReceipts(ctx, q, in.ReceiptIDs); err != nil {
				return err
			}
			now := s.clock.Now()
			d := SupplierDocument{ID: ids.New(), SupplierPartyID: in.SupplierPartyID, DocumentNo: strings.TrimSpace(in.DocumentNo),
				DocumentDate: *in.DocumentDate, GoodsAmount: in.GoodsAmount, TaxAmount: in.TaxAmount,
				TotalAmount: in.GoodsAmount.Add(in.TaxAmount), MatchStatus: MatchUnmatched, VarianceAmount: money.ZeroMoney,
				Note: in.Note, CreatedAt: now, UpdatedAt: now, Version: 1}
			if _, err := q.ExecContext(ctx, `
			INSERT INTO supplier_documents (id, tenant_id, supplier_party_id, document_no, document_date, goods_amount, tax_amount,
			  total_amount, match_status, variance_amount, note, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 1)`,
				d.ID, actor.TenantID, d.SupplierPartyID, d.DocumentNo, d.DocumentDate, d.GoodsAmount, d.TaxAmount, d.TotalAmount,
				d.MatchStatus, d.VarianceAmount, d.Note, d.CreatedAt, d.UpdatedAt); err != nil {
				if db.IsUniqueViolation(err, "ux_supplier_documents__supplier_party_id_document_no") {
					return apperr.NewConflict(CodeDocumentExists, "This supplier document number is already recorded")
				}
				return wrap("insert supplier document", err)
			}
			if err := linkReceipts(ctx, q, actor.TenantID, d, "receipt_ids", in.ReceiptIDs, now); err != nil {
				return err
			}
			var err error
			if out, err = documentDetail(ctx, q, d.ID); err != nil {
				return err
			}
			return s.record(ctx, q, actor, ScopeDocumentCreate, "supplier_document", d.ID, nil, out)
		})
		return err
	})
	return out, replayed, err
}

// MatchReceipts links more receipts of the document's supplier to it (version is the If-Match
// the client saw) and recomputes the match.
func (s *Service) MatchReceipts(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, receiptIDs []uuid.UUID) (DocumentDetail, error) {
	fields := map[string]string{}
	if len(receiptIDs) == 0 {
		fields["receipt_ids"] = "required"
	}
	validateReceiptIDs(fields, "receipt_ids", receiptIDs)
	if len(fields) > 0 {
		return DocumentDetail{}, errInvalid(fields)
	}
	var out DocumentDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if err := lockReceipts(ctx, q, receiptIDs); err != nil {
			return err
		}
		d, err := getDocument(ctx, q, id, true)
		if err != nil {
			return err
		}
		if d.Version != version {
			return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
		}
		before := d
		if err := linkReceipts(ctx, q, actor.TenantID, d, "receipt_ids", receiptIDs, s.clock.Now()); err != nil {
			return err
		}
		if out, err = documentDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "supplier_document.match", "supplier_document", id,
			map[string]any{"match_status": before.MatchStatus, "variance_amount": before.VarianceAmount},
			map[string]any{"match_status": out.MatchStatus, "variance_amount": out.VarianceAmount, "receipt_ids": receiptIDs})
	})
	return out, err
}

// lockReceipts share-locks receipts in id order before any supplier document is locked. A
// reversal locks its receipt and then looks for links, a price adjustment locks its receipt and
// then updates the documents it is matched on: taking receipts first keeps that order (no
// deadlock), and a reversal and a match of the same receipt run one after the other, so the
// reversal sees the link or the match sees the reversal.
func lockReceipts(ctx context.Context, q db.Querier, receiptIDs []uuid.UUID) error {
	if len(receiptIDs) == 0 {
		return nil
	}
	var locked []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &locked, `SELECT id FROM receipts WHERE id = ANY($1) ORDER BY id FOR SHARE`,
		sortedUnique(receiptIDs)); err != nil {
		return wrap("lock receipts", err)
	}
	return nil
}

// linkReceipts matches receiptIDs (posted receipts of the document's supplier, locked by
// lockReceipts) to d and recomputes its match. A receipt already linked to d stays as it is; one
// linked to another document is refused, a receipt is invoiced once.
func linkReceipts(ctx context.Context, q db.Querier, tenantID uuid.UUID, d SupplierDocument, field string, receiptIDs []uuid.UUID,
	now time.Time) error {
	if len(receiptIDs) == 0 {
		return nil
	}
	fields := map[string]string{}
	for i, rid := range receiptIDs {
		key := fmt.Sprintf("%s.%d", field, i)
		r, err := getReceipt(ctx, q, rid, false)
		switch {
		case apperr.KindOf(err) == apperr.NotFound:
			fields[key] = "not_found"
		case err != nil:
			return err
		case r.SupplierPartyID != d.SupplierPartyID:
			fields[key] = "other_supplier"
		case r.Status != DocPosted || r.ReversalOf != nil:
			fields[key] = "not_posted"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	var taken bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM supplier_document_receipts
		WHERE receipt_id = ANY($1) AND supplier_document_id <> $2)`, receiptIDs, d.ID).Scan(&taken); err != nil {
		return wrap("read receipt matches", err)
	}
	if taken {
		return errReceiptMatched()
	}
	for _, rid := range receiptIDs {
		// Two documents matching the same receipt at once both pass the check above (share locks do
		// not exclude each other); the unique receipt index refuses the second.
		if _, err := q.ExecContext(ctx, `
			INSERT INTO supplier_document_receipts (tenant_id, supplier_document_id, receipt_id, matched_amount)
			VALUES ($1, $2, $3, 0) ON CONFLICT (tenant_id, supplier_document_id, receipt_id) DO NOTHING`, tenantID, d.ID, rid); err != nil {
			if db.IsUniqueViolation(err, "ux_supplier_document_receipts__receipt_id") {
				return errReceiptMatched()
			}
			return wrap("link supplier document receipt", err)
		}
	}
	return refreshMatches(ctx, q, []uuid.UUID{d.ID}, now)
}

// refreshMatches recomputes documents: each linked receipt is matched at what the store owes for
// it (its total moved by its price adjustments), and a document is matched when they add up to
// its total, else it shows the variance a price adjustment resolves. Purchase returns are not
// subtracted: the supplier invoices returned goods on a separate adjustment invoice, which is
// reconciled against the return's credit note, not against the original invoice.
func refreshMatches(ctx context.Context, q db.Querier, documentIDs []uuid.UUID, now time.Time) error {
	if len(documentIDs) == 0 {
		return nil
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE supplier_document_receipts l SET matched_amount = r.total_amount + COALESCE((
		    SELECT SUM(a.payable_delta) FROM purchase_price_adjustments a
		    WHERE a.receipt_id = r.id AND a.status = 'posted' AND a.reversal_of IS NULL), 0)
		FROM receipts r WHERE r.id = l.receipt_id AND l.supplier_document_id = ANY($1)`, documentIDs); err != nil {
		return wrap("refresh matched amounts", err)
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE supplier_documents d SET
		  match_status = CASE WHEN m.n = 0 THEN 'unmatched' WHEN m.matched = d.total_amount THEN 'matched' ELSE 'variance' END,
		  variance_amount = CASE WHEN m.n = 0 THEN 0 ELSE d.total_amount - m.matched END,
		  version = d.version + 1, updated_at = $2
		FROM (SELECT x.id, COUNT(l.receipt_id) AS n, COALESCE(SUM(l.matched_amount), 0) AS matched
		      FROM supplier_documents x LEFT JOIN supplier_document_receipts l ON l.supplier_document_id = x.id
		      WHERE x.id = ANY($1) GROUP BY x.id) m
		WHERE m.id = d.id`, documentIDs, now); err != nil {
		return wrap("refresh supplier document match", err)
	}
	return nil
}

// documentsOfReceipt are the supplier documents a receipt is matched on.
func documentsOfReceipt(ctx context.Context, q db.Querier, receiptID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT supplier_document_id FROM supplier_document_receipts WHERE receipt_id = $1
		ORDER BY supplier_document_id`, receiptID); err != nil {
		return nil, wrap("read receipt supplier documents", err)
	}
	return out, nil
}
