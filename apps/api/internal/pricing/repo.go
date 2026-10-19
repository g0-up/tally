package pricing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// skuInfo is what pricing reads of a SKU: its base unit and sale units with their factors, its
// minimum price and its tax rate.
type skuInfo struct {
	ID         uuid.UUID
	Code       string
	Name       string
	BaseUnitID uuid.UUID
	MinPrice   *money.Money
	TaxRate    money.Percent
	// Units maps every active unit of the SKU (the base unit included, factor 1) to its factor.
	Units map[uuid.UUID]money.Factor
}

// unitOf resolves the unit of a line: nil is the base unit. ok is false for a unit the SKU does
// not sell in.
func (s *skuInfo) unitOf(unitID *uuid.UUID) (id uuid.UUID, factor money.Factor, ok bool) {
	if unitID == nil || *unitID == s.BaseUnitID {
		return s.BaseUnitID, money.FactorOne, true
	}
	f, ok := s.Units[*unitID]
	return *unitID, f, ok
}

// loadSkuInfo reads the SKUs of ids with their units; unknown ids are absent from the map.
func loadSkuInfo(ctx context.Context, q db.Querier, ids []uuid.UUID) (map[uuid.UUID]*skuInfo, error) {
	out := make(map[uuid.UUID]*skuInfo, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID         uuid.UUID     `db:"id"`
		Code       string        `db:"code"`
		Name       string        `db:"display_name"`
		BaseUnitID uuid.UUID     `db:"base_unit_id"`
		MinPrice   *money.Money  `db:"min_price"`
		TaxRate    money.Percent `db:"rate_percent"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT s.id, s.code, s.display_name, s.base_unit_id, s.min_price,
		       COALESCE(t.rate_percent, 0) AS rate_percent
		FROM skus s LEFT JOIN tax_rates t ON t.id = s.tax_rate_id
		WHERE s.id = ANY($1)`, ids); err != nil {
		return nil, fmt.Errorf("pricing: read skus: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = &skuInfo{
			ID: r.ID, Code: r.Code, Name: r.Name, BaseUnitID: r.BaseUnitID, MinPrice: r.MinPrice,
			TaxRate: r.TaxRate,
			Units:   map[uuid.UUID]money.Factor{r.BaseUnitID: money.FactorOne},
		}
	}
	var units []struct {
		SkuID  uuid.UUID    `db:"sku_id"`
		UnitID uuid.UUID    `db:"unit_id"`
		Factor money.Factor `db:"factor"`
	}
	if err := sqlx.SelectContext(ctx, q, &units, `
		SELECT sku_id, unit_id, factor FROM sku_units WHERE sku_id = ANY($1) AND active`, ids); err != nil {
		return nil, fmt.Errorf("pricing: read sku units: %w", err)
	}
	for _, u := range units {
		if s, ok := out[u.SkuID]; ok && u.UnitID != s.BaseUnitID {
			s.Units[u.UnitID] = u.Factor
		}
	}
	return out, nil
}

// existingIDs returns which of ids exist in table (colors, customer_groups …); table is a code
// constant.
func existingIDs(ctx context.Context, q db.Querier, table string, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var found []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &found, `SELECT id FROM `+table+` WHERE id = ANY($1)`, ids); err != nil {
		return nil, fmt.Errorf("pricing: read %s: %w", table, err)
	}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

func exists(ctx context.Context, q db.Querier, table string, id uuid.UUID) (bool, error) {
	found, err := existingIDs(ctx, q, table, []uuid.UUID{id})
	return found[id], err
}

// uniqueIDs returns ids without duplicates, in first-seen order.
func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

const priceListColumns = `
	pl.id, pl.name, pl.name_key, pl.kind, pl.customer_group_id, pl.party_id, pl.campaign_id, pl.priority,
	pl.valid_from, pl.valid_to, pl.active, pl.created_at, pl.updated_at, pl.version`

