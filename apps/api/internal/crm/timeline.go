package crm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Outbox topics the CRM projects onto the timeline and the loyalty ledger.
const (
	TopicSalePosted     = "sale.posted"
	TopicSaleReversed   = "sale.reversed"
	TopicReturnPosted   = "sales_return.posted"
	TopicPaymentPosted  = "payment.posted"
	TopicPaymentReverse = "payment.reversed"
)

// Handlers are the outbox consumers of the CRM by topic. Each is idempotent: a timeline row is
// written once per reference and kind, a loyalty entry once per source.
func (s *Service) Handlers() map[string]outbox.Handler {
	return map[string]outbox.Handler{
		TopicSalePosted:     s.handleSalePosted,
		TopicSaleReversed:   s.handleSaleReversed,
		TopicReturnPosted:   s.handleReturnPosted,
		TopicPaymentPosted:  s.handlePaymentPosted,
		TopicPaymentReverse: s.handlePaymentReversed,
	}
}

type saleEvent struct {
	SalesDocumentID uuid.UUID   `json:"sales_document_id"`
	DocNo           string      `json:"doc_no"`
	CustomerID      *uuid.UUID  `json:"customer_party_id"`
	TotalAmount     money.Money `json:"total_amount"`
	ReversalDocNo   string      `json:"reversal_doc_no"`
}

type returnEvent struct {
	SalesReturnID   uuid.UUID   `json:"sales_return_id"`
	DocNo           string      `json:"doc_no"`
	SalesDocumentID *uuid.UUID  `json:"sales_document_id"`
	CustomerID      *uuid.UUID  `json:"customer_party_id"`
	TotalAmount     money.Money `json:"total_amount"`
}

type paymentEvent struct {
	PaymentID uuid.UUID   `json:"payment_id"`
	DocNo     string      `json:"doc_no"`
	Direction string      `json:"direction"`
	PartyID   *uuid.UUID  `json:"party_id"`
	Amount    money.Money `json:"amount"`
}

func decode(msg outbox.Message, dst any) error {
	if err := json.Unmarshal(msg.Payload, dst); err != nil {
		return fmt.Errorf("crm: decode %s %s: %w", msg.Topic, msg.ID, err)
	}
	return nil
}

func (s *Service) handleSalePosted(ctx context.Context, q db.Querier, msg outbox.Message) error {
	var e saleEvent
	if err := decode(msg, &e); err != nil {
		return err
	}
	if e.CustomerID == nil {
		return nil // a walk-in sale has no customer timeline
	}
	if err := project(ctx, q, *e.CustomerID, ActivityPurchase, RefSalesDocument, e.SalesDocumentID,
		fmt.Sprintf("Mua hàng %s: %s đ", e.DocNo, e.TotalAmount.FormatVND()), msg.CreatedAt); err != nil {
		return err
	}
	return s.earn(ctx, q, e.SalesDocumentID, msg.CreatedAt)
}

func (s *Service) handleSaleReversed(ctx context.Context, q db.Querier, msg outbox.Message) error {
	var e saleEvent
	if err := decode(msg, &e); err != nil {
		return err
	}
	customer, err := saleCustomer(ctx, q, e.SalesDocumentID)
	if err != nil || customer == nil {
		return err
	}
	if err := project(ctx, q, *customer, ActivityPurchase, RefSaleReversal, e.SalesDocumentID,
		fmt.Sprintf("Huỷ %s (%s)", e.DocNo, e.ReversalDocNo), msg.CreatedAt); err != nil {
		return err
	}
	return s.reverseSale(ctx, q, e.SalesDocumentID, msg.CreatedAt)
}

func (s *Service) handleReturnPosted(ctx context.Context, q db.Querier, msg outbox.Message) error {
	var e returnEvent
	if err := decode(msg, &e); err != nil {
		return err
	}
	if e.CustomerID == nil {
		return nil
	}
	if err := project(ctx, q, *e.CustomerID, ActivityReturn, RefSalesReturn, e.SalesReturnID,
		fmt.Sprintf("Trả hàng %s: %s đ", e.DocNo, e.TotalAmount.FormatVND()), msg.CreatedAt); err != nil {
		return err
	}
	return s.reverseReturn(ctx, q, e.SalesReturnID, msg.CreatedAt)
}

func (s *Service) handlePaymentPosted(ctx context.Context, q db.Querier, msg outbox.Message) error {
	return s.projectPayment(ctx, q, msg, RefPayment)
}

func (s *Service) handlePaymentReversed(ctx context.Context, q db.Querier, msg outbox.Message) error {
	return s.projectPayment(ctx, q, msg, RefPaymentReverse)
}

