package kits

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// kitSelect reads a kit or combo product with its single SKU. A kit counts as sold once its SKU
// is on an invoice or a sales document, or an assembly order was made of it.
const kitSelect = `
	SELECT p.id AS product_id, p.type, p.name, p.description, p.status, s.id AS sku_id, s.code AS sku_code, s.base_unit_id,
	  (SELECT r.version_no FROM kit_recipes r WHERE r.product_id = p.id AND r.status = 'active') AS active_version,
	  (EXISTS (SELECT 1 FROM sales_document_lines l WHERE l.sku_id = s.id)
	    OR EXISTS (SELECT 1 FROM invoice_lines l WHERE l.sku_id = s.id)
	    OR EXISTS (SELECT 1 FROM assembly_orders o JOIN kit_recipes r ON r.id = o.kit_recipe_id WHERE r.product_id = p.id)) AS sold
	FROM products p
	JOIN LATERAL (SELECT * FROM skus s WHERE s.product_id = p.id ORDER BY s.created_at, s.id LIMIT 1) s ON true
	WHERE p.type IN ('kit', 'combo')`

const recipeColumns = `id, product_id, version_no, status, valid_from, valid_to, packing_cost, output_sku_id, note,
	created_at, updated_at, version`

// readKit reads a kit with its latest recipe version and the items of that version.
func readKit(ctx context.Context, q db.Querier, productID uuid.UUID) (Kit, error) {
	var k Kit
	err := q.QueryRowxContext(ctx, kitSelect+` AND p.id = $1`, productID).StructScan(&k)
	if db.IsNoRows(err) {
		return Kit{}, errNotFound("Kit")
	}
	if err != nil {
		return Kit{}, fmt.Errorf("kits: read kit: %w", err)
	}
	if k.Latest, err = latestRecipe(ctx, q, productID, false); err != nil {
		return Kit{}, err
	}
	if k.Items, err = readItems(ctx, q, k.Latest.ID); err != nil {
		return Kit{}, err
	}
	return k, nil
}

// latestRecipe reads the newest recipe version of a product, locked when lock is set.
func latestRecipe(ctx context.Context, q db.Querier, productID uuid.UUID, lock bool) (Recipe, error) {
	query := `SELECT ` + recipeColumns + ` FROM kit_recipes WHERE product_id = $1 ORDER BY version_no DESC LIMIT 1`
	if lock {
		query += ` FOR UPDATE`
	}
	var r Recipe
	err := sqlx.GetContext(ctx, q, &r, query, productID)
	if db.IsNoRows(err) {
		return Recipe{}, errNotFound("Kit")
	}
	if err != nil {
		return Recipe{}, fmt.Errorf("kits: read latest recipe: %w", err)
	}
	return r, nil
}

// recipeByVersion reads one recipe version of a product.
func recipeByVersion(ctx context.Context, q db.Querier, productID uuid.UUID, version int) (Recipe, error) {
	var r Recipe
	err := sqlx.GetContext(ctx, q, &r, `SELECT `+recipeColumns+` FROM kit_recipes WHERE product_id = $1 AND version_no = $2`,
		productID, version)
	if db.IsNoRows(err) {
		return Recipe{}, errNotFound("Recipe version")
	}
	if err != nil {
		return Recipe{}, fmt.Errorf("kits: read recipe version: %w", err)
	}
	return r, nil
}

// recipeByID reads a recipe by id.
func recipeByID(ctx context.Context, q db.Querier, id uuid.UUID) (Recipe, error) {
	var r Recipe
	if err := sqlx.GetContext(ctx, q, &r, `SELECT `+recipeColumns+` FROM kit_recipes WHERE id = $1`, id); err != nil {
		return Recipe{}, fmt.Errorf("kits: read recipe: %w", err)
	}
	return r, nil
}

// activeRecipe reads the active recipe of a product; ok is false before the first publish.
func activeRecipe(ctx context.Context, q db.Querier, productID uuid.UUID) (Recipe, bool, error) {
	var r Recipe
	err := sqlx.GetContext(ctx, q, &r, `SELECT `+recipeColumns+` FROM kit_recipes WHERE product_id = $1 AND status = 'active'`,
		productID)
	if db.IsNoRows(err) {
		return Recipe{}, false, nil
	}
	if err != nil {
		return Recipe{}, false, fmt.Errorf("kits: read active recipe: %w", err)
	}
	return r, true, nil
}

