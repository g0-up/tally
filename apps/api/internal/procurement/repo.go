package procurement

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Column lists. Headers join the supplier's party for its name.
const (
	orderColumns = `o.id, o.doc_no, o.supplier_party_id, p.name AS supplier_name, o.order_date, o.expected_date, o.status,
	o.goods_amount, o.discount_amount, o.tax_amount, o.total_amount, o.note, o.close_reason_id, o.closed_at, o.confirmed_at,
	o.confirmed_by, o.created_at, o.updated_at, o.version`
	orderFrom = ` FROM purchase_orders o JOIN parties p ON p.id = o.supplier_party_id`

	orderLineColumns = `id, purchase_order_id, line_no, sku_id, sku_code_snap, name_snap, variant_label_snap, unit_id,
	unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, unit_price, discount_amount, tax_rate_snap, line_amount,
	retail_price_snap, received_base_qty, returned_base_qty`

	receiptColumns = `r.id, r.warehouse_id, r.doc_no, r.supplier_party_id, p.name AS supplier_name, r.purchase_order_id,
	r.business_date, r.goods_amount, r.discount_amount, r.tax_amount, r.landed_cost_amount, r.total_amount, r.paid_now_amount,
	r.due_date, r.status, r.note, r.idempotency_key, r.posted_at, r.posted_by, r.reversal_of, r.created_at`
	receiptFrom = ` FROM receipts r JOIN parties p ON p.id = r.supplier_party_id`

	receiptLineColumns = `id, receipt_id, line_no, purchase_order_line_id, sku_id, sku_code_snap, name_snap, variant_label_snap,
	unit_id, unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, condition, lot_no, expiry_date, lot_id,
	unit_price, discount_amount, tax_rate_snap, line_amount, landed_cost_amount, unit_cost, returned_base_qty`

	landedColumns = `id, receipt_id, cost_type, description, amount, method, payee_party_id, business_date, posted_at,
	posted_by, created_at`

	documentColumns = `d.id, d.supplier_party_id, p.name AS supplier_name, d.document_no, d.document_date, d.goods_amount,
	d.tax_amount, d.total_amount, d.match_status, d.variance_amount, d.note, d.created_at, d.updated_at, d.version`
	documentFrom = ` FROM supplier_documents d JOIN parties p ON p.id = d.supplier_party_id`

	returnColumns = `x.id, x.warehouse_id, x.doc_no, x.supplier_party_id, p.name AS supplier_name, x.receipt_id, x.reason_id,
	x.settlement, x.business_date, x.total_amount, x.status, x.idempotency_key, x.posted_at, x.posted_by, x.reversal_of,
	x.created_at`
	returnFrom = ` FROM purchase_returns x JOIN parties p ON p.id = x.supplier_party_id`

	returnLineColumns = `xl.id, xl.purchase_return_id, xl.receipt_line_id, xl.lot_id, xl.sku_id, xl.sku_code_snap, xl.name_snap,
	xl.variant_label_snap, xl.unit_id, xl.unit_name_snap, xl.unit_factor_snap, xl.qty, xl.base_qty, xl.color_id, xl.spec_text,
	xl.unit_cost, xl.line_amount, rl.lot_no`
	returnLineFrom = ` FROM purchase_return_lines xl JOIN receipt_lines rl ON rl.id = xl.receipt_line_id`

	adjustmentColumns = `a.id, a.doc_no, a.supplier_party_id, p.name AS supplier_name, a.receipt_id, a.kind, a.reason_id,
	a.business_date, a.payable_delta, a.status, a.idempotency_key, a.posted_at, a.posted_by, a.reversal_of, a.created_at`
	adjustmentFrom = ` FROM purchase_price_adjustments a JOIN parties p ON p.id = a.supplier_party_id`
)

// skuInfo is what procurement snapshots of a SKU.
type skuInfo struct {
	ID           uuid.UUID `db:"id"`
	Code         string    `db:"code"`
	DisplayName  string    `db:"display_name"`
	VariantLabel string    `db:"variant_label"`
	BaseUnitID   uuid.UUID `db:"base_unit_id"`
	TrackLot     bool      `db:"track_lot"`
	TrackExpiry  bool      `db:"track_expiry"`
	Status       string    `db:"status"`
	SpecText     *string   `db:"spec_text"`
	ProductType  string    `db:"product_type"`
}

