package kits

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Idempotency scopes of the kits postings and creates; a create's scope doubles as its audit
// action.
const (
	ScopeKitCreate   = "kit.create"
	ScopeKitPublish  = "kit.publish"
	ScopeOrderCreate = "assembly_order.create"
	ScopeReserve     = "assembly.reserve"
	ScopeClose       = "assembly.close"
	ScopeSubstitute  = "assembly.substitute"
	ScopePost        = "assembly.post"
	ScopeDisassemble = "assembly.disassemble"
)

// ComponentColor picks the color of a recipe component whose color the recipe leaves open.
type ComponentColor struct {
	SkuID   uuid.UUID
	ColorID uuid.UUID
}

// OrderInput plans an LD of PlannedQty kits of KitID by its active recipe. Colors picks the color
// of open-color components; a component without one is assembled from whatever color the
// assembler takes.
type OrderInput struct {
	KitID       uuid.UUID
	PlannedQty  money.Qty
	PlannedDate *time.Time
	CampaignID  *uuid.UUID
	InvoiceID   *uuid.UUID
	Note        *string
	Colors      []ComponentColor
}

// OrderPatch edits an order; nil fields are kept, and PlannedDateSet/NoteSet replace the date and
// note (nil clears them). PlannedQty re-plans the components and is only allowed on a draft.
type OrderPatch struct {
	PlannedQty     *money.Qty
	PlannedDate    *time.Time
	PlannedDateSet bool
	Note           *string
	NoteSet        bool
}

// SubstituteInput replaces Qty (base units) of what an order item still needs with
// SubstituteQty of another SKU (Qty when zero), e.g. a sold-out flavor by another.
type SubstituteInput struct {
	ItemID        uuid.UUID
	SkuID         uuid.UUID
	ColorID       *uuid.UUID
	Qty           money.Qty
	SubstituteQty money.Qty
}

// OrderPreview is what an order would need before it is made: the components for PlannedQty
// kits against their free stock, the kits that stock covers and the expected cost.
type OrderPreview struct {
	KitID          uuid.UUID
	RecipeVersion  int
	PlannedQty     money.Qty
	Lines          []PreviewLine
	Buildable      money.Qty
	ComponentsCost money.Money
	PackingCost    money.Money
	ExpectedCost   money.Money
}

// PreviewLine is one component of an order preview. Available is the free stock (sellable less
// reserved), of the line's color when it has one.
type PreviewLine struct {
	SkuID     uuid.UUID
	SkuCode   string
	Name      string
	ColorID   *uuid.UUID
	PerKit    money.Qty
	Required  money.Qty
	Available money.Qty
	Shortage  money.Qty
	UnitCost  money.Money
	Cost      money.Money
}

func validateOrder(in *OrderInput) map[string]string {
	fields := map[string]string{}
	if in.KitID == uuid.Nil {
		fields["kit_id"] = "required"
	}
	if !in.PlannedQty.IsPositive() || !in.PlannedQty.IsWhole() {
		fields["planned_qty"] = "must_be_positive_whole"
	}
	in.Note = optionalText(fields, "note", in.Note, maxNoteLen)
	if len(in.Colors) > MaxItems {
		fields["colors"] = "too_many"
	}
	seen := map[uuid.UUID]bool{}
	for i, c := range in.Colors {
		f := func(name string) string { return fmt.Sprintf("colors.%d.%s", i, name) }
		switch {
		case c.SkuID == uuid.Nil:
			fields[f("sku_id")] = "required"
		case seen[c.SkuID]:
			fields[f("sku_id")] = "duplicate"
		}
		seen[c.SkuID] = true
		if c.ColorID == uuid.Nil {
			fields[f("color_id")] = "required"
		}
	}
	return fields
}

