package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Outbox topics of the inventory module.
const (
	TopicStockChanged    = "stock.changed"
	TopicStockLow        = "stock.low"
	TopicStockOver       = "stock.over"
	TopicLotExpiring     = "lot.expiring"
	TopicLotExpired      = "lot.expired"
	TopicRecostPreviewed = "cost_recalculation.previewed"
)

// signalWindow bounds the outbox rows read to dedupe signals: a dedupe key carries its business
// date, so only the rows of the last two days can hold it.
const signalWindow = 48 * time.Hour

// StockSignalDetector raises the low-stock (available < min_stock), overstock (on_hand >
// max_stock) and lot expiry signals on the outbox. It only notifies: it never creates purchase
// orders. Every signal carries a dedupe key "<topic>:<subject>:<business_date>", and a key is
// published at most once, so the posting-time checks and the daily scan never repeat a signal
// on the same day.
type StockSignalDetector struct{}

// stockSignal is the state of one SKU against its thresholds.
type stockSignal struct {
	low, over bool
}

func signalOf(s SkuStock, minStock money.Qty, maxStock *money.Qty) stockSignal {
	return stockSignal{
		low:  minStock.IsPositive() && s.Available().Cmp(minStock) < 0,
		over: maxStock != nil && s.OnHand.Cmp(*maxStock) > 0,
	}
}

// Check publishes the signals a change crosses into: a SKU that was not low (over) before and is
// now. before holds the projections as they were locked, after the projections written.
func (StockSignalDetector) Check(ctx context.Context, tx Tx, now, today time.Time, before map[uuid.UUID]SkuStock,
	after []SkuStock, infos map[uuid.UUID]skuInfo) error {
	var events []signal
	for _, s := range after {
		info, ok := infos[s.SkuID]
		if !ok || info.Status != skuActive {
			continue
		}
		was, is := signalOf(before[s.SkuID], info.MinStock, info.MaxStock), signalOf(s, info.MinStock, info.MaxStock)
		if is.low && !was.low {
			events = append(events, lowSignal(s, info.Code, info.DisplayName, info.MinStock, today))
		}
		if is.over && !was.over {
			events = append(events, overSignal(s, info.Code, info.DisplayName, *info.MaxStock, today))
		}
	}
	_, err := publishOnce(ctx, tx, now, events)
	return err
}

func lowSignal(s SkuStock, code, name string, minStock money.Qty, day time.Time) signal {
	return newSignal(TopicStockLow, s.SkuID, day, map[string]any{
		"sku_id": s.SkuID, "sku_code": code, "sku_name": name,
		"available": s.Available(), "sellable": s.Sellable, "reserved": s.Reserved, "min_stock": minStock,
	})
}

func overSignal(s SkuStock, code, name string, maxStock money.Qty, day time.Time) signal {
	return newSignal(TopicStockOver, s.SkuID, day, map[string]any{
		"sku_id": s.SkuID, "sku_code": code, "sku_name": name, "on_hand": s.OnHand, "max_stock": maxStock,
	})
}

// signalRow is a SKU at or past a threshold, as the daily scan reads it.
type signalRow struct {
	SkuStock
	Code     string     `db:"code"`
	Name     string     `db:"display_name"`
	MinStock money.Qty  `db:"min_stock"`
	MaxStock *money.Qty `db:"max_stock"`
}

// expiryRow is a normal lot holding stock that expires within the warning window.
type expiryRow struct {
	LotID      uuid.UUID  `db:"lot_id"`
	LotNo      string     `db:"lot_no"`
	SkuID      uuid.UUID  `db:"sku_id"`
	Code       string     `db:"code"`
	Name       string     `db:"display_name"`
	ColorID    *uuid.UUID `db:"color_id"`
	ColorCode  *string    `db:"color_code"`
	ColorName  *string    `db:"color_name"`
	ExpiryDate time.Time  `db:"expiry_date"`
	Qty        money.Qty  `db:"qty"`
}

