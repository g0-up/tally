package procurement

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// CodeNothingKept is returned when no line of a receipt still holds goods to carry a cost.
const CodeNothingKept = "receipt_has_no_kept_lines"

// LandedCostInput allocates a cost of bringing the goods of a receipt in (shipping, loading …) to
// its lines. Method is by_value when empty. A payee makes the cost a payable of that party, of
// which PaidNowAmount is paid at once with a PC.
type LandedCostInput struct {
	CostType        string
	Description     *string
	Amount          money.Money
	Method          string
	PayeePartyID    *uuid.UUID
	DueDate         *time.Time
	PaidNowAmount   money.Money
	PaymentMethodID *uuid.UUID
}

// LandedCostDetail is an allocation with its lines.
type LandedCostDetail struct {
	LandedCost
	Lines []SplitLine
}

func validateLandedCost(in LandedCostInput) map[string]string {
	fields := map[string]string{}
	switch in.CostType {
	case CostShipping, CostLoading, CostOther:
	case "":
		fields["cost_type"] = "required"
	default:
		fields["cost_type"] = "invalid"
	}
	switch in.Method {
	case "", MethodByValue, MethodByQty:
	default:
		fields["method"] = "invalid"
	}
	validText(fields, "description", in.Description)
	switch {
	case !in.Amount.IsPositive():
		fields["amount"] = "must_be_positive"
	case !in.Amount.Equal(in.Amount.RoundVND()):
		fields["amount"] = "not_whole_dong"
	}
	switch {
	case in.PaidNowAmount.IsNegative():
		fields["paid_now_amount"] = "must_not_be_negative"
	case !in.PaidNowAmount.IsPositive():
	case in.PayeePartyID == nil:
		fields["paid_now_amount"] = "needs_payee"
	case in.PaidNowAmount.Cmp(in.Amount) > 0:
		fields["paid_now_amount"] = "exceeds_total"
	case !in.PaidNowAmount.Equal(in.PaidNowAmount.RoundVND()):
		fields["paid_now_amount"] = "not_whole_dong"
	case in.PaymentMethodID == nil:
		fields["payment_method_id"] = "required"
	}
	if in.DueDate != nil && in.PayeePartyID == nil {
		fields["due_date"] = "needs_payee"
	}
	return fields
}

// allocationWeights are the weights of a method over the lines still holding goods: their kept
// value, or their kept quantity for by_qty and when every kept value is zero (free goods).
func allocationWeights(lines []ReceiptLine, method string) ([]money.Money, error) {
	out := make([]money.Money, len(lines))
	if method != MethodByQty {
		valued := false
		for i, l := range lines {
			v, err := keptValue(l)
			if err != nil {
				return nil, err
			}
			out[i] = v
			valued = valued || v.IsPositive()
		}
		if valued {
			return out, nil
		}
	}
	for i, l := range lines {
		w, err := money.RoundMoney(l.KeptBaseQty().Decimal())
		if err != nil {
			return nil, err
		}
		out[i] = w
	}
	return out, nil
}

// keptLines are the lines of a receipt a cost can fall on: in stock and not fully returned.
func keptLines(lines []ReceiptLine) []ReceiptLine {
	var out []ReceiptLine
	for _, l := range lines {
		if inStock(l.Condition) && l.KeptBaseQty().IsPositive() {
			out = append(out, l)
		}
	}
	return out
}

// PostLandedCost allocates a landed cost to the lines of a posted receipt (largest remainder, so
// the lines add up to the cost exactly) and raises their unit cost; the share of goods already
// sold becomes a COGS adjustment.
func (s *Service) PostLandedCost(ctx context.Context, actor audit.Actor, key idempotency.Key, receiptID uuid.UUID,
	in LandedCostInput) (posting.Response, error) {
	if fields := validateLandedCost(in); len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	if in.Method == "" {
		in.Method = MethodByValue
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	skus, err := s.receiptSkus(ctx, actor.TenantID, receiptID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, landedCostPost{s: s, date: date, receiptID: receiptID, in: in, skus: skus,
		key: ScopeLandedCost + ":" + key.Value})
}

// receiptSkus reads the SKUs of a receipt so that a posting on it locks their stock first.
func (s *Service) receiptSkus(ctx context.Context, tenantID, receiptID uuid.UUID) ([]uuid.UUID, error) {
	var skus []uuid.UUID
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		if _, err := getReceipt(ctx, q, receiptID, false); err != nil {
			return err
		}
		lines, err := receiptLines(ctx, q, receiptID)
		for _, l := range lines {
			skus = append(skus, l.SkuID)
		}
		return err
	})
	return sortedUnique(skus), err
}

type landedCostPost struct {
	s         *Service
	date      time.Time
	receiptID uuid.UUID
	in        LandedCostInput
	skus      []uuid.UUID
	key       string
}