// assemblyRecipe reads the recipe an order of kitID is made from: the active recipe of a
// published pre-packed kit, valid today.
func (s *Service) assemblyRecipe(ctx context.Context, q db.Querier, kitID uuid.UUID) (Recipe, []Component, error) {
	var typ, status string
	err := q.QueryRowxContext(ctx, `SELECT type, status FROM products WHERE id = $1`, kitID).Scan(&typ, &status)
	if db.IsNoRows(err) {
		return Recipe{}, nil, errInvalid(map[string]string{"kit_id": "not_found"})
	}
	if err != nil {
		return Recipe{}, nil, fmt.Errorf("kits: read kit product: %w", err)
	}
	if typ != TypeKit || status != ProductActive {
		return Recipe{}, nil, errNotAKit()
	}
	r, found, err := activeRecipe(ctx, q, kitID)
	if err != nil {
		return Recipe{}, nil, err
	}
	if !found {
		return Recipe{}, nil, errNotAKit()
	}
	today, err := s.todayIn(ctx, q)
	if err != nil {
		return Recipe{}, nil, err
	}
	if !r.ValidOn(today) {
		return Recipe{}, nil, errInvalid(map[string]string{"kit_id": "recipe_not_valid_today"})
	}
	comps, err := readComponents(ctx, q, r.ID)
	if err != nil {
		return Recipe{}, nil, err
	}
	return r, comps, nil
}

// activeColors reads which of the colors exist and are active.
func activeColors(ctx context.Context, q db.Querier, colors []ComponentColor) (map[uuid.UUID]bool, error) {
	colorIDs := make([]uuid.UUID, len(colors))
	for i, c := range colors {
		colorIDs[i] = c.ColorID
	}
	var found []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &found, `SELECT id FROM colors WHERE id = ANY($1) AND active`, colorIDs); err != nil {
		return nil, fmt.Errorf("kits: read colors: %w", err)
	}
	out := make(map[uuid.UUID]bool, len(found))
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// applyColors gives each open-color component the color picked for its SKU. A pick must name a
// component SKU whose color the recipe leaves open, and an active color.
func applyColors(comps []Component, colors []ComponentColor, active map[uuid.UUID]bool) ([]Component, error) {
	fields := map[string]string{}
	picked := make(map[uuid.UUID]uuid.UUID, len(colors))
	for i, c := range colors {
		f := func(name string) string { return fmt.Sprintf("colors.%d.%s", i, name) }
		component, open := false, false
		for _, comp := range comps {
			if comp.SkuID == c.SkuID {
				component = true
				open = open || comp.ColorID == nil
			}
		}
		switch {
		case !component:
			fields[f("sku_id")] = "not_a_component"
		case !open:
			fields[f("color_id")] = "color_fixed"
		case !active[c.ColorID]:
			fields[f("color_id")] = "unknown_color"
		default:
			picked[c.SkuID] = c.ColorID
		}
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	out := make([]Component, len(comps))
	for i, comp := range comps {
		out[i] = comp
		if color, ok := picked[comp.SkuID]; ok && comp.ColorID == nil {
			out[i].ColorID = &color
		}
	}
	return out, nil
}

// planItems is one order item per component for kits kits.
func planItems(comps []Component, kits money.Qty) ([]OrderItem, error) {
	items := make([]OrderItem, 0, len(comps))
	for _, c := range comps {
		qty, ok := mulExact(c.BaseQty, kits)
		if !ok {
			return nil, errInvalid(map[string]string{"planned_qty": "too_precise"})
		}
		recipeItemID := c.ItemID
		items = append(items, OrderItem{ID: ids.New(), RecipeItemID: &recipeItemID, SkuID: c.SkuID, ColorID: c.ColorID,
			PlannedBaseQty: qty, ConsumedBaseQty: money.ZeroQty})
	}
	return items, nil
}

// checkOrderLinks checks the campaign and invoice an order is linked to exist.
func checkOrderLinks(ctx context.Context, q db.Querier, campaignID, invoiceID *uuid.UUID) error {
	fields := map[string]string{}
	for field, link := range map[string]struct {
		id    *uuid.UUID
		table string
	}{"campaign_id": {campaignID, "campaigns"}, "invoice_id": {invoiceID, "invoices"}} {
		if link.id == nil {
			continue
		}
		var found bool
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+link.table+` WHERE id = $1)`, *link.id).Scan(&found); err != nil {
			return fmt.Errorf("kits: read order link: %w", err)
		}
		if !found {
			fields[field] = "not_found"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// CreateOrder plans an LD. Planning consumes and holds nothing; reserving the order holds its
// components. With a key it runs once per Idempotency-Key: a retry of the same request returns the
// stored order (replayed true) instead of planning a second one.
func (s *Service) CreateOrder(ctx context.Context, actor audit.Actor, key *idempotency.Key, in OrderInput) (OrderDetail, bool, error) {
	if fields := validateOrder(&in); len(fields) > 0 {
		return OrderDetail{}, false, errInvalid(fields)
	}
	var out OrderDetail
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeOrderCreate, key, http.StatusCreated, &out, func() error {
			r, comps, err := s.assemblyRecipe(ctx, q, in.KitID)
			if err != nil {
				return err
			}
			if err := checkOrderLinks(ctx, q, in.CampaignID, in.InvoiceID); err != nil {
				return err
			}
			active, err := activeColors(ctx, q, in.Colors)
			if err != nil {
				return err
			}
			if comps, err = applyColors(comps, in.Colors, active); err != nil {
				return err
			}
			items, err := planItems(comps, in.PlannedQty)
			if err != nil {
				return err
			}
			warehouseID, err := warehouseOf(ctx, q)
			if err != nil {
				return err
			}
			docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixKitAssembly)
			if err != nil {
				return err
			}
			now := s.clock.Now()
			o := AssemblyOrder{ID: ids.New(), WarehouseID: warehouseID, DocNo: docNo, KitRecipeID: r.ID, RecipeVersion: r.VersionNo,
				PlannedQty: in.PlannedQty, CompletedQty: money.ZeroQty, DisassembledQty: money.ZeroQty, Status: OrderDraft,
				PlannedDate: in.PlannedDate, CampaignID: in.CampaignID, InvoiceID: in.InvoiceID, Note: in.Note, CreatedAt: now,
				UpdatedAt: now, Version: 1}
			if err := insertOrder(ctx, q, actor.TenantID, o); err != nil {
				return err
			}
			for _, it := range items {
				it.OrderID = o.ID
				if err := insertOrderItem(ctx, q, actor.TenantID, it); err != nil {
					return err
				}
			}
			if out, err = readOrderDetail(ctx, q, o.ID, false); err != nil {
				return err
			}
			return s.record(ctx, q, actor, ScopeOrderCreate, "assembly_order", o.ID, nil, out)
		})
		return err
	})
	return out, replayed, err
}