// readItems reads the items of a recipe in line order with the flavors of its mix packs.
func readItems(ctx context.Context, q db.Querier, recipeID uuid.UUID) ([]RecipeItem, error) {
	var items []RecipeItem
	if err := sqlx.SelectContext(ctx, q, &items, `
		SELECT id, kit_recipe_id, line_no, component_kind, sku_id, unit_id, qty, unit_factor_snap, base_qty, color_id, mix_group_id
		FROM kit_recipe_items WHERE kit_recipe_id = $1 ORDER BY line_no`, recipeID); err != nil {
		return nil, fmt.Errorf("kits: read recipe items: %w", err)
	}
	var mix []MixItem
	if err := sqlx.SelectContext(ctx, q, &mix, `
		SELECT m.kit_recipe_item_id, m.sku_id, m.parts_qty
		FROM kit_recipe_mix_items m JOIN kit_recipe_items i ON i.id = m.kit_recipe_item_id
		JOIN skus s ON s.id = m.sku_id
		WHERE i.kit_recipe_id = $1 ORDER BY s.code, m.sku_id`, recipeID); err != nil {
		return nil, fmt.Errorf("kits: read recipe mix items: %w", err)
	}
	at := make(map[uuid.UUID]int, len(items))
	for i := range items {
		at[items[i].ID] = i
	}
	for _, m := range mix {
		if i, ok := at[m.ItemID]; ok {
			items[i].MixItems = append(items[i].MixItems, m)
		}
	}
	return items, nil
}

// readComponents reads the stocked SKUs one kit of a recipe takes: its SKU items and the flavors
// of its mix packs in their SKU's base unit.
func readComponents(ctx context.Context, q db.Querier, recipeID uuid.UUID) ([]Component, error) {
	var rows []struct {
		ItemID  uuid.UUID  `db:"item_id"`
		LineNo  int        `db:"line_no"`
		Kind    string     `db:"kind"`
		SkuID   uuid.UUID  `db:"sku_id"`
		ColorID *uuid.UUID `db:"color_id"`
		BaseQty money.Qty  `db:"base_qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT i.id AS item_id, i.line_no, i.component_kind AS kind, i.sku_id, i.color_id, i.base_qty
		FROM kit_recipe_items i WHERE i.kit_recipe_id = $1 AND i.sku_id IS NOT NULL
		UNION ALL
		SELECT i.id, i.line_no, i.component_kind, m.sku_id, NULL::uuid, round(m.parts_qty * g.units_per_part * u.factor, 4)
		FROM kit_recipe_items i
		JOIN kit_recipe_mix_items m ON m.kit_recipe_item_id = i.id
		JOIN mix_groups g ON g.id = i.mix_group_id
		JOIN sku_units u ON u.sku_id = m.sku_id AND u.unit_id = g.piece_unit_id
		WHERE i.kit_recipe_id = $1
		ORDER BY line_no, sku_id`, recipeID); err != nil {
		return nil, fmt.Errorf("kits: read recipe components: %w", err)
	}
	out := make([]Component, len(rows))
	for i, r := range rows {
		out[i] = Component{ItemID: r.ItemID, LineNo: r.LineNo, Kind: r.Kind, SkuID: r.SkuID, ColorID: r.ColorID, BaseQty: r.BaseQty}
	}
	return out, nil
}

