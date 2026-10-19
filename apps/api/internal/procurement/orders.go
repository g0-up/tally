package procurement

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Idempotency scopes of the procurement creates; each doubles as the audit action.
const (
	ScopeOrderCreate    = "purchase_order.create"
	ScopeDocumentCreate = "supplier_document.create"
)

// Limits of the procurement documents.
const (
	// MaxLines is the most lines a procurement document carries (the pricing engine's limit).
	MaxLines = 500
	// MaxText is the longest note, reason or description, in characters.
	MaxText = 500
)

// OrderLineInput is a PO line as typed on the screen. UnitID nil is the SKU's base unit;
// RetailPrice is the "Giá bán lẻ" cell, set only when the user edited it.
type OrderLineInput struct {
	SkuID          uuid.UUID
	UnitID         *uuid.UUID
	Qty            money.Qty
	ColorID        *uuid.UUID
	UnitPrice      *money.Money
	DiscountAmount money.Money
	RetailPrice    *money.Money
	// SpecText is the free "Quy cách" note of the line; empty keeps the SKU's own.
	SpecText *string
	// NewProduct creates the product of the line instead of SkuID; RetailPrice is then ignored
	// (the new product carries its own prices).
	NewProduct *NewProduct
}

// OrderInput creates a draft PO. OrderDate nil is today.
type OrderInput struct {
	SupplierPartyID uuid.UUID
	OrderDate       *time.Time
	ExpectedDate    *time.Time
	Note            *string
	Lines           []OrderLineInput
}

// OrderPatch changes the header of a draft PO; nil fields are kept.
type OrderPatch struct {
	SupplierPartyID *uuid.UUID
	OrderDate       *time.Time
	ExpectedDate    *time.Time
	Note            *string
}

// CloseShortInput closes an order that will not be delivered in full.
type CloseShortInput struct {
	ReasonID uuid.UUID
}

// OrderDetail is a PO with its lines and document photos.
type OrderDetail struct {
	PurchaseOrder
	Lines []PurchaseOrderLine
}

func validText(fields map[string]string, name string, v *string) {
	if v != nil && utf8.RuneCountInString(*v) > MaxText {
		fields[name] = "too_long"
	}
}

// lineSpec is the "Quy cách" a line records: the text typed on it, else the fallback (the SKU's
// spec, or the ordered line's when receiving against an order).
func lineSpec(typed, fallback *string) *string {
	if typed != nil {
		if t := strings.TrimSpace(*typed); t != "" {
			return &t
		}
	}
	return fallback
}

func validateOrderLines(fields map[string]string, lines []OrderLineInput) {
	if len(lines) > MaxLines {
		fields["lines"] = "too_many"
		return
	}
	for i, l := range lines {
		validateNewProduct(fields, i, l.SkuID, l.UnitID, l.NewProduct)
		if !l.Qty.IsPositive() {
			fields[lineField(i, "qty")] = "must_be_positive"
		}
		switch {
		case l.UnitPrice == nil:
			fields[lineField(i, "unit_price")] = "required"
		case l.UnitPrice.IsNegative():
			fields[lineField(i, "unit_price")] = "must_not_be_negative"
		}
		if l.DiscountAmount.IsNegative() {
			fields[lineField(i, "discount_amount")] = "must_not_be_negative"
		}
		if l.RetailPrice != nil && l.RetailPrice.IsNegative() {
			fields[lineField(i, "retail_price")] = "must_not_be_negative"
		}
		validText(fields, lineField(i, "spec_text"), l.SpecText)
	}
}

func validateOrderDates(fields map[string]string, orderDate time.Time, expected *time.Time, today time.Time) {
	if orderDate.After(today) {
		fields["order_date"] = "in_future"
	}
	if expected != nil && expected.Before(orderDate) {
		fields["expected_date"] = "before_order_date"
	}
}