func readSkuInfos(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]skuInfo, error) {
	var rows []skuInfo
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT s.id, s.code, s.display_name, s.variant_label, s.base_unit_id, s.track_lot, s.track_expiry, s.status, s.spec_text,
		  p.type AS product_type
		FROM skus s JOIN products p ON p.id = s.product_id
		WHERE s.id = ANY($1)`, sortedUnique(skuIDs)); err != nil {
		return nil, fmt.Errorf("procurement: read skus: %w", err)
	}
	out := make(map[uuid.UUID]skuInfo, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// checkSkus reports the lines whose SKU is missing, inactive or not stockable (a combo). index[i]
// is the request line of skuIDs[i]; nil keeps the numbering.
func checkSkus(skuIDs []uuid.UUID, infos map[uuid.UUID]skuInfo, index []int) error {
	fields := map[string]string{}
	for i, id := range skuIDs {
		n := i
		if index != nil {
			n = index[i]
		}
		info, ok := infos[id]
		switch {
		case !ok:
			fields[lineField(n, "sku_id")] = "not_found"
		case info.ProductType == "combo":
			fields[lineField(n, "sku_id")] = "not_stockable"
		case info.Status != "active":
			fields[lineField(n, "sku_id")] = "inactive"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

func unitNames(ctx context.Context, q db.Querier, unitIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	var rows []struct {
		ID   uuid.UUID `db:"id"`
		Name string    `db:"name"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, name FROM units WHERE id = ANY($1)`, sortedUnique(unitIDs)); err != nil {
		return nil, fmt.Errorf("procurement: read units: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// checkSupplier verifies that partyID is a supplier the store still trades with.
func checkSupplier(ctx context.Context, q db.Querier, partyID uuid.UUID, field string) error {
	var status string
	err := q.QueryRowxContext(ctx, `SELECT status FROM supplier_profiles WHERE party_id = $1`, partyID).Scan(&status)
	if db.IsNoRows(err) {
		return errInvalid(map[string]string{field: "not_a_supplier"})
	}
	if err != nil {
		return fmt.Errorf("procurement: read supplier: %w", err)
	}
	if status != "active" {
		return errInvalid(map[string]string{field: "not_trading"})
	}
	return nil
}

// checkParty verifies that partyID exists (the payee of a landed cost need not be a supplier).
func checkParty(ctx context.Context, q db.Querier, partyID uuid.UUID, field string) error {
	var ok bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM parties WHERE id = $1)`, partyID).Scan(&ok); err != nil {
		return fmt.Errorf("procurement: read party: %w", err)
	}
	if !ok {
		return errInvalid(map[string]string{field: "not_found"})
	}
	return nil
}

// checkReason verifies that id names an active reason of kind.
func checkReason(ctx context.Context, q db.Querier, id uuid.UUID, kind, field string) error {
	var got string
	var active bool
	err := q.QueryRowxContext(ctx, `SELECT kind, active FROM reasons WHERE id = $1`, id).Scan(&got, &active)
	if db.IsNoRows(err) {
		return errInvalid(map[string]string{field: "not_found"})
	}
	if err != nil {
		return fmt.Errorf("procurement: read reason: %w", err)
	}
	switch {
	case !active:
		return errInvalid(map[string]string{field: "inactive"})
	case got != kind:
		return errInvalid(map[string]string{field: "wrong_kind"})
	}
	return nil
}

func warehouseID(ctx context.Context, q db.Querier) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `SELECT id FROM warehouses ORDER BY created_at LIMIT 1`).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, fmt.Errorf("procurement: the tenant has no warehouse")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("procurement: read warehouse: %w", err)
	}
	return id, nil
}

// tenantToday is the business date of now in the tenant's time zone.
func tenantToday(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) (time.Time, error) {
	var name string
	if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = $1`, tenantID).Scan(&name); err != nil {
		return time.Time{}, fmt.Errorf("procurement: read tenant time zone: %w", err)
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}

// lotQtys reads the balance of lots.
func lotQtys(ctx context.Context, q db.Querier, lotIDs []uuid.UUID) (map[uuid.UUID]money.Qty, error) {
	var rows []struct {
		LotID uuid.UUID `db:"lot_id"`
		Qty   money.Qty `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT lot_id, qty FROM lot_balances WHERE lot_id = ANY($1)`,
		sortedUnique(lotIDs)); err != nil {
		return nil, fmt.Errorf("procurement: read lot balances: %w", err)
	}
	out := make(map[uuid.UUID]money.Qty, len(rows))
	for _, r := range rows {
		out[r.LotID] = r.Qty
	}
	return out, nil
}

// skuStock is the stock of a SKU as the posting left it so far.
type skuStock struct {
	SkuID      uuid.UUID   `db:"sku_id"`
	OnHand     money.Qty   `db:"on_hand"`
	StockValue money.Money `db:"stock_value"`
}

func readSkuStock(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]skuStock, error) {
	var rows []skuStock
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT sku_id, on_hand, stock_value FROM sku_stock WHERE sku_id = ANY($1)`,
		sortedUnique(skuIDs)); err != nil {
		return nil, fmt.Errorf("procurement: read sku stock: %w", err)
	}
	out := make(map[uuid.UUID]skuStock, len(rows))
	for _, r := range rows {
		out[r.SkuID] = r
	}
	return out, nil
}

