package debt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// DocumentSettlement is a settlement with the number of the document it settled.
type DocumentSettlement struct {
	Settlement
	ObligationDocNo string `db:"obligation_doc_no"`
}

// documentSettlements are every settlement a payment, DN or BT wrote, reversals included, oldest
// first.
func documentSettlements(ctx context.Context, q db.Querier, byType string, byID uuid.UUID) ([]DocumentSettlement, error) {
	var out []DocumentSettlement
	if err := selectAll(ctx, q, &out, `SELECT `+settlementColumns+`, o.source_doc_no AS obligation_doc_no
		FROM obligation_settlements s JOIN obligations o ON o.id = s.obligation_id
		WHERE s.settled_by_type = $1 AND s.settled_by_id = $2 ORDER BY s.created_at, s.id`, byType, byID); err != nil {
		return nil, fmt.Errorf("debt: read document settlements: %w", err)
	}
	return out, nil
}

// PaymentFilter narrows the payment list. DocNo finds one document by its exact number.
type PaymentFilter struct {
	Direction string
	PartyID   *uuid.UUID
	Status    string
	DocNo     string
	// Unallocated keeps the confirmed payments that still hold an advance.
	Unallocated bool
	// After is the keyset position (posted_at, id) of the last row of the previous page.
	AfterPostedAt *time.Time
	AfterID       uuid.UUID
	Limit         int
}

// ListPayments lists payments with their party and method names, newest first.
func (s *Service) ListPayments(ctx context.Context, scope Scope, f PaymentFilter) ([]PendingPayment, error) {
	var out []PendingPayment
	err := s.read(ctx, scope, func(q db.Querier) error {
		return selectAll(ctx, q, &out, `SELECT `+paymentColumns+`, pa.name AS party_name, m.name AS method_name
			FROM payments p LEFT JOIN parties pa ON pa.id = p.party_id JOIN payment_methods m ON m.id = p.payment_method_id
			WHERE ($1 = '' OR p.direction = $1) AND ($2::uuid IS NULL OR p.party_id = $2) AND ($3 = '' OR p.status = $3)
			  AND ($4 = '' OR p.doc_no = $4)
			  AND (NOT $5 OR (p.status = 'confirmed' AND p.allocated_amount < p.amount))
			  AND ($6::timestamptz IS NULL OR (p.posted_at, p.id) < ($6, $7::uuid))
			ORDER BY p.posted_at DESC, p.id DESC LIMIT $8`,
			f.Direction, f.PartyID, f.Status, strings.TrimSpace(f.DocNo), f.Unallocated, f.AfterPostedAt, f.AfterID, f.Limit)
	})
	if err != nil {
		return nil, fmt.Errorf("debt: list payments: %w", err)
	}
	return out, nil
}

// PaymentDetail is one payment with its party and method names and every settlement it wrote.
type PaymentDetail struct {
	PendingPayment
	Settlements []DocumentSettlement
}

// PaymentByID returns a payment of the tenant with its settlements.
func (s *Service) PaymentByID(ctx context.Context, scope Scope, id uuid.UUID) (PaymentDetail, error) {
	var out PaymentDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		err := getInto(ctx, q, &out.PendingPayment, `SELECT `+paymentColumns+`, pa.name AS party_name, m.name AS method_name
			FROM payments p LEFT JOIN parties pa ON pa.id = p.party_id JOIN payment_methods m ON m.id = p.payment_method_id
			WHERE p.id = $1`, id)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("debt: read payment: %w", err)
		}
		out.Settlements, err = documentSettlements(ctx, q, SettledByPayment, id)
		return err
	})
	return out, err
}

// DocumentFilter narrows the DN and BT lists. DocNo finds one document by its exact number.
type DocumentFilter struct {
	PartyID *uuid.UUID
	DocNo   string
	// After is the keyset position (posted_at, id) of the last row of the previous page.
	AfterPostedAt *time.Time
	AfterID       uuid.UUID
	Limit         int
}

const adjustmentColumns = `a.id, a.tenant_id, a.doc_no, a.party_id, a.direction, a.kind, a.amount, a.target_obligation_id,
	a.reason_id, a.opening_cutoff_date, a.business_date, a.note, a.posted_at, a.posted_by, a.created_at`

const offsetColumns = `b.id, b.tenant_id, b.doc_no, b.party_id, b.amount, b.receivable_obligation_id, b.payable_obligation_id,
	b.attachment_id, b.business_date, b.note, b.posted_at, b.posted_by, b.created_at`

// AdjustmentRow is a DN with its party, the number of the document it reduces and its reason.
type AdjustmentRow struct {
	Adjustment
	PartyName   string  `db:"party_name"`
	TargetDocNo *string `db:"target_doc_no"`
	ReasonName  string  `db:"reason_name"`
}

