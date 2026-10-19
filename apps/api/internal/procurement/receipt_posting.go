package procurement

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Idempotency scopes of the procurement postings.
const (
	ScopeReceiptPost    = "receipt.post"
	ScopeReceiptReverse = "receipt.reverse"
	ScopeLandedCost     = "landed_cost.post"
	ScopeReturnPost     = "purchase_return.post"
	ScopeAdjustmentPost = "purchase_price_adjustment.post"
)

// Payment sources of the PCs procurement records (payments.source_type).
const (
	PaymentSourcePurchase   = "purchase"
	PaymentSourceLandedCost = "landed_cost"
)

// MaxLotNo is the longest lot number the ledger keeps.
const MaxLotNo = 64

// ReceiptLineInput is a received line. On a receipt against a PO, PurchaseOrderLineID names the
// ordered line and SKU, unit, color and price default to it; a line the order does not have is
// either excess or wrong_item. Qty is in UnitID (the SKU's base unit when nil).
type ReceiptLineInput struct {
	PurchaseOrderLineID *uuid.UUID
	SkuID               uuid.UUID
	UnitID              *uuid.UUID
	Qty                 money.Qty
	ColorID             *uuid.UUID
	// Condition is accepted when empty.
	Condition  string
	LotNo      string
	ExpiryDate *time.Time
	// UnitPrice and DiscountAmount override the ordered price; the discount of an ordered line
	// defaults to its share of the ordered discount.
	UnitPrice      *money.Money
	DiscountAmount *money.Money
	// SpecText is the free "Quy cách" note; empty keeps the ordered line's, else the SKU's.
	SpecText *string
	// NewProduct creates the product of a line off the order instead of SkuID.
	NewProduct *NewProduct
}

// AdvanceUse applies part of an advance already paid to the supplier to the new payable.
type AdvanceUse struct {
	PaymentID uuid.UUID
	Amount    money.Money
}

// ReceiptInput posts a PN: a quick receipt (no PO, SupplierPartyID required) or a batch received
// against a confirmed PO (its supplier). PaidNowAmount is paid at once with a PC through
// PaymentMethodID; Schedule splits what stays open after the payment and the advances.
type ReceiptInput struct {
	SupplierPartyID *uuid.UUID
	PurchaseOrderID *uuid.UUID
	BusinessDate    *time.Time
	DueDate         *time.Time
	Note            *string
	PaidNowAmount   money.Money
	PaymentMethodID *uuid.UUID
	Schedule        []Installment
	UseAdvances     []AdvanceUse
	Lines           []ReceiptLineInput
	// CampaignID tags the receipt with the campaign the goods were bought for (purpose only).
	CampaignID *uuid.UUID
}

// ReverseInput is the reason a posted document is reversed.
type ReverseInput struct {
	Reason string
}

func validCondition(c string) bool {
	switch c {
	case ConditionAccepted, ConditionDamaged, ConditionWrongItem, ConditionExcess:
		return true
	}
	return false
}

// validateReceiptInput checks what needs no database: the shape of the lines, the payment and the
// advances. The schedule is checked once the total is known.
func validateReceiptInput(in ReceiptInput) map[string]string {
	fields := map[string]string{}
	if in.PurchaseOrderID == nil && in.SupplierPartyID == nil {
		fields["supplier_party_id"] = "required"
	}
	validText(fields, "note", in.Note)
	switch {
	case len(in.Lines) == 0:
		fields["lines"] = "required"
	case len(in.Lines) > MaxLines:
		fields["lines"] = "too_many"
	}
	for i, l := range in.Lines {
		if l.Condition != "" && !validCondition(l.Condition) {
			fields[lineField(i, "condition")] = "invalid"
		}
		switch {
		case l.PurchaseOrderLineID != nil && l.NewProduct != nil:
			fields[lineField(i, "new_product")] = "not_allowed_with_order_line"
		case l.PurchaseOrderLineID == nil:
			validateNewProduct(fields, i, l.SkuID, l.UnitID, l.NewProduct)
		}
		if !l.Qty.IsPositive() {
			fields[lineField(i, "qty")] = "must_be_positive"
		}
		if l.UnitPrice != nil && l.UnitPrice.IsNegative() {
			fields[lineField(i, "unit_price")] = "must_not_be_negative"
		}
		if l.DiscountAmount != nil && l.DiscountAmount.IsNegative() {
			fields[lineField(i, "discount_amount")] = "must_not_be_negative"
		}
		if utf8.RuneCountInString(l.LotNo) > MaxLotNo {
			fields[lineField(i, "lot_no")] = "too_long"
		}
		validText(fields, lineField(i, "spec_text"), l.SpecText)
	}
	switch {
	case in.PaidNowAmount.IsNegative():
		fields["paid_now_amount"] = "must_not_be_negative"
	case !in.PaidNowAmount.Equal(in.PaidNowAmount.RoundVND()):
		fields["paid_now_amount"] = "not_whole_dong"
	case in.PaidNowAmount.IsPositive() && in.PaymentMethodID == nil:
		fields["payment_method_id"] = "required"
	}
	seen := map[uuid.UUID]bool{}
	for i, a := range in.UseAdvances {
		switch {
		case a.PaymentID == uuid.Nil:
			fields[fmt.Sprintf("use_advances.%d.payment_id", i)] = "required"
		case seen[a.PaymentID]:
			fields[fmt.Sprintf("use_advances.%d.payment_id", i)] = "duplicate"
		}
		seen[a.PaymentID] = true
		if !a.Amount.IsPositive() {
			fields[fmt.Sprintf("use_advances.%d.amount", i)] = "must_be_positive"
		}
	}
	return fields
}

