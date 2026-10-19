package crm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Error codes of the loyalty ledger.
const (
	CodeLoyaltyDisabled    = "loyalty_disabled"
	CodeInsufficientPoints = "insufficient_points"
	CodeAlreadyRedeemed    = "already_redeemed"
)

// maxLoyaltyEntries bounds the entries one read of a customer's loyalty returns.
const maxLoyaltyEntries = 100

// netOfDocument is the revenue of a sale before tax: the priced lines (a mix pack header repeats
// its flavors, a combo component is priced on its combo line).
const netOfDocument = `
	SELECT COALESCE(sum(l.line_amount - l.tax_amount), 0) FROM sales_document_lines l
	WHERE l.sales_document_id = $1 AND l.line_kind NOT IN ('mix_pack', 'combo_component')`

// netOfReturn is the revenue a return (ret, of the enclosing query) takes back before tax. Return
// lines carry no tax: the share of tax comes from the sold line, else its combo line, else the
// whole document.
const netOfReturn = `
	SELECT COALESCE(sum(rl.line_amount - round(rl.line_amount * (1 - COALESCE(
			(l.line_amount - l.tax_amount) / NULLIF(l.line_amount, 0),
			(pl.line_amount - pl.tax_amount) / NULLIF(pl.line_amount, 0),
			(d.total_amount - d.tax_amount) / NULLIF(d.total_amount, 0), 1)))), 0)
	FROM sales_return_lines rl
		LEFT JOIN sales_document_lines l ON l.id = rl.sales_document_line_id
		LEFT JOIN sales_document_lines pl ON pl.id = l.parent_line_id
		LEFT JOIN sales_documents d ON d.id = ret.sales_document_id
	WHERE rl.sales_return_id = ret.id`

func loadProgram(ctx context.Context, q db.Querier) (Program, error) {
	var p Program
	err := q.QueryRowxContext(ctx, `
		SELECT enabled, points_per_amount, point_value, updated_at, version FROM loyalty_programs
		WHERE tenant_id = current_setting('app.tenant_id')::uuid`).StructScan(&p)
	if db.IsNoRows(err) {
		return Program{}, nil
	}
	if err != nil {
		return Program{}, fmt.Errorf("crm: read loyalty program: %w", err)
	}
	return p, nil
}

// earn credits the customer of a posted sale with floor(net revenue × rate) points, then takes
// back the share of the returns already posted against it, so the result does not depend on the
// order the events are handled in. Nothing is earned while the program is off, by a walk-in or
// reversed sale, or when the points round down to zero.
func (s *Service) earn(ctx context.Context, q db.Querier, docID uuid.UUID, at time.Time) error {
	p, err := loadProgram(ctx, q)
	if err != nil || !p.Enabled || !p.PointsPerAmount.IsPositive() {
		return err
	}
	sale, err := lockSaleCustomer(ctx, q, docID)
	if err != nil || sale == nil || sale.Status == "reversed" {
		return err
	}
	var net money.Money
	if err := q.QueryRowxContext(ctx, netOfDocument, docID).Scan(&net); err != nil {
		return fmt.Errorf("crm: read sale net: %w", err)
	}
	points := net.Decimal().Mul(p.PointsPerAmount.Decimal()).Floor()
	if !points.IsPositive() {
		return nil
	}
	if _, err := insertEntry(ctx, q, LoyaltyEntry{
		ID: ids.New(), PartyID: sale.Customer, Kind: EntryEarn, SourceType: RefSalesDocument, SourceID: docID,
		BusinessDate: sale.BusinessDate, CreatedAt: at,
	}, points); err != nil {
		return err
	}
	return settleReturns(ctx, q, docID, at)
}

// pointSale is the customer of a sale with the sale's state, read under the customer's lock.
type pointSale struct {
	Customer     uuid.UUID `db:"customer_party_id"`
	BusinessDate time.Time `db:"business_date"`
	Status       string    `db:"status"`
}

// lockSaleCustomer locks the customer of a sale, so the points of one customer move one event at
// a time, and reads the sale under that lock. A missing or walk-in sale is nil.
func lockSaleCustomer(ctx context.Context, q db.Querier, docID uuid.UUID) (*pointSale, error) {
	var customer *uuid.UUID
	if err := q.QueryRowxContext(ctx, `SELECT customer_party_id FROM sales_documents WHERE id = $1`, docID).Scan(&customer); err != nil {
		if db.IsNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crm: read sale for points: %w", err)
	}
	if customer == nil {
		return nil, nil
	}
	if err := lockParty(ctx, q, *customer); err != nil {
		return nil, err
	}
	var sale pointSale
	if err := q.QueryRowxContext(ctx, `SELECT customer_party_id, business_date, status FROM sales_documents WHERE id = $1`, docID).
		StructScan(&sale); err != nil {
		return nil, fmt.Errorf("crm: read sale for points: %w", err)
	}
	return &sale, nil
}

