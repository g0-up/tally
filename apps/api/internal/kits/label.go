package kits

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Label is the label payload of an order: per kit lot, its expiry and the component lots (with
// color and expiry) one kit of the lot holds.
func (s *Service) Label(ctx context.Context, scope Scope, orderID uuid.UUID) (Label, error) {
	var out Label
	err := s.read(ctx, scope, func(q db.Querier) error {
		o, err := readOrder(ctx, q, orderID, false)
		if err != nil {
			return err
		}
		out = Label{KitName: o.KitName, DocNo: o.DocNo, Lots: []LabelLot{}}
		if err := q.QueryRowxContext(ctx, `SELECT code FROM skus WHERE id = $1`, o.KitSkuID).Scan(&out.SkuCode); err != nil {
			return fmt.Errorf("kits: read kit sku: %w", err)
		}
		postings, err := readPostings(ctx, q, o.ID)
		if err != nil {
			return err
		}
		for _, p := range postings {
			comps, err := labelComponents(ctx, q, p)
			if err != nil {
				return err
			}
			out.Lots = append(out.Lots, LabelLot{LotID: p.OutputLotID, LotNo: p.OutputLotNo, Expiry: p.OutputExpiry,
				Qty: p.ProducedQty, Components: comps})
		}
		return nil
	})
	return out, err
}

// labelComponents lists what one kit of a batch holds of each component lot: what the batch put
// into its kits (scrap excluded) over the kits it produced.
func labelComponents(ctx context.Context, q db.Querier, p AssemblyPosting) ([]LabelComponent, error) {
	var rows []struct {
		SkuID     uuid.UUID  `db:"sku_id"`
		SkuCode   string     `db:"sku_code"`
		Name      string     `db:"name"`
		LotNo     string     `db:"lot_no"`
		ColorID   *uuid.UUID `db:"color_id"`
		ColorName *string    `db:"color_name"`
		Expiry    *time.Time `db:"expiry_date"`
		Qty       money.Qty  `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT pi.sku_id, s.code AS sku_code, s.display_name AS name, l.lot_no, pi.color_id, c.name AS color_name, l.expiry_date,
		       sum(pi.consumed_base_qty - pi.scrap_base_qty) AS qty
		FROM assembly_posting_items pi
		JOIN skus s ON s.id = pi.sku_id
		JOIN lots l ON l.id = pi.lot_id
		LEFT JOIN colors c ON c.id = pi.color_id
		WHERE pi.assembly_posting_id = $1
		GROUP BY pi.sku_id, s.code, s.display_name, l.lot_no, pi.color_id, c.name, l.expiry_date
		HAVING sum(pi.consumed_base_qty - pi.scrap_base_qty) > 0
		ORDER BY s.code, l.lot_no`, p.ID); err != nil {
		return nil, fmt.Errorf("kits: read label components: %w", err)
	}
	out := make([]LabelComponent, len(rows))
	for i, r := range rows {
		perKit, err := money.NewQty(r.Qty.Decimal().DivRound(p.ProducedQty.Decimal(), 4).String())
		if err != nil {
			return nil, err
		}
		out[i] = LabelComponent{SkuID: r.SkuID, SkuCode: r.SkuCode, Name: r.Name, LotNo: r.LotNo, ColorID: r.ColorID,
			ColorName: r.ColorName, Expiry: r.Expiry, QtyPerKit: perKit}
	}
	return out, nil
}

// Progress lists, per pre-packed kit with an order not yet closed or with open invoice demand,
// the kits planned, completed and taken apart on those orders, the kits on hand and what
// confirmed invoices still have to deliver.
func (s *Service) Progress(ctx context.Context, scope Scope) ([]Progress, error) {
	out := []Progress{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			WITH orders AS (
			  SELECT r.product_id, count(*) FILTER (WHERE o.status <> 'completed') AS open_orders, sum(o.planned_qty) AS planned_qty,
			         sum(o.completed_qty) AS completed_qty, sum(o.disassembled_qty) AS disassembled_qty
			  FROM assembly_orders o JOIN kit_recipes r ON r.id = o.kit_recipe_id
			  WHERE o.status <> 'closed'
			  GROUP BY r.product_id
			), demand AS (
			  SELECT il.sku_id, sum(il.base_qty - il.delivered_base_qty - il.cancelled_base_qty) AS qty
			  FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id
			  WHERE i.status IN ('confirmed', 'partially_delivered') AND il.sku_id IS NOT NULL
			  GROUP BY il.sku_id
			), stock AS (
			  SELECT sku_id, sum(on_hand) AS on_hand FROM sku_stock GROUP BY sku_id
			)
			SELECT p.id AS product_id, p.name, s.id AS sku_id, s.code AS sku_code, COALESCE(o.planned_qty, 0) AS planned_qty,
			       COALESCE(o.completed_qty, 0) AS completed_qty, COALESCE(o.disassembled_qty, 0) AS disassembled_qty,
			       COALESCE(st.on_hand, 0) AS on_hand, COALESCE(d.qty, 0) AS invoice_demand, COALESCE(o.open_orders, 0) AS open_orders
			FROM products p
			JOIN kit_recipes r ON r.product_id = p.id AND r.version_no = 1
			JOIN skus s ON s.id = r.output_sku_id
			LEFT JOIN orders o ON o.product_id = p.id
			LEFT JOIN demand d ON d.sku_id = s.id
			LEFT JOIN stock st ON st.sku_id = s.id
			WHERE p.type = 'kit' AND (o.product_id IS NOT NULL OR d.qty > 0)
			ORDER BY p.name, p.id`); err != nil {
			return fmt.Errorf("kits: read kit progress: %w", err)
		}
		return nil
	})
	return out, err
}

// Genealogy traces a lot: the component lots of a kit lot and the kit lots a component lot went
// into.
func (s *Service) Genealogy(ctx context.Context, scope Scope, lotID uuid.UUID) (Genealogy, error) {
	out := Genealogy{LotID: lotID}
	err := s.read(ctx, scope, func(q db.Querier) error {
		found, err := lotExists(ctx, q, lotID)
		if err != nil {
			return err
		}
		if !found {
			return errNotFound("Lot")
		}
		if out.Components, err = componentsOf(ctx, q, lotID); err != nil {
			return err
		}
		out.UsedIn, err = usedIn(ctx, q, lotID)
		return err
	})
	return out, err
}