// payableSchedule is the schedule of a payable of total of which settledNow is paid on posting:
// the requested installments must add up to what stays open, and the part settled now becomes a
// first installment due on the business date so that the payment settles it.
func payableSchedule(fields map[string]string, requested []Installment, total, settledNow money.Money, date time.Time) []Installment {
	if len(requested) == 0 {
		return nil
	}
	open := total.Sub(settledNow)
	sum := money.ZeroMoney
	prev := date
	for i, inst := range requested {
		if !inst.Amount.IsPositive() {
			fields[fmt.Sprintf("schedule.%d.amount", i)] = "must_be_positive"
		}
		switch {
		case inst.DueDate.Before(date):
			fields[fmt.Sprintf("schedule.%d.due_date", i)] = "before_business_date"
		case inst.DueDate.Before(prev):
			fields[fmt.Sprintf("schedule.%d.due_date", i)] = "not_in_order"
		}
		prev = inst.DueDate
		sum = sum.Add(inst.Amount)
	}
	if !sum.Equal(open) {
		fields["schedule"] = "total_mismatch"
	}
	if !settledNow.IsPositive() {
		return requested
	}
	return append([]Installment{{DueDate: date, Amount: settledNow}}, requested...)
}

// receiptLineRef is a request line resolved against the order: what to price and the ordered line.
type receiptLineRef struct {
	condition string
	orderLine *PurchaseOrderLine
	price     PriceLine
	lotNo     string
	expiry    *time.Time
	spec      *string
}

// resolveReceiptLines applies the order to the request lines. order is nil for a quick receipt.
func resolveReceiptLines(in []ReceiptLineInput, order *PurchaseOrder, orderLines map[uuid.UUID]*PurchaseOrderLine) (
	[]receiptLineRef, map[string]string) {
	fields := map[string]string{}
	out := make([]receiptLineRef, len(in))
	// earlier is what the lines above already receive of each ordered line, in the order unit.
	earlier := map[uuid.UUID]money.Qty{}
	for i, l := range in {
		ref := receiptLineRef{condition: l.Condition, lotNo: l.LotNo, expiry: l.ExpiryDate, spec: l.SpecText}
		if ref.condition == "" {
			ref.condition = ConditionAccepted
		}
		discount := money.ZeroMoney
		if l.DiscountAmount != nil {
			discount = *l.DiscountAmount
		}
		ref.price = PriceLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty, Discount: discount}
		switch {
		case l.PurchaseOrderLineID != nil && order == nil:
			fields[lineField(i, "purchase_order_line_id")] = "not_allowed"
		case l.PurchaseOrderLineID != nil:
			ol, ok := orderLines[*l.PurchaseOrderLineID]
			if !ok {
				fields[lineField(i, "purchase_order_line_id")] = "not_on_order"
				break
			}
			ref.orderLine = ol
			if l.SkuID != uuid.Nil && l.SkuID != ol.SkuID {
				fields[lineField(i, "sku_id")] = "not_on_order"
			}
			if l.UnitID != nil && *l.UnitID != ol.UnitID {
				fields[lineField(i, "unit_id")] = "not_order_unit"
			}
			if ol.ColorID != nil {
				if l.ColorID != nil && *l.ColorID != *ol.ColorID {
					fields[lineField(i, "color_id")] = "not_on_order"
				}
				ref.price.ColorID = ol.ColorID
			}
			unit := ol.UnitID
			ref.price.SkuID, ref.price.UnitID, ref.price.UnitPrice = ol.SkuID, &unit, ol.UnitPrice
			if l.UnitPrice != nil {
				ref.price.UnitPrice = *l.UnitPrice
			}
			if l.DiscountAmount == nil {
				ref.price.Discount = proratedDiscount(*ol, earlier[ol.ID], l.Qty)
			}
			if inStock(ref.condition) {
				earlier[ol.ID] = earlier[ol.ID].Add(l.Qty)
			}
		default:
			if order != nil && ref.condition != ConditionExcess && ref.condition != ConditionWrongItem {
				fields[lineField(i, "purchase_order_line_id")] = "required"
			}
			if order == nil && ref.condition == ConditionExcess {
				fields[lineField(i, "condition")] = "not_allowed"
			}
			if l.UnitPrice == nil {
				fields[lineField(i, "unit_price")] = "required"
			} else {
				ref.price.UnitPrice = *l.UnitPrice
			}
		}
		out[i] = ref
	}
	return out, fields
}