// readOrderDetail reads an order with its items, batches and expected cost: the planned
// components at today's cost plus the recipe's packing cost.
func readOrderDetail(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (OrderDetail, error) {
	o, err := readOrder(ctx, q, id, lock)
	if err != nil {
		return OrderDetail{}, err
	}
	d := OrderDetail{AssemblyOrder: o}
	if d.Items, err = readOrderItems(ctx, q, id); err != nil {
		return OrderDetail{}, err
	}
	if d.Postings, err = readPostings(ctx, q, id); err != nil {
		return OrderDetail{}, err
	}
	r, err := recipeByID(ctx, q, o.KitRecipeID)
	if err != nil {
		return OrderDetail{}, err
	}
	skuIDs := make([]uuid.UUID, len(d.Items))
	for i, it := range d.Items {
		skuIDs[i] = it.SkuID
	}
	stock, err := readComponentStock(ctx, q, skuIDs)
	if err != nil {
		return OrderDetail{}, err
	}
	d.ComponentsCost = money.ZeroMoney
	for _, it := range d.Items {
		d.ComponentsCost = d.ComponentsCost.Add(stock[it.SkuID].UnitCost.MulQty(it.PlannedBaseQty))
	}
	d.PackingCost = r.PackingCost.MulQty(o.PlannedQty)
	d.ExpectedCost = d.ComponentsCost.Add(d.PackingCost)
	return d, nil
}

// Orders lists assembly orders newest first.
func (s *Service) Orders(ctx context.Context, scope Scope, f OrderFilter) ([]AssemblyOrder, error) {
	var out []AssemblyOrder
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = listOrders(ctx, q, f)
		return err
	})
	return out, err
}

// Order returns an assembly order with its items and batches.
func (s *Service) Order(ctx context.Context, scope Scope, id uuid.UUID) (OrderDetail, error) {
	var out OrderDetail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = readOrderDetail(ctx, q, id, false)
		return err
	})
	return out, err
}