// CreateOrder saves a draft PO. A draft has no effect on stock or debt. With a key it runs once
// per Idempotency-Key: a retry of the same request returns the stored PO (replayed true) instead
// of saving a second one.
func (s *Service) CreateOrder(ctx context.Context, actor audit.Actor, key *idempotency.Key, in OrderInput) (OrderDetail, bool, error) {
	var out OrderDetail
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeOrderCreate, key, http.StatusCreated, &out, func() error {
			// Checked after the key is claimed, so a retry that lands on the next day still replays.
			today, err := tenantToday(ctx, q, actor.TenantID, s.clock.Now())
			if err != nil {
				return err
			}
			orderDate := today
			if in.OrderDate != nil {
				orderDate = *in.OrderDate
			}
			fields := map[string]string{}
			if in.SupplierPartyID == uuid.Nil {
				fields["supplier_party_id"] = "required"
			}
			validateOrderDates(fields, orderDate, in.ExpectedDate, today)
			validText(fields, "note", in.Note)
			validateOrderLines(fields, in.Lines)
			if len(fields) > 0 {
				return errInvalid(fields)
			}
			docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixPurchaseOrder)
			if err != nil {
				return err
			}
			if err := checkSupplier(ctx, q, in.SupplierPartyID, "supplier_party_id"); err != nil {
				return err
			}
			now := s.clock.Now()
			o := PurchaseOrder{ID: ids.New(), DocNo: docNo, SupplierPartyID: in.SupplierPartyID, OrderDate: orderDate,
				ExpectedDate: in.ExpectedDate, Status: StatusDraft, Note: in.Note, CreatedAt: now, UpdatedAt: now, Version: 1}
			lines, err := s.buildOrderLines(ctx, q, actor, &o, in.Lines)
			if err != nil {
				return err
			}
			if err := insertOrder(ctx, q, actor.TenantID, o); err != nil {
				return err
			}
			if err := insertOrderLines(ctx, q, actor.TenantID, lines); err != nil {
				return err
			}
			if out, err = orderDetail(ctx, q, o.ID); err != nil {
				return err
			}
			return s.record(ctx, q, actor, ScopeOrderCreate, "purchase_order", o.ID, nil, out)
		})
		return err
	})
	return out, replayed, err
}

// buildOrderLines prices the lines of order o (purchase mode: the typed price, no promotions, tax
// per line), sets its totals and writes the "Giá bán lẻ" the user edited through the price
// writer, so documents priced afterwards use it.
func (s *Service) buildOrderLines(ctx context.Context, q db.Querier, actor audit.Actor, o *PurchaseOrder,
	in []OrderLineInput) ([]PurchaseOrderLine, error) {
	o.GoodsAmount, o.DiscountAmount, o.TaxAmount, o.TotalAmount = money.ZeroMoney, money.ZeroMoney, money.ZeroMoney, money.ZeroMoney
	if len(in) == 0 {
		return nil, nil
	}
	in = slices.Clone(in)
	for i, l := range in {
		if l.NewProduct == nil {
			continue
		}
		skuID, unit, err := s.createProduct(ctx, q, actor, i, *l.NewProduct, l.UnitID, l.UnitPrice)
		if err != nil {
			return nil, err
		}
		in[i].SkuID, in[i].UnitID, in[i].RetailPrice = skuID, &unit, nil
	}
	req := make([]PriceLine, len(in))
	for i, l := range in {
		req[i] = PriceLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty, UnitPrice: *l.UnitPrice,
			Discount: l.DiscountAmount}
	}
	priced, doc, err := s.priceLines(ctx, q, actor.TenantID, o.OrderDate, req)
	if err != nil {
		return nil, err
	}
	for i := range req {
		u := priced[i].Priced.UnitID
		req[i].UnitID = &u
	}
	current, err := s.pricing.RetailPrices(ctx, q, actor.TenantID, o.OrderDate, req)
	if err != nil {
		return nil, remapLines(err, nil)
	}
	out := make([]PurchaseOrderLine, len(in))
	for i, l := range in {
		p := priced[i]
		retail := current[i]
		if l.RetailPrice != nil {
			if retail == nil || !retail.Equal(*l.RetailPrice) {
				if err := s.pricing.SetRetailPrice(ctx, q, actor, RetailPrice{SkuID: l.SkuID, ColorID: l.ColorID,
					UnitID: p.Priced.UnitID, Price: *l.RetailPrice, OrderID: o.ID}); err != nil {
					return nil, retailPriceError(err, i)
				}
			}
			v := *l.RetailPrice
			retail = &v
		}
		out[i] = PurchaseOrderLine{ID: ids.New(), PurchaseOrderID: o.ID, LineNo: i + 1, SkuID: l.SkuID,
			SkuCodeSnap: p.Sku.Code, NameSnap: p.Sku.DisplayName, VariantLabelSnap: p.Sku.VariantLabel,
			UnitID: p.Priced.UnitID, UnitNameSnap: p.UnitName, UnitFactorSnap: p.Priced.Factor, Qty: l.Qty,
			BaseQty: p.Priced.BaseQty, ColorID: l.ColorID, SpecText: lineSpec(l.SpecText, p.Sku.SpecText), UnitPrice: p.Priced.UnitPrice,
			DiscountAmount: p.Priced.Discount, TaxRateSnap: p.Priced.TaxRate, LineAmount: p.Priced.LineAmount,
			RetailPriceSnap: retail}
	}
	o.GoodsAmount, o.DiscountAmount, o.TaxAmount, o.TotalAmount = doc.Goods, doc.Discount, doc.Tax, doc.Total
	return out, nil
}