// proratedDiscount is the share of an ordered line's discount that a batch of qty (in the order
// unit) carries, earlier being what other lines of the same receipt already take of the line. The
// share is cumulative: the whole-dong discount of everything received so far, this batch included,
// minus that of what came before. The batches of a fully received line then add up to the ordered
// discount exactly (the rounding remainder lands on the last one), and what is received beyond the
// ordered quantity carries none.
func proratedDiscount(ol PurchaseOrderLine, earlier, qty money.Qty) money.Money {
	if ol.DiscountAmount.IsZero() || !ol.Qty.IsPositive() || !ol.BaseQty.IsPositive() {
		return money.ZeroMoney
	}
	ordered, total := ol.Qty.Decimal(), ol.DiscountAmount.Decimal()
	upTo := func(received decimal.Decimal) decimal.Decimal {
		if received.Cmp(ordered) >= 0 {
			return total
		}
		return total.Mul(received).Div(ordered).Round(0)
	}
	// received_base_qty is in base units; the order unit is the line's.
	before := ol.ReceivedBaseQty.Decimal().Mul(ordered).Div(ol.BaseQty.Decimal()).Add(earlier.Decimal())
	m, err := money.RoundMoney(upTo(before.Add(qty.Decimal())).Sub(upTo(before)))
	if err != nil {
		// Unreachable: the share lies between zero and the ordered discount, a valid amount.
		return money.ZeroMoney
	}
	return m
}

// checkLotFields applies the lot mode of the SKU: lot number and expiry date are required when
// tracked and refused otherwise. wrong_item lines never enter stock and are not checked.
func checkLotFields(fields map[string]string, i int, info skuInfo, ref receiptLineRef) {
	if ref.condition == ConditionWrongItem {
		return
	}
	switch {
	case info.TrackLot && ref.lotNo == "":
		fields[lineField(i, "lot_no")] = "required"
	case !info.TrackLot && ref.lotNo != "":
		fields[lineField(i, "lot_no")] = "not_tracked"
	}
	switch {
	case info.TrackExpiry && ref.expiry == nil:
		fields[lineField(i, "expiry_date")] = "required"
	case !info.TrackExpiry && ref.expiry != nil:
		fields[lineField(i, "expiry_date")] = "not_tracked"
	}
}

// inStock reports whether a receipt line's goods entered stock.
func inStock(condition string) bool { return condition != ConditionWrongItem }

// orderReceived is what a receipt adds to the received quantity of each ordered line: every line
// that entered stock (excess and damaged included); wrong items are not what was ordered.
func orderReceived(lines []ReceiptLine) map[uuid.UUID]money.Qty {
	out := map[uuid.UUID]money.Qty{}
	for _, l := range lines {
		if l.PurchaseOrderLineID == nil || !inStock(l.Condition) {
			continue
		}
		out[*l.PurchaseOrderLineID] = out[*l.PurchaseOrderLineID].Add(l.BaseQty)
	}
	return out
}

