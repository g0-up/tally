package pricing

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Names of the base lists the store always has.
const (
	RetailListName    = "Giá bán lẻ"
	WholesaleListName = "Giá bán sỉ"
)

func baseListName(kind string) string {
	if kind == KindWholesale {
		return WholesaleListName
	}
	return RetailListName
}

// ensureBaseList returns the base list of kind, creating it when the store has none. A list of
// another kind that took the base list's name blocks the creation (base_price_list_missing).
func (s *Service) ensureBaseList(ctx context.Context, q db.Querier, tenantID uuid.UUID, kind string) (PriceList, error) {
	if l, ok, err := baseList(ctx, q, kind); err != nil || ok {
		return l, err
	}
	if err := insertBaseList(ctx, q, tenantID, kind, s.clock.Now()); err != nil {
		return PriceList{}, err
	}
	l, ok, err := baseList(ctx, q, kind)
	if err != nil {
		return PriceList{}, err
	}
	if !ok {
		return PriceList{}, errBaseListMissing()
	}
	return l, nil
}

// insertBaseList adds the base list of kind unless the store has it already, or another list
// took its name.
func insertBaseList(ctx context.Context, q db.Querier, tenantID uuid.UUID, kind string, now time.Time) error {
	name := baseListName(kind)
	if _, err := q.ExecContext(ctx, `
		INSERT INTO price_lists (id, tenant_id, name, name_key, kind, priority, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7)
		ON CONFLICT DO NOTHING`, ids.New(), tenantID, name, textnorm.Key(name), kind, DefaultPriority, now); err != nil {
		return fmt.Errorf("pricing: create base price list: %w", err)
	}
	return nil
}

// rowKey identifies a price row: list, SKU, color (nil general), unit (nil base) and tier.
type rowKey struct {
	ListID  uuid.UUID
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  *uuid.UUID
	MinQty  money.Qty
}

// origin is where a price edit came from, as the history records it.
type origin struct {
	Source string
	DocID  *uuid.UUID
}

// lockRow reads the row of k for update; ok is false when it does not exist.
func lockRow(ctx context.Context, q db.Querier, k rowKey) (priceRow, bool, error) {
	var r priceRow
	err := q.QueryRowxContext(ctx, `
		SELECT id, price_list_id, sku_id, color_id, unit_id, min_qty, price FROM price_list_items
		WHERE price_list_id = $1 AND sku_id = $2 AND color_id IS NOT DISTINCT FROM $3::uuid
		  AND unit_id IS NOT DISTINCT FROM $4::uuid AND min_qty = $5
		FOR UPDATE`, k.ListID, k.SkuID, k.ColorID, k.UnitID, k.MinQty).StructScan(&r)
	if db.IsNoRows(err) {
		return priceRow{}, false, nil
	}
	if err != nil {
		return priceRow{}, false, fmt.Errorf("pricing: lock price row: %w", err)
	}
	return r, true, nil
}

