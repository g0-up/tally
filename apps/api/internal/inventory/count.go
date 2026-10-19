package inventory

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// ScopeCountPost is the idempotency scope of posting a stock count.
const ScopeCountPost = "stock_count.post"

// Bounds of a stock count request.
const (
	maxCountSelection = 2000
	maxCountLineEdits = 500
	countInsertBatch  = 500
)

const countColumns = `id, warehouse_id, doc_no, scope, category_id, blind, status, business_date, started_at, counted_at, note,
	idempotency_key, posted_at, posted_by, created_at, updated_at, version`

const countLineColumns = `id, stock_count_id, sku_id, color_id, lot_id, system_qty, counted_qty, moved_since_start_qty,
	variance_qty, reason_id, new_lot_no, new_expiry_date`

// maxLotNoLen bounds a lot number, as the ledger does.
const maxLotNoLen = 64

// CountLineView is a count line with what the counting sheet shows of its SKU, color and lot: the
// counted lot's number and expiry, or those recorded for the new lot a gain opens.
type CountLineView struct {
	StockCountLine
	SkuCode      string     `db:"sku_code"`
	SkuName      string     `db:"sku_name"`
	VariantLabel string     `db:"variant_label"`
	UnitName     string     `db:"unit_name"`
	TrackLot     bool       `db:"track_lot"`
	TrackExpiry  bool       `db:"track_expiry"`
	ColorCode    *string    `db:"color_code"`
	ColorName    *string    `db:"color_name"`
	ColorHex     *string    `db:"color_hex"`
	LotNo        *string    `db:"lot_no"`
	ExpiryDate   *time.Time `db:"expiry_date"`
}

// CountDetail is a KK with its lines and the DC its post created.
type CountDetail struct {
	StockCount
	Lines        []CountLineView
	AdjustmentID *uuid.UUID
	AdjustmentNo *string
}

// CountInput starts a KK.
type CountInput struct {
	Scope      string
	CategoryID *uuid.UUID
	SkuIDs     []uuid.UUID
	Blind      bool
	Note       *string
}

func (in CountInput) validate() error {
	fields := map[string]string{}
	switch in.Scope {
	case ScopeAll:
	case ScopeCategory:
		if in.CategoryID == nil {
			fields["category_id"] = "required"
		}
	case ScopeSelection:
		switch {
		case len(in.SkuIDs) == 0:
			fields["sku_ids"] = "required"
		case len(in.SkuIDs) > maxCountSelection:
			fields["sku_ids"] = "too_many"
		}
	default:
		fields["scope"] = "invalid"
	}
	if in.Scope != ScopeCategory && in.CategoryID != nil {
		fields["category_id"] = "not_allowed"
	}
	if in.Scope != ScopeSelection && len(in.SkuIDs) > 0 {
		fields["sku_ids"] = "not_allowed"
	}
	if in.Note != nil && utf8.RuneCountInString(*in.Note) > maxNoteLen {
		fields["note"] = "too_long"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// StartCount opens a KK: it snapshots system_qty of every SKU in scope, one line per SKU ×
// color, per lot for SKUs tracking lots, and a zero line for a SKU without stock. A retry with
// the same idempotency key returns the count it created (created false).
func (s *Service) StartCount(ctx context.Context, actor audit.Actor, idemKey *string, in CountInput) (CountDetail, bool, error) {
	if err := in.validate(); err != nil {
		return CountDetail{}, false, err
	}
	var out CountDetail
	created := false
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if idemKey != nil {
			var id uuid.UUID
			err := q.QueryRowxContext(ctx, `SELECT id FROM stock_counts WHERE idempotency_key = $1`, *idemKey).Scan(&id)
			if err == nil {
				out, err = readCountDetail(ctx, q, id)
				return err
			}
			if !db.IsNoRows(err) {
				return fmt.Errorf("inventory: read stock count by key: %w", err)
			}
		}
		skus, err := countSkus(ctx, q, in)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		today, err := tenantToday(ctx, q, actor.TenantID, now)
		if err != nil {
			return err
		}
		lines, err := snapshotCount(ctx, q, skus)
		if err != nil {
			return err
		}
		docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixStockCount)
		if err != nil {
			return err
		}
		c := StockCount{ID: ids.New(), DocNo: docNo, Scope: in.Scope, CategoryID: in.CategoryID, Blind: in.Blind,
			Status: CountCounting, BusinessDate: today, StartedAt: now, Note: in.Note, IdempotencyKey: idemKey,
			CreatedAt: now, UpdatedAt: now, Version: 1}
		if c.WarehouseID, err = warehouseID(ctx, q); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO stock_counts (id, tenant_id, warehouse_id, doc_no, scope, category_id, blind, status, business_date,
			  started_at, note, idempotency_key, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			c.ID, actor.TenantID, c.WarehouseID, c.DocNo, c.Scope, c.CategoryID, c.Blind, c.Status, c.BusinessDate,
			c.StartedAt, c.Note, c.IdempotencyKey, c.CreatedAt, c.UpdatedAt, c.Version); err != nil {
			return fmt.Errorf("inventory: insert stock count: %w", err)
		}
		for i := range lines {
			lines[i].ID, lines[i].StockCountID = ids.New(), c.ID
		}
		if err := insertCountLines(ctx, q, actor.TenantID, lines); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{Action: "stock_count.start", EntityType: DocStockCount, EntityID: c.ID,
			DocNo: c.DocNo, After: map[string]any{"scope": c.Scope, "category_id": c.CategoryID, "blind": c.Blind,
				"skus": len(skus), "lines": len(lines)}}); err != nil {
			return err
		}
		created = true
		out, err = readCountDetail(ctx, q, c.ID)
		return err
	})
	return out, created, err
}