// checkOrderQty refuses a batch that receives more of an ordered line than is still pending,
// except what is flagged as excess.
func checkOrderQty(lines []ReceiptLine, orderLines map[uuid.UUID]*PurchaseOrderLine) error {
	expected := map[uuid.UUID]money.Qty{}
	for _, l := range lines {
		if l.PurchaseOrderLineID == nil || !inStock(l.Condition) || l.Condition == ConditionExcess {
			continue
		}
		expected[*l.PurchaseOrderLineID] = expected[*l.PurchaseOrderLineID].Add(l.BaseQty)
	}
	for id, qty := range expected {
		ol := orderLines[id]
		if ol != nil && qty.Cmp(ol.PendingBaseQty()) > 0 {
			return apperr.NewConflict(CodeReceiptExceedsOrder, fmt.Sprintf(
				"Line %d of the order has %s pending but %s was received; flag the rest as excess", ol.LineNo,
				ol.PendingBaseQty(), qty))
		}
	}
	return nil
}

// receiptTotals adds up the lines that entered stock: wrong items are claimed, not owed.
func receiptTotals(r *Receipt, lines []ReceiptLine, gross []money.Money, taxes []money.Money) {
	r.GoodsAmount, r.DiscountAmount, r.TaxAmount, r.TotalAmount = money.ZeroMoney, money.ZeroMoney, money.ZeroMoney, money.ZeroMoney
	for i, l := range lines {
		if !inStock(l.Condition) {
			continue
		}
		r.GoodsAmount = r.GoodsAmount.Add(gross[i])
		r.DiscountAmount = r.DiscountAmount.Add(l.DiscountAmount)
		r.TaxAmount = r.TaxAmount.Add(taxes[i])
		r.TotalAmount = r.TotalAmount.Add(l.LineAmount)
	}
}

// unitCost is the cost of one base unit of a line: its amount and landed cost over its quantity.
func unitCost(lineAmount, landed money.Money, baseQty money.Qty) (money.Money, error) {
	if !baseQty.IsPositive() {
		return money.ZeroMoney, nil
	}
	return money.RoundMoney(lineAmount.Add(landed).Decimal().Div(baseQty.Decimal()))
}

// PostReceipt posts a PN: goods into stock (damaged lines into quarantine), the payable of the
// supplier once, the payment made now and the advances used, and the progress of the order. A
// retry with the same key replays the first response without writing anything.
func (s *Service) PostReceipt(ctx context.Context, actor audit.Actor, key idempotency.Key, in ReceiptInput) (posting.Response, error) {
	if fields := validateReceiptInput(in); len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	date, err := s.postingDate(ctx, actor.TenantID, in.BusinessDate)
	if err != nil {
		return posting.Response{}, err
	}
	skus := make([]uuid.UUID, 0, len(in.Lines))
	for _, l := range in.Lines {
		if l.SkuID != uuid.Nil {
			skus = append(skus, l.SkuID)
		}
	}
	if in.PurchaseOrderID != nil {
		// The SKUs of ordered lines come from the order; reading them first lets the posting lock
		// their stock before Apply.
		if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
			lines, err := orderLines(ctx, q, *in.PurchaseOrderID)
			for _, l := range lines {
				skus = append(skus, l.SkuID)
			}
			return err
		}); err != nil {
			return posting.Response{}, err
		}
	}
	return s.posting.Post(ctx, actor, key, receiptPost{s: s, date: date, in: in, skus: sortedUnique(skus),
		key: ScopeReceiptPost + ":" + key.Value})
}

type receiptPost struct {
	s    *Service
	date time.Time
	in   ReceiptInput
	skus []uuid.UUID
	key  string
}

func (c receiptPost) Scope() string           { return ScopeReceiptPost }
func (c receiptPost) BusinessDate() time.Time { return c.date }
func (c receiptPost) DocPrefix() string       { return docseq.PrefixReceipt }

func (c receiptPost) ExtraDocPrefixes() []string {
	if c.in.PaidNowAmount.IsPositive() {
		return []string{PaymentPrefix}
	}
	return nil
}

func (c receiptPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	for _, a := range c.in.UseAdvances {
		set.Add(posting.LockPayments, a.PaymentID)
	}
	return set
}