// saleEntry is an entry a sale booked — the points it earned or the points redeemed on it — with
// what returns and its reversal already undid of it. Points and Undone are magnitudes.
type saleEntry struct {
	ID      uuid.UUID `db:"id"`
	PartyID uuid.UUID `db:"party_id"`
	Points  money.Qty `db:"points"`
	Undone  money.Qty `db:"undone"`
}

func (e saleEntry) remaining() decimal.Decimal { return e.Points.Decimal().Sub(e.Undone.Decimal()) }

// undoing is how an entry of a sale is undone: earned points are taken back, redeemed points are
// given back. Each way has its own source types, so a return can do both once.
type undoing struct {
	kind       string
	sign       int64
	byReturn   string
	byReversal string
}

var (
	undoEarn   = undoing{kind: EntryEarn, sign: -1, byReturn: RefSalesReturn, byReversal: RefSaleReversal}
	undoRedeem = undoing{kind: EntryRedeem, sign: 1, byReturn: RefSalesReturnRedemption, byReversal: RefSaleReversalRedemption}
)

func saleEntryOf(ctx context.Context, q db.Querier, u undoing, docID uuid.UUID) (*saleEntry, error) {
	var e saleEntry
	err := q.QueryRowxContext(ctx, `
		SELECT e.id, e.party_id, abs(e.points) AS points,
			COALESCE((SELECT abs(sum(r.points)) FROM loyalty_entries r WHERE r.reversal_of = e.id), 0) AS undone
		FROM loyalty_entries e WHERE e.kind = $2 AND e.source_type = 'sales_document' AND e.source_id = $1`, docID, u.kind).
		StructScan(&e)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("crm: read %s points of a sale: %w", u.kind, err)
	}
	return &e, nil
}

// reverseReturn undoes the points of a returned share of a sale. Before the sale's points are
// earned there is nothing to take back: earning them settles the returns already posted.
func (s *Service) reverseReturn(ctx context.Context, q db.Querier, returnID uuid.UUID, at time.Time) error {
	var docID *uuid.UUID
	if err := q.QueryRowxContext(ctx, `SELECT sales_document_id FROM sales_returns WHERE id = $1 AND reversal_of IS NULL`, returnID).
		Scan(&docID); err != nil {
		if db.IsNoRows(err) {
			return nil
		}
		return fmt.Errorf("crm: read return for points: %w", err)
	}
	if docID == nil {
		return nil // a return without its sale cannot be traced to its points
	}
	sale, err := lockSaleCustomer(ctx, q, *docID)
	if err != nil || sale == nil {
		return err
	}
	return settleReturns(ctx, q, *docID, at)
}

// returnedShare is a posted return of a sale with its net and the points it already undid.
type returnedShare struct {
	ID           uuid.UUID   `db:"id"`
	BusinessDate time.Time   `db:"business_date"`
	Net          money.Money `db:"net"`
	Undone       *money.Qty  `db:"undone"`
}

// settleReturns undoes, for every posted return of a sale that has not done so yet, its share of
// the points the sale earned and of the points redeemed on it. Shares are cumulative, in posting
// order: after k returns round(points × returned net ÷ sale net) are undone, so returning a sale
// in parts undoes all of it. The caller holds the customer's lock.
func settleReturns(ctx context.Context, q db.Querier, docID uuid.UUID, at time.Time) error {
	var saleNet money.Money
	if err := q.QueryRowxContext(ctx, netOfDocument, docID).Scan(&saleNet); err != nil {
		return fmt.Errorf("crm: read sale net: %w", err)
	}
	if !saleNet.IsPositive() {
		return nil
	}
	for _, u := range []undoing{undoEarn, undoRedeem} {
		if err := settleReturnsOf(ctx, q, u, docID, saleNet.Decimal(), at); err != nil {
			return err
		}
	}
	return nil
}