// countSkus returns the active stock-holding SKUs of the scope, in id order.
func countSkus(ctx context.Context, q db.Querier, in CountInput) ([]uuid.UUID, error) {
	var out []uuid.UUID
	const stockable = `FROM skus s JOIN products p ON p.id = s.product_id WHERE s.status = 'active' AND p.type <> 'combo'`
	switch in.Scope {
	case ScopeCategory:
		var exists bool
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1)`, *in.CategoryID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("inventory: read category: %w", err)
		}
		if !exists {
			return nil, errInvalid(map[string]string{"category_id": "not_found"})
		}
		if err := sqlx.SelectContext(ctx, q, &out, `
			WITH RECURSIVE subtree AS (
			  SELECT id FROM categories WHERE id = $1::uuid
			  UNION ALL
			  SELECT c.id FROM categories c JOIN subtree t ON c.parent_id = t.id
			)
			SELECT s.id `+stockable+` AND p.category_id IN (SELECT id FROM subtree) ORDER BY s.id`, *in.CategoryID); err != nil {
			return nil, fmt.Errorf("inventory: read category skus: %w", err)
		}
	case ScopeSelection:
		infos, err := readSkuInfos(ctx, q, sortedUnique(in.SkuIDs))
		if err != nil {
			return nil, err
		}
		fields := map[string]string{}
		for i, id := range in.SkuIDs {
			info, ok := infos[id]
			switch {
			case !ok:
				fields[fmt.Sprintf("sku_ids.%d", i)] = "not_found"
			case info.ProductType == "combo":
				fields[fmt.Sprintf("sku_ids.%d", i)] = "not_stockable"
			case info.Status != skuActive:
				fields[fmt.Sprintf("sku_ids.%d", i)] = "inactive"
			}
		}
		if len(fields) > 0 {
			return nil, errInvalid(fields)
		}
		out = sortedUnique(in.SkuIDs)
	default:
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT s.id `+stockable+` ORDER BY s.id`); err != nil {
			return nil, fmt.Errorf("inventory: read skus: %w", err)
		}
	}
	if len(out) == 0 {
		return nil, apperr.NewValidation("count_empty", "There is no product to count in this scope", map[string]string{"scope": "empty"})
	}
	return out, nil
}

// snapshotCount reads the normal-lot stock of skus in one statement (one consistent snapshot)
// and returns the count lines.
func snapshotCount(ctx context.Context, q db.Querier, skus []uuid.UUID) ([]StockCountLine, error) {
	var rows []struct {
		SkuID   uuid.UUID  `db:"sku_id"`
		ColorID *uuid.UUID `db:"color_id"`
		LotID   *uuid.UUID `db:"lot_id"`
		Qty     money.Qty  `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT b.sku_id, b.color_id, CASE WHEN s.track_lot THEN b.lot_id END AS lot_id, SUM(b.qty) AS qty
		FROM lot_balances b JOIN lots l ON l.id = b.lot_id JOIN skus s ON s.id = b.sku_id
		WHERE b.sku_id = ANY($1) AND NOT l.is_quarantine AND b.qty > 0
		GROUP BY b.sku_id, b.color_id, CASE WHEN s.track_lot THEN b.lot_id END
		ORDER BY b.sku_id`, skus); err != nil {
		return nil, fmt.Errorf("inventory: snapshot stock: %w", err)
	}
	stocked := make(map[uuid.UUID]bool, len(rows))
	lines := make([]StockCountLine, 0, len(rows)+len(skus))
	for _, r := range rows {
		stocked[r.SkuID] = true
		lines = append(lines, StockCountLine{SkuID: r.SkuID, ColorID: r.ColorID, LotID: r.LotID, SystemQty: r.Qty})
	}
	for _, id := range skus {
		if !stocked[id] {
			lines = append(lines, StockCountLine{SkuID: id, SystemQty: money.ZeroQty})
		}
	}
	return lines, nil
}

