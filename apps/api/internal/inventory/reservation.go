package inventory

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

const reservationColumns = `id, warehouse_id, sku_id, source_type, source_id, source_doc_no, qty, consumed_qty, released_qty, status,
	expires_at, note, release_reason, created_at, updated_at, version`

// maxNoteLen bounds free-text notes of inventory documents.
const maxNoteLen = 500

// ReservationService holds sellable quantity of a SKU for invoices, kit assembly orders and
// manual holds. Every change locks the SKU's sku_stock row first (the global lock order), then
// the reservation, and keeps sku_stock.reserved equal to the open quantity of the active
// reservations. A new hold never takes reserved above sellable; an expiry roll or quarantine
// may still lower sellable below what is held, which shows as a negative available.
type ReservationService struct {
	signals StockSignalDetector
}

// NewReservationService returns the service.
func NewReservationService() *ReservationService { return &ReservationService{} }

// ReserveRequest is a new hold.
type ReserveRequest struct {
	SkuID       uuid.UUID
	Qty         money.Qty
	SourceType  string
	SourceID    uuid.UUID
	SourceDocNo *string
	ExpiresAt   *time.Time
	Note        *string
}

func (r ReserveRequest) validate() error {
	fields := map[string]string{}
	if r.SkuID == uuid.Nil {
		fields["sku_id"] = "required"
	}
	if !r.Qty.IsPositive() {
		fields["qty"] = "must_be_positive"
	}
	switch r.SourceType {
	case ReserveInvoiceLine, ReserveAssemblyOrderItem, ReserveManual:
	default:
		fields["source_type"] = "invalid"
	}
	if r.SourceID == uuid.Nil {
		fields["source_id"] = "required"
	}
	if r.Note != nil && utf8.RuneCountInString(*r.Note) > maxNoteLen {
		fields["note"] = "too_long"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// Reserve holds req.Qty of a SKU for its source; a source holds at most one reservation.
func (rs *ReservationService) Reserve(ctx context.Context, tx Tx, tenantID uuid.UUID, now time.Time, req ReserveRequest) (Reservation, error) {
	if err := req.validate(); err != nil {
		return Reservation{}, err
	}
	var out Reservation
	err := rs.change(ctx, tx, tenantID, now, req.SkuID, func(s *SkuStock, info skuInfo) error {
		if s.Reserved.Add(req.Qty).Cmp(s.Sellable) > 0 {
			return errOverReserve(info.Code, req.Qty, s.Available())
		}
		out = Reservation{ID: ids.New(), WarehouseID: s.WarehouseID, SkuID: req.SkuID, SourceType: req.SourceType,
			SourceID: req.SourceID, SourceDocNo: req.SourceDocNo, Qty: req.Qty, ConsumedQty: money.ZeroQty,
			ReleasedQty: money.ZeroQty, Status: ReservationActive, ExpiresAt: req.ExpiresAt, Note: req.Note,
			CreatedAt: now, UpdatedAt: now, Version: 1}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reservations (id, tenant_id, warehouse_id, sku_id, source_type, source_id, source_doc_no, qty, status,
			  expires_at, note, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`,
			out.ID, tenantID, out.WarehouseID, out.SkuID, out.SourceType, out.SourceID, out.SourceDocNo, out.Qty, out.Status,
			out.ExpiresAt, out.Note, now); err != nil {
			if db.IsUniqueViolation(err, "ux_reservations__source_type_source_id") {
				return apperr.NewConflict("reservation_exists", "This source already holds a reservation")
			}
			return fmt.Errorf("inventory: insert reservation: %w", err)
		}
		s.Reserved = s.Reserved.Add(req.Qty)
		return nil
	})
	return out, err
}

// Consume turns qty of an active reservation into an issue (the invoice posted its stock out).
// The reservation is consumed once nothing is held any more.
func (rs *ReservationService) Consume(ctx context.Context, tx Tx, tenantID uuid.UUID, now time.Time, id uuid.UUID, qty money.Qty) (Reservation, error) {
	if !qty.IsPositive() {
		return Reservation{}, errInvalid(map[string]string{"qty": "must_be_positive"})
	}
	return rs.update(ctx, tx, tenantID, now, id, false, func(r *Reservation, s *SkuStock, _ skuInfo) error {
		if qty.Cmp(r.Open()) > 0 {
			return errInvalid(map[string]string{"qty": "exceeds_reserved"})
		}
		r.ConsumedQty = r.ConsumedQty.Add(qty)
		s.Reserved = s.Reserved.Sub(qty)
		return nil
	})
}

// Release frees qty of an active reservation; nil frees everything still held.
func (rs *ReservationService) Release(ctx context.Context, tx Tx, tenantID uuid.UUID, now time.Time, id uuid.UUID, qty *money.Qty) (Reservation, error) {
	if qty != nil && !qty.IsPositive() {
		return Reservation{}, errInvalid(map[string]string{"qty": "must_be_positive"})
	}
	return rs.update(ctx, tx, tenantID, now, id, false, func(r *Reservation, s *SkuStock, _ skuInfo) error {
		n := r.Open()
		if qty != nil {
			if qty.Cmp(n) > 0 {
				return errInvalid(map[string]string{"qty": "exceeds_reserved"})
			}
			n = *qty
		}
		r.ReleasedQty = r.ReleasedQty.Add(n)
		s.Reserved = s.Reserved.Sub(n)
		return nil
	})
}

// Adjust changes the total quantity of a document's reservation (an edited invoice line, or more
// held for a line still on backorder). The new total may not fall below what was already
// consumed or released; an increase must fit in sellable. Raising a consumed or released
// reservation above what was settled holds the difference and makes it active again; a manual
// hold that ended is never reopened.
func (rs *ReservationService) Adjust(ctx context.Context, tx Tx, tenantID uuid.UUID, now time.Time, id uuid.UUID, total money.Qty) (Reservation, error) {
	if !total.IsPositive() {
		return Reservation{}, errInvalid(map[string]string{"qty": "must_be_positive"})
	}
	return rs.update(ctx, tx, tenantID, now, id, true, func(r *Reservation, s *SkuStock, info skuInfo) error {
		settled := r.ConsumedQty.Add(r.ReleasedQty)
		if total.Cmp(settled) < 0 {
			return errInvalid(map[string]string{"qty": "below_settled"})
		}
		delta := total.Sub(r.Qty)
		if delta.IsPositive() && s.Reserved.Add(delta).Cmp(s.Sellable) > 0 {
			return errOverReserve(info.Code, delta, s.Available())
		}
		r.Qty = total
		s.Reserved = s.Reserved.Add(delta)
		return nil
	})
}

// BySource returns the reservation of a source, nil when it has none.
func (rs *ReservationService) BySource(ctx context.Context, q db.Querier, sourceType string, sourceID uuid.UUID) (*Reservation, error) {
	var r Reservation
	err := q.QueryRowxContext(ctx, `SELECT `+reservationColumns+` FROM reservations WHERE source_type = $1 AND source_id = $2`,
		sourceType, sourceID).StructScan(&r)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read reservation: %w", err)
	}
	return &r, nil
}

func readReservation(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Reservation, error) {
	query := `SELECT ` + reservationColumns + ` FROM reservations WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var r Reservation
	err := q.QueryRowxContext(ctx, query, id).StructScan(&r)
	if db.IsNoRows(err) {
		return Reservation{}, errNotFound("Reservation")
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("inventory: read reservation: %w", err)
	}
	return r, nil
}

// update changes one active reservation under the SKU lock and the reservation lock; with reopen a
// document's settled reservation may be changed too. The status follows what is still held.
func (rs *ReservationService) update(ctx context.Context, tx Tx, tenantID uuid.UUID, now time.Time, id uuid.UUID, reopen bool,
	fn func(r *Reservation, s *SkuStock, info skuInfo) error) (Reservation, error) {
	peek, err := readReservation(ctx, tx, id, false)
	if err != nil {
		return Reservation{}, err
	}
	var out Reservation
	err = rs.change(ctx, tx, tenantID, now, peek.SkuID, func(s *SkuStock, info skuInfo) error {
		r, err := readReservation(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if r.Status != ReservationActive && (!reopen || r.SourceType == ReserveManual) {
			return apperr.NewConflict(CodeReservationNotActive, "This reservation is no longer active")
		}
		if err := fn(&r, s, info); err != nil {
			return err
		}
		r.Status = ReservationActive
		if r.Open().IsZero() {
			r.Status = ReservationReleased
			if r.ConsumedQty.IsPositive() {
				r.Status = ReservationConsumed
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE reservations SET qty = $2, consumed_qty = $3, released_qty = $4, status = $5, updated_at = $6, version = version + 1
			WHERE id = $1`, r.ID, r.Qty, r.ConsumedQty, r.ReleasedQty, r.Status, now); err != nil {
			return fmt.Errorf("inventory: update reservation: %w", err)
		}
		r.UpdatedAt, r.Version = now, r.Version+1
		out = r
		return nil
	})
	return out, err
}

// change locks the sku_stock row of skuID, lets fn change it and writes it back with the
// stock.changed event and the threshold signals it crosses. fn sees the SKU's stock recomputed
// from its lots for today, so a lot that expired since the last expiry roll is not held.
func (rs *ReservationService) change(ctx context.Context, tx Tx, tenantID uuid.UUID, now time.Time, skuID uuid.UUID,
	fn func(s *SkuStock, info skuInfo) error) error {
	infos, err := readSkuInfos(ctx, tx, []uuid.UUID{skuID})
	if err != nil {
		return err
	}
	info, ok := infos[skuID]
	if !ok {
		return errInvalid(map[string]string{"sku_id": "not_found"})
	}
	if info.ProductType == "combo" {
		return errNotStockable(info.Code)
	}
	today, err := tenantToday(ctx, tx, tenantID, now)
	if err != nil {
		return err
	}
	locked, err := lockSkuStock(ctx, tx, []uuid.UUID{skuID}, now)
	if err != nil {
		return err
	}
	s := locked[skuID]
	lots, err := readLotStocks(ctx, tx, []uuid.UUID{skuID}, nil)
	if err != nil {
		return err
	}
	if err := recompute(&s, lots, today); err != nil {
		return err
	}
	if err := fn(&s, info); err != nil {
		return err
	}
	if s.Reserved.IsNegative() {
		return fmt.Errorf("inventory: reserved of sku %s would go negative", skuID)
	}
	s.UpdatedAt = now
	if err := writeSkuStock(ctx, tx, s); err != nil {
		return err
	}
	s.Version++
	tx.Publish(outbox.Event{Topic: TopicStockChanged, Payload: map[string]any{
		"sku_id": skuID, "on_hand": s.OnHand, "sellable": s.Sellable, "reserved": s.Reserved, "available": s.Available(),
		"reason": "reservation",
	}})
	return rs.signals.Check(ctx, tx, now, today, locked, []SkuStock{s}, infos)
}

func errOverReserve(skuCode string, requested, available money.Qty) error {
	return &apperr.Error{Kind: apperr.Conflict, Code: CodeOverReserve,
		Message: "Not enough sellable stock of " + skuCode + " to reserve",
		Fields:  map[string]string{"sku": skuCode, "requested_qty": requested.String(), "available": available.String()}}
}

// ReservationFilter narrows the reservation list (newest first).
type ReservationFilter struct {
	SkuID      *uuid.UUID
	Status     string
	SourceType string
	AfterAt    *time.Time
	AfterID    uuid.UUID
	Limit      int
}

// ListReservations lists the reservations of the tenant in scope.
func (s *Service) ListReservations(ctx context.Context, scope Scope, f ReservationFilter) ([]Reservation, error) {
	out := []Reservation{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+reservationColumns+` FROM reservations
			WHERE ($1::uuid IS NULL OR sku_id = $1) AND ($2 = '' OR status = $2) AND ($3 = '' OR source_type = $3)
			  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5::uuid))
			ORDER BY created_at DESC, id DESC LIMIT $6`,
			f.SkuID, f.Status, f.SourceType, f.AfterAt, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("inventory: list reservations: %w", err)
		}
		return nil
	})
	return out, err
}

// ManualReservation is a hold the owner places by hand.
type ManualReservation struct {
	SkuID     uuid.UUID
	Qty       money.Qty
	ExpiresAt *time.Time
	Note      *string
}

// CreateReservation places a manual hold.
func (s *Service) CreateReservation(ctx context.Context, actor audit.Actor, in ManualReservation) (Reservation, error) {
	var out Reservation
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		now := s.clock.Now()
		if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
			return errInvalid(map[string]string{"expires_at": "in_past"})
		}
		tx := &jobTx{Querier: q}
		r, err := s.reservations.Reserve(ctx, tx, actor.TenantID, now, ReserveRequest{SkuID: in.SkuID, Qty: in.Qty,
			SourceType: ReserveManual, SourceID: ids.New(), ExpiresAt: in.ExpiresAt, Note: in.Note})
		if err != nil {
			return err
		}
		if err := s.record(ctx, q, actor, "reservation.create", "reservation", r.ID, nil, r); err != nil {
			return err
		}
		out = r
		return s.flush(ctx, tx, actor.TenantID)
	})
	return out, err
}

// ReleaseExpiredReservations frees what the manual holds of the tenant still hold once their
// expires_at has passed, one audited reservation.expire per hold, and returns how many it
// released. A released hold is no longer active, so running it again releases nothing; a hold
// released concurrently is skipped.
func (s *Service) ReleaseExpiredReservations(ctx context.Context, tenantID uuid.UUID) (int, error) {
	var n int
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		n = 0
		now := s.clock.Now()
		var due []Reservation
		// SKU order keeps the sku_stock locks of the releases in the global lock order.
		if err := sqlx.SelectContext(ctx, q, &due, `SELECT `+reservationColumns+` FROM reservations
			WHERE status = $1 AND source_type = $2 AND expires_at <= $3 ORDER BY sku_id, id`,
			ReservationActive, ReserveManual, now); err != nil {
			return fmt.Errorf("inventory: find expired reservations: %w", err)
		}
		tx := &jobTx{Querier: q}
		actor := workerActor(tenantID)
		for _, before := range due {
			r, err := s.reservations.Release(ctx, tx, tenantID, now, before.ID, nil)
			if apperr.Is(err, CodeReservationNotActive) {
				continue
			}
			if err != nil {
				return err
			}
			if err := s.record(ctx, q, actor, "reservation.expire", "reservation", r.ID, before, r); err != nil {
				return err
			}
			n++
		}
		return s.flush(ctx, tx, tenantID)
	})
	return n, err
}

// ReleaseInput frees a manual hold: Qty of it, all of it when nil, for Reason.
type ReleaseInput struct {
	Qty    *money.Qty
	Reason string
}

// ReleaseReservation frees a manual hold. The reservation keeps the reason of its last release;
// the audit log keeps the reason of every release. Holds of invoices and assembly orders are
// released by their documents.
func (s *Service) ReleaseReservation(ctx context.Context, actor audit.Actor, id uuid.UUID, in ReleaseInput) (Reservation, error) {
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return Reservation{}, errInvalid(map[string]string{"reason": "required"})
	case utf8.RuneCountInString(reason) > maxNoteLen:
		return Reservation{}, errInvalid(map[string]string{"reason": "too_long"})
	}
	var out Reservation
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readReservation(ctx, q, id, false)
		if err != nil {
			return err
		}
		if before.SourceType != ReserveManual {
			return apperr.NewConflict("reservation_not_manual", "This reservation belongs to a document; change the document instead")
		}
		tx := &jobTx{Querier: q}
		r, err := s.reservations.Release(ctx, tx, actor.TenantID, s.clock.Now(), id, in.Qty)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE reservations SET release_reason = $2 WHERE id = $1`, r.ID, reason); err != nil {
			return fmt.Errorf("inventory: record release reason: %w", err)
		}
		r.ReleaseReason = &reason
		if err := s.record(ctx, q, actor, "reservation.release", "reservation", r.ID, before, r); err != nil {
			return err
		}
		out = r
		return s.flush(ctx, tx, actor.TenantID)
	})
	return out, err
}
