package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
)

// procurementStock is the inventory ledger as procurement sees it: every call books the moves of
// one procurement document inside its posting.
type procurementStock struct{ l *inventory.StockLedger }

func (s procurementStock) Receive(ctx context.Context, tx posting.Tx, doc procurement.StockDoc, lines []procurement.StockReceive) ([]procurement.StockMove, error) {
	in := make([]inventory.ReceiveLine, len(lines))
	for i, l := range lines {
		lineID := l.LineID
		in[i] = inventory.ReceiveLine{DocLineID: &lineID, SkuID: l.SkuID, Qty: l.Qty, UnitCost: l.UnitCost, ColorID: l.ColorID,
			LotNo: l.LotNo, ExpiryDate: l.ExpiryDate}
	}
	moves, err := s.l.Receive(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return stockMoves(moves), err
}

func (s procurementStock) Quarantine(ctx context.Context, tx posting.Tx, doc procurement.StockDoc, lines []procurement.StockQuarantine) ([]procurement.StockMove, error) {
	in := make([]inventory.QuarantineLine, len(lines))
	for i, l := range lines {
		lineID, lotID := l.LineID, l.LotID
		in[i] = inventory.QuarantineLine{DocLineID: &lineID, SkuID: l.SkuID, Qty: l.Qty, LotID: &lotID}
	}
	moves, err := s.l.Quarantine(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return stockMoves(moves), err
}

// Issue takes a purchase return out of exactly the lot it was received into.
func (s procurementStock) Issue(ctx context.Context, tx posting.Tx, doc procurement.StockDoc, lines []procurement.StockIssue) ([]procurement.StockMove, error) {
	in := make([]inventory.IssueLine, len(lines))
	for i, l := range lines {
		lineID, lotID := l.LineID, l.LotID
		in[i] = inventory.IssueLine{DocLineID: &lineID, SkuID: l.SkuID, Qty: l.Qty, MoveType: inventory.MovePurchaseReturn,
			LotID: &lotID}
	}
	moves, err := s.l.Issue(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return stockMoves(moves), err
}

func (s procurementStock) CostAdjust(ctx context.Context, tx posting.Tx, doc procurement.StockDoc, lines []procurement.StockCost) ([]procurement.StockMove, error) {
	in := make([]inventory.CostLine, len(lines))
	for i, l := range lines {
		in[i] = inventory.CostLine{DocLineID: l.LineID, SkuID: l.SkuID, LotID: l.LotID, Value: l.Value}
	}
	moves, err := s.l.CostAdjust(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return stockMoves(moves), err
}

func (s procurementStock) Reverse(ctx context.Context, tx posting.Tx, by, doc procurement.StockDoc) error {
	_, err := s.l.Reverse(ctx, tx, inventory.PostingOf(tx, by.Type, by.ID), doc.Type, doc.ID)
	return err
}

func stockMoves(moves []inventory.PostedMove) []procurement.StockMove {
	out := make([]procurement.StockMove, len(moves))
	for i, m := range moves {
		out[i] = procurement.StockMove{LineID: m.DocLineID, SkuID: m.SkuID, LotID: m.LotID, Qty: m.Qty, Value: m.Value,
			Quarantine: m.IsQuarantine}
	}
	return out
}

// procurementDebt is the payable side of the debt ledger as procurement sees it, written inside
// the procurement posting.
type procurementDebt struct{ s *debt.Service }

func (d procurementDebt) RecordPayable(ctx context.Context, tx posting.Tx, in procurement.PayableInput) (procurement.Payable, error) {
	schedule := make([]debt.InstallmentInput, len(in.Schedule))
	for i, s := range in.Schedule {
		schedule[i] = debt.InstallmentInput{DueDate: s.DueDate, Amount: s.Amount}
	}
	r, err := d.s.Payables().Record(ctx, tx, debt.ObligationInput{PartyID: in.PartyID, SourceType: in.SourceType,
		SourceID: in.SourceID, SourceDocNo: in.SourceDocNo, Amount: in.Amount, DueDate: in.DueDate, Schedule: schedule})
	if err != nil {
		return procurement.Payable{}, err
	}
	return procurement.Payable{ID: r.Obligation.ID, Amount: r.Obligation.Amount, DueDate: r.Obligation.DueDate}, nil
}

func (d procurementDebt) PayNow(ctx context.Context, tx posting.Tx, in procurement.PaymentInput) (procurement.Payment, error) {
	partyID, sourceID := in.PartyID, in.SourceID
	r, err := d.s.Payments().Record(ctx, tx, debt.PaymentInput{Direction: debt.Out, PartyID: &partyID,
		Purpose: debt.PurposeSettlement, MethodID: in.MethodID, Amount: in.Amount, SourceType: in.SourceType, SourceID: &sourceID,
		Note: in.Note, Key: in.Key,
		Allocations: []debt.Allocation{{ObligationID: in.ObligationID, Amount: in.Amount, Mode: debt.ModeManual}}})
	if err != nil {
		return procurement.Payment{}, err
	}
	return procurement.Payment{ID: r.Payment.ID, DocNo: r.Payment.DocNo}, nil
}

func (d procurementDebt) UseAdvance(ctx context.Context, tx posting.Tx, paymentID, obligationID uuid.UUID, amount money.Money) error {
	_, err := d.s.Allocations().Record(ctx, tx, debt.AllocationInput{PaymentID: paymentID,
		Allocations: []debt.Allocation{{ObligationID: obligationID, Amount: amount, Mode: debt.ModeManual}}})
	return err
}

func (d procurementDebt) Credit(ctx context.Context, tx posting.Tx, in procurement.CreditInput) (procurement.Credit, error) {
	r, err := d.s.CreditNotes().Record(ctx, tx, debt.CreditNoteInput{PartyID: in.PartyID, Direction: debt.Payable,
		SourceType: in.SourceType, SourceID: in.SourceID, SourceDocNo: in.SourceDocNo, Amount: in.Amount, ReasonID: in.ReasonID,
		Targets: in.Targets, OnlyTargets: in.OnlyTargets})
	if err != nil {
		return procurement.Credit{}, err
	}
	out := procurement.Credit{ID: r.Note.ID}
	if r.RefundDue != nil {
		out.RefundDueID = &r.RefundDue.ID
	}
	return out, nil
}

func (d procurementDebt) ReversePayable(ctx context.Context, tx posting.Tx, sourceType string, sourceID uuid.UUID, reason string) error {
	_, err := d.s.Obligations().ReverseBySource(ctx, tx,
		debt.SourceRef{Direction: debt.Payable, SourceType: sourceType, SourceID: sourceID}, reason)
	return err
}

// procurementPricing is the pricing engine in purchase mode: the typed purchase prices with tax
// per line and no promotions, and the retail list prices a PO line may change.
type procurementPricing struct {
	svc *pricing.Service
	w   pricing.PriceWriter
}

func (p procurementPricing) Price(ctx context.Context, q db.Querier, tenantID uuid.UUID, date time.Time, lines []procurement.PriceLine) (procurement.PricedDoc, error) {
	draft := pricing.DocumentDraft{BusinessDate: &date, SkipPromotions: true, Lines: make([]pricing.DraftLine, len(lines))}
	for i, l := range lines {
		price := l.UnitPrice
		draft.Lines[i] = pricing.DraftLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty, UnitPrice: &price,
			DiscountAmount: l.Discount}
	}
	doc, err := p.svc.CalculateIn(ctx, q, tenantID, draft)
	if err != nil {
		return procurement.PricedDoc{}, err
	}
	out := procurement.PricedDoc{PricesIncludeTax: doc.PricesIncludeTax, Lines: make([]procurement.PricedLine, len(lines)),
		Goods: doc.GoodsAmount, Discount: doc.DiscountAmount, Tax: doc.TaxAmount, Total: doc.TotalAmount}
	for _, l := range doc.Lines {
		// Without promotions there are no gift lines: every line is a draft line.
		if l.DraftIndex < 0 || l.DraftIndex >= len(lines) {
			continue
		}
		out.Lines[l.DraftIndex] = procurement.PricedLine{UnitID: l.UnitID, Factor: l.UnitFactor, BaseQty: l.BaseQty,
			UnitPrice: l.UnitPrice, Gross: l.GrossAmount, Discount: l.DiscountAmount, TaxRate: l.TaxRateSnap, Tax: l.TaxAmount,
			LineAmount: l.LineAmount}
	}
	return out, nil
}

func (p procurementPricing) RetailPrices(ctx context.Context, q db.Querier, tenantID uuid.UUID, date time.Time, lines []procurement.PriceLine) ([]*money.Money, error) {
	in := make([]pricing.PriceLine, len(lines))
	for i, l := range lines {
		in[i] = pricing.PriceLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty}
	}
	resolved, err := p.svc.ResolveMany(ctx, q, tenantID, pricing.PriceContext{BusinessDate: date}, in)
	if err != nil {
		return nil, err
	}
	out := make([]*money.Money, len(resolved))
	for i, r := range resolved {
		// A walk-in customer is priced on the retail list; a manual source means it has no price.
		if r.Source.Kind != pricing.SourceManual {
			amount := r.Amount
			out[i] = &amount
		}
	}
	return out, nil
}

func (p procurementPricing) SetRetailPrice(ctx context.Context, q db.Querier, actor audit.Actor, rp procurement.RetailPrice) error {
	unitID, orderID, price := rp.UnitID, rp.OrderID, rp.Price
	_, err := p.w.SetSalePrice(ctx, q, actor, pricing.SalePriceCommand{SkuID: rp.SkuID, ColorID: rp.ColorID, UnitID: &unitID,
		Kind: pricing.KindRetail, Price: &price, Source: pricing.ChangePurchaseOrder, SourceDocID: &orderID})
	return err
}

// procurementCatalog creates the new products of purchase lines through the catalog's quick
// create, in the document's transaction.
type procurementCatalog struct{ s *catalog.Service }

func (c procurementCatalog) CreateProduct(ctx context.Context, q db.Querier, actor audit.Actor, p procurement.NewProductInput) (uuid.UUID, error) {
	res, err := c.s.QuickCreateProductIn(ctx, q, actor, catalog.QuickCreateInput{
		Name: p.Name, CategoryID: p.CategoryID, BaseUnitID: p.BaseUnitID, PurchaseUnitID: p.PurchaseUnitID, Factor: p.Factor,
		PurchasePrice: p.PurchasePrice, SalePrice: p.RetailPrice, WholesalePrice: p.WholesalePrice, TrackLot: p.TrackLot,
		PriceSource: pricing.ChangePurchaseOrder,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return res.Product.Skus[0].Sku.ID, nil
}