func insertCountLines(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []StockCountLine) error {
	for start := 0; start < len(lines); start += countInsertBatch {
		batch := lines[start:min(start+countInsertBatch, len(lines))]
		var sb strings.Builder
		sb.WriteString(`INSERT INTO stock_count_lines (id, tenant_id, stock_count_id, sku_id, color_id, lot_id, system_qty) VALUES `)
		args := make([]any, 0, len(batch)*7)
		for i, ln := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			n := i * 7
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7)
			args = append(args, ln.ID, tenantID, ln.StockCountID, ln.SkuID, ln.ColorID, ln.LotID, ln.SystemQty)
		}
		if _, err := q.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("inventory: insert stock count lines: %w", err)
		}
	}
	return nil
}

func readCount(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (StockCount, error) {
	query := `SELECT ` + countColumns + ` FROM stock_counts WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var c StockCount
	err := q.QueryRowxContext(ctx, query, id).StructScan(&c)
	if db.IsNoRows(err) {
		return StockCount{}, errNotFound("Stock count")
	}
	if err != nil {
		return StockCount{}, fmt.Errorf("inventory: read stock count: %w", err)
	}
	return c, nil
}

func readCountLines(ctx context.Context, q db.Querier, id uuid.UUID) ([]StockCountLine, error) {
	var out []StockCountLine
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+countLineColumns+` FROM stock_count_lines WHERE stock_count_id = $1
		ORDER BY sku_id, id`, id); err != nil {
		return nil, fmt.Errorf("inventory: read stock count lines: %w", err)
	}
	return out, nil
}

// readCountDetail reads a count with its sheet lines and its DC.
func readCountDetail(ctx context.Context, q db.Querier, id uuid.UUID) (CountDetail, error) {
	c, err := readCount(ctx, q, id, false)
	if err != nil {
		return CountDetail{}, err
	}
	out := CountDetail{StockCount: c, Lines: []CountLineView{}}
	if err := sqlx.SelectContext(ctx, q, &out.Lines, `
		SELECT `+prefixed("cl", countLineColumns)+`, s.code AS sku_code, s.display_name AS sku_name, s.variant_label,
		  u.name AS unit_name, s.track_lot, s.track_expiry, c.code AS color_code, c.name AS color_name, c.hex AS color_hex,
		  COALESCE(l.lot_no, cl.new_lot_no) AS lot_no, COALESCE(l.expiry_date, cl.new_expiry_date) AS expiry_date
		FROM stock_count_lines cl JOIN skus s ON s.id = cl.sku_id JOIN units u ON u.id = s.base_unit_id
		LEFT JOIN colors c ON c.id = cl.color_id LEFT JOIN lots l ON l.id = cl.lot_id
		WHERE cl.stock_count_id = $1
		ORDER BY s.code, c.sort_order NULLS LAST, c.code NULLS LAST, l.expiry_date NULLS LAST, l.lot_no, cl.id`, id); err != nil {
		return CountDetail{}, fmt.Errorf("inventory: read stock count sheet: %w", err)
	}
	var adj struct {
		ID    uuid.UUID `db:"id"`
		DocNo string    `db:"doc_no"`
	}
	err = q.QueryRowxContext(ctx, `SELECT id, doc_no FROM stock_adjustments WHERE stock_count_id = $1 AND reversal_of IS NULL`, id).StructScan(&adj)
	switch {
	case err == nil:
		out.AdjustmentID, out.AdjustmentNo = &adj.ID, &adj.DocNo
	case !db.IsNoRows(err):
		return CountDetail{}, fmt.Errorf("inventory: read stock count adjustment: %w", err)
	}
	return out, nil
}

// Count returns one KK with its sheet.
func (s *Service) Count(ctx context.Context, scope Scope, id uuid.UUID) (CountDetail, error) {
	var out CountDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = readCountDetail(ctx, q, id)
		return err
	})
	return out, err
}

// CountFilter narrows the KK list (newest first). DocNo finds one document by its exact number.
type CountFilter struct {
	Status  string
	DocNo   string
	AfterAt *time.Time
	AfterID uuid.UUID
	Limit   int
}

// CountSummary is a KK of the list with how far its sheet got: its lines, the lines counted so
// far and the lines whose count differs from the book.
type CountSummary struct {
	StockCount
	LineCount     int `db:"line_count"`
	CountedLines  int `db:"counted_lines"`
	VarianceLines int `db:"variance_lines"`
}

// Counts lists KKs with a summary of their sheet.
func (s *Service) Counts(ctx context.Context, scope Scope, f CountFilter) ([]CountSummary, error) {
	out := []CountSummary{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+countColumns+`, l.line_count, l.counted_lines, l.variance_lines
			FROM stock_counts k
			CROSS JOIN LATERAL (SELECT count(*) AS line_count, count(x.counted_qty) AS counted_lines,
					count(*) FILTER (WHERE x.variance_qty <> 0) AS variance_lines
				FROM stock_count_lines x WHERE x.stock_count_id = k.id) l
			WHERE ($1 = '' OR status = $1) AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3::uuid))
			  AND ($5 = '' OR doc_no = $5)
			ORDER BY created_at DESC, id DESC LIMIT $4`, f.Status, f.AfterAt, f.AfterID, f.Limit, strings.TrimSpace(f.DocNo)); err != nil {
			return fmt.Errorf("inventory: list stock counts: %w", err)
		}
		return nil
	})
	return out, err
}

// CountLineInput records the counted quantity of a line: LineID names an existing line; without
// it, SkuID and ColorID name a color line of a SKU of the count that does not track lots (added
// when the shelf holds a color the snapshot did not). CountedQty nil clears the count. On a line
// without a lot, LotNo (SKUs tracking lots) and ExpiryDate (SKUs tracking expiry) describe the new
// lot a gain opens; nil clears them.
type CountLineInput struct {
	LineID     *uuid.UUID
	SkuID      *uuid.UUID
	ColorID    *uuid.UUID
	CountedQty *money.Qty
	ReasonID   *uuid.UUID
	LotNo      *string
	ExpiryDate *time.Time
}

// lotNo is the trimmed lot number of the line, nil when blank.
func (in CountLineInput) lotNo() *string {
	if in.LotNo == nil {
		return nil
	}
	no := strings.TrimSpace(*in.LotNo)
	if no == "" {
		return nil
	}
	return &no
}

// UpdateCountLines records counted quantities while the count is counting.
func (s *Service) UpdateCountLines(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in []CountLineInput) (CountDetail, error) {
	fields := map[string]string{}
	switch {
	case len(in) == 0:
		fields["lines"] = "required"
	case len(in) > maxCountLineEdits:
		fields["lines"] = "too_many"
	}
	for i, ln := range in {
		if ln.LineID == nil && ln.SkuID == nil {
			fields[fmt.Sprintf("lines.%d.line_id", i)] = "required"
		}
		if ln.LineID != nil && (ln.SkuID != nil || ln.ColorID != nil) {
			fields[fmt.Sprintf("lines.%d.line_id", i)] = "conflicts_with_sku"
		}
		if ln.CountedQty != nil && ln.CountedQty.IsNegative() {
			fields[fmt.Sprintf("lines.%d.counted_qty", i)] = "must_not_be_negative"
		}
		if no := ln.lotNo(); no != nil && len(*no) > maxLotNoLen {
			fields[fmt.Sprintf("lines.%d.lot_no", i)] = "too_long"
		}
	}
	if len(fields) > 0 {
		return CountDetail{}, errInvalid(fields)
	}
	var out CountDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		c, err := lockCount(ctx, q, id, version)
		if err != nil {
			return err
		}
		if c.Status != CountCounting {
			return apperr.NewConflict(CodeCountNotCounting, "The count is not open for counting")
		}
		lines, err := readCountLines(ctx, q, id)
		if err != nil {
			return err
		}
		byID := make(map[uuid.UUID]StockCountLine, len(lines))
		skuInCount := map[uuid.UUID]bool{}
		for _, ln := range lines {
			byID[ln.ID] = ln
			skuInCount[ln.SkuID] = true
		}
		if err := checkCountReasons(ctx, q, in); err != nil {
			return err
		}
		var skus []uuid.UUID
		for _, ln := range in {
			if ln.LineID == nil {
				skus = append(skus, *ln.SkuID)
			} else if line, ok := byID[*ln.LineID]; ok {
				skus = append(skus, line.SkuID)
			}
		}
		infos, err := readSkuInfos(ctx, q, sortedUnique(skus))
		if err != nil {
			return err
		}
		if err := checkNewLots(in, byID, infos); err != nil {
			return err
		}
		for i, ln := range in {
			lineID := ln.LineID
			if lineID == nil {
				f := fmt.Sprintf("lines.%d.sku_id", i)
				switch {
				case !skuInCount[*ln.SkuID]:
					return errInvalid(map[string]string{f: "not_in_count"})
				case infos[*ln.SkuID].TrackLot:
					return errInvalid(map[string]string{f: "lot_tracked"})
				}
				newID := ids.New()
				if err := q.QueryRowxContext(ctx, `
					INSERT INTO stock_count_lines (id, tenant_id, stock_count_id, sku_id, color_id, lot_id, system_qty)
					VALUES ($1, $2, $3, $4, $5, NULL, 0)
					ON CONFLICT ON CONSTRAINT ux_stock_count_lines__stock_count_id_sku_id_color_id_lot_id
					DO UPDATE SET system_qty = stock_count_lines.system_qty
					RETURNING id`, newID, actor.TenantID, id, *ln.SkuID, ln.ColorID).Scan(&newID); err != nil {
					return fmt.Errorf("inventory: add stock count line: %w", err)
				}
				lineID = &newID
			} else if _, ok := byID[*lineID]; !ok {
				return errInvalid(map[string]string{fmt.Sprintf("lines.%d.line_id", i): "not_in_count"})
			}
			if _, err := q.ExecContext(ctx, `UPDATE stock_count_lines SET counted_qty = $2, reason_id = $3, variance_qty = NULL,
				new_lot_no = $4, new_expiry_date = $5 WHERE id = $1`, *lineID, ln.CountedQty, ln.ReasonID, ln.lotNo(),
				ln.ExpiryDate); err != nil {
				return fmt.Errorf("inventory: record stock count line: %w", err)
			}
		}
		if err := bumpCount(ctx, q, id, s.clock.Now(), nil); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{Action: "stock_count.record", EntityType: DocStockCount, EntityID: id,
			DocNo: c.DocNo, After: map[string]any{"lines": len(in)}}); err != nil {
			return err
		}
		out, err = readCountDetail(ctx, q, id)
		return err
	})
	return out, err
}

// checkNewLots verifies the new lot of the lines that describe one: only a line without a lot
// opens a lot, and it records what its SKU tracks only.
func checkNewLots(in []CountLineInput, byID map[uuid.UUID]StockCountLine, infos map[uuid.UUID]skuInfo) error {
	fields := map[string]string{}
	for i, ln := range in {
		lotNo := ln.lotNo()
		if lotNo == nil && ln.ExpiryDate == nil {
			continue
		}
		skuID := ln.SkuID
		if ln.LineID != nil {
			line, ok := byID[*ln.LineID]
			if !ok {
				continue // reported as not_in_count
			}
			if line.LotID != nil {
				if lotNo != nil {
					fields[fmt.Sprintf("lines.%d.lot_no", i)] = "not_allowed"
				}
				if ln.ExpiryDate != nil {
					fields[fmt.Sprintf("lines.%d.expiry_date", i)] = "not_allowed"
				}
				continue
			}
			skuID = &line.SkuID
		}
		info, ok := infos[*skuID]
		if !ok {
			continue // reported as not_in_count
		}
		if lotNo != nil && !info.TrackLot {
			fields[fmt.Sprintf("lines.%d.lot_no", i)] = "not_tracked"
		}
		if ln.ExpiryDate != nil && !info.TrackExpiry {
			fields[fmt.Sprintf("lines.%d.expiry_date", i)] = "not_tracked"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// gainLot is the lot a count gain on a line without a lot goes back to: none (a new lot) when the
// counter recorded the new lot's number or expiry, else the latest lot of the SKU and color, if
// any.
func gainLot(ctx context.Context, q db.Querier, ln StockCountLine) (*uuid.UUID, error) {
	if ln.NewLotNo != nil || ln.NewExpiryDate != nil {
		return nil, nil
	}
	return latestLot(ctx, q, ln.SkuID, ln.ColorID)
}

// checkGainLots verifies that every gain opening a new lot has the lot number of a SKU tracking
// lots and the expiry of a SKU tracking expiry; the error names the lines by id, so the counter can
// record them and finish again.
func checkGainLots(ctx context.Context, q db.Querier, lines []StockCountLine, infos map[uuid.UUID]skuInfo) error {
	fields := map[string]string{}
	for _, ln := range lines {
		info := infos[ln.SkuID]
		if ln.LotID != nil || ln.VarianceQty == nil || !ln.VarianceQty.IsPositive() || (!info.TrackLot && !info.TrackExpiry) {
			continue
		}
		lot, err := gainLot(ctx, q, ln)
		if err != nil {
			return err
		}
		if lot != nil {
			continue
		}
		if info.TrackLot && ln.NewLotNo == nil {
			fields["lines."+ln.ID.String()+".lot_no"] = "required"
		}
		if info.TrackExpiry && ln.NewExpiryDate == nil {
			fields["lines."+ln.ID.String()+".expiry_date"] = "required"
		}
	}
	if len(fields) > 0 {
		return apperr.NewValidation(CodeCountLotRequired, "Record the lot number and expiry of the stock found on these lines", fields)
	}
	return nil
}

// checkCountReasons verifies that the reasons of the lines are active count_variance reasons.
func checkCountReasons(ctx context.Context, q db.Querier, in []CountLineInput) error {
	for i, ln := range in {
		if _, err := checkReason(ctx, q, ln.ReasonID, []string{"count_variance"}, fmt.Sprintf("lines.%d.reason_id", i)); err != nil {
			return err
		}
	}
	return nil
}

// lockCount locks a count and checks its version.
func lockCount(ctx context.Context, q db.Querier, id uuid.UUID, version int) (StockCount, error) {
	c, err := readCount(ctx, q, id, true)
	if err != nil {
		return StockCount{}, err
	}
	if c.Version != version {
		return StockCount{}, apperr.NewPreconditionFailed("The count changed since it was read")
	}
	return c, nil
}

// bumpCount sets the status (when not nil) and moves the version on.
func bumpCount(ctx context.Context, q db.Querier, id uuid.UUID, now time.Time, set func() (string, []any)) error {
	query, args := `UPDATE stock_counts SET updated_at = $2, version = version + 1`, []any{id, now}
	if set != nil {
		extra, more := set()
		query += ", " + extra
		args = append(args, more...)
	}
	if _, err := q.ExecContext(ctx, query+` WHERE id = $1`, args...); err != nil {
		return fmt.Errorf("inventory: update stock count: %w", err)
	}
	return nil
}

// FinishCount closes counting. Every line must be counted; moved_since_start_qty is the stock
// that moved since the snapshot, read as the change of the line's normal-lot balance (the sum of
// every move committed after the snapshot, so a posting racing the start is never lost), and
// variance_qty = counted − system − moved.
func (s *Service) FinishCount(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (CountDetail, error) {
	var out CountDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		c, err := lockCount(ctx, q, id, version)
		if err != nil {
			return err
		}
		if c.Status != CountCounting {
			return apperr.NewConflict(CodeCountNotCounting, "The count is not open for counting")
		}
		var uncounted int
		if err := q.QueryRowxContext(ctx, `SELECT COUNT(*) FROM stock_count_lines WHERE stock_count_id = $1 AND counted_qty IS NULL`,
			id).Scan(&uncounted); err != nil {
			return fmt.Errorf("inventory: check stock count lines: %w", err)
		}
		if uncounted > 0 {
			return apperr.NewValidation(CodeCountIncomplete, fmt.Sprintf("%d lines are not counted yet", uncounted),
				map[string]string{"lines": "not_counted"})
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE stock_count_lines cl SET moved_since_start_qty = m.moved,
			  variance_qty = cl.counted_qty - cl.system_qty - m.moved
			FROM (
			  SELECT x.id, COALESCE((
			    SELECT SUM(b.qty) FROM lot_balances b JOIN lots l ON l.id = b.lot_id
			    WHERE NOT l.is_quarantine AND CASE WHEN x.lot_id IS NOT NULL THEN b.lot_id = x.lot_id
			      ELSE b.sku_id = x.sku_id AND b.color_id IS NOT DISTINCT FROM x.color_id END
			  ), 0) - x.system_qty AS moved
			  FROM stock_count_lines x WHERE x.stock_count_id = $1
			) m
			WHERE cl.id = m.id`, id); err != nil {
			return fmt.Errorf("inventory: compute stock count variance: %w", err)
		}
		lines, err := readCountLines(ctx, q, id)
		if err != nil {
			return err
		}
		skus := make([]uuid.UUID, len(lines))
		for i, ln := range lines {
			skus[i] = ln.SkuID
		}
		infos, err := readSkuInfos(ctx, q, sortedUnique(skus))
		if err != nil {
			return err
		}
		if err := checkGainLots(ctx, q, lines, infos); err != nil {
			return err
		}
		now := s.clock.Now()
		if err := bumpCount(ctx, q, id, now, func() (string, []any) {
			return `status = $3, counted_at = $2`, []any{CountPendingPost}
		}); err != nil {
			return err
		}
		if out, err = readCountDetail(ctx, q, id); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{Action: "stock_count.finish", EntityType: DocStockCount, EntityID: id,
			DocNo: c.DocNo, Before: map[string]any{"status": c.Status}, After: map[string]any{"status": CountPendingPost,
				"variance": countVariance(out.Lines)}})
	})
	return out, err
}