// readPriceList reads one list; forUpdate locks it.
func readPriceList(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (PriceList, error) {
	query := `SELECT ` + priceListColumns + `,
		(SELECT count(*) FROM price_list_items i WHERE i.price_list_id = pl.id) AS item_count
		FROM price_lists pl WHERE pl.id = $1`
	if forUpdate {
		query = `SELECT ` + priceListColumns + `, 0 AS item_count FROM price_lists pl WHERE pl.id = $1 FOR UPDATE`
	}
	var l PriceList
	err := q.QueryRowxContext(ctx, query, id).StructScan(&l)
	if db.IsNoRows(err) {
		return PriceList{}, errNotFound()
	}
	if err != nil {
		return PriceList{}, fmt.Errorf("pricing: read price list: %w", err)
	}
	return l, nil
}

// baseList reads the base list of kind (retail or wholesale); ok is false when it does not exist.
func baseList(ctx context.Context, q db.Querier, kind string) (PriceList, bool, error) {
	var l PriceList
	err := q.QueryRowxContext(ctx, `SELECT `+priceListColumns+`, 0 AS item_count FROM price_lists pl WHERE pl.kind = $1`, kind).StructScan(&l)
	if db.IsNoRows(err) {
		return PriceList{}, false, nil
	}
	if err != nil {
		return PriceList{}, false, fmt.Errorf("pricing: read base price list: %w", err)
	}
	return l, true, nil
}

// candidateLists reads the active lists valid on date that can price a customer of group with
// base kind: every season list, the base list of baseKind, the group's lists and the party's
// lists. They come ordered by priority, newest first, then id: the order each resolver level
// tries them in.
func candidateLists(ctx context.Context, q db.Querier, date time.Time, baseKind string, groupID, partyID *uuid.UUID) ([]PriceList, error) {
	var out []PriceList
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT `+priceListColumns+`, 0 AS item_count FROM price_lists pl
		WHERE pl.active
		  AND (pl.valid_from IS NULL OR pl.valid_from <= $1::date)
		  AND (pl.valid_to IS NULL OR pl.valid_to >= $1::date)
		  AND (pl.kind = 'season' OR pl.kind = $2
		       OR (pl.kind = 'group' AND pl.customer_group_id = $3::uuid)
		       OR (pl.kind = 'customer' AND pl.party_id = $4::uuid))
		ORDER BY pl.priority, pl.created_at DESC, pl.id`, date, baseKind, groupID, partyID); err != nil {
		return nil, fmt.Errorf("pricing: read candidate price lists: %w", err)
	}
	return out, nil
}

// priceRow is a price list row as the resolver matches it.
type priceRow struct {
	ID          uuid.UUID   `db:"id"`
	PriceListID uuid.UUID   `db:"price_list_id"`
	SkuID       uuid.UUID   `db:"sku_id"`
	ColorID     *uuid.UUID  `db:"color_id"`
	UnitID      *uuid.UUID  `db:"unit_id"`
	MinQty      money.Qty   `db:"min_qty"`
	Price       money.Money `db:"price"`
}

// candidateRows reads every row of lists for skus in one query (ix_price_list_items__sku).
func candidateRows(ctx context.Context, q db.Querier, lists, skus []uuid.UUID) ([]priceRow, error) {
	if len(lists) == 0 || len(skus) == 0 {
		return nil, nil
	}
	var out []priceRow
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT id, price_list_id, sku_id, color_id, unit_id, min_qty, price
		FROM price_list_items WHERE sku_id = ANY($1) AND price_list_id = ANY($2)`, skus, lists); err != nil {
		return nil, fmt.Errorf("pricing: read price rows: %w", err)
	}
	return out, nil
}

const itemSelect = `
	SELECT i.id, i.price_list_id, i.sku_id, i.color_id, i.unit_id, i.min_qty, i.price, i.created_at, i.updated_at,
	       s.code AS sku_code, s.display_name AS sku_name, c.name AS color_name, u.name AS unit_name
	FROM price_list_items i
	JOIN skus s ON s.id = i.sku_id
	LEFT JOIN colors c ON c.id = i.color_id
	LEFT JOIN units u ON u.id = i.unit_id`

