package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

const (
	skuColumns = `id, product_id, code, variant_key, display_name, variant_label, base_unit_id, qty_scale, track_lot,
		track_expiry, ref_cost, min_price, min_stock, tax_rate_id, spec_text, status, search_norm, created_at, updated_at, version`
	skuUnitSelect = `SELECT su.sku_id, su.unit_id, u.name AS unit_name, su.factor, su.is_base, su.active, su.is_default_sale,
		su.is_default_purchase FROM sku_units su JOIN units u ON u.id = su.unit_id`
	barcodeColumns = `id, sku_id, unit_id, code, created_at`
	// maxBarcodeLen bounds a barcode (EAN-13, Code 128 and QR payloads of shop labels fit).
	maxBarcodeLen = 64
	// maxQtyScale is the largest number of decimals a SKU quantity may have (numeric(18,4)).
	maxQtyScale = 4
)

// SkuInput holds the editable fields of a new SKU. Barcode, when given, is a barcode of the base unit.
type SkuInput struct {
	Code        string
	BaseUnitID  uuid.UUID
	QtyScale    int
	TrackLot    bool
	TrackExpiry bool
	RefCost     *money.Money
	MinPrice    *money.Money
	MinStock    *money.Qty
	TaxRateID   *uuid.UUID
	SpecText    *string
	Status      string
	Barcode     *string
}

// normalise trims and checks the input; field names get prefix ("sku.", "defaults.").
// normaliseFields checks everything but the code (generated variants get theirs from the allocator).
func (in *SkuInput) normalise(f fieldErrors, prefix string) {
	in.normaliseFields(f, prefix)
	in.Code = strings.TrimSpace(in.Code)
	switch {
	case in.Code == "":
		f[prefix+"code"] = "required"
	case !skuCodeFormat.MatchString(in.Code):
		f[prefix+"code"] = "invalid_format"
	}
}

func (in *SkuInput) normaliseFields(f fieldErrors, prefix string) {
	if in.BaseUnitID == uuid.Nil {
		f[prefix+"base_unit_id"] = "required"
	}
	if in.QtyScale < 0 || in.QtyScale > maxQtyScale {
		f[prefix+"qty_scale"] = "out_of_range"
	}
	if in.TrackExpiry && !in.TrackLot {
		f[prefix+"track_expiry"] = "requires_track_lot"
	}
	if in.RefCost != nil && in.RefCost.IsNegative() {
		f[prefix+"ref_cost"] = "negative"
	}
	if in.MinPrice != nil && in.MinPrice.IsNegative() {
		f[prefix+"min_price"] = "negative"
	}
	if in.MinStock != nil && in.MinStock.IsNegative() {
		f[prefix+"min_stock"] = "negative"
	}
	in.SpecText = f.optionalText(prefix+"spec_text", in.SpecText, maxSpecTextLen)
	if in.Status == "" {
		in.Status = StatusActive
	}
	if in.Status != StatusActive && in.Status != StatusInactive {
		f[prefix+"status"] = "invalid"
	}
	if in.Barcode != nil {
		code := strings.TrimSpace(*in.Barcode)
		if code == "" {
			in.Barcode = nil
		} else if reason := barcodeReason(code); reason != "" {
			f[prefix+"barcode"] = reason
		} else {
			in.Barcode = &code
		}
	}
}

// ValidateBarcode answers 422 validation_failed when a trimmed code cannot be a barcode: empty,
// longer than maxBarcodeLen, or not printable ASCII without spaces.
func ValidateBarcode(code string) error {
	if code == "" {
		return errInvalid(map[string]string{"code": "required"})
	}
	if reason := barcodeReason(code); reason != "" {
		return errInvalid(map[string]string{"code": reason})
	}
	return nil
}

// barcodeReason checks a trimmed barcode: printable ASCII without spaces, at most maxBarcodeLen.
func barcodeReason(code string) string {
	if len(code) > maxBarcodeLen {
		return "too_long"
	}
	for i := 0; i < len(code); i++ {
		if code[i] <= 0x20 || code[i] >= 0x7f {
			return "invalid_format"
		}
	}
	return ""
}