const adjustmentRowQuery = `SELECT ` + adjustmentColumns + `, pa.name AS party_name, t.source_doc_no AS target_doc_no,
		r.name AS reason_name
	FROM debt_adjustments a JOIN parties pa ON pa.id = a.party_id JOIN reasons r ON r.id = a.reason_id
	LEFT JOIN obligations t ON t.id = a.target_obligation_id`

// ListAdjustments lists DN, newest first.
func (s *Service) ListAdjustments(ctx context.Context, scope Scope, f DocumentFilter) ([]AdjustmentRow, error) {
	var out []AdjustmentRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		return selectAll(ctx, q, &out, adjustmentRowQuery+`
			WHERE ($1::uuid IS NULL OR a.party_id = $1) AND ($2 = '' OR a.doc_no = $2)
			  AND ($3::timestamptz IS NULL OR (a.posted_at, a.id) < ($3, $4::uuid))
			ORDER BY a.posted_at DESC, a.id DESC LIMIT $5`,
			f.PartyID, strings.TrimSpace(f.DocNo), f.AfterPostedAt, f.AfterID, f.Limit)
	})
	if err != nil {
		return nil, fmt.Errorf("debt: list debt adjustments: %w", err)
	}
	return out, nil
}

// AdjustmentDetail is a DN with the document it opened (increase, opening balance) and every
// settlement it wrote (decrease, write-off, dispute settlement).
type AdjustmentDetail struct {
	AdjustmentRow
	Obligation  *Obligation
	Settlements []DocumentSettlement
}

// AdjustmentByID returns a DN of the tenant.
func (s *Service) AdjustmentByID(ctx context.Context, scope Scope, id uuid.UUID) (AdjustmentDetail, error) {
	var out AdjustmentDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		err := getInto(ctx, q, &out.AdjustmentRow, adjustmentRowQuery+` WHERE a.id = $1`, id)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("debt: read debt adjustment: %w", err)
		}
		var obs []Obligation
		if err := selectAll(ctx, q, &obs, `SELECT `+obligationColumns+` FROM obligations o
			WHERE o.source_type IN ($1, $2) AND o.source_id = $3`, SourceDebtAdjustment, SourceOpeningBalance, id); err != nil {
			return fmt.Errorf("debt: read debt adjustment document: %w", err)
		}
		if len(obs) > 0 {
			out.Obligation = &obs[0]
		}
		out.Settlements, err = documentSettlements(ctx, q, SettledByDebtAdjustment, id)
		return err
	})
	return out, err
}

// OffsetRow is a BT with its party and the numbers of the two documents it settles.
type OffsetRow struct {
	Offset
	PartyName       string `db:"party_name"`
	ReceivableDocNo string `db:"receivable_doc_no"`
	PayableDocNo    string `db:"payable_doc_no"`
}

const offsetRowQuery = `SELECT ` + offsetColumns + `, pa.name AS party_name, ro.source_doc_no AS receivable_doc_no,
		po.source_doc_no AS payable_doc_no
	FROM debt_offsets b JOIN parties pa ON pa.id = b.party_id
	JOIN obligations ro ON ro.id = b.receivable_obligation_id JOIN obligations po ON po.id = b.payable_obligation_id`

// ListOffsets lists BT, newest first.
func (s *Service) ListOffsets(ctx context.Context, scope Scope, f DocumentFilter) ([]OffsetRow, error) {
	var out []OffsetRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		return selectAll(ctx, q, &out, offsetRowQuery+`
			WHERE ($1::uuid IS NULL OR b.party_id = $1) AND ($2 = '' OR b.doc_no = $2)
			  AND ($3::timestamptz IS NULL OR (b.posted_at, b.id) < ($3, $4::uuid))
			ORDER BY b.posted_at DESC, b.id DESC LIMIT $5`,
			f.PartyID, strings.TrimSpace(f.DocNo), f.AfterPostedAt, f.AfterID, f.Limit)
	})
	if err != nil {
		return nil, fmt.Errorf("debt: list debt offsets: %w", err)
	}
	return out, nil
}

// OffsetDetail is a BT with every settlement it wrote.
type OffsetDetail struct {
	OffsetRow
	Settlements []DocumentSettlement
}

// OffsetByID returns a BT of the tenant.
func (s *Service) OffsetByID(ctx context.Context, scope Scope, id uuid.UUID) (OffsetDetail, error) {
	var out OffsetDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		err := getInto(ctx, q, &out.OffsetRow, offsetRowQuery+` WHERE b.id = $1`, id)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("debt: read debt offset: %w", err)
		}
		out.Settlements, err = documentSettlements(ctx, q, SettledByDebtOffset, id)
		return err
	})
	return out, err
}