// readItems reads list rows by id, in the order of ids.
func readItems(ctx context.Context, q db.Querier, ids []uuid.UUID) ([]PriceListItem, error) {
	var rows []PriceListItem
	if err := sqlx.SelectContext(ctx, q, &rows, itemSelect+` WHERE i.id = ANY($1)`, ids); err != nil {
		return nil, fmt.Errorf("pricing: read price list items: %w", err)
	}
	byID := make(map[uuid.UUID]PriceListItem, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]PriceListItem, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// ItemFilter selects the rows of a list; AfterKey/AfterID continue after a (sku code, id) cursor.
type ItemFilter struct {
	SkuID    *uuid.UUID
	AfterKey *string
	AfterID  uuid.UUID
	Limit    int
}

func listItems(ctx context.Context, q db.Querier, listID uuid.UUID, f ItemFilter) ([]PriceListItem, error) {
	var out []PriceListItem
	if err := sqlx.SelectContext(ctx, q, &out, itemSelect+`
		WHERE i.price_list_id = $1 AND ($2::uuid IS NULL OR i.sku_id = $2)
		  AND (NOT $3 OR (s.code, i.id) > ($4, $5::uuid))
		ORDER BY s.code, i.id
		LIMIT $6`, listID, f.SkuID, f.AfterKey != nil, deref(f.AfterKey), f.AfterID, f.Limit); err != nil {
		return nil, fmt.Errorf("pricing: list price list items: %w", err)
	}
	return out, nil
}

// changeSelect reads history rows with their actor's name. changeSelectReadOnly leaves the name
// out: the users table is a platform table the read-only support role cannot see.
var (
	changeSelect         = changeQuery("au.full_name", "LEFT JOIN users au ON au.id = pc.actor_id")
	changeSelectReadOnly = changeQuery("NULL::text", "")
)

func changeQuery(actorName, actorJoin string) string {
	return `
	SELECT pc.id, pc.price_list_id, pl.name AS price_list_name, pl.kind AS price_list_kind, pc.sku_id,
	       s.code AS sku_code, s.display_name AS sku_name,
	       pc.color_id, c.name AS color_name, pc.unit_id, u.name AS unit_name, pc.min_qty, pc.old_price,
	       pc.new_price, pc.source, pc.source_doc_id, pc.actor_id, ` + actorName + ` AS actor_name, pc.created_at
	FROM price_changes pc
	JOIN price_lists pl ON pl.id = pc.price_list_id
	JOIN skus s ON s.id = pc.sku_id
	` + actorJoin + `
	LEFT JOIN colors c ON c.id = pc.color_id
	LEFT JOIN units u ON u.id = pc.unit_id`
}

// readChanges reads history rows by id, in the order of ids.
func readChanges(ctx context.Context, q db.Querier, ids []uuid.UUID) ([]PriceChange, error) {
	if len(ids) == 0 {
		return []PriceChange{}, nil
	}
	var rows []PriceChange
	if err := sqlx.SelectContext(ctx, q, &rows, changeSelect+` WHERE pc.id = ANY($1)`, ids); err != nil {
		return nil, fmt.Errorf("pricing: read price changes: %w", err)
	}
	byID := make(map[uuid.UUID]PriceChange, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]PriceChange, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// HistoryFilter pages the price history, newest first; AfterTime/AfterID continue after a
// (created_at, id) cursor.
type HistoryFilter struct {
	// SkuID narrows to one SKU; nil reads the whole tenant.
	SkuID     *uuid.UUID
	AfterTime *time.Time
	AfterID   uuid.UUID
	Limit     int
}

func listChanges(ctx context.Context, q db.Querier, f HistoryFilter) ([]PriceChange, error) {
	var after time.Time
	if f.AfterTime != nil {
		after = *f.AfterTime
	}
	var readsUsers bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('users', 'SELECT')`).Scan(&readsUsers); err != nil {
		return nil, fmt.Errorf("pricing: read users privilege: %w", err)
	}
	query := changeSelect
	if !readsUsers {
		query = changeSelectReadOnly
	}
	var out []PriceChange
	if err := sqlx.SelectContext(ctx, q, &out, query+`
		WHERE ($1::uuid IS NULL OR pc.sku_id = $1) AND (NOT $2 OR (pc.created_at, pc.id) < ($3, $4::uuid))
		ORDER BY pc.created_at DESC, pc.id DESC
		LIMIT $5`, f.SkuID, f.AfterTime != nil, after, f.AfterID, f.Limit); err != nil {
		return nil, fmt.Errorf("pricing: list price changes: %w", err)
	}
	return out, nil
}

// actorNames fills the actor names of history rows read without them, from one SELECT of the
// tenant's users in q.
func actorNames(ctx context.Context, q db.Querier, tenantID uuid.UUID, rows []PriceChange) error {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r.ActorName == nil {
			ids = append(ids, r.ActorID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var users []struct {
		ID   uuid.UUID `db:"id"`
		Name string    `db:"full_name"`
	}
	if err := sqlx.SelectContext(ctx, q, &users,
		`SELECT id, full_name FROM users WHERE tenant_id = $1 AND id = ANY($2)`, tenantID, ids); err != nil {
		return fmt.Errorf("pricing: read actor names: %w", err)
	}
	names := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Name
	}
	for i, r := range rows {
		if r.ActorName != nil {
			continue
		}
		if name, ok := names[r.ActorID]; ok {
			rows[i].ActorName = &name
		}
	}
	return nil
}
