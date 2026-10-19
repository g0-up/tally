package pricing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// PriceListInput is the price list form. Kind, customer group and party are set on creation only;
// on update nil keeps the stored value and a set Opt with nil clears a date.
type PriceListInput struct {
	Name            *string
	Kind            *string
	CustomerGroupID *uuid.UUID
	PartyID         *uuid.UUID
	Priority        *int
	ValidFrom       Opt[time.Time]
	ValidTo         Opt[time.Time]
	Active          *bool
}

// apply copies the editable fields of in onto l and checks the list invariants that do not need
// the database: name, priority, the validity range and which dates the kind allows.
func (in PriceListInput) apply(l *PriceList, f fieldErrors, creating bool) {
	if in.Name != nil || creating {
		name := deref(in.Name)
		f.text("name", &name, true, maxNameLen)
		l.Name, l.NameKey = name, textnorm.Key(name)
	}
	if in.Priority != nil {
		if *in.Priority < 0 || *in.Priority > MaxPriority {
			f.add("priority", "out_of_range")
		}
		l.Priority = *in.Priority
	}
	if in.ValidFrom.Set {
		l.ValidFrom = truncDate(in.ValidFrom.Value)
	}
	if in.ValidTo.Set {
		l.ValidTo = truncDate(in.ValidTo.Value)
	}
	if in.Active != nil {
		l.Active = *in.Active
	}
	switch {
	case isBaseKind(l.Kind):
		if l.ValidFrom != nil {
			f.add("valid_from", "not_allowed")
		}
		if l.ValidTo != nil {
			f.add("valid_to", "not_allowed")
		}
	case l.Kind == KindSeason:
		if l.ValidFrom == nil {
			f.add("valid_from", "required")
		}
		if l.ValidTo == nil {
			f.add("valid_to", "required")
		}
	}
	if l.ValidFrom != nil && l.ValidTo != nil && l.ValidTo.Before(*l.ValidFrom) {
		f.add("valid_to", "before_valid_from")
	}
}

func truncDate(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := clock.TruncateDate(*t)
	return &d
}

// PriceListFilter selects price lists; an empty Kind lists every kind.
type PriceListFilter struct {
	Kind       string
	ActiveOnly bool
}

// PriceLists lists the store's price lists with their row counts, base lists first, then by
// priority and name.
func (s *Service) PriceLists(ctx context.Context, scope Scope, f PriceListFilter) ([]PriceList, error) {
	if f.Kind != "" && !validListKind(f.Kind) {
		return nil, errInvalid(map[string]string{"kind": "invalid"})
	}
	var out []PriceList
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+priceListColumns+`,
			       (SELECT count(*) FROM price_list_items i WHERE i.price_list_id = pl.id) AS item_count
			FROM price_lists pl
			WHERE ($1 = '' OR pl.kind = $1) AND (NOT $2 OR pl.active)
			ORDER BY pl.kind NOT IN ('retail', 'wholesale'), pl.priority, pl.name_key, pl.id`, f.Kind, f.ActiveOnly); err != nil {
			return fmt.Errorf("pricing: list price lists: %w", err)
		}
		return nil
	})
	return out, err
}

// PriceList returns one list.
func (s *Service) PriceList(ctx context.Context, scope Scope, id uuid.UUID) (PriceList, error) {
	var out PriceList
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = readPriceList(ctx, q, id, false)
		return err
	})
	return out, err
}

// CreatePriceList adds a list. A group list needs its customer group, a customer list a party
// with a customer profile, a season list its validity range; base lists have no dates, and the
// store has one of each.
func (s *Service) CreatePriceList(ctx context.Context, actor audit.Actor, in PriceListInput) (PriceList, error) {
	l := PriceList{Kind: deref(in.Kind), Priority: DefaultPriority, Active: true, CustomerGroupID: in.CustomerGroupID, PartyID: in.PartyID}
	f := fieldErrors{}
	if !validListKind(l.Kind) {
		f.add("kind", "invalid")
	}
	in.apply(&l, f, true)
	switch {
	case l.Kind == KindGroup && l.CustomerGroupID == nil:
		f.add("customer_group_id", "required")
	case l.Kind != KindGroup && l.CustomerGroupID != nil:
		f.add("customer_group_id", "not_allowed")
	}
	switch {
	case l.Kind == KindCustomer && l.PartyID == nil:
		f.add("party_id", "required")
	case l.Kind != KindCustomer && l.PartyID != nil:
		f.add("party_id", "not_allowed")
	}
	if err := f.err(); err != nil {
		return PriceList{}, err
	}
	var out PriceList
	err := s.write(ctx, actor, func(q db.Querier) error {
		if err := checkListOwner(ctx, q, l); err != nil {
			return err
		}
		if isBaseKind(l.Kind) {
			if _, ok, err := baseList(ctx, q, l.Kind); err != nil {
				return err
			} else if ok {
				return errBaseListExists()
			}
		}
		now := s.clock.Now()
		l.ID = ids.New()
		if _, err := q.ExecContext(ctx, `
			INSERT INTO price_lists (id, tenant_id, name, name_key, kind, customer_group_id, party_id, priority,
			                         valid_from, valid_to, active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`,
			l.ID, actor.TenantID, l.Name, l.NameKey, l.Kind, l.CustomerGroupID, l.PartyID, l.Priority,
			l.ValidFrom, l.ValidTo, l.Active, now); err != nil {
			return mapListWriteError(err, "insert")
		}
		var err error
		if out, err = readPriceList(ctx, q, l.ID, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "price_list.create", "price_list", l.ID, nil, out)
	})
	return out, err
}