func (c landedCostPost) Scope() string           { return ScopeLandedCost }
func (c landedCostPost) BusinessDate() time.Time { return c.date }
func (c landedCostPost) DocPrefix() string       { return "" }

func (c landedCostPost) ExtraDocPrefixes() []string {
	if c.in.PaidNowAmount.IsPositive() {
		return []string{PaymentPrefix}
	}
	return nil
}

func (c landedCostPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	return set
}

func (c landedCostPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	r, err := getReceipt(ctx, tx, c.receiptID, true)
	if err != nil {
		return posting.Result{}, err
	}
	if r.Status != DocPosted || r.ReversalOf != nil {
		return posting.Result{}, errReceiptNotPosted()
	}
	if c.in.PayeePartyID != nil {
		if err := checkParty(ctx, tx, *c.in.PayeePartyID, "payee_party_id"); err != nil {
			return posting.Result{}, err
		}
	}
	all, err := receiptLines(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}
	lines := keptLines(all)
	if len(lines) == 0 {
		return posting.Result{}, apperr.NewConflict(CodeNothingKept, "No line of the receipt still holds goods to carry the cost")
	}
	weights, err := allocationWeights(lines, c.in.Method)
	if err != nil {
		return posting.Result{}, err
	}
	shares, err := money.AllocateVND(c.in.Amount, weights)
	if err != nil {
		return posting.Result{}, err
	}
	now := tx.Now()
	lc := LandedCost{ID: ids.New(), ReceiptID: r.ID, CostType: c.in.CostType, Description: c.in.Description, Amount: c.in.Amount,
		Method: c.in.Method, PayeePartyID: c.in.PayeePartyID, BusinessDate: c.date, PostedAt: now, PostedBy: tx.Actor().ID,
		CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO landed_cost_allocations (id, tenant_id, receipt_id, cost_type, description, amount, method, payee_party_id,
		  business_date, posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		lc.ID, tx.TenantID(), lc.ReceiptID, lc.CostType, lc.Description, lc.Amount, lc.Method, lc.PayeePartyID, lc.BusinessDate,
		lc.PostedAt, lc.PostedBy, lc.CreatedAt); err != nil {
		return posting.Result{}, wrap("insert landed cost allocation", err)
	}
	splits, err := c.s.costShares(ctx, tx, DocLandedCost, lc.ID, c.date, lines, shares, nil)
	if err != nil {
		return posting.Result{}, err
	}
	for i, sp := range splits {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO landed_cost_allocation_lines (tenant_id, landed_cost_allocation_id, receipt_line_id, amount, on_hand_amount,
			  sold_amount)
			VALUES ($1, $2, $3, $4, $5, $6)`, tx.TenantID(), lc.ID, sp.ReceiptLineID, sp.Amount, sp.OnHandAmount, sp.SoldAmount); err != nil {
			return posting.Result{}, wrap("insert landed cost allocation line", err)
		}
		l := lines[i]
		landed := l.LandedCostAmount.Add(sp.Amount)
		per, err := perKeptUnit(l, sp.Amount)
		if err != nil {
			return posting.Result{}, err
		}
		cost := l.UnitCost.Add(per)
		if _, err := tx.ExecContext(ctx, `UPDATE receipt_lines SET landed_cost_amount = $2, unit_cost = $3 WHERE id = $1`,
			l.ID, landed, cost); err != nil {
			return posting.Result{}, wrap("move receipt line landed cost", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE receipts SET landed_cost_amount = landed_cost_amount + $2 WHERE id = $1`,
		r.ID, lc.Amount); err != nil {
		return posting.Result{}, wrap("move receipt landed cost", err)
	}
	if c.in.PayeePartyID != nil {
		p, err := c.s.debt.RecordPayable(ctx, tx, PayableInput{PartyID: *c.in.PayeePartyID, SourceType: DocLandedCost,
			SourceID: lc.ID, SourceDocNo: r.DocNo, Amount: lc.Amount, DueDate: c.in.DueDate})
		if err != nil {
			return posting.Result{}, err
		}
		if c.in.PaidNowAmount.IsPositive() {
			if _, err := c.s.debt.PayNow(ctx, tx, PaymentInput{PartyID: *c.in.PayeePartyID, MethodID: *c.in.PaymentMethodID,
				Amount: c.in.PaidNowAmount, SourceType: PaymentSourceLandedCost, SourceID: lc.ID, ObligationID: p.ID,
				Key: c.key + ":pay", Note: "Chi phí nhập hàng " + r.DocNo}); err != nil {
				return posting.Result{}, err
			}
		}
	}
	d := LandedCostDetail{LandedCost: lc, Lines: splits}
	tx.Audit(audit.Event{Action: "landed_cost.post", EntityType: DocLandedCost, EntityID: lc.ID, DocNo: r.DocNo, After: d})
	return posting.Result{Status: http.StatusCreated, Body: toLandedCostDTO(d)}, nil
}