// UpdateOrder edits an order guarded by its version. A closed order is final; re-planning the
// quantity is only allowed on a draft without substitutions, whose items follow the recipe.
func (s *Service) UpdateOrder(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in OrderPatch) (OrderDetail, error) {
	fields := map[string]string{}
	if in.PlannedQty != nil && (!in.PlannedQty.IsPositive() || !in.PlannedQty.IsWhole()) {
		fields["planned_qty"] = "must_be_positive_whole"
	}
	if in.NoteSet {
		in.Note = optionalText(fields, "note", in.Note, maxNoteLen)
	}
	if len(fields) > 0 {
		return OrderDetail{}, errInvalid(fields)
	}
	var out OrderDetail
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readOrderDetail(ctx, q, id, true)
		if err != nil {
			return err
		}
		if before.Version != expected {
			return apperr.NewPreconditionFailed("The assembly order was changed by someone else; reload it and try again")
		}
		if before.Status == OrderClosed {
			return apperr.NewConflict(CodeOrderStatus, "A closed assembly order cannot be changed")
		}
		o := before.AssemblyOrder
		if in.PlannedQty != nil && !in.PlannedQty.Equal(o.PlannedQty) {
			if err := replan(ctx, q, before, *in.PlannedQty); err != nil {
				return err
			}
			o.PlannedQty = *in.PlannedQty
		}
		if in.PlannedDateSet {
			o.PlannedDate = in.PlannedDate
		}
		if in.NoteSet {
			o.Note = in.Note
		}
		if _, err := db.UpdateWithVersion(ctx, q, "assembly_orders", id, expected, s.clock.Now(),
			db.Set{Column: "planned_qty", Value: o.PlannedQty}, db.Set{Column: "planned_date", Value: o.PlannedDate},
			db.Set{Column: "note", Value: o.Note}); err != nil {
			return err
		}
		if out, err = readOrderDetail(ctx, q, id, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "assembly_order.update", "assembly_order", id, before, out)
	})
	return out, err
}

// replan sets the planned quantity of each item of a draft order to kits times its recipe
// component.
func replan(ctx context.Context, q db.Querier, d OrderDetail, kits money.Qty) error {
	if d.Status != OrderDraft {
		return apperr.NewConflict(CodeOrderStatus, "Only a draft assembly order can be re-planned")
	}
	for _, it := range d.Items {
		if it.SubstitutesID != nil {
			return apperr.NewConflict(CodeOrderStatus, "An assembly order with substitutions cannot be re-planned")
		}
	}
	comps, err := readComponents(ctx, q, d.KitRecipeID)
	if err != nil {
		return err
	}
	for _, it := range d.Items {
		for _, c := range comps {
			if it.RecipeItemID == nil || c.ItemID != *it.RecipeItemID || c.SkuID != it.SkuID {
				continue
			}
			qty, ok := mulExact(c.BaseQty, kits)
			if !ok {
				return errInvalid(map[string]string{"planned_qty": "too_precise"})
			}
			if err := setItemPlanned(ctx, q, it.ID, qty); err != nil {
				return err
			}
		}
	}
	return nil
}

// PreviewOrder is what an order of in would need: its components against their free stock, the
// kits that covers and the expected cost. It saves nothing.
func (s *Service) PreviewOrder(ctx context.Context, scope Scope, in OrderInput) (OrderPreview, error) {
	if fields := validateOrder(&in); len(fields) > 0 {
		return OrderPreview{}, errInvalid(fields)
	}
	var out OrderPreview
	err := s.read(ctx, scope, func(q db.Querier) error {
		r, comps, err := s.assemblyRecipe(ctx, q, in.KitID)
		if err != nil {
			return err
		}
		active, err := activeColors(ctx, q, in.Colors)
		if err != nil {
			return err
		}
		if comps, err = applyColors(comps, in.Colors, active); err != nil {
			return err
		}
		out, err = s.previewOf(ctx, q, r, comps, in.PlannedQty)
		if err != nil {
			return err
		}
		out.KitID = in.KitID
		return nil
	})
	return out, err
}