// checkListOwner checks that the customer group or party a list belongs to exists and that the
// party is a customer.
func checkListOwner(ctx context.Context, q db.Querier, l PriceList) error {
	if l.CustomerGroupID != nil {
		found, err := exists(ctx, q, "customer_groups", *l.CustomerGroupID)
		if err != nil {
			return err
		}
		if !found {
			return errInvalid(map[string]string{"customer_group_id": "not_found"})
		}
	}
	if l.PartyID != nil {
		var isCustomer bool
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE party_id = $1)`,
			*l.PartyID).Scan(&isCustomer); err != nil {
			return fmt.Errorf("pricing: read customer profile: %w", err)
		}
		if !isCustomer {
			return errInvalid(map[string]string{"party_id": "not_a_customer"})
		}
	}
	return nil
}

func mapListWriteError(err error, op string) error {
	switch {
	case db.IsUniqueViolation(err, "ux_price_lists__name_key"):
		return errListNameTaken()
	case db.IsUniqueViolation(err, "ux_price_lists__base"):
		return errBaseListExists()
	}
	return fmt.Errorf("pricing: %s price list: %w", op, err)
}

// UpdatePriceList renames, reprioritises, re-dates or (de)activates a list if it is still at
// version expected. Kind, group and party never change; a base list stays active and undated.
func (s *Service) UpdatePriceList(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in PriceListInput) (PriceList, error) {
	if in.Kind != nil || in.CustomerGroupID != nil || in.PartyID != nil {
		f := fieldErrors{}
		for name, set := range map[string]bool{"kind": in.Kind != nil, "customer_group_id": in.CustomerGroupID != nil, "party_id": in.PartyID != nil} {
			if set {
				f.add(name, "immutable")
			}
		}
		return PriceList{}, f.err()
	}
	var out PriceList
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readPriceList(ctx, q, id, false)
		if err != nil {
			return err
		}
		l := before
		f := fieldErrors{}
		if isBaseKind(l.Kind) && ((in.Active != nil && !*in.Active) || (in.ValidFrom.Set && in.ValidFrom.Value != nil) ||
			(in.ValidTo.Set && in.ValidTo.Value != nil)) {
			return errBaseListLocked()
		}
		in.apply(&l, f, false)
		if err := f.err(); err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "price_lists", id, expected, s.clock.Now(),
			db.Set{Column: "name", Value: l.Name}, db.Set{Column: "name_key", Value: l.NameKey},
			db.Set{Column: "priority", Value: l.Priority}, db.Set{Column: "valid_from", Value: l.ValidFrom},
			db.Set{Column: "valid_to", Value: l.ValidTo}, db.Set{Column: "active", Value: l.Active}); err != nil {
			if isNotFound(err) {
				return errNotFound()
			}
			return mapListWriteError(err, "update")
		}
		if out, err = readPriceList(ctx, q, id, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "price_list.update", "price_list", id, before, out)
	})
	return out, err
}

// DeletePriceList removes a list that has no rows and no history; base lists are never deleted.
// A list in use is deactivated instead.
func (s *Service) DeletePriceList(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		before, err := readPriceList(ctx, q, id, true)
		if err != nil {
			return err
		}
		if isBaseKind(before.Kind) {
			return errBaseListLocked()
		}
		var inUse bool
		if err := q.QueryRowxContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM price_list_items WHERE price_list_id = $1)
			    OR EXISTS (SELECT 1 FROM price_changes WHERE price_list_id = $1)`, id).Scan(&inUse); err != nil {
			return fmt.Errorf("pricing: check price list use: %w", err)
		}
		if inUse {
			return errListInUse()
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM price_lists WHERE id = $1`, id); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errListInUse()
			}
			return fmt.Errorf("pricing: delete price list: %w", err)
		}
		return s.record(ctx, q, actor, "price_list.delete", "price_list", id, before, nil)
	})
}

// PriceListItems pages the rows of a list by SKU code.
func (s *Service) PriceListItems(ctx context.Context, scope Scope, listID uuid.UUID, f ItemFilter) ([]PriceListItem, error) {
	var out []PriceListItem
	err := s.read(ctx, scope, func(q db.Querier) error {
		found, err := exists(ctx, q, "price_lists", listID)
		if err != nil {
			return err
		}
		if !found {
			return errNotFound()
		}
		out, err = listItems(ctx, q, listID, f)
		return err
	})
	return out, err
}

// ItemInput is one row of a bulk price upsert: SKU, optional color and unit (nil or the base unit
// is the base unit), the tier in base quantity (default 0) and the price.
type ItemInput struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  *uuid.UUID
	MinQty  *money.Qty
	Price   *money.Money
}

// PutPriceListItems adds or changes up to MaxBulkItems rows of a list, recording each change in
// the history (source price_list), and returns the rows in input order.
func (s *Service) PutPriceListItems(ctx context.Context, actor audit.Actor, listID uuid.UUID, items []ItemInput) ([]PriceListItem, error) {
	switch {
	case len(items) == 0:
		return nil, errInvalid(map[string]string{"items": "required"})
	case len(items) > MaxBulkItems:
		return nil, errInvalid(map[string]string{"items": "too_many"})
	}
	var out []PriceListItem
	err := s.write(ctx, actor, func(q db.Querier) error {
		list, err := readPriceList(ctx, q, listID, true)
		if err != nil {
			return err
		}
		keys, err := itemKeys(ctx, q, list.ID, items)
		if err != nil {
			return err
		}
		prices := make([]money.Money, len(items))
		for i, it := range items {
			prices[i] = *it.Price
		}
		rowIDs, err := s.putRows(ctx, q, actor, list.ID, keys, prices, origin{Source: ChangePriceList})
		if err != nil {
			return err
		}
		out, err = readItems(ctx, q, rowIDs)
		return err
	})
	return out, err
}

// itemKeys validates bulk rows against the catalog and returns their row keys.
func itemKeys(ctx context.Context, q db.Querier, listID uuid.UUID, items []ItemInput) ([]rowKey, error) {
	skuIDs := make([]uuid.UUID, 0, len(items))
	var colorIDs []uuid.UUID
	for _, it := range items {
		skuIDs = append(skuIDs, it.SkuID)
		if it.ColorID != nil {
			colorIDs = append(colorIDs, *it.ColorID)
		}
	}
	skus, err := loadSkuInfo(ctx, q, uniqueIDs(skuIDs))
	if err != nil {
		return nil, err
	}
	colors, err := existingIDs(ctx, q, "colors", uniqueIDs(colorIDs))
	if err != nil {
		return nil, err
	}
	f := fieldErrors{}
	field := func(i int, name string) string { return fmt.Sprintf("items[%d].%s", i, name) }
	type seenKey struct {
		sku, color, unit uuid.UUID
		minQty           string
	}
	seen := make(map[seenKey]bool, len(items))
	keys := make([]rowKey, len(items))
	for i, it := range items {
		switch {
		case it.Price == nil:
			f.add(field(i, "price"), "required")
		case it.Price.IsNegative():
			f.add(field(i, "price"), "negative")
		}
		minQty := money.ZeroQty
		if it.MinQty != nil {
			minQty = *it.MinQty
			if minQty.IsNegative() {
				f.add(field(i, "min_qty"), "negative")
			}
		}
		if it.ColorID != nil && !colors[*it.ColorID] {
			f.add(field(i, "color_id"), "not_found")
		}
		sku, ok := skus[it.SkuID]
		if !ok {
			f.add(field(i, "sku_id"), "not_found")
			continue
		}
		unitID, _, ok := sku.unitOf(it.UnitID)
		if !ok {
			f.add(field(i, "unit_id"), "unknown_unit")
			continue
		}
		var rowUnit *uuid.UUID
		if unitID != sku.BaseUnitID {
			rowUnit = &unitID
		}
		sk := seenKey{sku: sku.ID, color: deref(it.ColorID), unit: deref(rowUnit), minQty: minQty.Decimal().String()}
		if seen[sk] {
			f.add(fmt.Sprintf("items[%d]", i), "duplicate")
			continue
		}
		seen[sk] = true
		keys[i] = rowKey{ListID: listID, SkuID: sku.ID, ColorID: it.ColorID, UnitID: rowUnit, MinQty: minQty}
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// DeletePriceListItem removes one row of a list ("Bỏ giá riêng" on a color row) and records the
// removal with source (default price_list).
func (s *Service) DeletePriceListItem(ctx context.Context, actor audit.Actor, listID, itemID uuid.UUID, source string) error {
	if source == "" {
		source = ChangePriceList
	}
	if !validChangeSource(source) {
		return errInvalid(map[string]string{"source": "invalid"})
	}
	return s.write(ctx, actor, func(q db.Querier) error {
		// List before row, the lock order of every price row writer.
		if _, err := readPriceList(ctx, q, listID, true); err != nil {
			return err
		}
		var row priceRow
		err := q.QueryRowxContext(ctx, `
			SELECT id, price_list_id, sku_id, color_id, unit_id, min_qty, price FROM price_list_items
			WHERE id = $1 AND price_list_id = $2 FOR UPDATE`, itemID, listID).StructScan(&row)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("pricing: read price row: %w", err)
		}
		_, err = s.deleteRow(ctx, q, actor, row, origin{Source: source})
		return err
	})
}