func settleReturnsOf(ctx context.Context, q db.Querier, u undoing, docID uuid.UUID, saleNet decimal.Decimal, at time.Time) error {
	e, err := saleEntryOf(ctx, q, u, docID)
	if err != nil || e == nil {
		return err
	}
	var returns []returnedShare
	if err := sqlxSelect(ctx, q, &returns, `
		SELECT ret.id, ret.business_date, (`+netOfReturn+`) AS net,
			(SELECT abs(x.points) FROM loyalty_entries x
			 WHERE x.kind = 'reverse' AND x.source_type = $2 AND x.source_id = ret.id) AS undone
		FROM sales_returns ret
		WHERE ret.sales_document_id = $1 AND ret.reversal_of IS NULL AND ret.status = 'posted'
		ORDER BY ret.created_at, ret.id`, docID, u.byReturn); err != nil {
		return fmt.Errorf("crm: read returns for points: %w", err)
	}
	total, remaining := e.Points.Decimal(), e.remaining()
	returnedNet, undoneSoFar := decimal.Zero, decimal.Zero
	for _, r := range returns {
		returnedNet = returnedNet.Add(r.Net.Decimal())
		if r.Undone != nil {
			undoneSoFar = undoneSoFar.Add(r.Undone.Decimal())
			continue
		}
		target := decimal.Min(total.Mul(returnedNet).Div(saleNet).Round(0), total)
		points := decimal.Min(target.Sub(undoneSoFar), remaining)
		if !points.IsPositive() {
			continue
		}
		if _, err := insertEntry(ctx, q, LoyaltyEntry{
			ID: ids.New(), PartyID: e.PartyID, Kind: EntryReverse, SourceType: u.byReturn, SourceID: r.ID,
			ReversalOf: &e.ID, BusinessDate: r.BusinessDate, CreatedAt: at,
		}, points.Mul(decimal.NewFromInt(u.sign))); err != nil {
			return err
		}
		undoneSoFar = undoneSoFar.Add(points)
		remaining = remaining.Sub(points)
	}
	return nil
}

// reverseSale undoes what is left of the points of a reversed sale: the earned points are taken
// back and the redeemed points given back.
func (s *Service) reverseSale(ctx context.Context, q db.Querier, docID uuid.UUID, at time.Time) error {
	sale, err := lockSaleCustomer(ctx, q, docID)
	if err != nil || sale == nil {
		return err
	}
	today, err := todayIn(ctx, q, at)
	if err != nil {
		return err
	}
	for _, u := range []undoing{undoEarn, undoRedeem} {
		e, err := saleEntryOf(ctx, q, u, docID)
		if err != nil {
			return err
		}
		if e == nil || !e.remaining().IsPositive() {
			continue
		}
		if _, err := insertEntry(ctx, q, LoyaltyEntry{
			ID: ids.New(), PartyID: e.PartyID, Kind: EntryReverse, SourceType: u.byReversal, SourceID: docID,
			ReversalOf: &e.ID, BusinessDate: today, CreatedAt: at,
		}, e.remaining().Mul(decimal.NewFromInt(u.sign))); err != nil {
			return err
		}
	}
	return nil
}