func (s *Service) previewOf(ctx context.Context, q db.Querier, r Recipe, comps []Component, kits money.Qty) (OrderPreview, error) {
	perKit, err := expandComponents(comps, money.NewQtyFromInt(1))
	if err != nil {
		return OrderPreview{}, err
	}
	required, err := expandComponents(comps, kits)
	if err != nil {
		return OrderPreview{}, errInvalid(map[string]string{"planned_qty": "too_precise"})
	}
	skuIDs := make([]uuid.UUID, len(perKit))
	for i, p := range perKit {
		skuIDs[i] = p.SkuID
	}
	stock, err := readComponentStock(ctx, q, skuIDs)
	if err != nil {
		return OrderPreview{}, err
	}
	today, err := s.todayIn(ctx, q)
	if err != nil {
		return OrderPreview{}, err
	}
	colors, err := readColorStock(ctx, q, today, skuIDs)
	if err != nil {
		return OrderPreview{}, err
	}
	out := OrderPreview{RecipeVersion: r.VersionNo, PlannedQty: kits, ComponentsCost: money.ZeroMoney}
	var buildable *money.Qty
	for i, p := range perKit {
		st := stock[p.SkuID]
		l := PreviewLine{SkuID: p.SkuID, SkuCode: st.Code, Name: st.Name, ColorID: p.ColorID, PerKit: p.BaseQty,
			Required: required[i].BaseQty, Available: st.Sellable, Shortage: money.ZeroQty, UnitCost: st.UnitCost}
		if p.ColorID != nil {
			if c := colors[[2]uuid.UUID{p.SkuID, *p.ColorID}]; c.Cmp(l.Available) < 0 {
				l.Available = c
			}
		}
		if l.Required.Cmp(l.Available) > 0 {
			l.Shortage = l.Required.Sub(l.Available)
		}
		l.Cost = st.UnitCost.MulQty(l.Required)
		covered := kitsCovered(l.Available, l.PerKit)
		if buildable == nil || covered.Cmp(*buildable) < 0 {
			buildable = &covered
		}
		out.ComponentsCost = out.ComponentsCost.Add(l.Cost)
		out.Lines = append(out.Lines, l)
	}
	if buildable != nil {
		out.Buildable = *buildable
	}
	out.PackingCost = r.PackingCost.MulQty(kits)
	out.ExpectedCost = out.ComponentsCost.Add(out.PackingCost)
	return out, nil
}

// orderStep is the part of an order posting that runs on the order locked in the posting
// transaction, with its items read again.
type orderStep func(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem) (posting.Result, error)

// postOnOrder posts a command on an order: the stock of its component SKUs (the kit's too when
// withKit is set, and extra SKUs) and their reservations are locked first. An order whose items
// changed between the read and the lock is refused, so nothing runs on a row the posting did not
// lock.
func (s *Service) postOnOrder(ctx context.Context, actor audit.Actor, key idempotency.Key, scope string, orderID uuid.UUID,
	date time.Time, prefix string, withKit bool, extra []uuid.UUID, step orderStep) (posting.Response, error) {
	var skus, reservations []uuid.UUID
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		o, err := readOrder(ctx, q, orderID, false)
		if err != nil {
			return err
		}
		if withKit {
			skus = append(skus, o.KitSkuID)
		}
		items, err := readOrderItems(ctx, q, orderID)
		if err != nil {
			return err
		}
		for _, it := range items {
			skus = append(skus, it.SkuID)
			if it.ReservationID != nil {
				reservations = append(reservations, *it.ReservationID)
			}
		}
		return nil
	})
	if err != nil {
		return s.postRefused(ctx, actor, key, scope, err)
	}
	skus = append(skus, extra...)
	lockedSku := make(map[uuid.UUID]bool, len(skus))
	for _, id := range skus {
		lockedSku[id] = true
	}
	lockedRes := make(map[uuid.UUID]bool, len(reservations))
	for _, id := range reservations {
		lockedRes[id] = true
	}
	var locks posting.LockSet
	locks.Add(posting.LockSkuStock, skus...)
	if len(reservations) > 0 {
		locks.Add(posting.LockReservations, reservations...)
	}
	cmd := command{scope: scope, date: date, prefix: prefix, locks: locks}
	cmd.apply = func(ctx context.Context, tx posting.Tx) (posting.Result, error) {
		o, err := readOrder(ctx, tx, orderID, true)
		if err != nil {
			return posting.Result{}, err
		}
		items, err := readOrderItems(ctx, tx, orderID)
		if err != nil {
			return posting.Result{}, err
		}
		for _, it := range items {
			if !lockedSku[it.SkuID] || (it.ReservationID != nil && !lockedRes[*it.ReservationID]) {
				return posting.Result{}, errOrderChanged()
			}
		}
		return step(ctx, tx, o, items)
	}
	return s.posting.Post(ctx, actor, key, cmd)
}