// projectPayment puts a payment of a customer on the timeline: money received, a refund paid, or
// the reversal of either. Supplier payments stay off it.
func (s *Service) projectPayment(ctx context.Context, q db.Querier, msg outbox.Message, refType string) error {
	var e paymentEvent
	if err := decode(msg, &e); err != nil {
		return err
	}
	if e.PartyID == nil {
		return nil
	}
	var customer bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE party_id = $1)`, *e.PartyID).Scan(&customer); err != nil {
		return fmt.Errorf("crm: read customer: %w", err)
	}
	if !customer {
		return nil
	}
	verb := "Khách trả tiền"
	if e.Direction == "out" {
		verb = "Hoàn tiền cho khách"
	}
	summary := fmt.Sprintf("%s %s: %s đ", verb, e.DocNo, e.Amount.FormatVND())
	if refType == RefPaymentReverse {
		summary = "Huỷ phiếu " + e.DocNo
	}
	return project(ctx, q, *e.PartyID, ActivityPayment, refType, e.PaymentID, summary, msg.CreatedAt)
}

// project appends a timeline row unless the party already has one of this kind for the reference.
func project(ctx context.Context, q db.Querier, partyID uuid.UUID, kind, refType string, refID uuid.UUID, summary string, at time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO customer_activities (id, tenant_id, party_id, kind, ref_type, ref_id, summary, created_at)
		SELECT $1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (
			SELECT 1 FROM customer_activities WHERE party_id = $2 AND kind = $3 AND ref_type = $4 AND ref_id = $5)`,
		ids.New(), partyID, kind, refType, refID, summary, at); err != nil {
		return fmt.Errorf("crm: project %s %s: %w", kind, refID, err)
	}
	return nil
}

func saleCustomer(ctx context.Context, q db.Querier, docID uuid.UUID) (*uuid.UUID, error) {
	var customer *uuid.UUID
	if err := q.QueryRowxContext(ctx, `SELECT customer_party_id FROM sales_documents WHERE id = $1`, docID).Scan(&customer); err != nil {
		if db.IsNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crm: read sales document: %w", err)
	}
	return customer, nil
}

// TimelineQuery pages a customer's timeline, newest first, optionally of one kind.
type TimelineQuery struct {
	Kind  string
	After *TimelineCursor
	Limit int
}

// TimelineCursor is the position after the last row of a page.
type TimelineCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Timeline lists the activities of a customer.
func (s *Service) Timeline(ctx context.Context, scope Scope, partyID uuid.UUID, tq TimelineQuery) ([]Activity, error) {
	var out []Activity
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := partyExists(ctx, q, partyID); err != nil {
			return err
		}
		var at *time.Time
		var id *uuid.UUID
		if tq.After != nil {
			at, id = &tq.After.CreatedAt, &tq.After.ID
		}
		return sqlxSelect(ctx, q, &out, `
			SELECT id, party_id, kind, ref_type, ref_id, summary, created_by, created_at
			FROM customer_activities
			WHERE party_id = $1 AND ($2 = '' OR kind = $2)
				AND ($3::timestamptz IS NULL OR (created_at, id) < ($3, $4::uuid))
			ORDER BY created_at DESC, id DESC
			LIMIT $5`, partyID, tq.Kind, at, id, tq.Limit)
	})
	return out, err
}

// ActivityInput is a note or care call the owner writes on a customer's timeline.
type ActivityInput struct {
	Kind    string
	Summary string
}

// AddActivity records a note or a care call.
func (s *Service) AddActivity(ctx context.Context, actor audit.Actor, partyID uuid.UUID, in ActivityInput) (Activity, error) {
	fields := map[string]string{}
	if in.Kind != ActivityNote && in.Kind != ActivityCareCall {
		fields["kind"] = "invalid"
	}
	in.Summary = strings.TrimSpace(in.Summary)
	switch {
	case in.Summary == "":
		fields["summary"] = "required"
	case len([]rune(in.Summary)) > 2000:
		fields["summary"] = "too_long"
	}
	if len(fields) > 0 {
		return Activity{}, errInvalid(fields)
	}
	by := actor.ID
	a := Activity{ID: ids.New(), PartyID: partyID, Kind: in.Kind, Summary: in.Summary, CreatedBy: &by, CreatedAt: s.clock.Now()}
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if err := partyExists(ctx, q, partyID); err != nil {
			return err
		}
		return insertActivity(ctx, q, a)
	})
	return a, err
}

func insertActivity(ctx context.Context, q db.Querier, a Activity) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO customer_activities (id, tenant_id, party_id, kind, ref_type, ref_id, summary, created_by, created_at)
		VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.PartyID, a.Kind, a.RefType, a.RefID, a.Summary, a.CreatedBy, a.CreatedAt); err != nil {
		return fmt.Errorf("crm: insert activity: %w", err)
	}
	return nil
}