func (c receiptPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	in := c.in
	var order *PurchaseOrder
	ordered := map[uuid.UUID]*PurchaseOrderLine{}
	var supplier uuid.UUID
	if in.PurchaseOrderID != nil {
		o, err := getOrder(ctx, tx, *in.PurchaseOrderID, true)
		if err != nil {
			return posting.Result{}, err
		}
		if !c.s.status.Receivable(o.Status) {
			return posting.Result{}, apperr.NewConflict(CodeOrderNotReceivable, "A "+o.Status+" purchase order takes no receipts")
		}
		if in.SupplierPartyID != nil && *in.SupplierPartyID != o.SupplierPartyID {
			return posting.Result{}, errInvalid(map[string]string{"supplier_party_id": "not_order_supplier"})
		}
		lines, err := orderLines(ctx, tx, o.ID)
		if err != nil {
			return posting.Result{}, err
		}
		for i := range lines {
			ordered[lines[i].ID] = &lines[i]
		}
		order, supplier = &o, o.SupplierPartyID
	} else {
		supplier = *in.SupplierPartyID
		if err := checkSupplier(ctx, tx, supplier, "supplier_party_id"); err != nil {
			return posting.Result{}, err
		}
	}
	refs, fields := resolveReceiptLines(in.Lines, order, ordered)
	if len(fields) > 0 {
		return posting.Result{}, errInvalid(fields)
	}
	// A new product's SKU is created here; no other transaction can hold stock of it yet, so it
	// needs no lock.
	created := map[uuid.UUID]bool{}
	for i, l := range in.Lines {
		if l.NewProduct == nil {
			continue
		}
		skuID, unit, err := c.s.createProduct(ctx, tx, tx.Actor(), i, *l.NewProduct, l.UnitID, l.UnitPrice)
		if err != nil {
			return posting.Result{}, err
		}
		refs[i].price.SkuID, refs[i].price.UnitID = skuID, &unit
		created[skuID] = true
	}
	// The stock locks were taken on the SKUs the order had before the posting began; one it has
	// now that was not locked must not be received without its lock.
	for _, r := range refs {
		if created[r.price.SkuID] {
			continue
		}
		if _, ok := slices.BinarySearchFunc(c.skus, r.price.SkuID, compareUUID); !ok {
			return posting.Result{}, apperr.NewConflict(CodeOrderChanged, "The purchase order changed while the receipt was posted; try again")
		}
	}
	req := make([]PriceLine, len(refs))
	for i, r := range refs {
		req[i] = r.price
	}
	priced, _, err := c.s.priceLines(ctx, tx, tx.TenantID(), c.date, req)
	if err != nil {
		return posting.Result{}, err
	}
	for i, r := range refs {
		checkLotFields(fields, i, priced[i].Sku, r)
	}
	if len(fields) > 0 {
		return posting.Result{}, errInvalid(fields)
	}
	wh, err := warehouseID(ctx, tx)
	if err != nil {
		return posting.Result{}, err
	}

	now := tx.Now()
	r := Receipt{ID: ids.New(), WarehouseID: wh, DocNo: tx.DocNo(), SupplierPartyID: supplier, PurchaseOrderID: in.PurchaseOrderID,
		BusinessDate: c.date, LandedCostAmount: money.ZeroMoney, PaidNowAmount: in.PaidNowAmount, Status: DocPosted, Note: in.Note,
		IdempotencyKey: c.key, PostedAt: now, PostedBy: tx.Actor().ID, CreatedAt: now}
	lines := make([]ReceiptLine, len(refs))
	gross, taxes := make([]money.Money, len(refs)), make([]money.Money, len(refs))
	for i, ref := range refs {
		p := priced[i]
		spec := p.Sku.SpecText
		if ref.orderLine != nil {
			spec = ref.orderLine.SpecText
		}
		cost, err := unitCost(p.Priced.LineAmount, money.ZeroMoney, p.Priced.BaseQty)
		if err != nil {
			return posting.Result{}, err
		}
		l := ReceiptLine{ID: ids.New(), ReceiptID: r.ID, LineNo: i + 1, SkuID: ref.price.SkuID, SkuCodeSnap: p.Sku.Code,
			NameSnap: p.Sku.DisplayName, VariantLabelSnap: p.Sku.VariantLabel, UnitID: p.Priced.UnitID, UnitNameSnap: p.UnitName,
			UnitFactorSnap: p.Priced.Factor, Qty: ref.price.Qty, BaseQty: p.Priced.BaseQty, ColorID: ref.price.ColorID,
			SpecText: lineSpec(ref.spec, spec), Condition: ref.condition, LotNo: ref.lotNo, ExpiryDate: ref.expiry,
			UnitPrice: p.Priced.UnitPrice, DiscountAmount: p.Priced.Discount, TaxRateSnap: p.Priced.TaxRate,
			LineAmount: p.Priced.LineAmount, LandedCostAmount: money.ZeroMoney, UnitCost: cost, ReturnedBaseQty: money.ZeroQty}
		if ref.orderLine != nil {
			id := ref.orderLine.ID
			l.PurchaseOrderLineID = &id
		}
		lines[i], gross[i], taxes[i] = l, p.Priced.Gross, p.Priced.Tax
	}
	if err := checkOrderQty(lines, ordered); err != nil {
		return posting.Result{}, err
	}
	receiptTotals(&r, lines, gross, taxes)

	advances := money.ZeroMoney
	for _, a := range in.UseAdvances {
		advances = advances.Add(a.Amount)
	}
	switch {
	case in.PaidNowAmount.Cmp(r.TotalAmount) > 0:
		fields["paid_now_amount"] = "exceeds_total"
	case in.PaidNowAmount.Add(advances).Cmp(r.TotalAmount) > 0:
		fields["use_advances"] = "exceeds_total"
	}
	schedule := payableSchedule(fields, in.Schedule, r.TotalAmount, in.PaidNowAmount.Add(advances), c.date)
	if len(fields) > 0 {
		return posting.Result{}, errInvalid(fields)
	}

	if err := c.receiveStock(ctx, tx, r.ID, lines); err != nil {
		return posting.Result{}, err
	}
	if order != nil {
		if err := c.s.addOrderProgress(ctx, tx, *order, orderReceived(lines), now); err != nil {
			return posting.Result{}, err
		}
	}

	var payable *Payable
	if r.TotalAmount.IsPositive() {
		p, err := c.s.debt.RecordPayable(ctx, tx, PayableInput{PartyID: supplier, SourceType: DocReceipt, SourceID: r.ID,
			SourceDocNo: r.DocNo, Amount: r.TotalAmount, DueDate: in.DueDate, Schedule: schedule})
		if err != nil {
			return posting.Result{}, err
		}
		payable = &p
		r.DueDate = &p.DueDate
	}
	if err := insertReceipt(ctx, tx, tx.TenantID(), r); err != nil {
		return posting.Result{}, err
	}
	if err := insertReceiptLines(ctx, tx, tx.TenantID(), lines); err != nil {
		return posting.Result{}, err
	}
	if payable != nil {
		if in.PaidNowAmount.IsPositive() {
			note := "Thanh toán " + r.DocNo
			if _, err := c.s.debt.PayNow(ctx, tx, PaymentInput{PartyID: supplier, MethodID: *in.PaymentMethodID,
				Amount: in.PaidNowAmount, SourceType: PaymentSourcePurchase, SourceID: r.ID, ObligationID: payable.ID,
				Key: c.key + ":pay", Note: note}); err != nil {
				return posting.Result{}, err
			}
		}
		for i, a := range in.UseAdvances {
			if err := c.s.debt.UseAdvance(ctx, tx, a.PaymentID, payable.ID, a.Amount); err != nil {
				return posting.Result{}, renameFields(err, fmt.Sprintf("use_advances.%d", i))
			}
		}
	}

	if in.CampaignID != nil {
		if c.s.campaigns == nil {
			return posting.Result{}, errInvalid(map[string]string{"campaign_id": "not_found"})
		}
		if err := c.s.campaigns.TagReceipt(ctx, tx, r.ID, *in.CampaignID); err != nil {
			return posting.Result{}, err
		}
	}

	d, err := receiptDetail(ctx, tx, r.ID)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(audit.Event{Action: "receipt.post", EntityType: DocReceipt, EntityID: r.ID, DocNo: r.DocNo, After: d})
	return posting.Result{Status: http.StatusCreated, Body: toReceiptDTO(d)}, nil
}