// checkSkuRefs checks the base unit (active) and the tax rate of a SKU.
func checkSkuRefs(ctx context.Context, q db.Querier, baseUnitID uuid.UUID, taxRateID *uuid.UUID, prefix string) error {
	u, err := readUnit(ctx, q, baseUnitID)
	if apperrNotFound(err) || (err == nil && !u.Active) {
		return errInvalid(map[string]string{prefix + "base_unit_id": "unknown_unit"})
	}
	if err != nil {
		return err
	}
	if taxRateID != nil {
		t, err := readTaxRate(ctx, q, *taxRateID)
		if apperrNotFound(err) || (err == nil && !t.Active) {
			return errInvalid(map[string]string{prefix + "tax_rate_id": "unknown_tax_rate"})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// withinSkuLimit refuses one more SKU beyond the plan limit (409 plan_limit_reached). Every SKU
// counts, archived ones included, since they keep their history and can be restored.
func (s *Service) withinSkuLimit(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	if s.limits == nil {
		return nil
	}
	var n int64
	if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM skus WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		return fmt.Errorf("catalog: count skus: %w", err)
	}
	return s.limits.Within(ctx, q, tenantID, LimitSKUs, n+1)
}

// insertSku adds a SKU of product p for the variant d with its base unit row, variant values and
// optional base barcode, then indexes it.
func (s *Service) insertSku(ctx context.Context, q db.Querier, tenantID uuid.UUID, p Product, d SkuDraft, in SkuInput, prefix string) (Sku, error) {
	if err := checkSkuRefs(ctx, q, in.BaseUnitID, in.TaxRateID, prefix); err != nil {
		return Sku{}, err
	}
	if err := checkSkuCodeFree(ctx, q, in.Code, uuid.Nil); err != nil {
		return Sku{}, err
	}
	if err := s.withinSkuLimit(ctx, q, tenantID); err != nil {
		return Sku{}, err
	}
	now := s.clock.Now()
	sku := Sku{ID: ids.New(), ProductID: p.ID, Code: in.Code, VariantKey: d.VariantKey, DisplayName: SKUNamer(p.Name, d.VariantLabel),
		VariantLabel: d.VariantLabel, BaseUnitID: in.BaseUnitID, QtyScale: in.QtyScale, TrackLot: in.TrackLot,
		TrackExpiry: in.TrackExpiry, RefCost: in.RefCost, MinPrice: in.MinPrice, TaxRateID: in.TaxRateID, SpecText: in.SpecText,
		Status: in.Status, CreatedAt: now, UpdatedAt: now, Version: 1}
	if in.MinStock != nil {
		sku.MinStock = *in.MinStock
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO skus (id, tenant_id, product_id, code, variant_key, display_name, variant_label, base_unit_id, qty_scale,
		  track_lot, track_expiry, ref_cost, min_price, min_stock, tax_rate_id, spec_text, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $18)`,
		sku.ID, tenantID, sku.ProductID, sku.Code, sku.VariantKey, sku.DisplayName, sku.VariantLabel, sku.BaseUnitID, sku.QtyScale,
		sku.TrackLot, sku.TrackExpiry, sku.RefCost, sku.MinPrice, sku.MinStock, sku.TaxRateID, sku.SpecText, sku.Status, now)
	switch {
	case db.IsUniqueViolation(err, "ux_skus__code"):
		return Sku{}, errSkuCodeTaken()
	case db.IsUniqueViolation(err, "ux_skus__product_id_variant_key"):
		return Sku{}, errInvalid(map[string]string{prefix + "variant": "already_exists"})
	case err != nil:
		return Sku{}, fmt.Errorf("catalog: insert sku: %w", err)
	}
	if err := insertSkuUnit(ctx, q, tenantID, SkuUnit{SkuID: sku.ID, UnitID: sku.BaseUnitID, Factor: money.FactorOne,
		IsBase: true, Active: true, IsDefaultSale: true, IsDefaultPurchase: true}, now); err != nil {
		return Sku{}, err
	}
	for i, opt := range d.OptionIDs {
		if _, err := q.ExecContext(ctx, `INSERT INTO sku_variant_values (tenant_id, sku_id, attribute_id, option_id) VALUES ($1, $2, $3, $4)`,
			tenantID, sku.ID, d.AttributeIDs[i], opt); err != nil {
			return Sku{}, fmt.Errorf("catalog: insert variant value: %w", err)
		}
	}
	if in.Barcode != nil {
		if _, err := insertBarcode(ctx, q, tenantID, Barcode{ID: ids.New(), SkuID: sku.ID, UnitID: sku.BaseUnitID, Code: *in.Barcode, CreatedAt: now}); err != nil {
			return Sku{}, err
		}
	}
	return sku, s.indexer.Reindex(ctx, q, []uuid.UUID{sku.ID})
}

// checkSkuCodeFree returns sku_code_taken when another SKU (not self) has code in any letter case:
// codes are typed and scanned without regard to case, so "kd-01" and "KD-01" would be ambiguous.
// The unique constraint still guards the exact spelling against concurrent inserts.
func checkSkuCodeFree(ctx context.Context, q db.Querier, code string, self uuid.UUID) error {
	taken, err := exists(ctx, q, `SELECT 1 FROM skus WHERE upper(code) = upper($1) AND id <> $2`, code, self)
	if err != nil {
		return fmt.Errorf("catalog: check sku code: %w", err)
	}
	if taken {
		return errSkuCodeTaken()
	}
	return nil
}

func insertSkuUnit(ctx context.Context, q db.Querier, tenantID uuid.UUID, u SkuUnit, now any) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO sku_units (tenant_id, sku_id, unit_id, factor, is_base, active, is_default_sale, is_default_purchase, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		tenantID, u.SkuID, u.UnitID, u.Factor, u.IsBase, u.Active, u.IsDefaultSale, u.IsDefaultPurchase, now)
	if db.IsUniqueViolation(err, "pk_sku_units") {
		return errInvalid(map[string]string{"unit_id": "already_a_unit_of_sku"})
	}
	if err != nil {
		return fmt.Errorf("catalog: insert sku unit: %w", err)
	}
	return nil
}

// insertBarcode adds a barcode unless the store already has it under any of its CodeVariants, on
// any SKU: a scan must resolve to one (SKU, unit). The transaction lock on the canonical form makes
// two concurrent inserts of variants of one code wait for each other.
func insertBarcode(ctx context.Context, q db.Querier, tenantID uuid.UUID, b Barcode) (Barcode, error) {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('sku_barcode:' || $1::text || ':' || $2, 0))`,
		tenantID, canonicalCode(b.Code)); err != nil {
		return Barcode{}, fmt.Errorf("catalog: lock barcode: %w", err)
	}
	taken, err := exists(ctx, q, `SELECT 1 FROM sku_barcodes WHERE code = ANY($1::text[])`, CodeVariants(b.Code))
	if err != nil {
		return Barcode{}, fmt.Errorf("catalog: check barcode: %w", err)
	}
	if taken {
		return Barcode{}, errBarcodeTaken()
	}
	_, err = q.ExecContext(ctx, `INSERT INTO sku_barcodes (id, tenant_id, sku_id, unit_id, code, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		b.ID, tenantID, b.SkuID, b.UnitID, b.Code, b.CreatedAt)
	if db.IsUniqueViolation(err, "ux_sku_barcodes__code") {
		return Barcode{}, errBarcodeTaken()
	}
	if err != nil {
		return Barcode{}, fmt.Errorf("catalog: insert barcode: %w", err)
	}
	return b, nil
}

// readAxes returns the variant axes of a product in position order, with their attributes.
func readAxes(ctx context.Context, q db.Querier, productID uuid.UUID) ([]AxisDetail, error) {
	var axes []VariantAxis
	if err := sqlx.SelectContext(ctx, q, &axes, `SELECT attribute_id, position FROM product_variant_axes WHERE product_id = $1
		ORDER BY position`, productID); err != nil {
		return nil, fmt.Errorf("catalog: read variant axes: %w", err)
	}
	if len(axes) == 0 {
		return []AxisDetail{}, nil
	}
	idList := make([]uuid.UUID, len(axes))
	for i, a := range axes {
		idList[i] = a.AttributeID
	}
	attrs, err := listAttributes(ctx, q, "", idList)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]Attribute, len(attrs))
	for _, a := range attrs {
		byID[a.ID] = a
	}
	out := make([]AxisDetail, len(axes))
	for i, a := range axes {
		out[i] = AxisDetail{Position: a.Position, Attribute: byID[a.AttributeID]}
	}
	return out, nil
}

// readSkuUnits returns the units of the SKUs, base first then by factor, with their own prices
// and lock reasons.
func (s *Service) readSkuUnits(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) ([]SkuUnit, error) {
	var units []SkuUnit
	if err := sqlx.SelectContext(ctx, q, &units, skuUnitSelect+` WHERE su.sku_id = ANY($1) ORDER BY su.sku_id, su.is_base DESC, su.factor, u.name`,
		skuIDs); err != nil {
		return nil, fmt.Errorf("catalog: read sku units: %w", err)
	}
	if err := s.fillSkuUnits(ctx, q, units); err != nil {
		return nil, err
	}
	return units, nil
}

// fillSkuUnits sets the own retail and wholesale prices (general color, no tier, base lists) and
// the lock reason of each unit, with one query per source for all of them.
func (s *Service) fillSkuUnits(ctx context.Context, q db.Querier, units []SkuUnit) error {
	if len(units) == 0 {
		return nil
	}
	skuIDs := make([]uuid.UUID, 0, len(units))
	seen := map[uuid.UUID]bool{}
	var nonBase []SkuUnitKey
	for _, u := range units {
		if !seen[u.SkuID] {
			seen[u.SkuID] = true
			skuIDs = append(skuIDs, u.SkuID)
		}
		if !u.IsBase {
			nonBase = append(nonBase, SkuUnitKey{SkuID: u.SkuID, UnitID: u.UnitID})
		}
	}
	var prices []struct {
		SkuID  uuid.UUID   `db:"sku_id"`
		UnitID uuid.UUID   `db:"unit_id"`
		Kind   string      `db:"kind"`
		Price  money.Money `db:"price"`
	}
	// A row without unit_id prices the base unit; it wins over a row naming the base unit.
	if err := sqlx.SelectContext(ctx, q, &prices, `
		SELECT i.sku_id, COALESCE(i.unit_id, s.base_unit_id) AS unit_id, l.kind, i.price
		FROM price_list_items i
		JOIN price_lists l ON l.id = i.price_list_id
		JOIN skus s ON s.id = i.sku_id
		WHERE l.kind IN ('retail', 'wholesale') AND i.sku_id = ANY($1) AND i.color_id IS NULL AND i.min_qty = 0
		ORDER BY i.unit_id IS NULL`, skuIDs); err != nil {
		return fmt.Errorf("catalog: read unit prices: %w", err)
	}
	locks, err := s.usage.UnitLocks(ctx, q, nonBase)
	if err != nil {
		return err
	}
	byKey := make(map[SkuUnitKey]*SkuUnit, len(units))
	for i := range units {
		u := &units[i]
		u.RetailPrice, u.WholesalePrice, u.LockedBy = nil, nil, nil
		byKey[SkuUnitKey{SkuID: u.SkuID, UnitID: u.UnitID}] = u
		if reason, ok := locks[SkuUnitKey{SkuID: u.SkuID, UnitID: u.UnitID}]; ok && !u.IsBase {
			u.LockedBy = &reason
		}
	}
	for _, p := range prices {
		u, ok := byKey[SkuUnitKey{SkuID: p.SkuID, UnitID: p.UnitID}]
		if !ok {
			continue
		}
		price := p.Price
		switch p.Kind {
		case PriceRetail:
			u.RetailPrice = &price
		case PriceWholesale:
			u.WholesalePrice = &price
		}
	}
	return nil
}

// readSkuDetails returns the SKUs with their units, barcodes and variant options, in skuIDs order.
func (s *Service) readSkuDetails(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) ([]SkuDetail, error) {
	if len(skuIDs) == 0 {
		return []SkuDetail{}, nil
	}
	var skus []Sku
	if err := sqlx.SelectContext(ctx, q, &skus, `SELECT `+skuColumns+` FROM skus WHERE id = ANY($1)`, skuIDs); err != nil {
		return nil, fmt.Errorf("catalog: read skus: %w", err)
	}
	units, err := s.readSkuUnits(ctx, q, skuIDs)
	if err != nil {
		return nil, err
	}
	var barcodes []Barcode
	if err := sqlx.SelectContext(ctx, q, &barcodes, `SELECT `+barcodeColumns+` FROM sku_barcodes WHERE sku_id = ANY($1) ORDER BY created_at, code`, skuIDs); err != nil {
		return nil, fmt.Errorf("catalog: read barcodes: %w", err)
	}
	var values []struct {
		SkuID    uuid.UUID `db:"sku_id"`
		OptionID uuid.UUID `db:"option_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &values, `
		SELECT v.sku_id, v.option_id FROM sku_variant_values v
		JOIN skus s ON s.id = v.sku_id
		LEFT JOIN product_variant_axes ax ON ax.product_id = s.product_id AND ax.attribute_id = v.attribute_id
		WHERE v.sku_id = ANY($1) ORDER BY COALESCE(ax.position, 99), v.attribute_id`, skuIDs); err != nil {
		return nil, fmt.Errorf("catalog: read variant values: %w", err)
	}
	byID := make(map[uuid.UUID]*SkuDetail, len(skus))
	details := make([]SkuDetail, len(skus))
	for i := range skus {
		details[i] = SkuDetail{Sku: skus[i], Units: []SkuUnit{}, Barcodes: []Barcode{}, OptionIDs: []uuid.UUID{}}
		byID[skus[i].ID] = &details[i]
	}
	for _, u := range units {
		byID[u.SkuID].Units = append(byID[u.SkuID].Units, u)
	}
	for _, b := range barcodes {
		byID[b.SkuID].Barcodes = append(byID[b.SkuID].Barcodes, b)
	}
	for _, v := range values {
		byID[v.SkuID].OptionIDs = append(byID[v.SkuID].OptionIDs, v.OptionID)
	}
	out := make([]SkuDetail, 0, len(skuIDs))
	for _, id := range skuIDs {
		if d, ok := byID[id]; ok {
			out = append(out, *d)
		}
	}
	return out, nil
}

func lockSku(ctx context.Context, q db.Querier, id uuid.UUID) (Sku, error) {
	var sku Sku
	err := q.QueryRowxContext(ctx, `SELECT `+skuColumns+` FROM skus WHERE id = $1 FOR UPDATE`, id).StructScan(&sku)
	if db.IsNoRows(err) {
		return Sku{}, errNotFound()
	}
	if err != nil {
		return Sku{}, fmt.Errorf("catalog: lock sku: %w", err)
	}
	return sku, nil
}

func (s *Service) readSkuDetail(ctx context.Context, q db.Querier, id uuid.UUID) (SkuDetail, error) {
	list, err := s.readSkuDetails(ctx, q, []uuid.UUID{id})
	if err != nil {
		return SkuDetail{}, err
	}
	if len(list) == 0 {
		return SkuDetail{}, errNotFound()
	}
	return list[0], nil
}

// Sku returns a SKU with its units, barcodes and variant options.
func (s *Service) Sku(ctx context.Context, scope Scope, id uuid.UUID) (SkuDetail, error) {
	var d SkuDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		d, err = s.readSkuDetail(ctx, q, id)
		return err
	})
	return d, err
}

// SkuPatch changes a SKU; unset fields are kept.
type SkuPatch struct {
	Code        *string
	BaseUnitID  *uuid.UUID
	QtyScale    *int
	TrackLot    *bool
	TrackExpiry *bool
	RefCost     Opt[money.Money]
	MinPrice    Opt[money.Money]
	MinStock    *money.Qty
	TaxRateID   Opt[uuid.UUID]
	SpecText    Opt[string]
	Status      *string
}

// UpdateSku changes a SKU guarded by its version. Changing the base unit is allowed until the SKU
// has stock movements (see changeBaseUnit); toggling tracking needs zero stock on hand.
func (s *Service) UpdateSku(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in SkuPatch) (SkuDetail, error) {
	var d SkuDetail
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := lockSku(ctx, q, id)
		if err != nil {
			return err
		}
		next := SkuInput{Code: before.Code, BaseUnitID: before.BaseUnitID, QtyScale: before.QtyScale, TrackLot: before.TrackLot,
			TrackExpiry: before.TrackExpiry, RefCost: before.RefCost, MinPrice: before.MinPrice, MinStock: &before.MinStock,
			TaxRateID: before.TaxRateID, SpecText: before.SpecText, Status: before.Status}
		if in.Code != nil {
			next.Code = *in.Code
		}
		if in.BaseUnitID != nil {
			next.BaseUnitID = *in.BaseUnitID
		}
		if in.QtyScale != nil {
			next.QtyScale = *in.QtyScale
		}
		if in.TrackLot != nil {
			next.TrackLot = *in.TrackLot
		}
		if in.TrackExpiry != nil {
			next.TrackExpiry = *in.TrackExpiry
		}
		if in.RefCost.Set {
			next.RefCost = in.RefCost.Value
		}
		if in.MinPrice.Set {
			next.MinPrice = in.MinPrice.Value
		}
		if in.MinStock != nil {
			next.MinStock = in.MinStock
		}
		if in.TaxRateID.Set {
			next.TaxRateID = in.TaxRateID.Value
		}
		if in.SpecText.Set {
			next.SpecText = in.SpecText.Value
		}
		if in.Status != nil {
			next.Status = *in.Status
			if next.Status == "" {
				next.Status = "invalid"
			}
		}
		f := fieldErrors{}
		next.normalise(f, "")
		if err := f.err(); err != nil {
			return err
		}
		if err := s.skuSpec.CheckTracking(ctx, q, before, next.TrackLot, next.TrackExpiry); err != nil {
			return err
		}
		if next.TaxRateID != nil && (before.TaxRateID == nil || *before.TaxRateID != *next.TaxRateID) {
			if err := checkSkuRefs(ctx, q, before.BaseUnitID, next.TaxRateID, ""); err != nil {
				return err
			}
		}
		if !strings.EqualFold(next.Code, before.Code) {
			if err := checkSkuCodeFree(ctx, q, next.Code, id); err != nil {
				return err
			}
		}
		if next.BaseUnitID != before.BaseUnitID {
			if err := s.changeBaseUnit(ctx, q, actor.TenantID, before, &next); err != nil {
				return err
			}
		}
		_, err = db.UpdateWithVersion(ctx, q, "skus", id, expected, s.clock.Now(),
			db.Set{Column: "code", Value: next.Code}, db.Set{Column: "base_unit_id", Value: next.BaseUnitID},
			db.Set{Column: "qty_scale", Value: next.QtyScale}, db.Set{Column: "track_lot", Value: next.TrackLot},
			db.Set{Column: "track_expiry", Value: next.TrackExpiry}, db.Set{Column: "ref_cost", Value: next.RefCost},
			db.Set{Column: "min_price", Value: next.MinPrice}, db.Set{Column: "min_stock", Value: *next.MinStock},
			db.Set{Column: "tax_rate_id", Value: next.TaxRateID}, db.Set{Column: "spec_text", Value: next.SpecText},
			db.Set{Column: "status", Value: next.Status})
		if db.IsUniqueViolation(err, "ux_skus__code") {
			return errSkuCodeTaken()
		}
		if err != nil {
			return err
		}
		if next.Code != before.Code {
			if err := s.indexer.Reindex(ctx, q, []uuid.UUID{id}); err != nil {
				return err
			}
		}
		if d, err = s.readSkuDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.update", "sku", id, before, d.Sku)
	})
	return d, err
}

// changeBaseUnit moves the base of a SKU without stock movements to another unit. A unit the SKU
// already has (factor f) becomes the base: every factor is divided by f, the reference cost and
// minimum price are multiplied by f and the minimum stock divided by f; the conversion must stay
// exact at 6 decimals. A unit the SKU does not have replaces the base unit (a correction of the
// base unit's name), keeping the other conversions.
func (s *Service) changeBaseUnit(ctx context.Context, q db.Querier, tenantID uuid.UUID, sku Sku, next *SkuInput) error {
	if err := s.unitSpec.CheckBaseChange(ctx, q, sku.ID); err != nil {
		return err
	}
	if err := checkSkuRefs(ctx, q, next.BaseUnitID, nil, ""); err != nil {
		return err
	}
	var units []SkuUnit
	if err := sqlx.SelectContext(ctx, q, &units, skuUnitSelect+` WHERE su.sku_id = $1 FOR UPDATE OF su`, sku.ID); err != nil {
		return fmt.Errorf("catalog: lock sku units: %w", err)
	}
	var target *SkuUnit
	for i := range units {
		if units[i].UnitID == next.BaseUnitID {
			target = &units[i]
		}
	}
	now := s.clock.Now()
	if target == nil {
		var old SkuUnit
		for _, u := range units {
			if u.IsBase {
				old = u
			}
		}
		moved := old
		moved.UnitID = next.BaseUnitID
		if _, err := q.ExecContext(ctx, `UPDATE sku_units SET is_base = false, is_default_sale = false, is_default_purchase = false
			WHERE sku_id = $1 AND unit_id = $2`, sku.ID, old.UnitID); err != nil {
			return fmt.Errorf("catalog: release base unit: %w", err)
		}
		if err := insertSkuUnit(ctx, q, tenantID, moved, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE sku_barcodes SET unit_id = $3 WHERE sku_id = $1 AND unit_id = $2`,
			sku.ID, old.UnitID, next.BaseUnitID); err != nil {
			return fmt.Errorf("catalog: move base barcodes: %w", err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM sku_units WHERE sku_id = $1 AND unit_id = $2`, sku.ID, old.UnitID); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errSkuUnitInUse()
			}
			return fmt.Errorf("catalog: drop old base unit: %w", err)
		}
		return nil
	}
	f := target.Factor.Decimal()
	factors := make([]money.Factor, len(units))
	for i, u := range units {
		exact := u.Factor.Decimal().DivRound(f, 12)
		factor, err := money.NewFactor(exact.String())
		if err != nil || !exact.Mul(f).Equal(u.Factor.Decimal()) {
			return errInvalid(map[string]string{"base_unit_id": "conversion_not_exact"})
		}
		factors[i] = factor
	}
	scale := func(m *money.Money, by decimal.Decimal) (*money.Money, error) {
		if m == nil {
			return nil, nil
		}
		v, err := money.NewMoney(m.Decimal().Mul(by).Round(money.MoneyScale).String())
		if err != nil {
			return nil, errInvalid(map[string]string{"base_unit_id": "price_out_of_range"})
		}
		return &v, nil
	}
	var err error
	if next.RefCost, err = scale(next.RefCost, f); err != nil {
		return err
	}
	if next.MinPrice, err = scale(next.MinPrice, f); err != nil {
		return err
	}
	minStock, err := money.NewQty(next.MinStock.Decimal().DivRound(f, money.QtyScale).String())
	if err != nil {
		return errInvalid(map[string]string{"min_stock": "out_of_range"})
	}
	next.MinStock = &minStock
	if _, err := q.ExecContext(ctx, `UPDATE sku_units SET is_base = false WHERE sku_id = $1`, sku.ID); err != nil {
		return fmt.Errorf("catalog: release base unit: %w", err)
	}
	for i, u := range units {
		if _, err := q.ExecContext(ctx, `UPDATE sku_units SET factor = $3, is_base = $4, active = active OR $4, updated_at = $5
			WHERE sku_id = $1 AND unit_id = $2`, sku.ID, u.UnitID, factors[i], u.UnitID == next.BaseUnitID, now); err != nil {
			return fmt.Errorf("catalog: rescale sku units: %w", err)
		}
	}
	return nil
}

// DeleteSku removes an unused SKU with its units, barcodes and variant values.
func (s *Service) DeleteSku(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		sku, err := lockSku(ctx, q, id)
		if err != nil {
			return err
		}
		if err := s.skuSpec.CheckDelete(ctx, q, id); err != nil {
			return err
		}
		for _, stmt := range []string{
			`UPDATE product_images SET sku_id = NULL WHERE sku_id = $1`,
			`DELETE FROM sku_barcodes WHERE sku_id = $1`,
			`DELETE FROM sku_units WHERE sku_id = $1`,
			`DELETE FROM sku_variant_values WHERE sku_id = $1`,
			`DELETE FROM skus WHERE id = $1`,
		} {
			if _, err := q.ExecContext(ctx, stmt, id); err != nil {
				if db.IsForeignKeyViolation(err, "") {
					return errSkuInUse()
				}
				return fmt.Errorf("catalog: delete sku: %w", err)
			}
		}
		return s.record(ctx, q, actor, "sku.delete", "sku", id, sku, nil)
	})
}

// SkuUnits returns the units of a SKU, base first.
func (s *Service) SkuUnits(ctx context.Context, scope Scope, skuID uuid.UUID) ([]SkuUnit, error) {
	d, err := s.Sku(ctx, scope, skuID)
	return d.Units, err
}

// SkuUnitInput adds a unit to a SKU or changes one; Factor is the decimal string of how many base
// units one unit holds.
type SkuUnitInput struct {
	UnitID            uuid.UUID
	Factor            *string
	Active            *bool
	IsDefaultSale     *bool
	IsDefaultPurchase *bool
}

func parseFactor(raw string) (money.Factor, error) {
	f, err := money.NewFactor(strings.TrimSpace(raw))
	switch {
	case err == nil:
		return f, nil
	case errors.Is(err, money.ErrNotPositive):
		return money.Factor{}, errFactorNotPositive("factor")
	case errors.Is(err, money.ErrScale):
		return money.Factor{}, errInvalid(map[string]string{"factor": "too_many_decimals"})
	case errors.Is(err, money.ErrRange):
		return money.Factor{}, errInvalid(map[string]string{"factor": "out_of_range"})
	}
	// "-2" is a format error to the parser but a non-positive factor to the user.
	if strings.HasPrefix(strings.TrimSpace(raw), "-") {
		return money.Factor{}, errFactorNotPositive("factor")
	}
	return money.Factor{}, errInvalid(map[string]string{"factor": "invalid"})
}

// AddSkuUnit adds a conversion unit to a SKU.
func (s *Service) AddSkuUnit(ctx context.Context, actor audit.Actor, skuID uuid.UUID, in SkuUnitInput) (SkuUnit, error) {
	if in.Factor == nil {
		return SkuUnit{}, errInvalid(map[string]string{"factor": "required"})
	}
	factor, err := parseFactor(*in.Factor)
	if err != nil {
		return SkuUnit{}, err
	}
	var out SkuUnit
	err = s.write(ctx, actor, func(q db.Querier) error {
		sku, err := lockSku(ctx, q, skuID)
		if err != nil {
			return err
		}
		u, err := readUnit(ctx, q, in.UnitID)
		if apperrNotFound(err) || (err == nil && !u.Active) {
			return errInvalid(map[string]string{"unit_id": "unknown_unit"})
		}
		if err != nil {
			return err
		}
		if in.UnitID == sku.BaseUnitID {
			return errInvalid(map[string]string{"unit_id": "already_a_unit_of_sku"})
		}
		su := SkuUnit{SkuID: skuID, UnitID: in.UnitID, UnitName: u.Name, Factor: factor, Active: in.Active == nil || *in.Active,
			IsDefaultSale: in.IsDefaultSale != nil && *in.IsDefaultSale, IsDefaultPurchase: in.IsDefaultPurchase != nil && *in.IsDefaultPurchase}
		if !su.Active && (su.IsDefaultSale || su.IsDefaultPurchase) {
			return errInvalid(map[string]string{"active": "default_unit_inactive"})
		}
		if err := clearDefaults(ctx, q, skuID, su.IsDefaultSale, su.IsDefaultPurchase); err != nil {
			return err
		}
		if err := insertSkuUnit(ctx, q, actor.TenantID, su, s.clock.Now()); err != nil {
			return err
		}
		if err := restoreDefaults(ctx, q, skuID); err != nil {
			return err
		}
		if err := touchSku(ctx, q, skuID, s.clock.Now()); err != nil {
			return err
		}
		if out, err = s.readFilledSkuUnit(ctx, q, skuID, in.UnitID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.unit_add", "sku", skuID, nil, su)
	})
	return out, err
}

// clearDefaults removes the default flags the new default unit takes over.
func clearDefaults(ctx context.Context, q db.Querier, skuID uuid.UUID, sale, purchase bool) error {
	if !sale && !purchase {
		return nil
	}
	if _, err := q.ExecContext(ctx, `UPDATE sku_units SET is_default_sale = is_default_sale AND NOT $2,
		is_default_purchase = is_default_purchase AND NOT $3 WHERE sku_id = $1`, skuID, sale, purchase); err != nil {
		return fmt.Errorf("catalog: clear default units: %w", err)
	}
	return nil
}

// restoreDefaults makes the base unit the default sale or purchase unit when no unit is.
func restoreDefaults(ctx context.Context, q db.Querier, skuID uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE sku_units SET
		  is_default_sale = is_default_sale OR NOT EXISTS (SELECT 1 FROM sku_units d WHERE d.sku_id = $1 AND d.is_default_sale),
		  is_default_purchase = is_default_purchase OR NOT EXISTS (SELECT 1 FROM sku_units d WHERE d.sku_id = $1 AND d.is_default_purchase)
		WHERE sku_id = $1 AND is_base`, skuID); err != nil {
		return fmt.Errorf("catalog: restore default units: %w", err)
	}
	return nil
}

// touchSku moves the SKU's version with a change to its units, so a unit table read before the
// change is refused on save (412) instead of deleting or overwriting it.
func touchSku(ctx context.Context, q db.Querier, skuID uuid.UUID, now time.Time) error {
	if _, err := q.ExecContext(ctx, `UPDATE skus SET version = version + 1, updated_at = $2 WHERE id = $1`, skuID, now); err != nil {
		return fmt.Errorf("catalog: touch sku: %w", err)
	}
	return nil
}

func readSkuUnit(ctx context.Context, q db.Querier, skuID, unitID uuid.UUID) (SkuUnit, error) {
	var su SkuUnit
	err := q.QueryRowxContext(ctx, skuUnitSelect+` WHERE su.sku_id = $1 AND su.unit_id = $2`, skuID, unitID).StructScan(&su)
	if db.IsNoRows(err) {
		return SkuUnit{}, errNotFound()
	}
	if err != nil {
		return SkuUnit{}, fmt.Errorf("catalog: read sku unit: %w", err)
	}
	return su, nil
}

// readFilledSkuUnit reads a SKU unit with its own prices and lock reason.
func (s *Service) readFilledSkuUnit(ctx context.Context, q db.Querier, skuID, unitID uuid.UUID) (SkuUnit, error) {
	su, err := readSkuUnit(ctx, q, skuID, unitID)
	if err != nil {
		return SkuUnit{}, err
	}
	units := []SkuUnit{su}
	if err := s.fillSkuUnits(ctx, q, units); err != nil {
		return SkuUnit{}, err
	}
	return units[0], nil
}

// UpdateSkuUnit changes the factor, active flag or default flags of a SKU unit. The base unit's
// factor is always 1 and it stays active; factors may change at any time because document lines
// keep the factor they were written with.
func (s *Service) UpdateSkuUnit(ctx context.Context, actor audit.Actor, skuID, unitID uuid.UUID, in SkuUnitInput) (SkuUnit, error) {
	var out SkuUnit
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockSku(ctx, q, skuID); err != nil {
			return err
		}
		before, err := readSkuUnit(ctx, q, skuID, unitID)
		if err != nil {
			return err
		}
		next := before
		if in.Factor != nil {
			if next.Factor, err = parseFactor(*in.Factor); err != nil {
				return err
			}
			if before.IsBase && !next.Factor.Equal(money.FactorOne) {
				return errInvalid(map[string]string{"factor": "base_factor_fixed"})
			}
		}
		if in.Active != nil {
			if before.IsBase && !*in.Active {
				return errBaseUnitRequired()
			}
			next.Active = *in.Active
		}
		if in.IsDefaultSale != nil {
			next.IsDefaultSale = *in.IsDefaultSale
		}
		if in.IsDefaultPurchase != nil {
			next.IsDefaultPurchase = *in.IsDefaultPurchase
		}
		if !next.Active && (next.IsDefaultSale || next.IsDefaultPurchase) {
			return errInvalid(map[string]string{"active": "default_unit_inactive"})
		}
		if err := clearDefaults(ctx, q, skuID, next.IsDefaultSale && !before.IsDefaultSale,
			next.IsDefaultPurchase && !before.IsDefaultPurchase); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE sku_units SET factor = $3, active = $4, is_default_sale = $5, is_default_purchase = $6,
			updated_at = $7 WHERE sku_id = $1 AND unit_id = $2`,
			skuID, unitID, next.Factor, next.Active, next.IsDefaultSale, next.IsDefaultPurchase, s.clock.Now()); err != nil {
			return fmt.Errorf("catalog: update sku unit: %w", err)
		}
		if err := restoreDefaults(ctx, q, skuID); err != nil {
			return err
		}
		if err := touchSku(ctx, q, skuID, s.clock.Now()); err != nil {
			return err
		}
		if out, err = s.readFilledSkuUnit(ctx, q, skuID, unitID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.unit_update", "sku", skuID, before, out)
	})
	return out, err
}

// DeleteSkuUnit removes a conversion unit no document or price uses and without barcodes.
func (s *Service) DeleteSkuUnit(ctx context.Context, actor audit.Actor, skuID, unitID uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockSku(ctx, q, skuID); err != nil {
			return err
		}
		su, err := readSkuUnit(ctx, q, skuID, unitID)
		if err != nil {
			return err
		}
		if su.IsBase {
			return errBaseUnitRequired()
		}
		used, err := s.usage.SkuUnitInUse(ctx, q, skuID, unitID)
		if err != nil {
			return err
		}
		if used {
			return errSkuUnitInUse()
		}
		hasBarcodes, err := exists(ctx, q, `SELECT 1 FROM sku_barcodes WHERE sku_id = $1 AND unit_id = $2`, skuID, unitID)
		if err != nil {
			return err
		}
		if hasBarcodes {
			return errUnitInUse()
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM sku_units WHERE sku_id = $1 AND unit_id = $2`, skuID, unitID); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errSkuUnitInUse()
			}
			return fmt.Errorf("catalog: delete sku unit: %w", err)
		}
		if err := restoreDefaults(ctx, q, skuID); err != nil {
			return err
		}
		if err := touchSku(ctx, q, skuID, s.clock.Now()); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.unit_remove", "sku", skuID, su, nil)
	})
}