// Purchase orders.

func getOrder(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (PurchaseOrder, error) {
	query := `SELECT ` + orderColumns + orderFrom + ` WHERE o.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF o`
	}
	var o PurchaseOrder
	err := sqlx.GetContext(ctx, q, &o, query, id)
	if db.IsNoRows(err) {
		return o, errNotFound("Purchase order")
	}
	if err != nil {
		return o, fmt.Errorf("procurement: read purchase order: %w", err)
	}
	return o, nil
}

func orderLines(ctx context.Context, q db.Querier, orderIDs ...uuid.UUID) ([]PurchaseOrderLine, error) {
	var out []PurchaseOrderLine
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+orderLineColumns+` FROM purchase_order_lines
		WHERE purchase_order_id = ANY($1) ORDER BY purchase_order_id, line_no`, orderIDs); err != nil {
		return nil, fmt.Errorf("procurement: read purchase order lines: %w", err)
	}
	return out, nil
}

func insertOrder(ctx context.Context, q db.Querier, tenantID uuid.UUID, o PurchaseOrder) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO purchase_orders (id, tenant_id, doc_no, supplier_party_id, order_date, expected_date, status, goods_amount,
		  discount_amount, tax_amount, total_amount, note, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 1)`,
		o.ID, tenantID, o.DocNo, o.SupplierPartyID, o.OrderDate, o.ExpectedDate, o.Status, o.GoodsAmount, o.DiscountAmount,
		o.TaxAmount, o.TotalAmount, o.Note, o.CreatedAt, o.UpdatedAt); err != nil {
		return fmt.Errorf("procurement: insert purchase order: %w", err)
	}
	return nil
}

func insertOrderLines(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []PurchaseOrderLine) error {
	for _, l := range lines {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO purchase_order_lines (id, tenant_id, purchase_order_id, line_no, sku_id, sku_code_snap, name_snap,
			  variant_label_snap, unit_id, unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, unit_price,
			  discount_amount, tax_rate_snap, line_amount, retail_price_snap, received_base_qty, returned_base_qty)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, 0, 0)`,
			l.ID, tenantID, l.PurchaseOrderID, l.LineNo, l.SkuID, l.SkuCodeSnap, l.NameSnap, l.VariantLabelSnap, l.UnitID,
			l.UnitNameSnap, l.UnitFactorSnap, l.Qty, l.BaseQty, l.ColorID, l.SpecText, l.UnitPrice, l.DiscountAmount,
			l.TaxRateSnap, l.LineAmount, l.RetailPriceSnap); err != nil {
			return fmt.Errorf("procurement: insert purchase order line: %w", err)
		}
	}
	return nil
}

// Receipts.

func getReceipt(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Receipt, error) {
	query := `SELECT ` + receiptColumns + receiptFrom + ` WHERE r.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF r`
	}
	var r Receipt
	err := sqlx.GetContext(ctx, q, &r, query, id)
	if db.IsNoRows(err) {
		return r, errNotFound("Receipt")
	}
	if err != nil {
		return r, fmt.Errorf("procurement: read receipt: %w", err)
	}
	return r, nil
}

func receiptLines(ctx context.Context, q db.Querier, receiptID uuid.UUID) ([]ReceiptLine, error) {
	var out []ReceiptLine
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+receiptLineColumns+` FROM receipt_lines WHERE receipt_id = $1
		ORDER BY line_no`, receiptID); err != nil {
		return nil, fmt.Errorf("procurement: read receipt lines: %w", err)
	}
	return out, nil
}

func insertReceipt(ctx context.Context, q db.Querier, tenantID uuid.UUID, r Receipt) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO receipts (id, tenant_id, warehouse_id, doc_no, supplier_party_id, purchase_order_id, business_date, goods_amount,
		  discount_amount, tax_amount, landed_cost_amount, total_amount, paid_now_amount, due_date, status, note, idempotency_key,
		  posted_at, posted_by, reversal_of, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		r.ID, tenantID, r.WarehouseID, r.DocNo, r.SupplierPartyID, r.PurchaseOrderID, r.BusinessDate, r.GoodsAmount,
		r.DiscountAmount, r.TaxAmount, r.LandedCostAmount, r.TotalAmount, r.PaidNowAmount, r.DueDate, r.Status, r.Note,
		r.IdempotencyKey, r.PostedAt, r.PostedBy, r.ReversalOf, r.CreatedAt); err != nil {
		return fmt.Errorf("procurement: insert receipt: %w", err)
	}
	return nil
}

