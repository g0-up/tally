package sales

import (
	"context"
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
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// ExchangeInput is an exchange (DH): goods of a sales document come back and new goods go out to
// the same customer in one posting. The returned value first clears what is still owed on the
// original sale, then pays for the new goods; the difference is paid by the customer or refunded
// by the store with MethodID. A walk-in customer settles the difference at once; a named customer
// may leave it on their account. The Expected amounts are what the client showed the customer; a
// posting that works out differently is refused with 409 exchange_changed.
type ExchangeInput struct {
	OriginalDocumentID uuid.UUID         `json:"original_sales_document_id"`
	ReasonID           uuid.UUID         `json:"reason_id"`
	ReturnLines        []ReturnLineInput `json:"return_lines"`
	DraftInput
	MethodID             *uuid.UUID   `json:"payment_method_id,omitempty"`
	ExpectedCustomerPays *money.Money `json:"expected_customer_pays_amount,omitempty"`
	ExpectedStoreRefunds *money.Money `json:"expected_store_refunds_amount,omitempty"`
}

// ExchangeSplit is how the value of an exchange settles: Returned is the value of the goods
// brought back, Taken the total of the new sale; DebtReduction is what the return settles of what
// was still owed on the original sale. The rest of the return's value pays the new sale and what
// is left is refunded, so Returned - DebtReduction - StoreRefunds + CustomerPays = Taken, and at
// most one of CustomerPays and StoreRefunds is not zero.
type ExchangeSplit struct {
	Returned      money.Money `json:"returned_amount"`
	Taken         money.Money `json:"taken_amount"`
	DebtReduction money.Money `json:"debt_reduction_amount"`
	CustomerPays  money.Money `json:"customer_pays_amount"`
	StoreRefunds  money.Money `json:"store_refunds_amount"`
}

func (a ExchangeSplit) equal(b ExchangeSplit) bool {
	return a.Returned.Equal(b.Returned) && a.Taken.Equal(b.Taken) && a.DebtReduction.Equal(b.DebtReduction) &&
		a.CustomerPays.Equal(b.CustomerPays) && a.StoreRefunds.Equal(b.StoreRefunds)
}

// ExchangePreview is an exchange worked out without posting it, with the line the cashier reads
// to the customer.
type ExchangePreview struct {
	ExchangeSplit
	Message     string       `json:"message"`
	ReturnLines []ReturnLine `json:"return_lines"`
	Lines       []Line       `json:"lines"`
}

// ExchangePosted is a posted exchange: the DH, its TH and BH, and the payment that settled the
// difference, if any.
type ExchangePosted struct {
	Exchange Exchange       `json:"exchange"`
	Return   SalesReturn    `json:"sales_return"`
	Document DocumentDetail `json:"sales_document"`
	Payment  *Payment       `json:"payment"`
}

// ExchangeFilter narrows the exchange list.
type ExchangeFilter struct {
	OriginalDocumentID *uuid.UUID
	CustomerID         *uuid.UUID
	Page               ListPage
}

// exchangeSplit settles a return of value returned against a new sale of taken. A named customer's
// return first clears what is open on the original sale (open), then pays the new sale; what is
// left is refunded. A walk-in customer owes nothing, so the two values offset each other.
func exchangeSplit(returned, taken, open money.Money, walkIn bool) ExchangeSplit {
	out := ExchangeSplit{Returned: returned, Taken: taken}
	if walkIn {
		offset := minMoney(returned, taken)
		out.CustomerPays = taken.Sub(offset)
		out.StoreRefunds = returned.Sub(offset)
		return out
	}
	if open.IsNegative() {
		open = money.ZeroMoney
	}
	original := minMoney(returned, open)
	onNew := minMoney(returned.Sub(original), taken)
	out.DebtReduction = original
	out.CustomerPays = taken.Sub(onNew)
	out.StoreRefunds = returned.Sub(original).Sub(onNew)
	return out
}

// exchangeMessage is what the cashier tells the customer about the difference.
func exchangeMessage(sp ExchangeSplit) string {
	switch {
	case sp.CustomerPays.IsPositive():
		return "Khách trả thêm " + formatVND(sp.CustomerPays)
	case sp.StoreRefunds.IsPositive():
		return "Cửa hàng trả lại khách " + formatVND(sp.StoreRefunds)
	}
	return "Không phát sinh thu chi"
}

// formatVND writes a whole-dong amount with spaces between thousands ("1 250 000").
func formatVND(m money.Money) string {
	s := m.RoundVND().Decimal().StringFixed(0)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}

func validateExchange(in ExchangeInput) error {
	fields := map[string]string{}
	if in.OriginalDocumentID == uuid.Nil {
		fields["original_sales_document_id"] = "required"
	}
	if in.ReasonID == uuid.Nil {
		fields["reason_id"] = "required"
	}
	validateReturnLines(fields, "return_lines", true, in.ReturnLines)
	mergeFields(fields, validateDraft(in.DraftInput))
	if in.ExpectedCustomerPays != nil && in.ExpectedCustomerPays.IsNegative() {
		fields["expected_customer_pays_amount"] = "negative"
	}
	if in.ExpectedStoreRefunds != nil && in.ExpectedStoreRefunds.IsNegative() {
		fields["expected_store_refunds_amount"] = "negative"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// exchangePlan is an exchange worked out against the current data.
type exchangePlan struct {
	ret   returnPlan
	built builtDoc
	// orig is the receivable of the original sale, if it has one.
	orig  *Obligation
	split ExchangeSplit
}

// planExchange works an exchange out on date; lock reads the original sale's lines for update.
func (s *Service) planExchange(ctx context.Context, q db.Querier, tenantID uuid.UUID, date time.Time, in ExchangeInput,
	lock bool,
) (exchangePlan, error) {
	ret, err := s.planReturn(ctx, q, &in.OriginalDocumentID, nil, in.ReasonID, in.ReturnLines, "return_lines", lock)
	if err != nil {
		return exchangePlan{}, err
	}
	doc := ret.Doc
	built, err := s.buildLines(ctx, q, tenantID, lineBuild{PartyID: doc.CustomerID, Date: date, Draft: in.DraftInput})
	if err != nil {
		return exchangePlan{}, err
	}
	if err := s.applyColors(ctx, q, built.Lines, built.Origin, colorAutoPick); err != nil {
		return exchangePlan{}, err
	}
	orig, err := s.debt.ReceivableOf(ctx, q, doc.ID)
	if err != nil {
		return exchangePlan{}, err
	}
	open := money.ZeroMoney
	if orig != nil {
		open = orig.Open()
	}
	return exchangePlan{
		ret: ret, built: built, orig: orig, split: exchangeSplit(ret.Total, built.Totals.Total, open, doc.PayerID == nil),
	}, nil
}

// checkSettlement refuses an exchange whose difference differs from what the customer was shown
// or that a walk-in customer cannot settle without a payment method.
func checkSettlement(in ExchangeInput, p exchangePlan) error {
	sp := p.split
	if (in.ExpectedCustomerPays != nil && !in.ExpectedCustomerPays.Equal(sp.CustomerPays)) ||
		(in.ExpectedStoreRefunds != nil && !in.ExpectedStoreRefunds.Equal(sp.StoreRefunds)) {
		return errExchangeChanged(sp)
	}
	if p.ret.Doc.PayerID == nil && in.MethodID == nil {
		switch {
		case sp.CustomerPays.IsPositive():
			return apperr.NewValidation(CodeWalkInPaysInFull, "A walk-in customer pays the difference at once; choose how",
				map[string]string{"payment_method_id": "required"})
		case sp.StoreRefunds.IsPositive():
			return refundRequired("payment_method_id")
		}
	}
	return nil
}

func errExchangeChanged(sp ExchangeSplit) error {
	return errConflict(CodeExchangeChanged, "The exchange works out differently now ("+exchangeMessage(sp)+"); review it and post again")
}

// PreviewExchange works an exchange out today without posting it.
func (s *Service) PreviewExchange(ctx context.Context, scope Scope, in ExchangeInput) (ExchangePreview, error) {
	if err := validateExchange(in); err != nil {
		return ExchangePreview{}, err
	}
	var out ExchangePreview
	err := s.read(ctx, scope, func(q db.Querier) error {
		date, err := s.today(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		p, err := s.planExchange(ctx, q, scope.TenantID, date, in, false)
		if err != nil {
			return err
		}
		out = ExchangePreview{ExchangeSplit: p.split, Message: exchangeMessage(p.split), Lines: p.built.Lines}
		out.ReturnLines = make([]ReturnLine, len(p.ret.Lines))
		for i, l := range p.ret.Lines {
			out.ReturnLines[i] = l.Line
		}
		return nil
	})
	return out, err
}

// PostExchange posts an exchange (DH) with its return (TH) and new sale (BH, source exchange): the
// goods come back first, so the new sale may take the stock they bring; the return's credit
// settles the original sale and then the new one, and the difference is paid in (PT) or out (PC)
// with MethodID or left on the customer's account. A confirmation the new sale needs (credit
// limit, minimum price) binds the original sales document.
func (s *Service) PostExchange(ctx context.Context, actor audit.Actor, key idempotency.Key, in ExchangeInput, exc Exceptions) (posting.Response, error) {
	if err := validateExchange(in); err != nil {
		return posting.Response{}, err
	}
	var date time.Time
	var plan exchangePlan
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		var err error
		if date, err = s.today(ctx, q, actor.TenantID); err != nil {
			return err
		}
		if plan, err = s.planExchange(ctx, q, actor.TenantID, date, in, false); err != nil {
			return err
		}
		return checkSettlement(in, plan)
	}); err != nil {
		return s.postRefused(ctx, actor, key, ScopeExchangePost, err)
	}
	extra := []string{"TH", "BH"}
	if in.MethodID != nil {
		switch {
		case plan.split.CustomerPays.IsPositive():
			extra = append(extra, "PT")
		case plan.split.StoreRefunds.IsPositive():
			extra = append(extra, "PC")
		}
	}
	locks := stockLocks(exchangeSkus(plan, in))
	if plan.orig != nil {
		locks.Add(posting.LockObligations, plan.orig.ID)
	}
	cmd := command{scope: ScopeExchangePost, date: date, prefix: "DH", extra: extra, locks: locks}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		out, err := s.applyExchange(ctx, tx, key, in, plan.split, setOf(exchangeSkus(plan, in)), exc)
		if err != nil {
			return posting.Result{}, err
		}
		return posting.Result{Status: http.StatusCreated, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// exchangeSkus are the SKUs whose stock rows an exchange planned as p locks: those the goods come
// back to and those the new sale's lines take, its combo components and gifts included.
func exchangeSkus(p exchangePlan, in ExchangeInput) []uuid.UUID {
	skus := append(p.ret.skus(), draftSkus(&in.DraftInput)...)
	return sortedUnique(append(skus, stockedSkus(p.built.Lines)...))
}

// applyExchange writes an exchange inside its posting; predicted is the split its numbers were
// reserved for and locked the SKUs whose stock rows it holds.
func (s *Service) applyExchange(ctx context.Context, tx posting.Tx, key idempotency.Key, in ExchangeInput, predicted ExchangeSplit,
	locked skuSet, exc Exceptions,
) (ExchangePosted, error) {
	p, err := s.planExchange(ctx, tx, tx.TenantID(), tx.BusinessDate(), in, true)
	if err != nil {
		return ExchangePosted{}, err
	}
	if err := locked.covers(stockedSkus(p.built.Lines)); err != nil {
		return ExchangePosted{}, err
	}
	if !p.split.equal(predicted) {
		return ExchangePosted{}, errExchangeChanged(p.split)
	}
	if err := checkSettlement(in, p); err != nil {
		return ExchangePosted{}, err
	}
	var method *PaymentMethod
	if in.MethodID != nil {
		m, err := s.checkMethod(ctx, tx, *in.MethodID, "payment_method_id")
		if err != nil {
			return ExchangePosted{}, err
		}
		method = &m
	}
	orig, sp := p.ret.Doc, p.split
	party := orig.PayerID
	exID, retID, newID := ids.New(), ids.New(), ids.New()
	retNo, err := posting.TakeDocNo(tx, "TH")
	if err != nil {
		return ExchangePosted{}, err
	}
	newNo, err := posting.TakeDocNo(tx, "BH")
	if err != nil {
		return ExchangePosted{}, err
	}

	// The return settles the original sale and the new one; only its refund leaves the account.
	settled := sp.Returned.Sub(sp.StoreRefunds)
	ret, err := s.writeReturn(ctx, tx, retID, retNo, key.Value, p.ret, settled, sp.StoreRefunds)
	if err != nil {
		return ExchangePosted{}, err
	}
	if err := s.checkStock(ctx, tx, p.built.Lines); err != nil {
		return ExchangePosted{}, err
	}
	credit := money.ZeroMoney
	if method == nil {
		credit = sp.CustomerPays
	}
	if err := s.guardSale(ctx, tx, party, p.built, credit, exc.bind(entityExchange, orig.ID, 0), newID); err != nil {
		return ExchangePosted{}, err
	}
	campaign, err := s.campaigns.Attribute(ctx, tx, orig.CustomerID, tx.BusinessDate())
	if err != nil {
		return ExchangePosted{}, err
	}
	doc := SalesDocument{
		ID: newID, DocNo: newNo, Source: SourceExchange, CustomerID: orig.CustomerID, PayerID: party,
		BusinessDate: DateOf(tx.BusinessDate()), CampaignID: campaign, CashTendered: money.ZeroMoney, Change: money.ZeroMoney,
		Totals: p.built.Totals,
	}
	cogs, lineCampaigns, err := s.insertSale(ctx, tx, key.Value+":sale", doc, p.built)
	if err != nil {
		return ExchangePosted{}, err
	}

	var receivable *Obligation
	var refundDue *Obligation
	if party != nil {
		if sp.Taken.IsPositive() {
			st, err := loadSettings(ctx, tx)
			if err != nil {
				return ExchangePosted{}, err
			}
			days, err := paymentTerms(ctx, tx, *party, st.PaymentTermsDays)
			if err != nil {
				return ExchangePosted{}, err
			}
			due := tx.BusinessDate().AddDate(0, 0, days)
			ob, err := s.debt.RecordReceivable(ctx, tx, ReceivableInput{
				PartyID: *party, SourceID: newID, SourceDocNo: newNo, Amount: sp.Taken, DueDate: &due, CampaignID: campaign,
			})
			if err != nil {
				return ExchangePosted{}, err
			}
			receivable = &ob
		}
		if sp.Returned.IsPositive() {
			var targets []uuid.UUID
			if p.orig != nil {
				targets = append(targets, p.orig.ID)
			}
			if receivable != nil {
				targets = append(targets, receivable.ID)
			}
			cn, err := s.debt.RecordCreditNote(ctx, tx, CreditNoteInput{
				PartyID: *party, SourceID: retID, SourceDocNo: retNo, Amount: sp.Returned, ReasonID: &p.ret.ReasonID,
				Targets: targets, OnlyTargets: true,
			})
			if err != nil {
				return ExchangePosted{}, err
			}
			if !cn.Settled.Equal(settled) {
				return ExchangePosted{}, errExchangeChanged(sp)
			}
			refundDue = cn.RefundDue
		}
	}

	var fund *uuid.UUID
	paying := method != nil && (sp.CustomerPays.IsPositive() || sp.StoreRefunds.IsPositive())
	if paying {
		fund = &method.FundID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO exchanges (id, tenant_id, doc_no, customer_party_id, original_sales_document_id, sales_return_id,
		                       new_sales_document_id, returned_amount, taken_amount, debt_reduction_amount,
		                       customer_pays_amount, store_refunds_amount, fund_account_id, business_date, idempotency_key,
		                       posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $16)`,
		exID, tx.TenantID(), tx.DocNo(), party, orig.ID, retID, newID, sp.Returned, sp.Taken, sp.DebtReduction,
		sp.CustomerPays, sp.StoreRefunds, fund, tx.BusinessDate(), key.Value, tx.Now(), tx.Actor().ID); err != nil {
		return ExchangePosted{}, wrap("insert exchange", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sales_returns SET exchange_id = $2 WHERE id = $1`, retID, exID); err != nil {
		return ExchangePosted{}, wrap("link sales return to exchange", err)
	}
	ret.ExchangeID = &exID

	out := ExchangePosted{Return: ret}
	if paying {
		var pay Payment
		if sp.CustomerPays.IsPositive() {
			pin := PaymentInput{
				Direction: DirectionIn, PartyID: party, Purpose: PurposeSettlement, MethodID: method.ID, Amount: sp.CustomerPays,
				SourceType: PaymentSourceExchange, SourceID: &exID, Key: paymentKey(ScopeExchangePost, key, 0),
			}
			if receivable != nil {
				pin.Allocations = []PaymentAllocation{{ObligationID: receivable.ID, Amount: sp.CustomerPays}}
			}
			pay, err = s.debt.RecordPayment(ctx, tx, pin)
		} else {
			pay, err = s.payRefund(ctx, tx, ScopeExchangePost, key, party, method.ID, sp.StoreRefunds, PaymentSourceExchange, exID, refundDue)
		}
		if err != nil {
			return ExchangePosted{}, err
		}
		out.Payment = &pay
	}

	if out.Exchange, err = getExchange(ctx, tx, exID); err != nil {
		return ExchangePosted{}, err
	}
	if out.Document, err = s.documentDetail(ctx, tx, newID); err != nil {
		return ExchangePosted{}, err
	}
	s.publishReturn(tx, ret, p.ret)
	tx.Audit(audit.Event{Action: ActionDocumentPost, EntityType: entityDocument, EntityID: newID, DocNo: newNo, After: out.Document})
	tx.Publish(outbox.Event{Topic: TopicSalePosted, Payload: map[string]any{
		"sales_document_id": newID, "doc_no": newNo, "source": SourceExchange, "customer_party_id": orig.CustomerID,
		"payer_party_id": party, "business_date": doc.BusinessDate, "total_amount": sp.Taken, "cogs_amount": cogs,
		"campaign_id": campaign, "exchange_id": exID, "line_campaigns": lineCampaigns,
	}})
	tx.Audit(audit.Event{Action: ScopeExchangePost, EntityType: entityExchange, EntityID: exID, DocNo: tx.DocNo(), After: out.Exchange})
	tx.Publish(outbox.Event{Topic: TopicExchangePosted, Payload: map[string]any{
		"exchange_id": exID, "doc_no": tx.DocNo(), "customer_party_id": party, "original_sales_document_id": orig.ID,
		"sales_return_id": retID, "new_sales_document_id": newID, "business_date": doc.BusinessDate,
		"returned_amount": sp.Returned, "taken_amount": sp.Taken, "debt_reduction_amount": sp.DebtReduction,
		"customer_pays_amount": sp.CustomerPays, "store_refunds_amount": sp.StoreRefunds,
	}})
	return out, nil
}

// Exchange returns an exchange.
func (s *Service) Exchange(ctx context.Context, scope Scope, id uuid.UUID) (Exchange, error) {
	var out Exchange
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = getExchange(ctx, q, id)
		return err
	})
	return out, err
}

// Exchanges lists exchanges newest first.
func (s *Service) Exchanges(ctx context.Context, scope Scope, f ExchangeFilter) ([]Exchange, error) {
	var out []Exchange
	err := s.read(ctx, scope, func(q db.Querier) error {
		var w where
		if f.OriginalDocumentID != nil {
			w.add("original_sales_document_id = $%d", *f.OriginalDocumentID)
		}
		if f.CustomerID != nil {
			w.add("customer_party_id = $%d", *f.CustomerID)
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+exchangeColumns+` FROM exchanges`+w.sql()+tail, w.args...); err != nil {
			return wrap("list exchanges", err)
		}
		return nil
	})
	if err == nil && out == nil {
		out = []Exchange{}
	}
	return out, err
}