// receiveStock books the lines that entered stock and resolves the lot of each line: the lot it
// was received into, or for a damaged line the quarantine twin it was moved on to.
func (c receiptPost) receiveStock(ctx context.Context, tx posting.Tx, receiptID uuid.UUID, lines []ReceiptLine) error {
	doc := StockDoc{Type: DocReceipt, ID: receiptID}
	var recv []StockReceive
	var index []int
	for i, l := range lines {
		if !inStock(l.Condition) {
			continue
		}
		recv = append(recv, StockReceive{LineID: l.ID, SkuID: l.SkuID, Qty: l.BaseQty, UnitCost: l.UnitCost, ColorID: l.ColorID,
			LotNo: l.LotNo, ExpiryDate: l.ExpiryDate})
		index = append(index, i)
	}
	if len(recv) == 0 {
		return nil
	}
	moves, err := c.s.stock.Receive(ctx, tx, doc, recv)
	if err != nil {
		return remapLines(err, index)
	}
	lotOf := movedLots(moves, false)
	var quarantine []StockQuarantine
	for i := range lines {
		l := &lines[i]
		if !inStock(l.Condition) {
			continue
		}
		lot, ok := lotOf[l.ID]
		if !ok {
			return fmt.Errorf("procurement: the ledger wrote no move for receipt line %d", l.LineNo)
		}
		l.LotID = &lot
		if l.Condition == ConditionDamaged {
			quarantine = append(quarantine, StockQuarantine{LineID: l.ID, SkuID: l.SkuID, Qty: l.BaseQty, LotID: lot})
		}
	}
	if len(quarantine) == 0 {
		return nil
	}
	moves, err = c.s.stock.Quarantine(ctx, tx, doc, quarantine)
	if err != nil {
		return err
	}
	twins := movedLots(moves, true)
	for i := range lines {
		l := &lines[i]
		if l.Condition != ConditionDamaged {
			continue
		}
		twin, ok := twins[l.ID]
		if !ok {
			return fmt.Errorf("procurement: the ledger wrote no quarantine move for receipt line %d", l.LineNo)
		}
		l.LotID = &twin
	}
	return nil
}