func insertRecipe(ctx context.Context, q db.Querier, tenantID uuid.UUID, r Recipe) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO kit_recipes (id, tenant_id, product_id, version_no, status, valid_from, valid_to, packing_cost, output_sku_id, note,
		  created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, $12)`,
		r.ID, tenantID, r.ProductID, r.VersionNo, r.Status, r.ValidFrom, r.ValidTo, r.PackingCost, r.OutputSkuID, r.Note,
		r.CreatedAt, r.Version); err != nil {
		return fmt.Errorf("kits: insert recipe: %w", err)
	}
	return nil
}

func insertItems(ctx context.Context, q db.Querier, tenantID uuid.UUID, items []RecipeItem) error {
	for _, it := range items {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO kit_recipe_items (id, tenant_id, kit_recipe_id, line_no, component_kind, sku_id, unit_id, qty,
			  unit_factor_snap, base_qty, color_id, mix_group_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			it.ID, tenantID, it.RecipeID, it.LineNo, it.Kind, it.SkuID, it.UnitID, it.Qty, it.UnitFactor, it.BaseQty,
			it.ColorID, it.MixGroupID); err != nil {
			return fmt.Errorf("kits: insert recipe item: %w", err)
		}
		for _, m := range it.MixItems {
			if _, err := q.ExecContext(ctx, `
				INSERT INTO kit_recipe_mix_items (tenant_id, kit_recipe_item_id, sku_id, parts_qty) VALUES ($1, $2, $3, $4)`,
				tenantID, it.ID, m.SkuID, m.Parts); err != nil {
				return fmt.Errorf("kits: insert recipe flavor: %w", err)
			}
		}
	}
	return nil
}

// deleteItems removes the items of a draft recipe before it is rewritten or deleted.
func deleteItems(ctx context.Context, q db.Querier, recipeID uuid.UUID) error {
	for _, stmt := range []string{
		`DELETE FROM kit_recipe_mix_items WHERE kit_recipe_item_id IN (SELECT id FROM kit_recipe_items WHERE kit_recipe_id = $1)`,
		`DELETE FROM kit_recipe_items WHERE kit_recipe_id = $1`,
	} {
		if _, err := q.ExecContext(ctx, stmt, recipeID); err != nil {
			return fmt.Errorf("kits: delete recipe items: %w", err)
		}
	}
	return nil
}

// likePattern is a case-insensitive substring pattern of s with LIKE wildcards escaped.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.TrimSpace(s)) + "%"
}

// listKits lists kits by name. Archived (inactive) kits are listed only when asked for.
func listKits(ctx context.Context, q db.Querier, f KitFilter) ([]KitRow, error) {
	var rows []KitRow
	pattern := ""
	if strings.TrimSpace(f.Q) != "" {
		pattern = likePattern(f.Q)
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT p.id AS product_id, p.type, p.name, p.status, s.id AS sku_id, s.code AS sku_code,
		       r.version_no AS latest_version, r.status AS latest_status, a.version_no AS active_version, r.updated_at, r.version
		FROM products p
		JOIN LATERAL (SELECT * FROM skus s WHERE s.product_id = p.id ORDER BY s.created_at, s.id LIMIT 1) s ON true
		JOIN LATERAL (SELECT * FROM kit_recipes r WHERE r.product_id = p.id ORDER BY r.version_no DESC LIMIT 1) r ON true
		LEFT JOIN kit_recipes a ON a.product_id = p.id AND a.status = 'active'
		WHERE p.type IN ('kit', 'combo')
		  AND ($1 = '' OR p.type = $1)
		  AND (CASE WHEN $2 = '' THEN p.status <> 'inactive' ELSE p.status = $2 END)
		  AND ($3 = '' OR p.name ILIKE $3 OR s.code ILIKE $3)
		  AND ($4::text IS NULL OR (p.name, p.id) > ($4::text, $5::uuid))
		ORDER BY p.name, p.id
		LIMIT $6`, f.Type, f.Status, pattern, f.AfterName, f.AfterID, f.Limit); err != nil {
		return nil, fmt.Errorf("kits: list kits: %w", err)
	}
	return rows, nil
}

const orderSelect = `
	SELECT o.id, o.warehouse_id, o.doc_no, o.kit_recipe_id, o.recipe_version_snap, o.planned_qty, o.completed_qty,
	       o.disassembled_qty, o.status, o.planned_date, o.campaign_id, o.invoice_id, o.note, o.created_at, o.updated_at, o.version,
	       r.product_id, p.name AS kit_name, r.output_sku_id AS kit_sku_id
	FROM assembly_orders o JOIN kit_recipes r ON r.id = o.kit_recipe_id JOIN products p ON p.id = r.product_id`