// insertEntry appends an entry once per kind and source; a replayed event inserts nothing.
func insertEntry(ctx context.Context, q db.Querier, e LoyaltyEntry, points decimal.Decimal) (bool, error) {
	pts, err := money.NewQty(points.String())
	if err != nil {
		return false, fmt.Errorf("crm: loyalty points %s: %w", points, err)
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO loyalty_entries (id, tenant_id, party_id, points, kind, source_type, source_id, reversal_of,
			business_date, created_by, created_at)
		VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT ON CONSTRAINT ux_loyalty_entries__kind_source_type_source_id DO NOTHING`,
		e.ID, e.PartyID, pts, e.Kind, e.SourceType, e.SourceID, e.ReversalOf, e.BusinessDate, e.CreatedBy, e.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("crm: insert loyalty entry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("crm: insert loyalty entry: %w", err)
	}
	return n == 1, nil
}

// Program reads the loyalty program; a tenant that never saved one has it off at version 0.
func (s *Service) Program(ctx context.Context, scope Scope) (Program, error) {
	var p Program
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		p, err = loadProgram(ctx, q)
		return err
	})
	return p, err
}

// ProgramInput sets the loyalty program.
type ProgramInput struct {
	Enabled         bool
	PointsPerAmount money.Money
	PointValue      money.Money
}

// PutProgram saves the loyalty program over the version the owner read (expected, 0 while it was
// never saved); a stale version is 412.
func (s *Service) PutProgram(ctx context.Context, actor audit.Actor, in ProgramInput, expected int) (Program, error) {
	fields := map[string]string{}
	if in.PointsPerAmount.IsNegative() || (in.Enabled && !in.PointsPerAmount.IsPositive()) {
		fields["points_per_amount"] = "must_be_positive"
	}
	if in.PointValue.IsNegative() || (in.Enabled && !in.PointValue.IsPositive()) {
		fields["point_value"] = "must_be_positive"
	}
	if len(fields) > 0 {
		return Program{}, errInvalid(fields)
	}
	var out Program
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := loadProgram(ctx, q)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		switch {
		case expected != before.Version:
			return apperr.NewPreconditionFailed("The loyalty program changed; reload it")
		case before.Version == 0:
			_, err = q.ExecContext(ctx, `
				INSERT INTO loyalty_programs (tenant_id, enabled, points_per_amount, point_value, updated_at, version)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, 1)`,
				in.Enabled, in.PointsPerAmount, in.PointValue, now)
			if db.IsUniqueViolation(err, "pk_loyalty_programs") {
				return apperr.NewPreconditionFailed("The loyalty program changed; reload it")
			}
		default:
			var res sql.Result
			res, err = q.ExecContext(ctx, `
				UPDATE loyalty_programs SET enabled = $1, points_per_amount = $2, point_value = $3, updated_at = $4,
					version = version + 1
				WHERE tenant_id = current_setting('app.tenant_id')::uuid AND version = $5`,
				in.Enabled, in.PointsPerAmount, in.PointValue, now, expected)
			if err == nil {
				var n int64
				if n, err = res.RowsAffected(); err == nil && n == 0 {
					return apperr.NewPreconditionFailed("The loyalty program changed; reload it")
				}
			}
		}
		if err != nil {
			return fmt.Errorf("crm: save loyalty program: %w", err)
		}
		if out, err = loadProgram(ctx, q); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "loyalty_program.updated", EntityType: "loyalty_program", EntityID: actor.TenantID, Before: before, After: out,
		})
	})
	return out, err
}

// Loyalty reads a customer's balance with the latest entries.
func (s *Service) Loyalty(ctx context.Context, scope Scope, partyID uuid.UUID) (Loyalty, error) {
	out := Loyalty{PartyID: partyID, Entries: []LoyaltyEntry{}}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := partyExists(ctx, q, partyID); err != nil {
			return err
		}
		var err error
		if out.Balance, err = balance(ctx, q, partyID); err != nil {
			return err
		}
		return sqlxSelect(ctx, q, &out.Entries, `
			SELECT id, party_id, points, kind, source_type, source_id, reversal_of, business_date, created_by, created_at
			FROM loyalty_entries WHERE party_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2`, partyID, maxLoyaltyEntries)
	})
	return out, err
}

func balance(ctx context.Context, q db.Querier, partyID uuid.UUID) (money.Qty, error) {
	var b money.Qty
	if err := q.QueryRowxContext(ctx, `SELECT COALESCE(sum(points), 0) FROM loyalty_entries WHERE party_id = $1`, partyID).Scan(&b); err != nil {
		return money.Qty{}, fmt.Errorf("crm: read loyalty balance: %w", err)
	}
	return b, nil
}

// AdjustmentInput corrects a customer's points by hand; the reason is kept in the audit log.
type AdjustmentInput struct {
	PartyID uuid.UUID
	Points  money.Qty
	Reason  string
}

// Adjust books a manual correction. A deduction cannot take the balance below zero.
// adjustScope is the idempotency scope of manual loyalty adjustments.
const adjustScope = "loyalty.adjust"

// Adjusted is the response of a manual adjustment: the stored one when Replayed.
type Adjusted struct {
	Status   int
	Body     json.RawMessage
	Replayed bool
}

// Adjust records a manual adjustment once per Idempotency-Key: a retry of the same request
// replays the stored entry instead of adding the points again.
func (s *Service) Adjust(ctx context.Context, actor audit.Actor, key idempotency.Key, in AdjustmentInput) (Adjusted, error) {
	fields := map[string]string{}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if in.Points.IsZero() {
		fields["points"] = "must_not_be_zero"
	} else if !in.Points.IsWhole() {
		fields["points"] = "must_be_whole"
	}
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case in.Reason == "":
		fields["reason"] = "required"
	case len([]rune(in.Reason)) > 500:
		fields["reason"] = "too_long"
	}
	if len(fields) > 0 {
		return Adjusted{}, errInvalid(fields)
	}
	var out Adjusted
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		stored, err := s.idem.Begin(ctx, q, actor.TenantID, adjustScope, key)
		if err != nil {
			return err
		}
		if stored != nil {
			out = Adjusted{Status: stored.Status, Body: stored.Body, Replayed: true}
			return nil
		}
		if err := partyExists(ctx, q, in.PartyID); err != nil {
			return err
		}
		if err := lockParty(ctx, q, in.PartyID); err != nil {
			return err
		}
		if in.Points.IsNegative() {
			b, err := balance(ctx, q, in.PartyID)
			if err != nil {
				return err
			}
			if b.Add(in.Points).IsNegative() {
				return apperr.NewConflict(CodeInsufficientPoints, "The customer does not have that many points")
			}
		}
		now := s.clock.Now()
		today, err := todayIn(ctx, q, now)
		if err != nil {
			return err
		}
		by, id := actor.ID, ids.New()
		e := LoyaltyEntry{
			ID: id, PartyID: in.PartyID, Points: in.Points, Kind: EntryManual, SourceType: RefManual, SourceID: id,
			BusinessDate: today, CreatedBy: &by, CreatedAt: now,
		}
		if _, err := insertEntry(ctx, q, e, in.Points.Decimal()); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{
			Action: "loyalty.adjusted", EntityType: "party", EntityID: in.PartyID,
			After: struct {
				LoyaltyEntry
				Reason string `json:"reason"`
			}{e, in.Reason},
		}); err != nil {
			return err
		}
		resp, err := json.Marshal(toLoyaltyEntryDTO(e))
		if err != nil {
			return fmt.Errorf("encode loyalty adjustment: %w", err)
		}
		out = Adjusted{Status: http.StatusCreated, Body: resp}
		return s.idem.Complete(ctx, q, actor.TenantID, adjustScope, key, out.Status, resp)
	})
	return out, err
}

// lockParty serializes the point deductions of one customer so two cannot overdraw the balance.
func lockParty(ctx context.Context, q db.Querier, partyID uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `SELECT 1 FROM parties WHERE id = $1 FOR UPDATE`, partyID); err != nil {
		return fmt.Errorf("crm: lock customer: %w", err)
	}
	return nil
}

// Redemption is points spent as a discount and the đồng they are worth.
type Redemption struct {
	Entry LoyaltyEntry
	Value money.Money
}

// RedeemInput is whole points a customer spends on a sales document, booked at At on the
// document's business date.
type RedeemInput struct {
	PartyID      uuid.UUID
	Points       money.Qty
	SourceID     uuid.UUID
	By           *uuid.UUID
	At           time.Time
	BusinessDate time.Time
}

// Redeem spends a customer's points on a sales document in the caller's transaction and returns
// the discount they are worth (points × point value, whole đồng). One document redeems once; the
// program must be on and the balance must cover the points. A return or reversal of the document
// gives its share of the points back.
func Redeem(ctx context.Context, q db.Querier, in RedeemInput) (Redemption, error) {
	if !in.Points.IsPositive() || !in.Points.IsWhole() {
		return Redemption{}, errInvalid(map[string]string{"points": "must_be_positive_whole"})
	}
	p, err := loadProgram(ctx, q)
	if err != nil {
		return Redemption{}, err
	}
	if !p.Enabled {
		return Redemption{}, apperr.NewConflict(CodeLoyaltyDisabled, "The loyalty program is off")
	}
	if err := lockParty(ctx, q, in.PartyID); err != nil {
		return Redemption{}, err
	}
	b, err := balance(ctx, q, in.PartyID)
	if err != nil {
		return Redemption{}, err
	}
	if b.Cmp(in.Points) < 0 {
		return Redemption{}, apperr.NewConflict(CodeInsufficientPoints, "The customer does not have that many points")
	}
	e := LoyaltyEntry{
		ID: ids.New(), PartyID: in.PartyID, Points: in.Points.Neg(), Kind: EntryRedeem, SourceType: RefSalesDocument,
		SourceID: in.SourceID, BusinessDate: in.BusinessDate, CreatedBy: in.By, CreatedAt: in.At,
	}
	inserted, err := insertEntry(ctx, q, e, e.Points.Decimal())
	if err != nil {
		return Redemption{}, err
	}
	if !inserted {
		return Redemption{}, apperr.NewConflict(CodeAlreadyRedeemed, "Points were already redeemed on this document")
	}
	return Redemption{Entry: e, Value: p.PointValue.MulQty(in.Points).RoundVND()}, nil
}
