package campaign

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// maxDemandDays bounds the range of one demand query.
const maxDemandDays = 366

// DemandQuery asks for the demand of a campaign between From and To; nil bounds default to the
// campaign period. After and Limit page the SKUs by code.
type DemandQuery struct {
	From, To *time.Time
	After    *string
	Limit    int
}

// DemandDay is one day of a SKU's projection: the demand and expected receipts dated that day and
// the projected sellable stock and shortage at the end of the day.
type DemandDay struct {
	Date      time.Time
	Demand    money.Qty
	Incoming  money.Qty
	Projected money.Qty
	Shortage  money.Qty
}

// DemandRow is the projection of one SKU over the range. Available is today's sellable stock;
// expected receipts are never added to it, only to Projected. Backlog is demand dated before the
// range, counted on its first day; UndatedIncoming is pending purchase quantity without an
// expected date, left out of the projection.
type DemandRow struct {
	SkuID           uuid.UUID
	Code            string
	Name            string
	Available       money.Qty
	Backlog         money.Qty
	UndatedIncoming money.Qty
	FirstShortageOn *time.Time
	MaxShortage     money.Qty
	Days            []DemandDay
}

// Demand is the demand tracking of a campaign. Days of a row list only the days with demand or
// expected receipts.
type Demand struct {
	CampaignID uuid.UUID
	From, To   time.Time
	Rows       []DemandRow
}

type demandEvent struct {
	SkuID uuid.UUID `db:"sku_id"`
	// On is nil for undated quantities.
	On  *time.Time `db:"on_date"`
	Qty money.Qty  `db:"qty"`
}

// Demand projects, per SKU and day, confirmed undelivered invoice lines and active reservations
// that are not invoice lines against sellable stock and confirmed purchase orders still pending
// by their expected date.
func (s *Service) Demand(ctx context.Context, scope Scope, id uuid.UUID, dq DemandQuery) (Demand, error) {
	var out Demand
	err := s.read(ctx, scope, func(q db.Querier) error {
		c, err := loadCampaign(ctx, q, id, false)
		if err != nil {
			return err
		}
		from, to := c.StartsOn, c.EndsOn
		if dq.From != nil {
			from = *dq.From
		}
		if dq.To != nil {
			to = *dq.To
		}
		switch {
		case to.Before(from):
			return errInvalid(map[string]string{"to": "before_from"})
		case to.Sub(from) > maxDemandDays*24*time.Hour:
			return errInvalid(map[string]string{"to": "range_too_long"})
		}
		out = Demand{CampaignID: id, From: from, To: to}
		skus, err := demandSKUs(ctx, q, to, dq.After, dq.Limit)
		if err != nil {
			return err
		}
		if len(skus) == 0 {
			out.Rows = []DemandRow{}
			return nil
		}
		skuIDs := make([]uuid.UUID, len(skus))
		for i, r := range skus {
			skuIDs[i] = r.SkuID
		}
		demand, incoming, err := demandEvents(ctx, q, skuIDs, to)
		if err != nil {
			return err
		}
		out.Rows = project(skus, demand, incoming, from)
		return nil
	})
	return out, err
}

