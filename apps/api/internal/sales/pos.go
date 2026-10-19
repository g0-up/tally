package sales

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// maxPayments is the most payment methods one POS sale is split over.
const maxPayments = 10

// docTypeCheckout is the document an exception confirmation of a direct POS checkout binds: the
// sale has no id before it posts, so the confirmation binds the tenant at version 0 and the
// payload hash carries the sale itself.
const docTypeCheckout = "pos_checkout"

// CheckoutInput is a POS sale (HD). CustomerID nil is a walk-in customer ("khách lẻ"), who pays
// the whole total at the counter; a named customer may leave part of it on credit.
type CheckoutInput struct {
	CustomerID *uuid.UUID `json:"customer_party_id,omitempty"`
	DraftInput
	Payments []PosPayment `json:"payments"`
	// CashTendered is the cash handed over; the change is what it exceeds the cash payments by.
	CashTendered *money.Money `json:"cash_tendered_amount,omitempty"`
	// LoyaltyPoints are whole points the customer spends on the sale, worth an order discount of
	// points × the program's point value. An offline sale cannot spend points.
	LoyaltyPoints *money.Qty `json:"loyalty_points,omitempty"`
}

// PosPayment is the part of a POS sale paid through one payment method.
type PosPayment struct {
	MethodID uuid.UUID   `json:"payment_method_id"`
	Amount   money.Money `json:"amount"`
}

// PosSale is a posted POS sale: the HD, the shift it belongs to, the payments that took its money
// and the loyalty points spent on it.
type PosSale struct {
	Document DocumentDetail     `json:"sales_document"`
	Shift    Shift              `json:"shift"`
	Payments []Payment          `json:"payments"`
	Loyalty  *LoyaltyRedemption `json:"loyalty_redemption,omitempty"`
}

// LoyaltyRedemption is the loyalty points a sale spent and the order discount they gave.
type LoyaltyRedemption struct {
	Points money.Qty   `json:"points"`
	Amount money.Money `json:"amount"`
}

// DocumentReversal is a reversed POS sale, the document that reversed it and what had been paid
// on it: that money is now the customer's advance.
type DocumentReversal struct {
	Document      DocumentDetail `json:"sales_document"`
	ReversalID    uuid.UUID      `json:"reversal_id"`
	ReversalDocNo string         `json:"reversal_doc_no"`
	Advance       money.Money    `json:"advance_amount"`
}