func insertReceiptLines(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []ReceiptLine) error {
	for _, l := range lines {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO receipt_lines (id, tenant_id, receipt_id, line_no, purchase_order_line_id, sku_id, sku_code_snap, name_snap,
			  variant_label_snap, unit_id, unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, condition, lot_no,
			  expiry_date, lot_id, unit_price, discount_amount, tax_rate_snap, line_amount, landed_cost_amount, unit_cost,
			  returned_base_qty)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24,
			  $25, $26, 0)`,
			l.ID, tenantID, l.ReceiptID, l.LineNo, l.PurchaseOrderLineID, l.SkuID, l.SkuCodeSnap, l.NameSnap, l.VariantLabelSnap,
			l.UnitID, l.UnitNameSnap, l.UnitFactorSnap, l.Qty, l.BaseQty, l.ColorID, l.SpecText, l.Condition, l.LotNo,
			l.ExpiryDate, l.LotID, l.UnitPrice, l.DiscountAmount, l.TaxRateSnap, l.LineAmount, l.LandedCostAmount, l.UnitCost); err != nil {
			return fmt.Errorf("procurement: insert receipt line: %w", err)
		}
	}
	return nil
}

// insertCogs writes the parts of a landed cost, price adjustment or purchase return that fell on
// stock already sold as COGS adjustments, one per SKU.
func insertCogs(ctx context.Context, q db.Querier, tenantID uuid.UUID, docType string, docID uuid.UUID, date, now time.Time,
	bySku map[uuid.UUID]money.Money, newID func() uuid.UUID) error {
	skus := make([]uuid.UUID, 0, len(bySku))
	for id, amt := range bySku {
		if !amt.IsZero() {
			skus = append(skus, id)
		}
	}
	slices.SortFunc(skus, compareUUID)
	for _, sku := range skus {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO cogs_adjustments (id, tenant_id, sku_id, amount, source_doc_type, source_doc_id, business_date, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, newID(), tenantID, sku, bySku[sku], docType, docID, date, now); err != nil {
			return fmt.Errorf("procurement: insert cogs adjustment: %w", err)
		}
	}
	return nil
}

// Attachment is a file (document photo) uploaded for a procurement document.
type Attachment struct {
	ID          uuid.UUID `db:"id"`
	FileName    string    `db:"file_name"`
	ContentType string    `db:"content_type"`
	SizeBytes   int64     `db:"size_bytes"`
	ScanStatus  string    `db:"scan_status"`
	CreatedAt   time.Time `db:"created_at"`
}

// AttachmentOwnerSupplierDocument is the attachments.owner_type of supplier document photos;
// receipt photos use DocReceipt.
const AttachmentOwnerSupplierDocument = "supplier_document"

// AttachmentOwners are the documents a file can be uploaded for, by attachments.owner_type: each
// reports whether the document exists in the tenant of q.
func AttachmentOwners() map[string]func(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error) {
	return map[string]func(context.Context, db.Querier, uuid.UUID) (bool, error){
		DocReceipt:                      ownerExists(`SELECT EXISTS (SELECT 1 FROM receipts WHERE id = $1)`),
		AttachmentOwnerSupplierDocument: ownerExists(`SELECT EXISTS (SELECT 1 FROM supplier_documents WHERE id = $1)`),
	}
}

func ownerExists(query string) func(context.Context, db.Querier, uuid.UUID) (bool, error) {
	return func(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error) {
		var ok bool
		if err := q.QueryRowxContext(ctx, query, id).Scan(&ok); err != nil {
			return false, fmt.Errorf("procurement: read attachment owner: %w", err)
		}
		return ok, nil
	}
}

func attachmentsOf(ctx context.Context, q db.Querier, ownerType string, ownerID uuid.UUID) ([]Attachment, error) {
	var out []Attachment
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT a.id, a.file_name, a.content_type, a.size_bytes, a.scan_status, a.created_at FROM attachments a
		WHERE a.owner_type = $1 AND a.owner_id = $2 AND a.scan_status <> 'rejected'
		  AND NOT EXISTS (SELECT 1 FROM attachments n WHERE n.supersedes_id = a.id)
		ORDER BY a.created_at, a.id`, ownerType, ownerID); err != nil {
		return nil, fmt.Errorf("procurement: read attachments: %w", err)
	}
	return out, nil
}

// Helpers.

func wrap(what string, err error) error { return fmt.Errorf("procurement: %s: %w", what, err) }

func sortedUnique(in []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if id != uuid.Nil {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, compareUUID)
	return slices.Compact(out)
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func minQty(a, b money.Qty) money.Qty {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}