// SkuUnitRow is one non-base row of a SKU's unit table: Factor base units per unit, whether it is
// in use for new documents, and its own prices per unit (nil sells it at base price × factor).
type SkuUnitRow struct {
	UnitID         uuid.UUID
	Factor         *string
	Active         bool
	RetailPrice    *money.Money
	WholesalePrice *money.Money
}

// SkuUnitsInput is the whole unit table of a SKU: the base unit's prices (nil keeps the current
// one) and every other unit; a unit of the SKU missing from Units is deleted.
type SkuUnitsInput struct {
	RetailPrice    *money.Money
	WholesalePrice *money.Money
	Units          []SkuUnitRow
}

// ReplaceSkuUnits saves the unit table of a SKU in one transaction guarded by the SKU's version:
// it adds the new units, changes the factor and active flag of the others, sets or removes their
// own prices and deletes the units missing from in. A unit that documents, price history or
// barcodes reference cannot be deleted (409 unit_in_use naming each such unit); it can be made
// inactive. When the default purchase unit becomes inactive, the active unit with the largest
// factor above 1 takes over, else the base unit; the default sale unit falls back to the base unit.
// Factors may change at any time because document lines keep the factor they were written with.
func (s *Service) ReplaceSkuUnits(ctx context.Context, actor audit.Actor, skuID uuid.UUID, expected int, in SkuUnitsInput) (SkuDetail, error) {
	var d SkuDetail
	err := s.write(ctx, actor, func(q db.Querier) error {
		sku, err := lockSku(ctx, q, skuID)
		if err != nil {
			return err
		}
		if sku.Version != expected {
			return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
		}
		var before []SkuUnit
		if err := sqlx.SelectContext(ctx, q, &before, skuUnitSelect+` WHERE su.sku_id = $1 ORDER BY su.is_base DESC, su.factor, u.name
			FOR UPDATE OF su`, skuID); err != nil {
			return fmt.Errorf("catalog: lock sku units: %w", err)
		}
		if err := s.fillSkuUnits(ctx, q, before); err != nil {
			return err
		}
		current := make(map[uuid.UUID]SkuUnit, len(before))
		for _, u := range before {
			current[u.UnitID] = u
		}
		factors, err := checkUnitRows(ctx, q, sku.BaseUnitID, current, in)
		if err != nil {
			return err
		}
		kept := make(map[uuid.UUID]bool, len(in.Units))
		for _, r := range in.Units {
			kept[r.UnitID] = true
		}
		locked := fieldErrors{}
		var removed []SkuUnit
		for _, u := range before {
			if u.IsBase || kept[u.UnitID] {
				continue
			}
			if u.LockedBy != nil {
				locked["units["+u.UnitID.String()+"]"] = *u.LockedBy
			}
			removed = append(removed, u)
		}
		if len(locked) > 0 {
			return errUnitsLocked(locked)
		}
		now := s.clock.Now()
		for _, u := range removed {
			// Only a current price row without history can be left on an unlocked unit.
			for _, stmt := range []string{
				`DELETE FROM price_list_items WHERE sku_id = $1 AND unit_id = $2`,
				`DELETE FROM sku_units WHERE sku_id = $1 AND unit_id = $2`,
			} {
				if _, err := q.ExecContext(ctx, stmt, skuID, u.UnitID); err != nil {
					if db.IsForeignKeyViolation(err, "") {
						return errSkuUnitInUse()
					}
					return fmt.Errorf("catalog: delete sku unit: %w", err)
				}
			}
		}
		// Every row is active while its prices are written: the price writer only prices active units.
		for i, r := range in.Units {
			if _, ok := current[r.UnitID]; ok {
				if _, err := q.ExecContext(ctx, `UPDATE sku_units SET factor = $3, active = true, updated_at = $4
					WHERE sku_id = $1 AND unit_id = $2`, skuID, r.UnitID, factors[i], now); err != nil {
					return fmt.Errorf("catalog: update sku unit: %w", err)
				}
				continue
			}
			if err := insertSkuUnit(ctx, q, actor.TenantID, SkuUnit{SkuID: skuID, UnitID: r.UnitID, Factor: factors[i], Active: true},
				now); err != nil {
				return err
			}
		}
		base := current[sku.BaseUnitID]
		if err := s.replaceUnitPrices(ctx, q, actor, skuID, sku.BaseUnitID, base, in.RetailPrice, in.WholesalePrice, true); err != nil {
			return err
		}
		for _, r := range in.Units {
			if err := s.replaceUnitPrices(ctx, q, actor, skuID, r.UnitID, current[r.UnitID], r.RetailPrice, r.WholesalePrice,
				false); err != nil {
				return err
			}
		}
		var inactive []uuid.UUID
		for _, r := range in.Units {
			if !r.Active {
				inactive = append(inactive, r.UnitID)
			}
		}
		if len(inactive) > 0 {
			if _, err := q.ExecContext(ctx, `UPDATE sku_units SET active = false, is_default_sale = false, is_default_purchase = false,
				updated_at = $3 WHERE sku_id = $1 AND unit_id = ANY($2)`, skuID, inactive, now); err != nil {
				return fmt.Errorf("catalog: deactivate sku units: %w", err)
			}
		}
		if err := movePurchaseDefault(ctx, q, skuID); err != nil {
			return err
		}
		if err := restoreDefaults(ctx, q, skuID); err != nil {
			return err
		}
		// The version moves with the unit table so another editor's stale table is refused.
		if _, err := db.UpdateWithVersion(ctx, q, "skus", skuID, expected, now,
			db.Set{Column: "base_unit_id", Value: sku.BaseUnitID}); err != nil {
			return err
		}
		if d, err = s.readSkuDetail(ctx, q, skuID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.units_replace", "sku", skuID, before, d.Units)
	})
	return d, err
}

// checkUnitRows checks the rows of a unit table against the SKU's base unit and current units and
// returns the parsed factor of each row. Field errors name the row: units[1].unit_id is
// required, is_base, duplicate or unknown_unit (a new unit missing or inactive in the store);
// units[1].factor is required or why it is not a positive factor; prices must not be negative.
func checkUnitRows(ctx context.Context, q db.Querier, baseUnitID uuid.UUID, current map[uuid.UUID]SkuUnit,
	in SkuUnitsInput) ([]money.Factor, error) {
	if len(in.Units) > maxUnitRows {
		return nil, errInvalid(map[string]string{"units": "too_many"})
	}
	f := fieldErrors{}
	checkPrices(f, "", in.RetailPrice, in.WholesalePrice)
	factors := make([]money.Factor, len(in.Units))
	seen := make(map[uuid.UUID]bool, len(in.Units))
	var added []uuid.UUID
	for i, r := range in.Units {
		at := fmt.Sprintf("units[%d].", i)
		switch {
		case r.UnitID == uuid.Nil:
			f[at+"unit_id"] = "required"
		case r.UnitID == baseUnitID:
			f[at+"unit_id"] = "is_base"
		case seen[r.UnitID]:
			f[at+"unit_id"] = "duplicate"
		default:
			if _, ok := current[r.UnitID]; !ok {
				added = append(added, r.UnitID)
			}
		}
		seen[r.UnitID] = true
		var reason string
		if factors[i], reason = factorOf(r.Factor); reason != "" {
			f[at+"factor"] = reason
		}
		checkPrices(f, at, r.RetailPrice, r.WholesalePrice)
	}
	if len(added) > 0 {
		var active []uuid.UUID
		if err := sqlx.SelectContext(ctx, q, &active, `SELECT id FROM units WHERE id = ANY($1) AND active`, added); err != nil {
			return nil, fmt.Errorf("catalog: read units: %w", err)
		}
		for i, r := range in.Units {
			if slices.Contains(added, r.UnitID) && !slices.Contains(active, r.UnitID) {
				f[fmt.Sprintf("units[%d].unit_id", i)] = "unknown_unit"
			}
		}
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	return factors, nil
}

// replaceUnitPrices writes the retail and wholesale prices of one unit that differ from its
// current ones. A nil price removes the own price of a non-base unit; on the base unit it keeps
// the current price, which cannot be removed. Without a price writer nothing is stored.
func (s *Service) replaceUnitPrices(ctx context.Context, q db.Querier, actor audit.Actor, skuID, unitID uuid.UUID, current SkuUnit,
	retail, wholesale *money.Money, isBase bool) error {
	if s.prices == nil {
		return nil
	}
	for _, p := range []struct {
		kind      string
		now, next *money.Money
	}{{PriceRetail, current.RetailPrice, retail}, {PriceWholesale, current.WholesalePrice, wholesale}} {
		switch {
		case p.next == nil && (isBase || p.now == nil):
			continue
		case p.next != nil && p.now != nil && p.next.Equal(*p.now):
			continue
		}
		if err := s.prices.SetSalePrice(ctx, q, actor, SalePrice{SkuID: skuID, UnitID: unitID, Kind: p.kind, Price: p.next}); err != nil {
			return err
		}
	}
	return nil
}

// movePurchaseDefault gives a SKU without a default purchase unit the active unit with the
// largest factor above 1, else its base unit.
func movePurchaseDefault(ctx context.Context, q db.Querier, skuID uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE sku_units SET is_default_purchase = true
		WHERE sku_id = $1
		  AND NOT EXISTS (SELECT 1 FROM sku_units d WHERE d.sku_id = $1 AND d.is_default_purchase)
		  AND unit_id = (SELECT su.unit_id FROM sku_units su JOIN units u ON u.id = su.unit_id
		                 WHERE su.sku_id = $1 AND su.active AND (su.is_base OR su.factor > 1)
		                 ORDER BY su.factor DESC, u.name LIMIT 1)`, skuID); err != nil {
		return fmt.Errorf("catalog: move default purchase unit: %w", err)
	}
	return nil
}

// Barcodes returns the barcodes of a SKU.
func (s *Service) Barcodes(ctx context.Context, scope Scope, skuID uuid.UUID) ([]Barcode, error) {
	d, err := s.Sku(ctx, scope, skuID)
	return d.Barcodes, err
}

// BarcodeInput adds a barcode to a unit of a SKU (the base unit when UnitID is nil).
type BarcodeInput struct {
	Code   string
	UnitID *uuid.UUID
}

// AddBarcode adds a barcode; barcodes are unique in the store.
func (s *Service) AddBarcode(ctx context.Context, actor audit.Actor, skuID uuid.UUID, in BarcodeInput) (Barcode, error) {
	code := strings.TrimSpace(in.Code)
	if err := ValidateBarcode(code); err != nil {
		return Barcode{}, err
	}
	var out Barcode
	err := s.write(ctx, actor, func(q db.Querier) error {
		sku, err := lockSku(ctx, q, skuID)
		if err != nil {
			return err
		}
		unitID := sku.BaseUnitID
		if in.UnitID != nil {
			unitID = *in.UnitID
		}
		if _, err := readSkuUnit(ctx, q, skuID, unitID); apperrNotFound(err) {
			return errInvalid(map[string]string{"unit_id": "not_a_unit_of_sku"})
		} else if err != nil {
			return err
		}
		if out, err = insertBarcode(ctx, q, actor.TenantID, Barcode{ID: ids.New(), SkuID: skuID, UnitID: unitID, Code: code,
			CreatedAt: s.clock.Now()}); err != nil {
			return err
		}
		if err := s.indexer.Reindex(ctx, q, []uuid.UUID{skuID}); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.barcode_add", "sku", skuID, nil, out)
	})
	return out, err
}

// UpdateBarcodeUnit moves a barcode of a SKU to another unit of the same SKU, so the next scan of
// that code answers the new unit. A barcode already on the unit is returned unchanged and unaudited.
func (s *Service) UpdateBarcodeUnit(ctx context.Context, actor audit.Actor, skuID, barcodeID, unitID uuid.UUID) (Barcode, error) {
	if unitID == uuid.Nil {
		return Barcode{}, errInvalid(map[string]string{"unit_id": "required"})
	}
	var out Barcode
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockSku(ctx, q, skuID); err != nil {
			return err
		}
		// The barcode in the path is looked up before the unit in the body is checked against the SKU,
		// so a barcode outside this SKU (or this store) answers 404 for any unit_id the body names. A
		// body without unit_id is refused with 422 above, before any lookup, so that answer says
		// nothing about which barcodes exist. Barcodes of a SKU change only under its lock, so this
		// read stays current until the update.
		var before Barcode
		err := q.QueryRowxContext(ctx, `SELECT `+barcodeColumns+` FROM sku_barcodes WHERE id = $1 AND sku_id = $2`,
			barcodeID, skuID).StructScan(&before)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("catalog: read barcode: %w", err)
		}
		if _, err := readSkuUnit(ctx, q, skuID, unitID); apperrNotFound(err) {
			return errInvalid(map[string]string{"unit_id": "not_a_unit_of_sku"})
		} else if err != nil {
			return err
		}
		if before.UnitID == unitID {
			out = before
			return nil
		}
		if err := q.QueryRowxContext(ctx, `UPDATE sku_barcodes SET unit_id = $1 WHERE id = $2 AND sku_id = $3 RETURNING `+barcodeColumns,
			unitID, barcodeID, skuID).StructScan(&out); err != nil {
			return fmt.Errorf("catalog: update barcode unit: %w", err)
		}
		if err := s.indexer.Reindex(ctx, q, []uuid.UUID{skuID}); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.barcode_update", "sku", skuID, before, out)
	})
	return out, err
}

// DeleteBarcode removes a barcode of a SKU.
func (s *Service) DeleteBarcode(ctx context.Context, actor audit.Actor, skuID, barcodeID uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockSku(ctx, q, skuID); err != nil {
			return err
		}
		var b Barcode
		err := q.QueryRowxContext(ctx, `DELETE FROM sku_barcodes WHERE id = $1 AND sku_id = $2 RETURNING `+barcodeColumns,
			barcodeID, skuID).StructScan(&b)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("catalog: delete barcode: %w", err)
		}
		if err := s.indexer.Reindex(ctx, q, []uuid.UUID{skuID}); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "sku.barcode_remove", "sku", skuID, b, nil)
	})
}