// validateCheckout checks the shape of a POS sale.
func validateCheckout(in CheckoutInput) error {
	fields := map[string]string{}
	mergeFields(fields, validateDraft(in.DraftInput))
	if len(in.Payments) > maxPayments {
		fields["payments"] = "too_many"
	}
	for i, p := range in.Payments {
		if p.MethodID == uuid.Nil {
			fields[paymentField(i, "payment_method_id")] = "required"
		}
		if !p.Amount.IsPositive() {
			fields[paymentField(i, "amount")] = "must_be_positive"
		}
	}
	if in.CashTendered != nil && in.CashTendered.IsNegative() {
		fields["cash_tendered_amount"] = "negative"
	}
	if in.LoyaltyPoints != nil {
		switch {
		case !in.LoyaltyPoints.IsPositive() || !in.LoyaltyPoints.IsWhole():
			fields["loyalty_points"] = "must_be_positive_whole"
		case in.CustomerID == nil:
			fields["loyalty_points"] = "customer_required"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// mergeFields adds the field errors of a validation error to fields.
func mergeFields(fields map[string]string, err error) {
	if e, ok := apperr.As(err); ok {
		maps.Copy(fields, e.Fields)
	}
}

func paymentField(i int, name string) string { return "payments." + strconv.Itoa(i) + "." + name }

// tender is how a POS sale is paid: what the payments add up to, the cash part of it, the cash
// handed over and the change given back.
type tender struct {
	Paid     money.Money
	Cash     money.Money
	Tendered money.Money
	Change   money.Money
}

// planTender checks the payments of a sale of total against its customer: they never add up to
// more than the total, a walk-in customer pays all of it, and cash handed over covers the cash
// payments (the rest is change). methods[i] is the method of payments[i].
func planTender(total money.Money, walkIn bool, payments []PosPayment, methods []PaymentMethod, tendered *money.Money) (tender, error) {
	var t tender
	for i, p := range payments {
		t.Paid = t.Paid.Add(p.Amount)
		if methods[i].Cash {
			t.Cash = t.Cash.Add(p.Amount)
		}
	}
	if t.Paid.Cmp(total) > 0 {
		return tender{}, apperr.NewValidation(CodePaymentMismatch,
			fmt.Sprintf("The payments add up to %s, more than the total of %s", t.Paid.String(), total.String()),
			map[string]string{"payments": CodePaymentMismatch})
	}
	if walkIn && !t.Paid.Equal(total) {
		return tender{}, apperr.NewValidation(CodeWalkInPaysInFull,
			"A walk-in customer pays the whole total; choose a customer to sell on credit",
			map[string]string{"payments": CodeWalkInPaysInFull})
	}
	t.Tendered = t.Cash
	if tendered != nil {
		switch {
		case !t.Cash.IsPositive():
			return tender{}, errInvalid(map[string]string{"cash_tendered_amount": "no_cash_payment"})
		case tendered.Cmp(t.Cash) < 0:
			return tender{}, errInvalid(map[string]string{"cash_tendered_amount": "below_cash_payment"})
		}
		t.Tendered = *tendered
		t.Change = tendered.Sub(t.Cash)
	}
	return t, nil
}

// sale is a POS sale to post: a direct checkout, the checkout of a saved cart or an offline sale.
type sale struct {
	scope    string
	source   string
	customer *uuid.UUID
	draft    DraftInput
	payments []PosPayment
	tendered *money.Money
	// points are the loyalty points the customer spends on the sale.
	points *money.Qty
	// cart is the saved cart the sale checks out, at the version the client saw.
	cart *Cart
	// offline is the client evidence of an offline sale.
	offline *offlineMark
	// resolves is the sync exception a corrected offline sale closes.
	resolves *uuid.UUID
	exc      confirmations
}

// offlineMark is what an offline sale records of the device that made it.
type offlineMark struct {
	ClientID  string
	CreatedAt time.Time
}

// Checkout posts a POS sale: it prices the lines for the customer, picks the only color in stock
// for a colored line without one, issues the stock, writes the HD in the day's shift (opened by
// the first sale of the day), records the payments and, for a named customer, the receivable they
// settle. Stock is checked under the SKU locks, so concurrent sales never sell more than is
// available (409 insufficient_stock).
func (s *Service) Checkout(ctx context.Context, actor audit.Actor, key idempotency.Key, in CheckoutInput, exc Exceptions) (posting.Response, error) {
	in.CustomerID = nonNilID(in.CustomerID)
	if err := validateCheckout(in); err != nil {
		return posting.Response{}, err
	}
	sl := sale{
		scope: ScopePOSCheckout, source: SourcePOS, customer: in.CustomerID, draft: in.DraftInput, payments: in.Payments,
		tendered: in.CashTendered, points: in.LoyaltyPoints, exc: exc.bind(docTypeCheckout, actor.TenantID, 0),
	}
	return s.postSale(ctx, actor, key, sl)
}

// shiftAttempts bounds how often a POS sale is planned again because the day's shift changed
// while it posted; only the first sales of a day can race on opening it.
const shiftAttempts = 3

// postSale runs a POS sale through the coordinator, planning it again when the day's shift changed
// between the plan and the posting. A sale that keeps losing that race is refused with 409
// shift_changed; nothing of it was kept, so the till sends it again under the same key.
func (s *Service) postSale(ctx context.Context, actor audit.Actor, key idempotency.Key, sl sale) (posting.Response, error) {
	for range shiftAttempts {
		resp, err := s.postSaleOnce(ctx, actor, key, sl)
		if !errors.Is(err, errShiftMoved) {
			return resp, err
		}
	}
	return posting.Response{}, errConflict(CodeShiftChanged, "The day's shift changed while the sale posted; send it again")
}

// postSaleOnce plans a POS sale with a read before its posting and posts it. The plan holds the
// business date (today, or the day an offline sale was made), the day's shift, whose CA number
// the posting issues when the sale opens it, and the SKUs whose stock rows the posting locks: the
// goods the lines name plus the combo components and gifts their build adds. The shift opens
// inside the posting, after its entitlement and period guards, so a refused sale opens nothing.
func (s *Service) postSaleOnce(ctx context.Context, actor audit.Actor, key idempotency.Key, sl sale) (posting.Response, error) {
	var date time.Time
	var plan shiftPlan
	var skus []uuid.UUID
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		today, err := s.today(ctx, q, actor.TenantID)
		if err != nil {
			return err
		}
		date = today
		if sl.offline != nil {
			if date, err = s.dateAt(ctx, q, actor.TenantID, sl.offline.CreatedAt); err != nil {
				return err
			}
		}
		if plan, err = planShift(ctx, q, today); err != nil {
			return err
		}
		skus, err = s.saleSkus(ctx, q, actor.TenantID, lineBuild{PartyID: sl.customer, Date: date, Draft: sl.draft})
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	extra := make([]string, len(sl.payments), len(sl.payments)+1)
	for i := range extra {
		extra[i] = "PT"
	}
	if plan.open == nil {
		extra = append(extra, "CA")
	}
	cmd := command{scope: sl.scope, date: date, prefix: "HD", extra: extra, locks: stockLocks(skus)}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		out, err := s.applySale(ctx, tx, key, sl, plan, setOf(skus))
		if err != nil {
			return posting.Result{}, err
		}
		return posting.Result{Status: http.StatusCreated, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// saleSkus are the SKUs a document built from b takes stock of: what its lines name plus the combo
// components and gifts the build adds. A draft that does not build counts what it names; its
// posting then fails on it the same way.
func (s *Service) saleSkus(ctx context.Context, q db.Querier, tenantID uuid.UUID, b lineBuild) ([]uuid.UUID, error) {
	named := draftSkus(&b.Draft)
	built, err := s.buildLines(ctx, q, tenantID, b)
	if err != nil {
		if _, ok := apperr.As(err); ok {
			return sortedUnique(named), nil
		}
		return nil, err
	}
	return sortedUnique(append(named, stockedSkus(built.Lines)...)), nil
}

// applySale writes a POS sale inside its posting; locked are the SKUs whose stock rows it holds.
func (s *Service) applySale(ctx context.Context, tx posting.Tx, key idempotency.Key, sl sale, plan shiftPlan, locked skuSet) (PosSale, error) {
	if sl.resolves != nil {
		exc, err := lockOpenException(ctx, tx, *sl.resolves)
		if err != nil {
			return PosSale{}, err
		}
		if err := checkNotPosted(ctx, tx, exc.OfflineClientID); err != nil {
			return PosSale{}, err
		}
	}
	if sl.offline != nil {
		if err := checkNotParked(ctx, tx, sl.offline.ClientID); err != nil {
			return PosSale{}, err
		}
	}
	if sl.cart != nil {
		cur, err := getCart(ctx, tx, sl.cart.ID, true)
		if err != nil {
			return PosSale{}, err
		}
		if cur.Version != sl.cart.Version {
			return PosSale{}, errStale()
		}
		if cur.Status != CartOpen {
			return PosSale{}, errConflict(CodeCartSold, "The cart was already sold; copy it to sell it again")
		}
	}
	if sl.customer != nil {
		if err := checkCustomer(ctx, tx, *sl.customer, "customer_party_id"); err != nil {
			return PosSale{}, err
		}
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return PosSale{}, err
	}
	// Points spent on the sale are an order discount on top of the one typed at the counter.
	docID := ids.New()
	draft := sl.draft
	var redeemed *LoyaltyRedemption
	if sl.points != nil {
		value, err := s.loyalty.Redeem(ctx, tx, *sl.customer, *sl.points, docID)
		if err != nil {
			return PosSale{}, err
		}
		draft.OrderDiscount = draft.OrderDiscount.Add(value)
		redeemed = &LoyaltyRedemption{Points: *sl.points, Amount: value}
	}
	built, err := s.prepareSale(ctx, tx, sl.customer, draft, locked)
	if err != nil {
		return PosSale{}, redemptionTooLarge(err, redeemed)
	}
	methods := make([]PaymentMethod, len(sl.payments))
	for i, p := range sl.payments {
		m, err := s.checkMethod(ctx, tx, p.MethodID, paymentField(i, "payment_method_id"))
		if err != nil {
			return PosSale{}, err
		}
		if sl.offline != nil && !m.Cash {
			return PosSale{}, errOfflinePayment(i)
		}
		methods[i] = m
	}
	t, err := planTender(built.Totals.Total, sl.customer == nil, sl.payments, methods, sl.tendered)
	if err != nil {
		return PosSale{}, err
	}
	if sl.offline != nil {
		if err := checkOfflineTotal(built.Totals.Total, t.Paid, st.OfflineMaxAmount); err != nil {
			return PosSale{}, err
		}
	}

	if err := s.guardSale(ctx, tx, sl.customer, built, built.Totals.Total.Sub(t.Paid), sl.exc, docID); err != nil {
		return PosSale{}, err
	}
	campaign, err := s.campaigns.Attribute(ctx, tx, sl.customer, tx.BusinessDate())
	if err != nil {
		return PosSale{}, err
	}
	shift, err := dayShift(ctx, tx, plan)
	if err != nil {
		return PosSale{}, err
	}
	doc := SalesDocument{
		ID: docID, DocNo: tx.DocNo(), Source: sl.source, ShiftID: &shift.ID, CustomerID: sl.customer, PayerID: sl.customer,
		BusinessDate: DateOf(tx.BusinessDate()), CampaignID: campaign, CashTendered: t.Tendered, Change: t.Change,
		Totals: built.Totals,
	}
	if sl.cart != nil {
		doc.CartID = &sl.cart.ID
	}
	if sl.offline != nil {
		doc.OfflineClientID, doc.OfflineCreatedAt = &sl.offline.ClientID, &sl.offline.CreatedAt
	}
	cogs, lineCampaigns, err := s.insertSale(ctx, tx, key.Value, doc, built)
	if err != nil {
		return PosSale{}, err
	}

	var receivable *Obligation
	if sl.customer != nil && built.Totals.Total.IsPositive() {
		days, err := paymentTerms(ctx, tx, *sl.customer, st.PaymentTermsDays)
		if err != nil {
			return PosSale{}, err
		}
		due := tx.BusinessDate().AddDate(0, 0, days)
		ob, err := s.debt.RecordReceivable(ctx, tx, ReceivableInput{
			PartyID: *sl.customer, SourceID: docID, SourceDocNo: doc.DocNo, Amount: built.Totals.Total, DueDate: &due,
			CampaignID: campaign,
		})
		if err != nil {
			return PosSale{}, err
		}
		receivable = &ob
	}
	payments := make([]Payment, 0, len(sl.payments))
	for i, p := range sl.payments {
		in := PaymentInput{
			Direction: DirectionIn, PartyID: sl.customer, Purpose: PurposeSettlement, MethodID: p.MethodID, Amount: p.Amount,
			SourceType: PaymentSourcePOS, SourceID: &docID, ShiftID: &shift.ID, Key: paymentKey(sl.scope, key, i),
		}
		if receivable != nil {
			in.Allocations = []PaymentAllocation{{ObligationID: receivable.ID, Amount: p.Amount}}
		}
		pay, err := s.debt.RecordPayment(ctx, tx, in)
		if err != nil {
			return PosSale{}, err
		}
		payments = append(payments, pay)
	}
	if sl.cart != nil {
		if _, err := db.UpdateWithVersion(ctx, tx, "pos_carts", sl.cart.ID, sl.cart.Version, tx.Now(),
			db.Set{Column: "status", Value: CartSold}, db.Set{Column: "sales_document_id", Value: docID}); err != nil {
			return PosSale{}, err
		}
		tx.Audit(audit.Event{Action: ActionCartUpdate, EntityType: entityCart, EntityID: sl.cart.ID, DocNo: sl.cart.DocNo,
			Before: map[string]any{"status": CartOpen}, After: map[string]any{"status": CartSold, "sales_document_id": docID}})
	}

	if sl.resolves != nil {
		if err := resolveException(ctx, tx, *sl.resolves, docID); err != nil {
			return PosSale{}, err
		}
	}

	detail, err := s.documentDetail(ctx, tx, docID)
	if err != nil {
		return PosSale{}, err
	}
	tx.Audit(audit.Event{Action: ActionDocumentPost, EntityType: entityDocument, EntityID: docID, DocNo: doc.DocNo, After: detail})
	tx.Publish(outbox.Event{Topic: TopicSalePosted, Payload: map[string]any{
		"sales_document_id": docID, "doc_no": doc.DocNo, "source": sl.source, "shift_id": shift.ID,
		"pos_cart_id": doc.CartID, "customer_party_id": sl.customer, "payer_party_id": sl.customer,
		"business_date": doc.BusinessDate, "total_amount": built.Totals.Total, "cogs_amount": cogs,
		"paid_amount": t.Paid, "campaign_id": campaign, "offline_client_id": doc.OfflineClientID,
		"line_campaigns": lineCampaigns,
	}})
	return PosSale{Document: detail, Shift: shift, Payments: payments, Loyalty: redeemed}, nil
}

// redemptionTooLarge points an order discount larger than the sale at the loyalty points when
// points were spent: they are the part of the discount the cashier did not type.
func redemptionTooLarge(err error, redeemed *LoyaltyRedemption) error {
	e, ok := apperr.As(err)
	if !ok || redeemed == nil || e.Fields["order_discount"] == "" {
		return err
	}
	fields := maps.Clone(e.Fields)
	fields["loyalty_points"] = "exceeds_amount"
	return apperr.NewValidation(e.Code, e.Message, fields)
}

// prepareSale prices the lines of a sale for its customer on the posting's date, gives each
// colored line without a color the only color in stock, and checks that the stock not held by
// reservations covers every SKU (409 insufficient_stock). It runs under the locks of the SKUs in
// locked; lines needing the stock of another SKU fail with 409 sale_changed.
func (s *Service) prepareSale(ctx context.Context, tx posting.Tx, customer *uuid.UUID, draft DraftInput, locked skuSet) (builtDoc, error) {
	built, err := s.buildLines(ctx, tx, tx.TenantID(), lineBuild{PartyID: customer, Date: tx.BusinessDate(), Draft: draft})
	if err != nil {
		return builtDoc{}, err
	}
	if err := locked.covers(stockedSkus(built.Lines)); err != nil {
		return builtDoc{}, err
	}
	if err := s.applyColors(ctx, tx, built.Lines, built.Origin, colorAutoPick); err != nil {
		return builtDoc{}, err
	}
	if err := s.checkStock(ctx, tx, built.Lines); err != nil {
		return builtDoc{}, err
	}
	return built, nil
}

// checkStock fails with 409 insufficient_stock when the stocked lines need more of a SKU than is
// sellable and not reserved.
func (s *Service) checkStock(ctx context.Context, q db.Querier, lines []Line) error {
	need := map[uuid.UUID]money.Qty{}
	var skus []uuid.UUID
	for _, l := range lines {
		if !stocked(l.Kind) || l.SkuID == nil {
			continue
		}
		if _, ok := need[*l.SkuID]; !ok {
			skus = append(skus, *l.SkuID)
		}
		need[*l.SkuID] = need[*l.SkuID].Add(l.BaseQty)
	}
	if len(skus) == 0 {
		return nil
	}
	avail, err := s.stock.Available(ctx, q, sortedUnique(skus))
	if err != nil {
		return err
	}
	if sku, short := shortSku(lines, need, avail); short {
		return errConflict(CodeInsufficientStock, fmt.Sprintf("Only %s of %s is in stock", avail[sku].String(), skuLabel(lines, sku)))
	}
	return nil
}

// shortSku is the first SKU, in line order, that needs more than is available.
func shortSku(lines []Line, need, avail map[uuid.UUID]money.Qty) (uuid.UUID, bool) {
	for _, l := range lines {
		if l.SkuID == nil {
			continue
		}
		if n, ok := need[*l.SkuID]; ok && n.Cmp(avail[*l.SkuID]) > 0 {
			return *l.SkuID, true
		}
	}
	return uuid.Nil, false
}

func skuLabel(lines []Line, sku uuid.UUID) string {
	for _, l := range lines {
		if l.SkuID != nil && *l.SkuID == sku {
			if l.SkuCode != nil {
				return *l.SkuCode + " " + l.Name
			}
			return l.Name
		}
	}
	return sku.String()
}

// guardSale spends the owner's confirmations a POS sale needs: one when what a named customer
// leaves on credit takes them over their limit, one when a line sells under its minimum price.
func (s *Service) guardSale(ctx context.Context, tx posting.Tx, customer *uuid.UUID, built builtDoc, credit money.Money,
	c confirmations, docID uuid.UUID,
) error {
	if customer != nil && credit.IsPositive() {
		err := s.debt.CheckCredit(ctx, tx, CreditQuery{PartyID: *customer, Undelivered: credit, AsOf: tx.BusinessDate()})
		if err != nil {
			if apperr.KindOf(err) != apperr.ExceptionRequired {
				return err
			}
			if spent := s.spend(ctx, tx, tx.TenantID(), c, exception.CreditLimitExceeded, entityDocument, docID); spent != nil {
				if apperr.KindOf(spent) == apperr.ExceptionRequired {
					// The ledger's error says by how much the limit is exceeded.
					return err
				}
				return spent
			}
		}
	}
	if slices.ContainsFunc(built.Lines, func(l Line) bool { return l.belowMin(built.IncludeTax) }) {
		return s.spend(ctx, tx, tx.TenantID(), c, exception.BelowMinPrice, entityDocument, docID)
	}
	return nil
}

// insertSale writes a POS or exchange sales document with its lines attributed to their campaigns,
// issues its stock, stores its cost and counts the promotions it used; it returns the cost of goods
// and the campaign of every line.
func (s *Service) insertSale(ctx context.Context, tx posting.Tx, idemKey string, doc SalesDocument, built builtDoc) (money.Money, []LineCampaign, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sales_documents (id, tenant_id, doc_no, source, pos_cart_id, shift_id, customer_party_id, payer_party_id,
		                             business_date, goods_amount, discount_amount, tax_amount, total_amount, cogs_amount,
		                             campaign_id, offline_client_id, offline_created_at, status, idempotency_key, posted_at,
		                             posted_by, cash_tendered_amount, change_amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 0, $14, $15, $16, 'posted', $17, $18, $19, $20, $21, $18)`,
		doc.ID, tx.TenantID(), doc.DocNo, doc.Source, doc.CartID, doc.ShiftID, doc.CustomerID, doc.PayerID, doc.BusinessDate,
		doc.Goods, doc.Discount, doc.Tax, doc.Total, doc.CampaignID, doc.OfflineClientID, doc.OfflineCreatedAt, idemKey,
		tx.Now(), tx.Actor().ID, doc.CashTendered, doc.Change); err != nil {
		return money.Money{}, nil, wrap("insert sales document", err)
	}
	docLines := make([]DocumentLine, len(built.Lines))
	var issues []StockIssue
	for i, l := range built.Lines {
		docLines[i] = DocumentLine{Line: l, Cogs: money.ZeroMoney, Returned: money.ZeroQty, CampaignID: doc.CampaignID}
		if stocked(l.Kind) && l.SkuID != nil {
			issues = append(issues, StockIssue{DocLineID: l.ID, SkuID: *l.SkuID, BaseQty: l.BaseQty, ColorID: l.ColorID})
		}
	}
	campaigns, err := s.attributeLines(ctx, tx, doc.ID, doc.CampaignID, docLines)
	if err != nil {
		return money.Money{}, nil, err
	}
	if err := documentLines.insert(ctx, tx, tx.TenantID(), doc.ID, built.Lines, documentExtras(docLines)); err != nil {
		return money.Money{}, nil, err
	}
	cogs := money.ZeroMoney
	if len(issues) > 0 {
		moves, err := s.stock.Issue(ctx, tx, StockDoc{Type: StockDocSalesDocument, ID: doc.ID}, issues)
		if err != nil {
			return money.Money{}, nil, err
		}
		if cogs, err = storeCogs(ctx, tx, doc.ID, moves); err != nil {
			return money.Money{}, nil, err
		}
	}
	if err := s.recordPromotions(ctx, tx, tx.TenantID(), entityDocument, doc.ID, built.Promotions); err != nil {
		return money.Money{}, nil, err
	}
	return cogs, campaigns, nil
}

// ReverseDocument undoes a POS sale nothing was returned from: a reversing HD books the stock
// back and the sale and its receivable are reversed; what had been paid on it becomes the
// customer's advance. A walk-in sale has no one to hold that money, so it is undone with a return
// that refunds it; a BH is undone by reversing its stock out.
func (s *Service) ReverseDocument(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in ReverseInput) (posting.Response, error) {
	fields := map[string]string{}
	checkText(fields, "reason", in.Reason, maxNoteLen)
	if len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	var date time.Time
	var skus []uuid.UUID
	var receivable *Obligation
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if _, err := getDocument(ctx, q, id, false); err != nil {
			return err
		}
		var err error
		if receivable, err = s.debt.ReceivableOf(ctx, q, id); err != nil {
			return err
		}
		if skus, err = documentSkus(ctx, q, id); err != nil {
			return err
		}
		date, err = s.today(ctx, q, actor.TenantID)
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	locks := stockLocks(skus)
	if receivable != nil {
		locks.Add(posting.LockObligations, receivable.ID)
	}
	cmd := command{scope: ActionDocumentReverse, date: date, prefix: "HD", locks: locks}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		doc, err := getDocument(ctx, tx, id, true)
		if err != nil {
			return posting.Result{}, err
		}
		if err := checkReversible(doc); err != nil {
			return posting.Result{}, err
		}
		lines, err := lockDocumentLines(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		hasStock := false
		for _, l := range lines {
			if l.Returned.IsPositive() {
				return posting.Result{}, errConflict(CodeDocumentHasReturns,
					"Goods of this sale were returned; return the rest through a sales return instead of reversing it")
			}
			hasStock = hasStock || (stocked(l.Kind) && l.SkuID != nil)
		}
		reason := "Reversal of sales document " + doc.DocNo
		if in.Reason != nil && *in.Reason != "" {
			reason = *in.Reason
		}
		advance := money.ZeroMoney
		ob, err := s.debt.ReceivableOf(ctx, tx, id)
		if err != nil {
			return posting.Result{}, err
		}
		if ob != nil {
			advance, err = s.debt.ReverseReceivable(ctx, tx, id, reason)
			if e, ok := apperr.As(err); ok && e.Code == codeObligationHasCredits {
				return posting.Result{}, errConflict(codeObligationHasCredits,
					"Credit notes already settle this sale; post a sales return instead of reversing it")
			}
			if err != nil {
				return posting.Result{}, err
			}
		}
		revID, now := ids.New(), tx.Now()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sales_documents (id, tenant_id, doc_no, source, customer_party_id, payer_party_id, business_date,
			                             goods_amount, discount_amount, tax_amount, total_amount, cogs_amount, campaign_id,
			                             status, idempotency_key, posted_at, posted_by, reversal_of, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'posted', $14, $15, $16, $17, $15)`,
			revID, tx.TenantID(), tx.DocNo(), doc.Source, doc.CustomerID, doc.PayerID, tx.BusinessDate(), doc.Goods,
			doc.Discount, doc.Tax, doc.Total, doc.Cogs, doc.CampaignID, key.Value, now, tx.Actor().ID, doc.ID); err != nil {
			return posting.Result{}, wrap("insert reversing sales document", err)
		}
		if hasStock {
			if _, err := s.stock.Reverse(ctx, tx, StockDoc{Type: StockDocSalesDocument, ID: revID},
				StockDoc{Type: StockDocSalesDocument, ID: doc.ID}); err != nil {
				return posting.Result{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sales_documents SET status = 'reversed' WHERE id = $1`, doc.ID); err != nil {
			return posting.Result{}, wrap("mark sales document reversed", err)
		}
		detail, err := s.documentDetail(ctx, tx, doc.ID)
		if err != nil {
			return posting.Result{}, err
		}
		out := DocumentReversal{Document: detail, ReversalID: revID, ReversalDocNo: tx.DocNo(), Advance: advance}
		tx.Audit(audit.Event{Action: ActionDocumentReverse, EntityType: entityDocument, EntityID: doc.ID, DocNo: doc.DocNo,
			Before: map[string]any{"status": doc.Status},
			After: map[string]any{"status": DocReversed, "reversal_id": revID, "reversal_doc_no": tx.DocNo(),
				"advance_amount": advance, "reason": reason}})
		tx.Publish(outbox.Event{Topic: TopicSaleReversed, Payload: map[string]any{
			"sales_document_id": doc.ID, "doc_no": doc.DocNo, "source": doc.Source, "reversal_doc_no": tx.DocNo(),
			"payer_party_id": doc.PayerID, "total_amount": doc.Total, "cogs_amount": doc.Cogs, "advance_amount": advance,
			"campaign_id": doc.CampaignID,
		}})
		return posting.Result{Status: http.StatusCreated, Body: out}, nil
	}
	return s.post(ctx, actor, key, cmd)
}

// checkReversible tells whether a sales document can be reversed on its own.
func checkReversible(doc SalesDocument) error {
	switch {
	case doc.ReversalOf != nil:
		return errTransition("reversing", "reversed")
	case doc.Status == DocReversed:
		return errConflict(CodeDocumentReversed, "The sales document was already reversed")
	case doc.Source == SourceStockOut:
		return errConflict(CodeInvalidTransition, "A sales document of a stock out is reversed by reversing the stock out")
	case doc.Source == SourceExchange:
		return errTransition(SourceExchange, "reversed")
	case doc.CustomerID == nil && doc.Total.IsPositive():
		return errConflict(CodeWalkInReverse, "A walk-in sale is undone with a sales return that refunds the customer")
	}
	return nil
}

// documentSkus are the SKUs on the lines of a sales document.
func documentSkus(ctx context.Context, q db.Querier, id uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT DISTINCT sku_id FROM sales_document_lines WHERE sales_document_id = $1 AND sku_id IS NOT NULL`, id); err != nil {
		return nil, wrap("read sales document skus", err)
	}
	return sortedUnique(out), nil
}

// nonNilID treats a zero id as absent.
func nonNilID(id *uuid.UUID) *uuid.UUID {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	return id
}

// errOfflinePayment rejects a payment an offline sale cannot take: only cash is collected without
// the server confirming the money.
func errOfflinePayment(i int) error {
	return apperr.NewValidation(CodeOfflineNotAllowed, "An offline sale is paid in cash only",
		map[string]string{paymentField(i, "payment_method_id"): CodeOfflineNotAllowed})
}