// hold makes the reservation of an item hold want: reserved when it has none, released when want
// is zero, otherwise its total set to what it already consumed and released plus want.
func (s *Service) hold(ctx context.Context, tx posting.Tx, it OrderItem, want money.Qty, docNo string) error {
	switch {
	case it.ReservationID == nil:
		if !want.IsPositive() {
			return nil
		}
		return s.reservations.Reserve(ctx, tx, it.ID, it.SkuID, want, docNo)
	case want.Equal(it.Reserved):
		return nil
	case !want.IsPositive():
		return s.reservations.Release(ctx, tx, *it.ReservationID, nil)
	default:
		return s.reservations.Adjust(ctx, tx, *it.ReservationID, it.ReservationQty.Sub(it.Reserved).Add(want))
	}
}

// orderResult reads the order again after a posting changed it, queues its audit event and
// returns it as the response body.
func (s *Service) orderResult(ctx context.Context, tx posting.Tx, action string, before AssemblyOrder, status int) (posting.Result, error) {
	d, err := readOrderDetail(ctx, tx, before.ID, false)
	if err != nil {
		return posting.Result{}, err
	}
	tx.Audit(audit.Event{Action: action, EntityType: "assembly_order", EntityID: before.ID, Before: before, After: d})
	return posting.Result{Status: status, Body: orderDTO(d)}, nil
}

// ReserveOrder holds what each item of an order still needs. It may run again after a
// substitution or a disassembly to hold the components anew.
func (s *Service) ReserveOrder(ctx context.Context, actor audit.Actor, key idempotency.Key, orderID uuid.UUID) (posting.Response, error) {
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.postOnOrder(ctx, actor, key, ScopeReserve, orderID, date, "", false, nil,
		func(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem) (posting.Result, error) {
			if !s.status.Open(o.Status) {
				return posting.Result{}, errOrderNotOpen()
			}
			for _, it := range items {
				if err := s.hold(ctx, tx, it, it.Remaining(), o.DocNo); err != nil {
					return posting.Result{}, err
				}
			}
			next := o
			if o.Status == OrderDraft {
				next.Status = OrderReserved
			}
			if err := saveOrderProgress(ctx, tx, next, tx.Now()); err != nil {
				return posting.Result{}, err
			}
			return s.orderResult(ctx, tx, "assembly_order.reserve", o, http.StatusOK)
		})
}

// CloseOrder closes an order: it releases what its items still hold and takes no more batches.
// Its kits stay in stock and may still be disassembled.
func (s *Service) CloseOrder(ctx context.Context, actor audit.Actor, key idempotency.Key, orderID uuid.UUID) (posting.Response, error) {
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.postOnOrder(ctx, actor, key, ScopeClose, orderID, date, "", false, nil,
		func(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem) (posting.Result, error) {
			next := o
			var err error
			if next.Status, err = s.status.Move(o.Status, OrderClosed); err != nil {
				return posting.Result{}, err
			}
			for _, it := range items {
				if err := s.hold(ctx, tx, it, money.ZeroQty, o.DocNo); err != nil {
					return posting.Result{}, err
				}
			}
			if err := saveOrderProgress(ctx, tx, next, tx.Now()); err != nil {
				return posting.Result{}, err
			}
			return s.orderResult(ctx, tx, "assembly_order.close", o, http.StatusOK)
		})
}

func validateSubstitute(in *SubstituteInput) map[string]string {
	fields := map[string]string{}
	if in.ItemID == uuid.Nil {
		fields["item_id"] = "required"
	}
	if in.SkuID == uuid.Nil {
		fields["sku_id"] = "required"
	}
	if !in.Qty.IsPositive() {
		fields["qty"] = "must_be_positive"
	}
	switch {
	case in.SubstituteQty.IsZero():
		in.SubstituteQty = in.Qty
	case in.SubstituteQty.IsNegative():
		fields["substitute_qty"] = "must_be_positive"
	}
	return fields
}