// countVariance sums the variances of a sheet.
func countVariance(lines []CountLineView) map[string]string {
	gain, loss := money.ZeroQty, money.ZeroQty
	for _, ln := range lines {
		switch {
		case ln.VarianceQty == nil:
		case ln.VarianceQty.IsPositive():
			gain = gain.Add(*ln.VarianceQty)
		case ln.VarianceQty.IsNegative():
			loss = loss.Add(ln.VarianceQty.Neg())
		}
	}
	return map[string]string{"gain": gain.String(), "loss": loss.String()}
}

// RecountCount reopens a finished count: the listed lines lose their counted quantity and every
// line its variance, so the next finish computes them again.
func (s *Service) RecountCount(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, lineIDs []uuid.UUID) (CountDetail, error) {
	switch {
	case len(lineIDs) == 0:
		return CountDetail{}, errInvalid(map[string]string{"line_ids": "required"})
	case len(lineIDs) > maxCountLineEdits:
		return CountDetail{}, errInvalid(map[string]string{"line_ids": "too_many"})
	}
	var out CountDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		c, err := lockCount(ctx, q, id, version)
		if err != nil {
			return err
		}
		if c.Status != CountPendingPost {
			return apperr.NewConflict(CodeCountNotFinished, "Only a finished count can be recounted")
		}
		lineIDs = sortedUnique(lineIDs)
		// The variance goes first: a line may not keep a variance without a counted quantity.
		if _, err := q.ExecContext(ctx, `UPDATE stock_count_lines SET variance_qty = NULL WHERE stock_count_id = $1`, id); err != nil {
			return fmt.Errorf("inventory: clear stock count variance: %w", err)
		}
		res, err := q.ExecContext(ctx, `UPDATE stock_count_lines SET counted_qty = NULL WHERE stock_count_id = $1 AND id = ANY($2)`,
			id, lineIDs)
		if err != nil {
			return fmt.Errorf("inventory: reopen stock count lines: %w", err)
		}
		if n, _ := res.RowsAffected(); n != int64(len(lineIDs)) {
			return errInvalid(map[string]string{"line_ids": "not_in_count"})
		}
		if err := bumpCount(ctx, q, id, s.clock.Now(), func() (string, []any) {
			return `status = $3, counted_at = NULL`, []any{CountCounting}
		}); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{Action: "stock_count.recount", EntityType: DocStockCount, EntityID: id,
			DocNo: c.DocNo, Before: map[string]any{"status": c.Status}, After: map[string]any{"status": CountCounting,
				"line_ids": lineIDs}}); err != nil {
			return err
		}
		out, err = readCountDetail(ctx, q, id)
		return err
	})
	return out, err
}