// demandSKUs are the SKUs with open demand or pending purchases up to `to`, by code.
func demandSKUs(ctx context.Context, q db.Querier, to time.Time, after *string, limit int) ([]DemandRow, error) {
	var rows []struct {
		SkuID     uuid.UUID `db:"sku_id"`
		Code      string    `db:"code"`
		Name      string    `db:"display_name"`
		Available money.Qty `db:"available"`
	}
	afterCode := ""
	if after != nil {
		afterCode = *after
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		WITH wanted AS (
			SELECT il.sku_id FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id
			WHERE i.status IN ('confirmed', 'partially_delivered') AND il.sku_id IS NOT NULL
				AND il.line_kind NOT IN ('mix_pack', 'combo') AND i.invoice_date <= $1
				AND il.base_qty - il.delivered_base_qty - il.cancelled_base_qty > 0
			UNION
			SELECT r.sku_id FROM reservations r
			WHERE r.status = 'active' AND r.source_type <> 'invoice_line' AND r.qty - r.consumed_qty - r.released_qty > 0
			UNION
			SELECT pl.sku_id FROM purchase_order_lines pl JOIN purchase_orders po ON po.id = pl.purchase_order_id
			WHERE po.status IN ('confirmed', 'partially_received') AND pl.base_qty - pl.received_base_qty > 0
				AND (po.expected_date IS NULL OR po.expected_date <= $1)
		)
		SELECT s.id AS sku_id, s.code, s.display_name,
			COALESCE((SELECT sum(st.sellable) FROM sku_stock st WHERE st.sku_id = s.id), 0) AS available
		FROM skus s
		WHERE s.id IN (SELECT sku_id FROM wanted) AND s.code > $2
		ORDER BY s.code
		LIMIT $3`, to, afterCode, limit); err != nil {
		return nil, fmt.Errorf("campaign: demand skus: %w", err)
	}
	out := make([]DemandRow, len(rows))
	for i, r := range rows {
		out[i] = DemandRow{SkuID: r.SkuID, Code: r.Code, Name: r.Name, Available: r.Available}
	}
	return out, nil
}

// demandEvents reads the dated demand and the expected receipts of skuIDs up to `to`. Active
// reservations that are not invoice lines are commitments today, held as undated demand.
func demandEvents(ctx context.Context, q db.Querier, skuIDs []uuid.UUID, to time.Time) (demand, incoming []demandEvent, err error) {
	if err = sqlx.SelectContext(ctx, q, &demand, `
		SELECT il.sku_id, i.invoice_date AS on_date,
			sum(il.base_qty - il.delivered_base_qty - il.cancelled_base_qty) AS qty
		FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id
		WHERE il.sku_id = ANY($1) AND i.status IN ('confirmed', 'partially_delivered')
			AND il.line_kind NOT IN ('mix_pack', 'combo') AND i.invoice_date <= $2
			AND il.base_qty - il.delivered_base_qty - il.cancelled_base_qty > 0
		GROUP BY il.sku_id, i.invoice_date
		UNION ALL
		SELECT r.sku_id, NULL::date AS on_date, sum(r.qty - r.consumed_qty - r.released_qty) AS qty
		FROM reservations r
		WHERE r.sku_id = ANY($1) AND r.status = 'active' AND r.source_type <> 'invoice_line'
			AND r.qty - r.consumed_qty - r.released_qty > 0
		GROUP BY r.sku_id`, skuIDs, to); err != nil {
		return nil, nil, fmt.Errorf("campaign: demand: %w", err)
	}
	if err = sqlx.SelectContext(ctx, q, &incoming, `
		SELECT pl.sku_id, po.expected_date AS on_date, sum(pl.base_qty - pl.received_base_qty) AS qty
		FROM purchase_order_lines pl JOIN purchase_orders po ON po.id = pl.purchase_order_id
		WHERE pl.sku_id = ANY($1) AND po.status IN ('confirmed', 'partially_received')
			AND pl.base_qty - pl.received_base_qty > 0 AND (po.expected_date IS NULL OR po.expected_date <= $2)
		GROUP BY pl.sku_id, po.expected_date`, skuIDs, to); err != nil {
		return nil, nil, fmt.Errorf("campaign: pending purchases: %w", err)
	}
	return demand, incoming, nil
}

// project builds each SKU's day series. Demand dated before from (and undated reservations) is
// the backlog of the first day; receipts expected before from are overdue and count on the first
// day; undated receipts stay out of the projection.
func project(skus []DemandRow, demand, incoming []demandEvent, from time.Time) []DemandRow {
	type day struct{ demand, incoming money.Qty }
	perSKU := map[uuid.UUID]map[time.Time]*day{}
	at := func(sku uuid.UUID, d time.Time) *day {
		if perSKU[sku] == nil {
			perSKU[sku] = map[time.Time]*day{}
		}
		if perSKU[sku][d] == nil {
			perSKU[sku][d] = &day{}
		}
		return perSKU[sku][d]
	}
	backlog := map[uuid.UUID]money.Qty{}
	undated := map[uuid.UUID]money.Qty{}
	for _, e := range demand {
		if e.On == nil || e.On.Before(from) {
			backlog[e.SkuID] = backlog[e.SkuID].Add(e.Qty)
			if e.On == nil {
				// Reservations are today's commitments; they are part of the first day.
				d := at(e.SkuID, from)
				d.demand = d.demand.Add(e.Qty)
				continue
			}
		}
		on := *e.On
		if on.Before(from) {
			on = from
		}
		d := at(e.SkuID, on)
		d.demand = d.demand.Add(e.Qty)
	}
	for _, e := range incoming {
		if e.On == nil {
			undated[e.SkuID] = undated[e.SkuID].Add(e.Qty)
			continue
		}
		on := *e.On
		if on.Before(from) {
			on = from
		}
		d := at(e.SkuID, on)
		d.incoming = d.incoming.Add(e.Qty)
	}
	for i := range skus {
		r := &skus[i]
		r.Backlog, r.UndatedIncoming, r.Days = backlog[r.SkuID], undated[r.SkuID], []DemandDay{}
		days := make([]time.Time, 0, len(perSKU[r.SkuID]))
		for d := range perSKU[r.SkuID] {
			days = append(days, d)
		}
		sort.Slice(days, func(a, b int) bool { return days[a].Before(days[b]) })
		projected := r.Available
		for _, d := range days {
			v := perSKU[r.SkuID][d]
			projected = projected.Add(v.incoming).Sub(v.demand)
			shortage := money.ZeroQty
			if projected.IsNegative() {
				shortage = projected.Neg()
				if r.FirstShortageOn == nil {
					on := d
					r.FirstShortageOn = &on
				}
				if shortage.Cmp(r.MaxShortage) > 0 {
					r.MaxShortage = shortage
				}
			}
			r.Days = append(r.Days, DemandDay{Date: d, Demand: v.demand, Incoming: v.incoming, Projected: projected, Shortage: shortage})
		}
	}
	return skus
}