// movedLots maps each document line to the lot an inbound move put stock into (normal lots, or
// the quarantine twins when quarantine is set).
func movedLots(moves []StockMove, quarantine bool) map[uuid.UUID]uuid.UUID {
	out := map[uuid.UUID]uuid.UUID{}
	for _, m := range moves {
		if m.LineID != nil && m.Qty.IsPositive() && m.Quarantine == quarantine {
			out[*m.LineID] = m.LotID
		}
	}
	return out
}

// addOrderProgress adds received quantities (signed) to the ordered lines and moves the order to
// the status they justify.
func (s *Service) addOrderProgress(ctx context.Context, q db.Querier, o PurchaseOrder, received map[uuid.UUID]money.Qty, now time.Time) error {
	for _, id := range sortedKeys(received) {
		if _, err := q.ExecContext(ctx, `UPDATE purchase_order_lines SET received_base_qty = received_base_qty + $2 WHERE id = $1`,
			id, received[id]); err != nil {
			return wrap("move purchase order received quantity", err)
		}
	}
	lines, err := orderLines(ctx, q, o.ID)
	if err != nil {
		return err
	}
	next, err := s.status.Follow(o.Status, lines)
	if err != nil {
		return err
	}
	if next == o.Status {
		_, err = q.ExecContext(ctx, `UPDATE purchase_orders SET updated_at = $2, version = version + 1 WHERE id = $1`, o.ID, now)
	} else {
		_, err = q.ExecContext(ctx, `UPDATE purchase_orders SET status = $2, updated_at = $3, version = version + 1 WHERE id = $1`,
			o.ID, next, now)
	}
	if err != nil {
		return wrap("move purchase order status", err)
	}
	return nil
}

func sortedKeys[V any](m map[uuid.UUID]V) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return sortedUnique(out)
}

// renameFields puts the fields of a validation error raised for one entry of a list under the
// entry's name ("use_advances.0.amount").
func renameFields(err error, prefix string) error {
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.Validation {
		return err
	}
	fields := map[string]string{}
	for k, v := range e.Fields {
		fields[prefix+"."+k] = v
	}
	if len(fields) == 0 {
		fields[prefix] = "invalid"
	}
	out := *e
	out.Fields = fields
	return out.WithCause(err)
}

// ReverseReceipt reverses a posted receipt that nothing else built on yet: its stock comes back out
// of the lots (409 receipt_stock_consumed when some was sold or moved), its payable is reversed
// (its payments become the supplier's advance) and the order is moved back. Landed costs,
// returns, price adjustments and supplier documents must be undone first.
func (s *Service) ReverseReceipt(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in ReverseInput) (posting.Response, error) {
	switch n := utf8.RuneCountInString(in.Reason); {
	case n == 0:
		return posting.Response{}, errInvalid(map[string]string{"reason": "required"})
	case n > MaxText:
		return posting.Response{}, errInvalid(map[string]string{"reason": "too_long"})
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var skus []uuid.UUID
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if _, err := getReceipt(ctx, q, id, false); err != nil {
			return err
		}
		lines, err := receiptLines(ctx, q, id)
		for _, l := range lines {
			skus = append(skus, l.SkuID)
		}
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, receiptReverse{s: s, date: date, id: id, skus: sortedUnique(skus), in: in,
		key: ScopeReceiptReverse + ":" + key.Value})
}

type receiptReverse struct {
	s    *Service
	date time.Time
	id   uuid.UUID
	skus []uuid.UUID
	in   ReverseInput
	key  string
}