// readOrder reads an assembly order, locked when lock is set.
func readOrder(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (AssemblyOrder, error) {
	query := orderSelect + ` WHERE o.id = $1`
	if lock {
		query += ` FOR UPDATE OF o`
	}
	var o AssemblyOrder
	err := sqlx.GetContext(ctx, q, &o, query, id)
	if db.IsNoRows(err) {
		return AssemblyOrder{}, errNotFound("Assembly order")
	}
	if err != nil {
		return AssemblyOrder{}, fmt.Errorf("kits: read assembly order: %w", err)
	}
	return o, nil
}

// readOrderItems reads the items of an order in recipe line order (substitutes after the item
// they replace) with what their reservations still hold.
func readOrderItems(ctx context.Context, q db.Querier, orderID uuid.UUID) ([]OrderItem, error) {
	var items []OrderItem
	if err := sqlx.SelectContext(ctx, q, &items, `
		SELECT i.id, i.assembly_order_id, i.kit_recipe_item_id, i.substitutes_item_id, i.sku_id, i.color_id, i.planned_base_qty,
		       i.consumed_base_qty, s.code AS sku_code, s.display_name AS sku_name,
		       COALESCE(rv.qty - rv.consumed_qty - rv.released_qty, 0) AS reserved, rv.id AS reservation_id,
		       COALESCE(rv.qty, 0) AS reservation_qty
		FROM assembly_order_items i
		JOIN skus s ON s.id = i.sku_id
		LEFT JOIN kit_recipe_items ri ON ri.id = i.kit_recipe_item_id
		LEFT JOIN reservations rv ON rv.source_type = 'assembly_order_item' AND rv.source_id = i.id
		WHERE i.assembly_order_id = $1
		ORDER BY ri.line_no NULLS LAST, s.code, i.id`, orderID); err != nil {
		return nil, fmt.Errorf("kits: read assembly order items: %w", err)
	}
	return items, nil
}

func insertOrder(ctx context.Context, q db.Querier, tenantID uuid.UUID, o AssemblyOrder) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO assembly_orders (id, tenant_id, warehouse_id, doc_no, kit_recipe_id, recipe_version_snap, planned_qty, completed_qty,
		  disassembled_qty, status, planned_date, campaign_id, invoice_id, note, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		o.ID, tenantID, o.WarehouseID, o.DocNo, o.KitRecipeID, o.RecipeVersion, o.PlannedQty, o.CompletedQty, o.DisassembledQty,
		o.Status, o.PlannedDate, o.CampaignID, o.InvoiceID, o.Note, o.CreatedAt, o.UpdatedAt, o.Version); err != nil {
		return fmt.Errorf("kits: insert assembly order: %w", err)
	}
	return nil
}

// setItemPlanned stores the planned quantity of an order item.
func setItemPlanned(ctx context.Context, q db.Querier, itemID uuid.UUID, qty money.Qty) error {
	if _, err := q.ExecContext(ctx, `UPDATE assembly_order_items SET planned_base_qty = $2 WHERE id = $1`, itemID, qty); err != nil {
		return fmt.Errorf("kits: update assembly order item: %w", err)
	}
	return nil
}

// addItemConsumed adds delta (negative to take back) to what an order item consumed.
func addItemConsumed(ctx context.Context, q db.Querier, itemID uuid.UUID, delta money.Qty) error {
	if _, err := q.ExecContext(ctx, `UPDATE assembly_order_items SET consumed_base_qty = consumed_base_qty + $2 WHERE id = $1`,
		itemID, delta); err != nil {
		return fmt.Errorf("kits: update assembly order item consumption: %w", err)
	}
	return nil
}

func insertOrderItem(ctx context.Context, q db.Querier, tenantID uuid.UUID, it OrderItem) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO assembly_order_items (id, tenant_id, assembly_order_id, kit_recipe_item_id, substitutes_item_id, sku_id, color_id,
		  planned_base_qty, consumed_base_qty)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		it.ID, tenantID, it.OrderID, it.RecipeItemID, it.SubstitutesID, it.SkuID, it.ColorID, it.PlannedBaseQty,
		it.ConsumedBaseQty); err != nil {
		return fmt.Errorf("kits: insert assembly order item: %w", err)
	}
	return nil
}

