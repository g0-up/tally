package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// kitsStock is the inventory ledger as kits sees it: every call books the moves of one assembly
// batch or disassembly inside its posting.
type kitsStock struct{ l *inventory.StockLedger }

func (s kitsStock) Issue(ctx context.Context, tx posting.Tx, doc kits.StockDoc, moveType string, lines []kits.IssueLine) ([]kits.Move, error) {
	in := make([]inventory.IssueLine, len(lines))
	for i, l := range lines {
		in[i] = inventory.IssueLine{DocLineID: l.DocLineID, SkuID: l.SkuID, Qty: l.Qty, MoveType: inventory.MoveType(moveType),
			ColorID: l.ColorID, LotID: l.LotID}
	}
	moves, err := s.l.Issue(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return kitsMoves(moves), err
}

func (s kitsStock) Receive(ctx context.Context, tx posting.Tx, doc kits.StockDoc, moveType string, lines []kits.ReceiveLine) ([]kits.Move, error) {
	in := make([]inventory.ReceiveLine, len(lines))
	for i, l := range lines {
		value := l.Value
		in[i] = inventory.ReceiveLine{DocLineID: l.DocLineID, SkuID: l.SkuID, Qty: l.Qty, MoveType: inventory.MoveType(moveType),
			ColorID: l.ColorID, LotNo: l.LotNo, ExpiryDate: l.ExpiryDate, LotID: l.LotID, Value: &value}
	}
	moves, err := s.l.Receive(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return kitsMoves(moves), err
}

func (s kitsStock) Quarantine(ctx context.Context, tx posting.Tx, doc kits.StockDoc, lines []kits.QuarantineLine) ([]kits.Move, error) {
	in := make([]inventory.QuarantineLine, len(lines))
	for i, l := range lines {
		lotID := l.LotID
		in[i] = inventory.QuarantineLine{DocLineID: l.DocLineID, SkuID: l.SkuID, Qty: l.Qty, ColorID: l.ColorID, LotID: &lotID}
	}
	moves, err := s.l.Quarantine(ctx, tx, inventory.PostingOf(tx, doc.Type, doc.ID), in)
	return kitsMoves(moves), err
}

func kitsMoves(moves []inventory.PostedMove) []kits.Move {
	out := make([]kits.Move, len(moves))
	for i, m := range moves {
		out[i] = kits.Move{ID: m.ID, SkuID: m.SkuID, LotID: m.LotID, ColorID: m.ColorID, DocLineID: m.DocLineID, Qty: m.Qty,
			Value: m.Value, LotNo: m.LotNo, Expiry: m.ExpiryDate, Quarantine: m.IsQuarantine}
	}
	return out
}

// kitsReservations holds component stock for assembly order items.
type kitsReservations struct{ r *inventory.ReservationService }

func (k kitsReservations) Reserve(ctx context.Context, tx posting.Tx, itemID, skuID uuid.UUID, qty money.Qty, docNo string) error {
	_, err := k.r.Reserve(ctx, tx, tx.TenantID(), tx.Now(), inventory.ReserveRequest{SkuID: skuID, Qty: qty,
		SourceType: inventory.ReserveAssemblyOrderItem, SourceID: itemID, SourceDocNo: &docNo})
	return err
}

func (k kitsReservations) Consume(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty money.Qty) error {
	_, err := k.r.Consume(ctx, tx, tx.TenantID(), tx.Now(), reservationID, qty)
	return err
}

func (k kitsReservations) Release(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty *money.Qty) error {
	_, err := k.r.Release(ctx, tx, tx.TenantID(), tx.Now(), reservationID, qty)
	return err
}

func (k kitsReservations) Adjust(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, total money.Qty) error {
	_, err := k.r.Adjust(ctx, tx, tx.TenantID(), tx.Now(), reservationID, total)
	return err
}

// kitsPricing prices kit components and kits on today's retail list for a walk-in customer.
type kitsPricing struct{ svc *pricing.Service }

func (p kitsPricing) RetailPrices(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []kits.PriceQuery) ([]*money.Money, error) {
	in := make([]pricing.PriceLine, len(lines))
	for i, l := range lines {
		unitID := l.UnitID
		in[i] = pricing.PriceLine{SkuID: l.SkuID, ColorID: l.ColorID, UnitID: &unitID, Qty: l.Qty}
	}
	resolved, err := p.svc.ResolveMany(ctx, q, tenantID, pricing.PriceContext{}, in)
	if err != nil {
		return nil, err
	}
	out := make([]*money.Money, len(resolved))
	for i, r := range resolved {
		// A manual source means the retail list has no price for the SKU.
		if r.Source.Kind != pricing.SourceManual {
			amount := r.Amount
			out[i] = &amount
		}
	}
	return out, nil
}

// kitsCatalog writes the product of a kit or combo through the catalog, in the kit's
// transaction. A new kit is a draft product until its recipe is published.
type kitsCatalog struct{ s *catalog.Service }

func (c kitsCatalog) CreateProduct(ctx context.Context, q db.Querier, actor audit.Actor, in kits.NewProduct) (uuid.UUID, uuid.UUID, error) {
	d, err := c.s.CreateProductIn(ctx, q, actor, catalog.ProductInput{Type: in.Type, Name: in.Name, CategoryID: in.CategoryID,
		Description: in.Description, Status: catalog.StatusDraft,
		Sku: &catalog.SkuInput{Code: in.SkuCode, BaseUnitID: in.BaseUnitID, TrackLot: in.TrackLot, TaxRateID: in.TaxRateID,
			Barcode: in.Barcode, MinStock: in.MinStock}})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if len(d.Skus) != 1 {
		return uuid.Nil, uuid.Nil, fmt.Errorf("app: kit product %s has %d skus", d.Product.ID, len(d.Skus))
	}
	return d.Product.ID, d.Skus[0].Sku.ID, nil
}

// UpdateProduct edits the kit's product at its current version: the kit's own recipe version is
// what guards the caller's edit.
func (c kitsCatalog) UpdateProduct(ctx context.Context, q db.Querier, actor audit.Actor, productID uuid.UUID, in kits.ProductChange) error {
	var version int
	if err := q.QueryRowxContext(ctx, `SELECT version FROM products WHERE id = $1 FOR UPDATE`, productID).Scan(&version); err != nil {
		return fmt.Errorf("app: read kit product version: %w", err)
	}
	_, err := c.s.UpdateKitProductIn(ctx, q, actor, productID, version, catalog.ProductPatch{Name: in.Name, Status: in.Status,
		Description: catalog.Opt[string]{Set: in.DescriptionSet, Value: in.Description}})
	return err
}

func (c kitsCatalog) DeleteProduct(ctx context.Context, q db.Querier, actor audit.Actor, productID uuid.UUID) error {
	return c.s.DeleteProductIn(ctx, q, actor, productID)
}

// salesCombos expands combo lines of a sale by the combo's active recipe.
type salesCombos struct{ s *kits.Service }

func (c salesCombos) Expand(ctx context.Context, q db.Querier, comboSkuID uuid.UUID, qty money.Qty) ([]sales.ComboLine, error) {
	parts, err := c.s.ExpandCombo(ctx, q, comboSkuID, qty)
	if err != nil {
		return nil, err
	}
	out := make([]sales.ComboLine, len(parts))
	for i, p := range parts {
		out[i] = sales.ComboLine{SkuID: p.SkuID, ColorID: p.ColorID, BaseQty: p.BaseQty}
	}
	return out, nil
}