func (c receiptReverse) Scope() string           { return ScopeReceiptReverse }
func (c receiptReverse) BusinessDate() time.Time { return c.date }
func (c receiptReverse) DocPrefix() string       { return docseq.PrefixReceipt }

func (c receiptReverse) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	return set
}

func (c receiptReverse) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	orig, err := getReceipt(ctx, tx, c.id, true)
	if err != nil {
		return posting.Result{}, err
	}
	switch {
	case orig.ReversalOf != nil:
		return posting.Result{}, apperr.NewConflict(CodeReversalNotReversed, "A reversal cannot be reversed; post a new receipt")
	case orig.Status != DocPosted:
		return posting.Result{}, apperr.NewConflict(CodeAlreadyReversed, "The receipt was already reversed")
	}
	if err := checkNoFollowups(ctx, tx, c.id); err != nil {
		return posting.Result{}, err
	}
	lines, err := receiptLines(ctx, tx, c.id)
	if err != nil {
		return posting.Result{}, err
	}
	now := tx.Now()
	revID := ids.New()
	if err := c.s.stock.Reverse(ctx, tx, StockDoc{Type: DocReceipt, ID: revID}, StockDoc{Type: DocReceipt, ID: c.id}); err != nil {
		if apperr.Is(err, codeInsufficientStock) {
			return posting.Result{}, apperr.NewConflict(CodeReceiptStockConsumed,
				"Part of the received goods was already sold or moved; post a purchase return instead").WithCause(err)
		}
		return posting.Result{}, err
	}
	if orig.TotalAmount.IsPositive() {
		if err := c.s.debt.ReversePayable(ctx, tx, DocReceipt, c.id, c.in.Reason); err != nil {
			return posting.Result{}, err
		}
	}
	if orig.PurchaseOrderID != nil {
		o, err := getOrder(ctx, tx, *orig.PurchaseOrderID, true)
		if err != nil {
			return posting.Result{}, err
		}
		back := orderReceived(lines)
		for id, q := range back {
			back[id] = q.Neg()
		}
		if err := c.s.addOrderProgress(ctx, tx, o, back, now); err != nil {
			return posting.Result{}, err
		}
	}
	origID := c.id
	note := c.in.Reason
	rev := orig
	rev.ID, rev.DocNo, rev.BusinessDate, rev.PaidNowAmount, rev.Status = revID, tx.DocNo(), c.date, money.ZeroMoney, DocPosted
	rev.Note, rev.IdempotencyKey, rev.PostedAt, rev.PostedBy, rev.ReversalOf, rev.CreatedAt = &note, c.key, now, tx.Actor().ID, &origID, now
	rev.DueDate = nil
	if err := insertReceipt(ctx, tx, tx.TenantID(), rev); err != nil {
		return posting.Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE receipts SET status = $2 WHERE id = $1`, c.id, DocReversed); err != nil {
		return posting.Result{}, wrap("mark receipt reversed", err)
	}
	tx.Audit(audit.Event{Action: "receipt.reverse", EntityType: DocReceipt, EntityID: c.id, DocNo: orig.DocNo,
		Before: map[string]any{"status": orig.Status}, After: map[string]any{"status": DocReversed, "reversal_id": revID,
			"reversal_doc_no": rev.DocNo, "reason": c.in.Reason}})
	d, err := receiptDetail(ctx, tx, revID)
	if err != nil {
		return posting.Result{}, err
	}
	return posting.Result{Status: http.StatusCreated, Body: toReceiptDTO(d)}, nil
}

// checkNoFollowups refuses to reverse a receipt that later documents build on: their amounts
// were computed from its lines.
func checkNoFollowups(ctx context.Context, q db.Querier, receiptID uuid.UUID) error {
	var has bool
	if err := q.QueryRowxContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM landed_cost_allocations WHERE receipt_id = $1)
		OR EXISTS (SELECT 1 FROM purchase_returns WHERE receipt_id = $1 AND status = 'posted')
		OR EXISTS (SELECT 1 FROM purchase_price_adjustments WHERE receipt_id = $1 AND status = 'posted')
		OR EXISTS (SELECT 1 FROM supplier_document_receipts WHERE receipt_id = $1)`, receiptID).Scan(&has); err != nil {
		return wrap("read receipt follow-ups", err)
	}
	if has {
		return apperr.NewConflict(CodeReceiptHasFollowups,
			"Landed costs, returns, price adjustments or supplier documents are based on this receipt; it cannot be reversed")
	}
	return nil
}
