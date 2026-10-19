package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// StockThresholds raises below_min_stock for an active SKU whose available stock (sellable −
// reserved) is under its minimum, and surplus_stock for one whose on-hand exceeds its maximum.
type StockThresholds struct{}

// Kinds implements Producer.
func (StockThresholds) Kinds() []string { return []string{KindBelowMinStock, KindSurplusStock} }

// Sweep implements Producer.
func (StockThresholds) Sweep(ctx context.Context, q db.Querier, _ SweepTime) ([]Notice, error) {
	return stockNotices(ctx, q, nil)
}

type thresholdRow struct {
	SkuID     uuid.UUID  `db:"sku_id"`
	Code      string     `db:"code"`
	Name      string     `db:"display_name"`
	OnHand    money.Qty  `db:"on_hand"`
	Available money.Qty  `db:"available"`
	MinStock  money.Qty  `db:"min_stock"`
	MaxStock  *money.Qty `db:"max_stock"`
	Low       bool       `db:"low"`
	Over      bool       `db:"over"`
}

// stockNotices returns the threshold notices of the SKUs in skuIDs, or of every SKU when skuIDs
// is nil.
func stockNotices(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) ([]Notice, error) {
	var rows []thresholdRow
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT ss.sku_id, s.code, s.display_name, ss.on_hand, ss.sellable - ss.reserved AS available, s.min_stock, s.max_stock,
		  s.min_stock > 0 AND ss.sellable - ss.reserved < s.min_stock AS low,
		  s.max_stock IS NOT NULL AND ss.on_hand > s.max_stock AS over
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		WHERE s.status = 'active' AND ($1::uuid[] IS NULL OR ss.sku_id = ANY($1))
		  AND ((s.min_stock > 0 AND ss.sellable - ss.reserved < s.min_stock) OR (s.max_stock IS NOT NULL AND ss.on_hand > s.max_stock))
		ORDER BY ss.sku_id`, skuIDs); err != nil {
		return nil, fmt.Errorf("notification: scan stock thresholds: %w", err)
	}
	var out []Notice
	for _, r := range rows {
		path := ptr("/inventory/stock-card?sku=" + r.SkuID.String())
		if r.Low {
			out = append(out, Notice{
				Kind: KindBelowMinStock, DedupeKey: key(KindBelowMinStock, r.SkuID), Severity: SeverityWarn,
				Title:      fmt.Sprintf("%s dưới tồn tối thiểu", r.Code),
				Body:       ptr(fmt.Sprintf("%s: còn bán được %s, tối thiểu %s.", r.Name, r.Available, r.MinStock)),
				EntityType: ptr("sku"), EntityID: ptr(r.SkuID), ActionPath: path,
			})
		}
		if r.Over && r.MaxStock != nil {
			out = append(out, Notice{
				Kind: KindSurplusStock, DedupeKey: key(KindSurplusStock, r.SkuID), Severity: SeverityInfo,
				Title:      fmt.Sprintf("%s vượt tồn tối đa", r.Code),
				Body:       ptr(fmt.Sprintf("%s: tồn %s, tối đa %s.", r.Name, r.OnHand, *r.MaxStock)),
				EntityType: ptr("sku"), EntityID: ptr(r.SkuID), ActionPath: path,
			})
		}
	}
	return out, nil
}

// LotExpiry raises lot_expiring for a normal lot holding stock that expires within the tenant's
// warning days, and lot_expired once it is past its expiry date. Both name the lot's color.
type LotExpiry struct{}

// Kinds implements Producer.
func (LotExpiry) Kinds() []string { return []string{KindLotExpiring, KindLotExpired} }

// defaultExpiryWarningDays applies when the tenant has no settings row.
const defaultExpiryWarningDays = 30

type lotRow struct {
	LotID      uuid.UUID  `db:"lot_id"`
	LotNo      string     `db:"lot_no"`
	SkuID      uuid.UUID  `db:"sku_id"`
	Code       string     `db:"code"`
	Name       string     `db:"display_name"`
	ColorID    *uuid.UUID `db:"color_id"`
	ColorName  *string    `db:"color_name"`
	ExpiryDate time.Time  `db:"expiry_date"`
	Qty        money.Qty  `db:"qty"`
}

// Sweep implements Producer.
func (LotExpiry) Sweep(ctx context.Context, q db.Querier, at SweepTime) ([]Notice, error) {
	return lotNotices(ctx, q, at, nil, nil)
}

// skuLotNotices returns the expiry notices of a SKU's lots and the dedupe keys of the lot notices
// it holds open, so a lot sold out or written off loses its notice when the SKU's stock changes.
func skuLotNotices(ctx context.Context, q db.Querier, at SweepTime, skuID uuid.UUID) ([]string, []Notice, error) {
	var open []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &open, `
		SELECT DISTINCT n.entity_id FROM notifications n JOIN lots l ON l.id = n.entity_id
		WHERE n.kind IN ($2, $3) AND n.resolved_by IS DISTINCT FROM 'system' AND l.sku_id = $1`,
		skuID, KindLotExpiring, KindLotExpired); err != nil {
		return nil, nil, fmt.Errorf("notification: read lot notices of a sku: %w", err)
	}
	keys := make([]string, 0, 2*len(open))
	for _, id := range open {
		keys = append(keys, key(KindLotExpiring, id), key(KindLotExpired, id))
	}
	notices, err := lotNotices(ctx, q, at, nil, &skuID)
	return keys, notices, err
}

// lotNotices returns the expiry notices of lotID, else of skuID's lots, else of every lot.
func lotNotices(ctx context.Context, q db.Querier, at SweepTime, lotID, skuID *uuid.UUID) ([]Notice, error) {
	warn := defaultExpiryWarningDays
	if err := q.QueryRowxContext(ctx, `SELECT expiry_warning_days FROM tenant_settings LIMIT 1`).Scan(&warn); err != nil && !db.IsNoRows(err) {
		return nil, fmt.Errorf("notification: read expiry warning days: %w", err)
	}
	var rows []lotRow
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT l.id AS lot_id, l.lot_no, l.sku_id, s.code, s.display_name, l.color_id, c.name AS color_name, l.expiry_date, b.qty
		FROM lots l JOIN lot_balances b ON b.lot_id = l.id JOIN skus s ON s.id = l.sku_id LEFT JOIN colors c ON c.id = l.color_id
		WHERE b.qty > 0 AND NOT l.is_quarantine AND l.expiry_date IS NOT NULL AND l.expiry_date <= $1
		  AND ($2::uuid IS NULL OR l.id = $2) AND ($3::uuid IS NULL OR l.sku_id = $3)
		ORDER BY l.expiry_date, l.id`, at.Today.AddDate(0, 0, warn), lotID, skuID); err != nil {
		return nil, fmt.Errorf("notification: scan expiring lots: %w", err)
	}
	out := make([]Notice, 0, len(rows))
	for _, r := range rows {
		color := "Chưa gán màu"
		if r.ColorName != nil {
			color = *r.ColorName
		}
		lot := r.LotNo
		if lot == "" {
			lot = "mặc định"
		}
		n := Notice{
			EntityType: ptr("lot"), EntityID: ptr(r.LotID), ColorID: r.ColorID,
			ActionPath: ptr("/inventory/lots"),
		}
		if r.LotNo != "" {
			n.DocNo = ptr(r.LotNo)
		}
		days := int(r.ExpiryDate.Sub(at.Today).Hours() / 24)
		expiry := r.ExpiryDate.Format("02/01/2006")
		if days < 0 {
			n.Kind, n.Severity = KindLotExpired, SeverityBad
			n.Title = fmt.Sprintf("Lô %s %s (%s) đã hết hạn", lot, r.Code, color)
			n.Body = ptr(fmt.Sprintf("%s – màu %s: còn %s, hết hạn %s.", r.Name, color, r.Qty, expiry))
		} else {
			n.Kind, n.Severity = KindLotExpiring, SeverityWarn
			n.Title = fmt.Sprintf("Lô %s %s (%s) còn %d ngày hết hạn", lot, r.Code, color, days)
			n.Body = ptr(fmt.Sprintf("%s – màu %s: còn %s, hết hạn %s.", r.Name, color, r.Qty, expiry))
		}
		n.DedupeKey = key(n.Kind, r.LotID)
		out = append(out, n)
	}
	return out, nil
}