// saveOrderProgress stores the progress and status of an order the caller holds locked.
func saveOrderProgress(ctx context.Context, q db.Querier, o AssemblyOrder, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE assembly_orders SET completed_qty = $2, disassembled_qty = $3, status = $4, updated_at = $5, version = version + 1
		WHERE id = $1`, o.ID, o.CompletedQty, o.DisassembledQty, o.Status, now); err != nil {
		return fmt.Errorf("kits: update assembly order: %w", err)
	}
	return nil
}

// listOrders lists assembly orders newest first.
func listOrders(ctx context.Context, q db.Querier, f OrderFilter) ([]AssemblyOrder, error) {
	pattern := ""
	if strings.TrimSpace(f.Q) != "" {
		pattern = likePattern(f.Q)
	}
	var rows []AssemblyOrder
	if err := sqlx.SelectContext(ctx, q, &rows, orderSelect+`
		WHERE ($1 = '' OR o.status = $1)
		  AND ($2::uuid IS NULL OR r.product_id = $2)
		  AND ($3 = '' OR o.doc_no ILIKE $3 OR p.name ILIKE $3)
		  AND ($4::timestamptz IS NULL OR (o.created_at, o.id) < ($4::timestamptz, $5::uuid))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $6`, f.Status, f.ProductID, pattern, f.AfterAt, f.AfterID, f.Limit); err != nil {
		return nil, fmt.Errorf("kits: list assembly orders: %w", err)
	}
	return rows, nil
}

const postingSelect = `
	SELECT ap.id, ap.assembly_order_id, ap.produced_qty, ap.output_lot_id, l.lot_no AS output_lot_no,
	       l.expiry_date AS output_expiry, ap.components_cost, ap.packing_cost, ap.output_unit_cost, ap.business_date,
	       ap.posted_at, ap.posted_by
	FROM assembly_postings ap JOIN lots l ON l.id = ap.output_lot_id`

// readPostings reads the batches of an order oldest first with what each consumed.
func readPostings(ctx context.Context, q db.Querier, orderID uuid.UUID) ([]AssemblyPosting, error) {
	var out []AssemblyPosting
	if err := sqlx.SelectContext(ctx, q, &out, postingSelect+` WHERE ap.assembly_order_id = $1 ORDER BY ap.posted_at, ap.id`,
		orderID); err != nil {
		return nil, fmt.Errorf("kits: read assembly postings: %w", err)
	}
	for i := range out {
		items, err := readPostingItems(ctx, q, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Items = items
	}
	return out, nil
}

// readPosting reads one batch.
func readPosting(ctx context.Context, q db.Querier, id uuid.UUID) (AssemblyPosting, error) {
	var p AssemblyPosting
	if err := sqlx.GetContext(ctx, q, &p, postingSelect+` WHERE ap.id = $1`, id); err != nil {
		return AssemblyPosting{}, fmt.Errorf("kits: read assembly posting: %w", err)
	}
	items, err := readPostingItems(ctx, q, id)
	if err != nil {
		return AssemblyPosting{}, err
	}
	p.Items = items
	return p, nil
}

// postingOfLot reads the batch that produced a kit lot of an order, with its items.
func postingOfLot(ctx context.Context, q db.Querier, orderID, lotID uuid.UUID) (AssemblyPosting, bool, error) {
	var p AssemblyPosting
	err := sqlx.GetContext(ctx, q, &p, postingSelect+` WHERE ap.assembly_order_id = $1 AND ap.output_lot_id = $2`, orderID, lotID)
	if db.IsNoRows(err) {
		return AssemblyPosting{}, false, nil
	}
	if err != nil {
		return AssemblyPosting{}, false, fmt.Errorf("kits: read posting of kit lot: %w", err)
	}
	if p.Items, err = readPostingItems(ctx, q, p.ID); err != nil {
		return AssemblyPosting{}, false, err
	}
	return p, true, nil
}

func readPostingItems(ctx context.Context, q db.Querier, postingID uuid.UUID) ([]PostingItem, error) {
	var items []PostingItem
	if err := sqlx.SelectContext(ctx, q, &items, `
		SELECT pi.id, pi.assembly_order_item_id, pi.sku_id, pi.lot_id, l.lot_no, pi.color_id, pi.consumed_base_qty,
		       pi.scrap_base_qty, pi.unit_cost
		FROM assembly_posting_items pi JOIN lots l ON l.id = pi.lot_id JOIN skus s ON s.id = pi.sku_id
		WHERE pi.assembly_posting_id = $1 ORDER BY s.code, l.lot_no, pi.id`, postingID); err != nil {
		return nil, fmt.Errorf("kits: read assembly posting items: %w", err)
	}
	return items, nil
}

const disassemblySelect = `
	SELECT d.id, d.warehouse_id, d.doc_no, d.assembly_order_id, o.doc_no AS order_doc_no, d.kit_lot_id, l.lot_no AS kit_lot_no,
	       d.qty, d.reason_id, d.business_date, d.posted_at, d.posted_by, d.reversal_of, d.created_at
	FROM disassemblies d JOIN assembly_orders o ON o.id = d.assembly_order_id JOIN lots l ON l.id = d.kit_lot_id`

// readDisassembly reads a disassembly with its lines.
func readDisassembly(ctx context.Context, q db.Querier, id uuid.UUID) (Disassembly, error) {
	var d Disassembly
	err := sqlx.GetContext(ctx, q, &d, disassemblySelect+` WHERE d.id = $1`, id)
	if db.IsNoRows(err) {
		return Disassembly{}, errNotFound("Disassembly")
	}
	if err != nil {
		return Disassembly{}, fmt.Errorf("kits: read disassembly: %w", err)
	}
	if err := sqlx.SelectContext(ctx, q, &d.Lines, `
		SELECT dl.id, dl.sku_id, dl.source_lot_id, l.lot_no, dl.color_id, dl.return_base_qty, dl.damaged_base_qty, dl.unit_cost
		FROM disassembly_lines dl JOIN lots l ON l.id = dl.source_lot_id JOIN skus s ON s.id = dl.sku_id
		WHERE dl.disassembly_id = $1 ORDER BY s.code, l.lot_no, dl.id`, id); err != nil {
		return Disassembly{}, fmt.Errorf("kits: read disassembly lines: %w", err)
	}
	return d, nil
}

// listDisassemblies lists disassemblies newest first.
func listDisassemblies(ctx context.Context, q db.Querier, f DisassemblyFilter) ([]Disassembly, error) {
	pattern := ""
	if strings.TrimSpace(f.Q) != "" {
		pattern = likePattern(f.Q)
	}
	var rows []Disassembly
	if err := sqlx.SelectContext(ctx, q, &rows, disassemblySelect+`
		WHERE ($1::uuid IS NULL OR d.assembly_order_id = $1)
		  AND ($2::date IS NULL OR d.business_date >= $2)
		  AND ($3::date IS NULL OR d.business_date <= $3)
		  AND ($4::timestamptz IS NULL OR (d.created_at, d.id) < ($4::timestamptz, $5::uuid))
		  AND ($7 = '' OR d.doc_no ILIKE $7 OR o.doc_no ILIKE $7 OR l.lot_no ILIKE $7
		       OR EXISTS (SELECT 1 FROM skus s WHERE s.id = l.sku_id AND s.display_name ILIKE $7))
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $6`, f.OrderID, f.From, f.To, f.AfterAt, f.AfterID, f.Limit, pattern); err != nil {
		return nil, fmt.Errorf("kits: list disassemblies: %w", err)
	}
	return rows, nil
}

const genealogySelect = `
	SELECT g.parent_lot_id, pl.lot_no AS parent_lot_no, g.child_lot_id, cl.lot_no AS child_lot_no, cl.sku_id, s.code AS sku_code,
	       cl.color_id, cl.expiry_date, sum(g.child_base_qty) AS qty, g.assembly_posting_id, o.doc_no AS order_doc_no
	FROM lot_genealogy g
	JOIN lots pl ON pl.id = g.parent_lot_id
	JOIN lots cl ON cl.id = g.child_lot_id
	JOIN skus s ON s.id = cl.sku_id
	JOIN assembly_postings ap ON ap.id = g.assembly_posting_id
	JOIN assembly_orders o ON o.id = ap.assembly_order_id`

const genealogyGroup = `
	GROUP BY g.parent_lot_id, pl.lot_no, g.child_lot_id, cl.lot_no, cl.sku_id, s.code, cl.color_id, cl.expiry_date,
	         g.assembly_posting_id, o.doc_no
	ORDER BY s.code, cl.lot_no, pl.lot_no`

// componentsOf reads the component lots of a kit lot.
func componentsOf(ctx context.Context, q db.Querier, kitLotID uuid.UUID) ([]GenealogyLink, error) {
	var out []GenealogyLink
	if err := sqlx.SelectContext(ctx, q, &out, genealogySelect+` WHERE g.parent_lot_id = $1`+genealogyGroup, kitLotID); err != nil {
		return nil, fmt.Errorf("kits: read kit lot components: %w", err)
	}
	return out, nil
}

// usedIn reads the kit lots a component lot went into.
func usedIn(ctx context.Context, q db.Querier, lotID uuid.UUID) ([]GenealogyLink, error) {
	var out []GenealogyLink
	if err := sqlx.SelectContext(ctx, q, &out, genealogySelect+` WHERE g.child_lot_id = $1`+genealogyGroup, lotID); err != nil {
		return nil, fmt.Errorf("kits: read kit lots of a component lot: %w", err)
	}
	return out, nil
}

// lotExists reports whether the lot exists in the tenant.
func lotExists(ctx context.Context, q db.Querier, lotID uuid.UUID) (bool, error) {
	var found bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM lots WHERE id = $1)`, lotID).Scan(&found); err != nil {
		return false, fmt.Errorf("kits: read lot: %w", err)
	}
	return found, nil
}

// warehouseOf is the store's warehouse (one per tenant).
func warehouseOf(ctx context.Context, q db.Querier) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `SELECT id FROM warehouses ORDER BY created_at, id LIMIT 1`).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, fmt.Errorf("kits: the tenant has no warehouse")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("kits: read warehouse: %w", err)
	}
	return id, nil
}