// Detect is the daily scan of the tenant in scope: every active SKU below its minimum or above
// its maximum, every normal lot with stock expiring within the tenant's warning days
// (lot.expiring) or already expired (lot.expired), with the lot's color. It returns how many
// signals it published.
func (StockSignalDetector) Detect(ctx context.Context, tx Tx, now, today time.Time) (int, error) {
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return 0, err
	}
	var skus []signalRow
	if err := sqlx.SelectContext(ctx, tx, &skus, `
		SELECT `+prefixed("ss", skuStockColumns)+`, s.code, s.display_name, s.min_stock, s.max_stock
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		WHERE s.status = 'active'
		  AND ((s.min_stock > 0 AND ss.sellable - ss.reserved < s.min_stock) OR (s.max_stock IS NOT NULL AND ss.on_hand > s.max_stock))
		ORDER BY ss.sku_id`); err != nil {
		return 0, fmt.Errorf("inventory: scan stock thresholds: %w", err)
	}
	var events []signal
	for _, r := range skus {
		sig := signalOf(r.SkuStock, r.MinStock, r.MaxStock)
		if sig.low {
			events = append(events, lowSignal(r.SkuStock, r.Code, r.Name, r.MinStock, today))
		}
		if sig.over {
			events = append(events, overSignal(r.SkuStock, r.Code, r.Name, *r.MaxStock, today))
		}
	}
	var lots []expiryRow
	if err := sqlx.SelectContext(ctx, tx, &lots, `
		SELECT l.id AS lot_id, l.lot_no, l.sku_id, s.code, s.display_name, l.color_id, c.code AS color_code, c.name AS color_name,
		  l.expiry_date, b.qty
		FROM lots l JOIN lot_balances b ON b.lot_id = l.id JOIN skus s ON s.id = l.sku_id LEFT JOIN colors c ON c.id = l.color_id
		WHERE b.qty > 0 AND NOT l.is_quarantine AND l.expiry_date IS NOT NULL AND l.expiry_date <= $1
		ORDER BY l.expiry_date, l.id`, today.AddDate(0, 0, settings.ExpiryWarningDays)); err != nil {
		return 0, fmt.Errorf("inventory: scan expiring lots: %w", err)
	}
	for _, r := range lots {
		topic := TopicLotExpiring
		if r.ExpiryDate.Before(today) {
			topic = TopicLotExpired
		}
		events = append(events, newSignal(topic, r.LotID, today, map[string]any{
			"lot_id": r.LotID, "lot_no": r.LotNo, "sku_id": r.SkuID, "sku_code": r.Code, "sku_name": r.Name,
			"color_id": r.ColorID, "color_code": r.ColorCode, "color_name": r.ColorName,
			"expiry_date": r.ExpiryDate.Format(time.DateOnly), "days_left": int(r.ExpiryDate.Sub(today).Hours() / 24), "qty": r.Qty,
		}))
	}
	return publishOnce(ctx, tx, now, events)
}

// signal is an outbox event with its dedupe key.
type signal struct {
	key string
	evt outbox.Event
}

func newSignal(topic string, subject uuid.UUID, day time.Time, payload map[string]any) signal {
	key := topic + ":" + subject.String() + ":" + day.Format(time.DateOnly)
	payload["dedupe_key"], payload["business_date"] = key, day.Format(time.DateOnly)
	return signal{key: key, evt: outbox.Event{Topic: topic, Payload: payload}}
}

// publishOnce publishes the signals whose dedupe key the outbox does not hold yet and returns
// how many it published.
func publishOnce(ctx context.Context, tx Tx, now time.Time, signals []signal) (int, error) {
	if len(signals) == 0 {
		return 0, nil
	}
	keys := make([]string, len(signals))
	for i, s := range signals {
		keys[i] = s.key
	}
	var seen []string
	if err := sqlx.SelectContext(ctx, tx, &seen, `
		SELECT payload->>'dedupe_key' FROM outbox_events
		WHERE created_at >= $1 AND topic IN ('stock.low', 'stock.over', 'lot.expiring', 'lot.expired')
		  AND payload->>'dedupe_key' = ANY($2)`, now.Add(-signalWindow), keys); err != nil {
		return 0, fmt.Errorf("inventory: read published signals: %w", err)
	}
	published := make(map[string]bool, len(seen))
	for _, k := range seen {
		published[k] = true
	}
	n := 0
	for _, s := range signals {
		if !published[s.key] {
			published[s.key] = true
			tx.Publish(s.evt)
			n++
		}
	}
	return n, nil
}