// retailPriceError names a validation error of the price writer after request line i.
func retailPriceError(err error, i int) error {
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.Validation {
		return err
	}
	return errInvalid(map[string]string{lineField(i, "retail_price"): "invalid"})
}

// pricedLine is a request line priced in purchase mode, with the snapshot of its SKU and unit.
type pricedLine struct {
	Sku      skuInfo
	UnitName string
	Priced   PricedLine
}

// priceLines checks the SKUs of the lines (stockable, active) and prices them.
func (s *Service) priceLines(ctx context.Context, q db.Querier, tenantID uuid.UUID, date time.Time,
	lines []PriceLine) ([]pricedLine, PricedDoc, error) {
	skuIDs := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		skuIDs[i] = l.SkuID
	}
	infos, err := readSkuInfos(ctx, q, skuIDs)
	if err != nil {
		return nil, PricedDoc{}, err
	}
	if err := checkSkus(skuIDs, infos, nil); err != nil {
		return nil, PricedDoc{}, err
	}
	doc, err := s.pricing.Price(ctx, q, tenantID, date, lines)
	if err != nil {
		return nil, PricedDoc{}, remapLines(err, nil)
	}
	if len(doc.Lines) != len(lines) {
		return nil, PricedDoc{}, apperr.NewConflict("pricing_mismatch", "The priced document does not match its lines")
	}
	unitIDs := make([]uuid.UUID, len(doc.Lines))
	for i, l := range doc.Lines {
		unitIDs[i] = l.UnitID
	}
	names, err := unitNames(ctx, q, unitIDs)
	if err != nil {
		return nil, PricedDoc{}, err
	}
	out := make([]pricedLine, len(lines))
	for i, l := range lines {
		out[i] = pricedLine{Sku: infos[l.SkuID], UnitName: names[doc.Lines[i].UnitID], Priced: doc.Lines[i]}
	}
	return out, doc, nil
}

// lockDraft reads order id for update and checks it is a draft at version.
func lockDraft(ctx context.Context, q db.Querier, id uuid.UUID, version int) (PurchaseOrder, error) {
	o, err := getOrder(ctx, q, id, true)
	if err != nil {
		return o, err
	}
	if o.Version != version {
		return o, apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
	}
	if o.Status != StatusDraft {
		return o, errNotDraft()
	}
	return o, nil
}

// UpdateOrder changes the header of a draft PO at version.
func (s *Service) UpdateOrder(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in OrderPatch) (OrderDetail, error) {
	today, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return OrderDetail{}, err
	}
	var out OrderDetail
	err = s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		o, err := lockDraft(ctx, q, id, version)
		if err != nil {
			return err
		}
		before, err := orderDetail(ctx, q, id)
		if err != nil {
			return err
		}
		var sets []db.Set
		fields := map[string]string{}
		if in.SupplierPartyID != nil && *in.SupplierPartyID != o.SupplierPartyID {
			if err := checkSupplier(ctx, q, *in.SupplierPartyID, "supplier_party_id"); err != nil {
				return err
			}
			sets = append(sets, db.Set{Column: "supplier_party_id", Value: *in.SupplierPartyID})
		}
		orderDate, expected := o.OrderDate, o.ExpectedDate
		if in.OrderDate != nil {
			orderDate = *in.OrderDate
			sets = append(sets, db.Set{Column: "order_date", Value: orderDate})
		}
		if in.ExpectedDate != nil {
			expected = in.ExpectedDate
			sets = append(sets, db.Set{Column: "expected_date", Value: *expected})
		}
		if in.OrderDate != nil || in.ExpectedDate != nil {
			validateOrderDates(fields, orderDate, expected, today)
		}
		if in.Note != nil {
			validText(fields, "note", in.Note)
			sets = append(sets, db.Set{Column: "note", Value: *in.Note})
		}
		if len(fields) > 0 {
			return errInvalid(fields)
		}
		if len(sets) == 0 {
			out = before
			return nil
		}
		if _, err := db.UpdateWithVersion(ctx, q, "purchase_orders", id, version, s.clock.Now(), sets...); err != nil {
			return err
		}
		if out, err = orderDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "purchase_order.update", "purchase_order", id, before.PurchaseOrder, out.PurchaseOrder)
	})
	return out, err
}