// recordChange appends a history row; oldPrice nil is an added price, newPrice nil a removed one.
func (s *Service) recordChange(ctx context.Context, q db.Querier, actor audit.Actor, k rowKey, oldPrice, newPrice *money.Money, o origin) (uuid.UUID, error) {
	id := ids.New()
	if _, err := q.ExecContext(ctx, `
		INSERT INTO price_changes (id, tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty, old_price, new_price,
		                           source, source_doc_id, actor_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		id, actor.TenantID, k.ListID, k.SkuID, k.ColorID, k.UnitID, k.MinQty, oldPrice, newPrice,
		o.Source, o.DocID, actor.ID, s.clock.Now()); err != nil {
		return uuid.Nil, fmt.Errorf("pricing: record price change: %w", err)
	}
	return id, nil
}

// putRow sets the price of the row of k, adding the row when it does not exist, and records the
// change; the same price again changes nothing. It returns the row id and the history row id
// (nil when nothing changed).
func (s *Service) putRow(ctx context.Context, q db.Querier, actor audit.Actor, k rowKey, price money.Money, o origin) (uuid.UUID, *uuid.UUID, error) {
	for attempt := 0; attempt < 2; attempt++ {
		row, ok, err := lockRow(ctx, q, k)
		if err != nil {
			return uuid.Nil, nil, err
		}
		now := s.clock.Now()
		if ok {
			if row.Price.Equal(price) {
				return row.ID, nil, nil
			}
			if _, err := q.ExecContext(ctx, `UPDATE price_list_items SET price = $2, updated_at = $3 WHERE id = $1`,
				row.ID, price, now); err != nil {
				return uuid.Nil, nil, fmt.Errorf("pricing: update price row: %w", err)
			}
			old := row.Price
			changeID, err := s.recordChange(ctx, q, actor, k, &old, &price, o)
			return row.ID, &changeID, err
		}
		id := ids.New()
		res, err := q.ExecContext(ctx, `
			INSERT INTO price_list_items (id, tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty, price, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
			ON CONFLICT ON CONSTRAINT ux_price_list_items__tier DO NOTHING`,
			id, actor.TenantID, k.ListID, k.SkuID, k.ColorID, k.UnitID, k.MinQty, price, now)
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("pricing: insert price row: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return uuid.Nil, nil, fmt.Errorf("pricing: insert price row: %w", err)
		} else if n == 1 {
			changeID, err := s.recordChange(ctx, q, actor, k, nil, &price, o)
			return id, &changeID, err
		}
		// A concurrent transaction added the same row first: update it instead.
	}
	return uuid.Nil, nil, fmt.Errorf("pricing: price row %v keeps changing concurrently", k)
}

// tier identifies k inside its list; equal quantities give equal tiers however they are scaled.
func (k rowKey) tier() string {
	return fmt.Sprintf("%s|%s|%s|%s", k.SkuID, deref(k.ColorID), deref(k.UnitID), k.MinQty.Decimal().String())
}

func (r priceRow) key() rowKey {
	return rowKey{ListID: r.PriceListID, SkuID: r.SkuID, ColorID: r.ColorID, UnitID: r.UnitID, MinQty: r.MinQty}
}

// putRows is putRow for distinct keys of one list whose row the caller locked, in a handful of
// statements for the whole set: it locks the existing rows, updates the changed ones, adds the
// missing ones and records each change in key order. It returns the row ids in key order.
func (s *Service) putRows(ctx context.Context, q db.Querier, actor audit.Actor, listID uuid.UUID, keys []rowKey, prices []money.Money, o origin) ([]uuid.UUID, error) {
	skuIDs := make([]uuid.UUID, len(keys))
	for i, k := range keys {
		skuIDs[i] = k.SkuID
	}
	var rows []priceRow
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT id, price_list_id, sku_id, color_id, unit_id, min_qty, price FROM price_list_items
		WHERE price_list_id = $1 AND sku_id = ANY($2)
		ORDER BY id
		FOR UPDATE`, listID, uniqueIDs(skuIDs)); err != nil {
		return nil, fmt.Errorf("pricing: lock price rows: %w", err)
	}
	existing := make(map[string]priceRow, len(rows))
	for _, r := range rows {
		existing[r.key().tier()] = r
	}

	var upd struct {
		ids    []uuid.UUID
		prices []string
	}
	var add struct {
		at              []int
		ids, skus       []uuid.UUID
		colors, units   []*uuid.UUID
		minQtys, prices []string
	}
	rowIDs := make([]uuid.UUID, len(keys))
	oldPrices := make([]*money.Money, len(keys))
	changed := make([]bool, len(keys))
	for i, k := range keys {
		row, ok := existing[k.tier()]
		if !ok {
			rowIDs[i] = ids.New()
			add.at = append(add.at, i)
			add.ids, add.skus = append(add.ids, rowIDs[i]), append(add.skus, k.SkuID)
			add.colors, add.units = append(add.colors, k.ColorID), append(add.units, k.UnitID)
			add.minQtys, add.prices = append(add.minQtys, k.MinQty.String()), append(add.prices, prices[i].String())
			continue
		}
		rowIDs[i] = row.ID
		if row.Price.Equal(prices[i]) {
			continue
		}
		old := row.Price
		oldPrices[i], changed[i] = &old, true
		upd.ids, upd.prices = append(upd.ids, row.ID), append(upd.prices, prices[i].String())
	}

	now := s.clock.Now()
	if len(upd.ids) > 0 {
		if _, err := q.ExecContext(ctx, `
			UPDATE price_list_items i SET price = v.price::numeric, updated_at = $3
			FROM unnest($1::uuid[], $2::text[]) AS v(id, price) WHERE i.id = v.id`,
			upd.ids, upd.prices, now); err != nil {
			return nil, fmt.Errorf("pricing: update price rows: %w", err)
		}
	}
	// Keys a concurrent transaction added first are set one by one, as updates.
	var late []int
	if len(add.ids) > 0 {
		var inserted []uuid.UUID
		if err := sqlx.SelectContext(ctx, q, &inserted, `
			INSERT INTO price_list_items (id, tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty, price, created_at, updated_at)
			SELECT v.id, $1, $2, v.sku_id, v.color_id, v.unit_id, v.min_qty::numeric, v.price::numeric, $3, $3
			FROM unnest($4::uuid[], $5::uuid[], $6::uuid[], $7::uuid[], $8::text[], $9::text[])
			     AS v(id, sku_id, color_id, unit_id, min_qty, price)
			ON CONFLICT ON CONSTRAINT ux_price_list_items__tier DO NOTHING
			RETURNING id`,
			actor.TenantID, listID, now, add.ids, add.skus, add.colors, add.units, add.minQtys, add.prices); err != nil {
			return nil, fmt.Errorf("pricing: insert price rows: %w", err)
		}
		done := make(map[uuid.UUID]bool, len(inserted))
		for _, id := range inserted {
			done[id] = true
		}
		for _, i := range add.at {
			if done[rowIDs[i]] {
				changed[i] = true
			} else {
				late = append(late, i)
			}
		}
	}

	var hist struct {
		ids, skus         []uuid.UUID
		colors, units     []*uuid.UUID
		minQtys           []string
		oldPrices, prices []*string
	}
	for i, k := range keys {
		if !changed[i] {
			continue
		}
		var old *string
		if oldPrices[i] != nil {
			v := oldPrices[i].String()
			old = &v
		}
		price := prices[i].String()
		hist.ids, hist.skus = append(hist.ids, ids.New()), append(hist.skus, k.SkuID)
		hist.colors, hist.units = append(hist.colors, k.ColorID), append(hist.units, k.UnitID)
		hist.minQtys = append(hist.minQtys, k.MinQty.String())
		hist.oldPrices, hist.prices = append(hist.oldPrices, old), append(hist.prices, &price)
	}
	if len(hist.ids) > 0 {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO price_changes (id, tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty, old_price, new_price,
			                           source, source_doc_id, actor_id, created_at)
			SELECT v.id, $1, $2, v.sku_id, v.color_id, v.unit_id, v.min_qty::numeric, v.old_price::numeric, v.new_price::numeric,
			       $3, $4, $5, $6
			FROM unnest($7::uuid[], $8::uuid[], $9::uuid[], $10::uuid[], $11::text[], $12::text[], $13::text[])
			     AS v(id, sku_id, color_id, unit_id, min_qty, old_price, new_price)`,
			actor.TenantID, listID, o.Source, o.DocID, actor.ID, now,
			hist.ids, hist.skus, hist.colors, hist.units, hist.minQtys, hist.oldPrices, hist.prices); err != nil {
			return nil, fmt.Errorf("pricing: record price changes: %w", err)
		}
	}
	for _, i := range late {
		id, _, err := s.putRow(ctx, q, actor, keys[i], prices[i], o)
		if err != nil {
			return nil, err
		}
		rowIDs[i] = id
	}
	return rowIDs, nil
}

// deleteRow removes row and records its removal (new price NULL).
func (s *Service) deleteRow(ctx context.Context, q db.Querier, actor audit.Actor, row priceRow, o origin) (uuid.UUID, error) {
	if _, err := q.ExecContext(ctx, `DELETE FROM price_list_items WHERE id = $1`, row.ID); err != nil {
		return uuid.Nil, fmt.Errorf("pricing: delete price row: %w", err)
	}
	old := row.Price
	return s.recordChange(ctx, q, actor, row.key(), &old, nil, o)
}

// SalePriceCommand sets or removes a base sale price of a SKU from a product, purchase or stock
// screen.
type SalePriceCommand struct {
	SkuID uuid.UUID
	// ColorID nil sets the general price; set, the price of that color.
	ColorID *uuid.UUID
	// UnitID nil (or the base unit) prices the base unit; another unit gets its own row.
	UnitID *uuid.UUID
	// Kind is KindRetail or KindWholesale: which base list is written.
	Kind string
	// ApplyAllColors ("Áp dụng cho tất cả màu") also removes every color price of the SKU in the
	// list, so all colors fall back to the general price. Only with a general price.
	ApplyAllColors bool
	// Price nil removes the price of a color ("Bỏ giá riêng") or the own price of a non-base unit,
	// which then sells at base price × factor; the general base price cannot be removed.
	Price *money.Money
	// Source is the screen the edit came from (catalog, purchase_order, product_list, inventory,
	// price_list) and SourceDocID the document it was made on, if any.
	Source      string
	SourceDocID *uuid.UUID
}

func (c SalePriceCommand) validate() error {
	f := fieldErrors{}
	if !isBaseKind(c.Kind) {
		f.add("kind", "invalid")
	}
	if !validChangeSource(c.Source) {
		f.add("source", "invalid")
	}
	switch {
	case c.Price == nil && c.ColorID == nil && c.UnitID == nil:
		f.add("price", "required")
	case c.Price != nil && c.Price.IsNegative():
		f.add("price", "negative")
	}
	switch {
	case c.ApplyAllColors && c.ColorID != nil:
		f.add("apply_all_colors", "not_allowed_with_color")
	case c.ApplyAllColors && c.Price == nil && c.UnitID != nil:
		f.add("apply_all_colors", "requires_price")
	}
	return f.err()
}

// PriceWriter writes base sale prices with their history inside the caller's transaction. Catalog
// quick-create, purchase lines, the product list and the stock screen set prices through it; only
// documents priced afterwards see the new price.
type PriceWriter struct {
	svc *Service
}

// NewPriceWriter returns the writer of svc.
func NewPriceWriter(svc *Service) PriceWriter { return PriceWriter{svc: svc} }

// SetSalePrice applies cmd in q and returns the history rows it wrote, oldest first.
func (w PriceWriter) SetSalePrice(ctx context.Context, q db.Querier, actor audit.Actor, cmd SalePriceCommand) ([]PriceChange, error) {
	if err := cmd.validate(); err != nil {
		return nil, err
	}
	s := w.svc
	skus, err := loadSkuInfo(ctx, q, []uuid.UUID{cmd.SkuID})
	if err != nil {
		return nil, err
	}
	sku, ok := skus[cmd.SkuID]
	if !ok {
		return nil, errNotFound()
	}
	unitID, _, ok := sku.unitOf(cmd.UnitID)
	if !ok {
		return nil, errInvalid(map[string]string{"unit_id": "unknown_unit"})
	}
	var rowUnit *uuid.UUID
	if unitID != sku.BaseUnitID {
		rowUnit = &unitID
	}
	if cmd.Price == nil && cmd.ColorID == nil && rowUnit == nil {
		return nil, errInvalid(map[string]string{"price": "required"})
	}
	if cmd.ColorID != nil {
		found, err := exists(ctx, q, "colors", *cmd.ColorID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errInvalid(map[string]string{"color_id": "not_found"})
		}
	}
	list, err := s.ensureBaseList(ctx, q, actor.TenantID, cmd.Kind)
	if err != nil {
		return nil, err
	}
	// The list row is locked before its item rows, as PutPriceListItems does, so concurrent
	// writers of one list queue instead of deadlocking on each other's rows.
	if _, err := readPriceList(ctx, q, list.ID, true); err != nil {
		return nil, err
	}
	o := origin{Source: cmd.Source, DocID: cmd.SourceDocID}
	key := rowKey{ListID: list.ID, SkuID: sku.ID, ColorID: cmd.ColorID, UnitID: rowUnit, MinQty: money.ZeroQty}
	var changes []uuid.UUID
	if cmd.Price == nil {
		row, found, err := lockRow(ctx, q, key)
		if err != nil {
			return nil, err
		}
		if found {
			id, err := s.deleteRow(ctx, q, actor, row, o)
			if err != nil {
				return nil, err
			}
			changes = append(changes, id)
		}
	} else {
		_, changeID, err := s.putRow(ctx, q, actor, key, *cmd.Price, o)
		if err != nil {
			return nil, err
		}
		if changeID != nil {
			changes = append(changes, *changeID)
		}
	}
	if cmd.ApplyAllColors {
		var rows []priceRow
		if err := sqlx.SelectContext(ctx, q, &rows, `
			SELECT id, price_list_id, sku_id, color_id, unit_id, min_qty, price FROM price_list_items
			WHERE price_list_id = $1 AND sku_id = $2 AND color_id IS NOT NULL
			ORDER BY color_id, unit_id NULLS FIRST, min_qty
			FOR UPDATE`, list.ID, sku.ID); err != nil {
			return nil, fmt.Errorf("pricing: read color prices: %w", err)
		}
		for _, row := range rows {
			id, err := s.deleteRow(ctx, q, actor, row, o)
			if err != nil {
				return nil, err
			}
			changes = append(changes, id)
		}
	}
	return readChanges(ctx, q, changes)
}

// ScopeSalePriceSet is the idempotency scope of setting a sale price from a screen.
const ScopeSalePriceSet = "sale_price.set"

// SetSalePrice sets a base sale price from the product, purchase or stock screens in its own
// transaction. With a key it runs once per Idempotency-Key: a retry of the same request returns
// the stored changes (replayed true) instead of writing the price again.
func (s *Service) SetSalePrice(ctx context.Context, actor audit.Actor, key *idempotency.Key, cmd SalePriceCommand) ([]PriceChange, bool, error) {
	var out []PriceChange
	var replayed bool
	err := s.write(ctx, actor, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeSalePriceSet, key, http.StatusOK, &out, func() error {
			var err error
			out, err = NewPriceWriter(s).SetSalePrice(ctx, q, actor, cmd)
			return err
		})
		return err
	})
	return out, replayed, err
}

// PriceHistory pages the price history of a SKU, newest first.
func (s *Service) PriceHistory(ctx context.Context, scope Scope, skuID uuid.UUID, f HistoryFilter) ([]PriceChange, error) {
	var out []PriceChange
	err := s.read(ctx, scope, func(q db.Querier) error {
		found, err := exists(ctx, q, "skus", skuID)
		if err != nil {
			return err
		}
		if !found {
			return errNotFound()
		}
		f.SkuID = &skuID
		out, err = listChanges(ctx, q, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, s.supportActorNames(ctx, scope, out)
}

// PriceChanges pages the price history of the whole tenant, newest first.
func (s *Service) PriceChanges(ctx context.Context, scope Scope, f HistoryFilter) ([]PriceChange, error) {
	var out []PriceChange
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = listChanges(ctx, q, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, s.supportActorNames(ctx, scope, out)
}

// supportActorNames fills the actor names of history a support session read: the read-only role
// cannot read the users table, so the names come from one SELECT in a regular tenant transaction.
func (s *Service) supportActorNames(ctx context.Context, scope Scope, rows []PriceChange) error {
	if !scope.ReadOnly {
		return nil
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		return actorNames(ctx, q, scope.TenantID, rows)
	})
}