// Substitute replaces part of what an order item still needs with another component. The item
// keeps what it consumed; the substitute is a new item naming it, and both are held again when
// the order holds stock, so the order's expected cost follows the actual composition.
func (s *Service) Substitute(ctx context.Context, actor audit.Actor, key idempotency.Key, orderID uuid.UUID, in SubstituteInput) (posting.Response, error) {
	if fields := validateSubstitute(&in); len(fields) > 0 {
		return posting.Response{}, errInvalid(fields)
	}
	date, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	return s.postOnOrder(ctx, actor, key, ScopeSubstitute, orderID, date, "", false, []uuid.UUID{in.SkuID},
		func(ctx context.Context, tx posting.Tx, o AssemblyOrder, items []OrderItem) (posting.Result, error) {
			var it OrderItem
			found := false
			for _, x := range items {
				if x.ID == in.ItemID {
					it, found = x, true
				}
			}
			if !found {
				return posting.Result{}, errInvalid(map[string]string{"item_id": "not_found"})
			}
			if !s.status.Open(o.Status) {
				return posting.Result{}, errOrderNotOpen()
			}
			remaining := it.Remaining()
			if !remaining.IsPositive() {
				return posting.Result{}, apperr.NewConflict(CodeSubstituteConsumed, "The item has nothing left to substitute")
			}
			if in.Qty.Cmp(remaining) > 0 {
				return posting.Result{}, errInvalid(map[string]string{"qty": CodeExceedsRemaining})
			}
			if err := checkSubstituteSku(ctx, tx, o, it, in); err != nil {
				return posting.Result{}, err
			}
			held := it.Reserved.IsPositive() || o.Status == OrderReserved
			it.PlannedBaseQty = it.PlannedBaseQty.Sub(in.Qty)
			if err := setItemPlanned(ctx, tx, it.ID, it.PlannedBaseQty); err != nil {
				return posting.Result{}, err
			}
			original := it.ID
			sub := OrderItem{ID: ids.New(), OrderID: o.ID, SubstitutesID: &original, SkuID: in.SkuID, ColorID: in.ColorID,
				PlannedBaseQty: in.SubstituteQty, ConsumedBaseQty: money.ZeroQty}
			if err := insertOrderItem(ctx, tx, tx.TenantID(), sub); err != nil {
				return posting.Result{}, err
			}
			if held {
				if err := s.hold(ctx, tx, it, it.Remaining(), o.DocNo); err != nil {
					return posting.Result{}, err
				}
				if err := s.hold(ctx, tx, sub, sub.PlannedBaseQty, o.DocNo); err != nil {
					return posting.Result{}, err
				}
			}
			if err := saveOrderProgress(ctx, tx, o, tx.Now()); err != nil {
				return posting.Result{}, err
			}
			return s.orderResult(ctx, tx, "assembly_order.substitute", o, http.StatusCreated)
		})
}

// checkSubstituteSku checks the substitute is a component SKU in an active color and differs
// from the item it replaces. A SKU stocked by color needs its color named: the substitute is
// what the order's author chose, not whatever color the batch would find first.
func checkSubstituteSku(ctx context.Context, q db.Querier, o AssemblyOrder, it OrderItem, in SubstituteInput) error {
	skuID := in.SkuID
	facts, err := loadRecipeFacts(ctx, q, []ItemInput{{SkuID: &skuID, ColorID: in.ColorID}})
	if err != nil {
		return err
	}
	fields := map[string]string{}
	if reason := facts.componentSku(in.SkuID, o.KitSkuID); reason != "" {
		fields["sku_id"] = reason
	}
	switch {
	case in.ColorID != nil && !facts.colors[*in.ColorID]:
		fields["color_id"] = "unknown_color"
	case in.ColorID == nil:
		var colored bool
		if err := q.QueryRowxContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM lots l JOIN lot_balances b ON b.lot_id = l.id
			               WHERE l.sku_id = $1 AND l.color_id IS NOT NULL AND NOT l.is_quarantine AND b.qty > 0)`,
			in.SkuID).Scan(&colored); err != nil {
			return fmt.Errorf("kits: read substitute color stock: %w", err)
		}
		if colored {
			fields["color_id"] = CodeColorRequired
		}
	}
	if in.SkuID == it.SkuID && sameColor(in.ColorID, it.ColorID) {
		fields["sku_id"] = "same_component"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// sameColor reports whether two optional colors are the same (both nil included).
func sameColor(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