// ReplaceLines replaces every line of a draft PO at version and reprices it.
func (s *Service) ReplaceLines(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, lines []OrderLineInput) (OrderDetail, error) {
	fields := map[string]string{}
	validateOrderLines(fields, lines)
	if len(fields) > 0 {
		return OrderDetail{}, errInvalid(fields)
	}
	var out OrderDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		o, err := lockDraft(ctx, q, id, version)
		if err != nil {
			return err
		}
		before, err := orderDetail(ctx, q, id)
		if err != nil {
			return err
		}
		built, err := s.buildOrderLines(ctx, q, actor, &o, lines)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM purchase_order_lines WHERE purchase_order_id = $1`, id); err != nil {
			return wrap("delete purchase order lines", err)
		}
		if err := insertOrderLines(ctx, q, actor.TenantID, built); err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "purchase_orders", id, version, s.clock.Now(),
			db.Set{Column: "goods_amount", Value: o.GoodsAmount}, db.Set{Column: "discount_amount", Value: o.DiscountAmount},
			db.Set{Column: "tax_amount", Value: o.TaxAmount}, db.Set{Column: "total_amount", Value: o.TotalAmount}); err != nil {
			return err
		}
		if out, err = orderDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "purchase_order.replace_lines", "purchase_order", id, before, out)
	})
	return out, err
}

// DeleteOrder deletes a draft PO at version. Confirmed orders are closed short instead.
func (s *Service) DeleteOrder(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if _, err := lockDraft(ctx, q, id, version); err != nil {
			return err
		}
		before, err := orderDetail(ctx, q, id)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM purchase_order_lines WHERE purchase_order_id = $1`, id); err != nil {
			return wrap("delete purchase order lines", err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM purchase_orders WHERE id = $1`, id); err != nil {
			return wrap("delete purchase order", err)
		}
		return s.record(ctx, q, actor, "purchase_order.delete", "purchase_order", id, before, nil)
	})
}

// ConfirmOrder confirms a draft PO at version; no approval is needed. The supplier must still be
// trading and the order must have lines.
func (s *Service) ConfirmOrder(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (OrderDetail, error) {
	var out OrderDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		o, err := getOrder(ctx, q, id, true)
		if err != nil {
			return err
		}
		if o.Version != version {
			return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
		}
		if err := s.status.Transition(o.Status, StatusConfirmed); err != nil {
			return err
		}
		lines, err := orderLines(ctx, q, id)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return apperr.NewConflict(CodeOrderEmpty, "A purchase order needs at least one line to be confirmed")
		}
		if err := checkSupplier(ctx, q, o.SupplierPartyID, "supplier_party_id"); err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := db.UpdateWithVersion(ctx, q, "purchase_orders", id, version, now,
			db.Set{Column: "status", Value: StatusConfirmed}, db.Set{Column: "confirmed_at", Value: now},
			db.Set{Column: "confirmed_by", Value: actorID(actor)}); err != nil {
			return err
		}
		if out, err = orderDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "purchase_order.confirm", "purchase_order", id,
			map[string]string{"status": o.Status}, map[string]string{"status": out.Status})
	})
	return out, err
}

// CloseShort closes an order still waiting for goods with a reason; its receipts stay as they are
// and the pending quantities are no longer expected.
func (s *Service) CloseShort(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in CloseShortInput) (OrderDetail, error) {
	if in.ReasonID == uuid.Nil {
		return OrderDetail{}, errInvalid(map[string]string{"reason_id": "required"})
	}
	var out OrderDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		o, err := getOrder(ctx, q, id, true)
		if err != nil {
			return err
		}
		if o.Version != version {
			return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
		}
		if o.Status == StatusDraft || !s.status.CanTransition(o.Status, StatusClosedShort) {
			return apperr.NewConflict(CodeOrderStatus, "Only an order waiting for goods can be closed short")
		}
		if err := checkReason(ctx, q, in.ReasonID, ReasonCloseShort, "reason_id"); err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "purchase_orders", id, version, s.clock.Now(),
			db.Set{Column: "status", Value: StatusClosedShort}, db.Set{Column: "close_reason_id", Value: in.ReasonID},
			db.Set{Column: "closed_at", Value: s.clock.Now()}); err != nil {
			return err
		}
		if out, err = orderDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "purchase_order.close_short", "purchase_order", id,
			map[string]string{"status": o.Status}, map[string]any{"status": out.Status, "reason_id": in.ReasonID})
	})
	return out, err
}

func orderDetail(ctx context.Context, q db.Querier, id uuid.UUID) (OrderDetail, error) {
	o, err := getOrder(ctx, q, id, false)
	if err != nil {
		return OrderDetail{}, err
	}
	lines, err := orderLines(ctx, q, id)
	if err != nil {
		return OrderDetail{}, err
	}
	return OrderDetail{PurchaseOrder: o, Lines: lines}, nil
}

// actorID is the user a document is posted or confirmed by (nil for the system actor).
func actorID(a audit.Actor) *uuid.UUID {
	if a.ID == uuid.Nil {
		return nil
	}
	id := a.ID
	return &id
}