// CountPostInput posts a finished count at Version. ExceptionConfirmationID and PayloadHash
// bind the owner's count_variance confirmation (doc_type stock_count, doc_id the count, doc
// version Version) when a line has a variance.
type CountPostInput struct {
	Version                 int
	ExceptionConfirmationID *uuid.UUID
	PayloadHash             []byte
}

// PostCount posts a finished count: the non-zero variances become a DC of kind count (gains at
// the average cost, losses FEFO from the line's color or lot) and the count is closed.
func (s *Service) PostCount(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in CountPostInput) (posting.Response, error) {
	today, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var lines []StockCountLine
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if _, err := readCount(ctx, q, id, false); err != nil {
			return err
		}
		lines, err = readCountLines(ctx, q, id)
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	cmd := countPost{s: s, date: today, id: id, in: in, key: ScopeCountPost + ":" + key.Value}
	for _, ln := range lines {
		cmd.skus = append(cmd.skus, ln.SkuID)
		if ln.VarianceQty != nil && !ln.VarianceQty.IsZero() {
			cmd.variance = true
		}
	}
	return s.posting.Post(ctx, actor, key, cmd)
}

type countPost struct {
	s        *Service
	date     time.Time
	id       uuid.UUID
	in       CountPostInput
	key      string
	skus     []uuid.UUID
	variance bool
}

func (c countPost) Scope() string           { return ScopeCountPost }
func (c countPost) BusinessDate() time.Time { return c.date }

// DocPrefix issues a DC number only when the count has a variance to book.
func (c countPost) DocPrefix() string {
	if c.variance {
		return docseq.PrefixStockAdjustment
	}
	return ""
}

func (c countPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	return set
}

func (c countPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	count, err := readCount(ctx, tx, c.id, true)
	if err != nil {
		return posting.Result{}, err
	}
	switch {
	case count.Status == CountPosted:
		return posting.Result{}, apperr.NewConflict(CodeCountPosted, "The count was already posted")
	case count.Status != CountPendingPost:
		return posting.Result{}, apperr.NewConflict(CodeCountNotFinished, "Finish the count before posting it")
	case count.Version != c.in.Version:
		return posting.Result{}, apperr.NewPreconditionFailed("The count changed since it was read")
	}
	lines, err := readCountLines(ctx, tx, c.id)
	if err != nil {
		return posting.Result{}, err
	}
	var changed []StockCountLine
	for _, ln := range lines {
		if ln.VarianceQty != nil && !ln.VarianceQty.IsZero() {
			changed = append(changed, ln)
		}
	}
	if (len(changed) > 0) != c.variance {
		// The sheet changed between the read and the lock; the version check above makes this
		// unreachable short of a direct database edit.
		return posting.Result{}, apperr.NewPreconditionFailed("The count changed since it was read")
	}
	var adjID *uuid.UUID
	if len(changed) > 0 {
		id := ids.New()
		adjID = &id
		var confirmation uuid.UUID
		if c.in.ExceptionConfirmationID != nil {
			confirmation = *c.in.ExceptionConfirmationID
		}
		if err := c.s.exceptions.Consume(ctx, tx, tx.TenantID(), exception.Expectation{ConfirmationID: confirmation,
			Kind: exception.CountVariance, DocType: DocStockCount, DocID: c.id, DocVersion: count.Version,
			PayloadHash: c.in.PayloadHash, UsedByDocType: DocStockAdjustment, UsedByDocID: id}); err != nil {
			return posting.Result{}, err
		}
		if err := c.book(ctx, tx, count, id, changed); err != nil {
			return posting.Result{}, err
		}
	}
	now := tx.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE stock_counts SET status = $2, posted_at = $3, posted_by = $4, updated_at = $3,
		version = version + 1 WHERE id = $1`, c.id, CountPosted, now, nullUUID(tx.Actor().ID)); err != nil {
		return posting.Result{}, fmt.Errorf("inventory: close stock count: %w", err)
	}
	detail, err := readCountDetail(ctx, tx, c.id)
	if err != nil {
		return posting.Result{}, err
	}
	after := map[string]any{"status": CountPosted, "variance": countVariance(detail.Lines)}
	if adjID != nil {
		after["stock_adjustment_id"], after["stock_adjustment_no"] = *adjID, tx.DocNo()
	}
	tx.Audit(audit.Event{Action: "stock_count.post", EntityType: DocStockCount, EntityID: c.id, DocNo: count.DocNo,
		Before: map[string]any{"status": count.Status}, After: after})
	return posting.Result{Status: http.StatusOK, Body: toCountDTO(detail)}, nil
}

// book posts the variances as a DC of kind count.
func (c countPost) book(ctx context.Context, tx posting.Tx, count StockCount, adjID uuid.UUID, changed []StockCountLine) error {
	skuIDs := make([]uuid.UUID, len(changed))
	for i, ln := range changed {
		skuIDs[i] = ln.SkuID
	}
	infos, err := readSkuInfos(ctx, tx, sortedUnique(skuIDs))
	if err != nil {
		return err
	}
	if err := checkGainLots(ctx, tx, changed, infos); err != nil {
		return err
	}
	one := money.MustFactor("1")
	var gains, losses []StockAdjustmentLine
	var gainLines []StockCountLine
	for _, ln := range changed {
		info := infos[ln.SkuID]
		adj := StockAdjustmentLine{ID: ids.New(), StockAdjustmentID: adjID, SkuID: ln.SkuID, SkuCodeSnap: info.Code,
			NameSnap: info.DisplayName, VariantLabelSnap: info.VariantLabel, UnitID: info.BaseUnitID, UnitNameSnap: info.BaseUnitName,
			UnitFactorSnap: one, ColorID: ln.ColorID, SpecText: info.SpecText, LotID: ln.LotID}
		if ln.VarianceQty.IsPositive() {
			adj.Qty, adj.BaseQty, adj.Direction = *ln.VarianceQty, *ln.VarianceQty, 1
			gains = append(gains, adj)
			gainLines = append(gainLines, ln)
		} else {
			adj.Qty, adj.BaseQty, adj.Direction = ln.VarianceQty.Neg(), ln.VarianceQty.Neg(), -1
			losses = append(losses, adj)
		}
	}
	pst := PostingOf(tx, DocStockAdjustment, adjID)
	if len(gains) > 0 {
		in := make([]ReceiveLine, len(gains))
		for i, ln := range gains {
			src := gainLines[i]
			lot := ln.LotID
			if lot == nil {
				if lot, err = gainLot(ctx, tx, src); err != nil {
					return err
				}
			}
			in[i] = ReceiveLine{DocLineID: &gains[i].ID, SkuID: ln.SkuID, Qty: ln.Qty, MoveType: MoveCountGain, AtAverageCost: true,
				ColorID: ln.ColorID, LotID: lot}
			if lot == nil {
				in[i].ExpiryDate = src.NewExpiryDate
				if src.NewLotNo != nil {
					in[i].LotNo = *src.NewLotNo
				}
			}
		}
		moves, err := c.s.ledger.Receive(ctx, tx, pst, in)
		if err != nil {
			return err
		}
		fillLots(gains, moves, sideReceive)
	}
	if len(losses) > 0 {
		in := make([]IssueLine, len(losses))
		for i, ln := range losses {
			in[i] = IssueLine{DocLineID: &losses[i].ID, SkuID: ln.SkuID, Qty: ln.Qty, MoveType: MoveCountLoss, ColorID: ln.ColorID,
				Uncolored: ln.ColorID == nil && ln.LotID == nil, LotID: ln.LotID}
		}
		moves, err := c.s.ledger.Issue(ctx, tx, pst, in)
		if err != nil {
			return err
		}
		fillLots(losses, moves, sideIssue)
	}
	adj := StockAdjustment{ID: adjID, WarehouseID: count.WarehouseID, DocNo: tx.DocNo(), Kind: KindCount, StockCountID: &count.ID,
		BusinessDate: c.date, Note: count.Note, Status: AdjustmentPosted, IdempotencyKey: c.key, PostedAt: tx.Now(),
		PostedBy: tx.Actor().ID, CreatedAt: tx.Now()}
	return insertAdjustment(ctx, tx, tx.TenantID(), adj, append(gains, losses...))
}

// latestLot is the most recent normal lot of a SKU and color, where a count gain goes back to;
// nil when the SKU never held that color (the ledger then opens a lot).
func latestLot(ctx context.Context, q db.Querier, skuID uuid.UUID, colorID *uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `SELECT id FROM lots WHERE sku_id = $1 AND color_id IS NOT DISTINCT FROM $2 AND NOT is_quarantine
		ORDER BY received_on DESC, created_at DESC, id DESC LIMIT 1`, skuID, colorID).Scan(&id)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read latest lot: %w", err)
	}
	return &id, nil
}

// nullUUID maps the system actor (uuid.Nil) to NULL.
func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
