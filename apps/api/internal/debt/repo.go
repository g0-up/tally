package debt

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

const (
	obligationColumns = `o.id, o.tenant_id, o.party_id, o.direction, o.source_type, o.source_id, o.source_doc_no,
		o.business_date, o.due_date, o.amount, o.settled_amount, o.status, o.campaign_id, o.reversed_on,
		o.reversal_reason, o.reversal_doc_no, o.created_at, o.updated_at, o.version`
	paymentColumns = `p.id, p.tenant_id, p.doc_no, p.direction, p.party_id, p.purpose, p.fund_account_id, p.payment_method_id,
		p.amount, p.allocated_amount, p.status, p.earmarked_invoice_id, p.source_type, p.source_id, p.business_date,
		p.reference, p.note, p.posted_at, p.posted_by, p.refund_of, p.confirmed_at, p.reversed_on, p.planned_allocations,
		p.created_at, p.updated_at, p.version`
	settlementColumns = `s.id, s.tenant_id, s.obligation_id, s.settled_by_type, s.settled_by_id, s.amount, s.business_date,
		s.allocation_mode, s.posted_at, s.posted_by, s.reversal_of, s.created_at`
	//nolint:gosec // a column list, not a credential
	creditNoteColumns = `c.id, c.tenant_id, c.party_id, c.direction, c.source_type, c.source_id, c.source_doc_no, c.amount,
		c.allocated_amount, c.business_date, c.reason_id, c.posted_at, c.posted_by, c.created_at, c.version`
)

// writeCtx is what one ledger write needs: the transaction, the tenant, the posting time, who
// posts and the accounting date of the document.
type writeCtx struct {
	q        db.Querier
	tenantID uuid.UUID
	now      time.Time
	by       uuid.UUID
	date     time.Time
}

func fromTx(tx posting.Tx) writeCtx {
	return writeCtx{q: tx, tenantID: tx.TenantID(), now: tx.Now(), by: tx.Actor().ID, date: tx.BusinessDate()}
}

func selectAll(ctx context.Context, q db.Querier, dst any, query string, args ...any) error {
	return sqlx.SelectContext(ctx, q, dst, query, args...)
}

func getInto(ctx context.Context, q db.Querier, dst any, query string, args ...any) error {
	return sqlx.GetContext(ctx, q, dst, query, args...)
}

func getObligation(ctx context.Context, q db.Querier, id uuid.UUID) (Obligation, error) {
	var o Obligation
	err := sqlx.GetContext(ctx, q, &o, `SELECT `+obligationColumns+` FROM obligations o WHERE o.id = $1`, id)
	if db.IsNoRows(err) {
		return Obligation{}, errNotFound()
	}
	if err != nil {
		return Obligation{}, fmt.Errorf("debt: read obligation: %w", err)
	}
	return o, nil
}

// lockObligations locks the obligations in id order and returns them by id. Missing ids are
// absent from the map.
func lockObligations(ctx context.Context, q db.Querier, idList []uuid.UUID) (map[uuid.UUID]*Obligation, error) {
	out := map[uuid.UUID]*Obligation{}
	if len(idList) == 0 {
		return out, nil
	}
	var rows []Obligation
	if err := selectAll(ctx, q, &rows, `SELECT `+obligationColumns+` FROM obligations o WHERE o.id = ANY($1) ORDER BY o.id FOR UPDATE`,
		idList); err != nil {
		return nil, fmt.Errorf("debt: lock obligations: %w", err)
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

func getPayment(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Payment, error) {
	query := `SELECT ` + paymentColumns + ` FROM payments p WHERE p.id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var p Payment
	err := sqlx.GetContext(ctx, q, &p, query, id)
	if db.IsNoRows(err) {
		return Payment{}, errNotFound()
	}
	if err != nil {
		return Payment{}, fmt.Errorf("debt: read payment: %w", err)
	}
	return p, nil
}

// LockObligations is the posting locker of obligation rows.
func LockObligations(ctx context.Context, q db.Querier, idList []uuid.UUID) error {
	_, err := q.ExecContext(ctx, `SELECT 1 FROM obligations WHERE id = ANY($1) ORDER BY id FOR UPDATE`, idList)
	return err
}

// LockPayments is the posting locker of payment rows.
func LockPayments(ctx context.Context, q db.Querier, idList []uuid.UUID) error {
	_, err := q.ExecContext(ctx, `SELECT 1 FROM payments WHERE id = ANY($1) ORDER BY id FOR UPDATE`, idList)
	return err
}

// RegisterLockers installs the obligation and payment lockers on the coordinator.
func RegisterLockers(c *posting.Coordinator) {
	c.RegisterLocker(posting.LockObligations, LockObligations)
	c.RegisterLocker(posting.LockPayments, LockPayments)
}

// settle appends a settlement of amount (> 0) to ob and moves its projection. The caller holds
// the obligation's row lock and has checked amount against what is open.
func settle(ctx context.Context, w writeCtx, ob *Obligation, byType string, byID uuid.UUID, amount money.Money, mode string) (Settlement, error) {
	s := Settlement{
		ID: ids.New(), TenantID: w.tenantID, ObligationID: ob.ID, SettledByType: byType, SettledByID: byID, Amount: amount,
		BusinessDate: w.date, AllocationMode: mode, PostedAt: w.now, PostedBy: w.by, CreatedAt: w.now,
	}
	if err := insertSettlement(ctx, w.q, s); err != nil {
		return Settlement{}, err
	}
	if err := moveSettled(ctx, w, ob, amount); err != nil {
		return Settlement{}, err
	}
	return s, nil
}

// reverseSettlement appends the negative mirror of s and reopens its obligation by s.Amount.
func reverseSettlement(ctx context.Context, w writeCtx, ob *Obligation, s Settlement) (Settlement, error) {
	orig := s.ID
	r := Settlement{
		ID: ids.New(), TenantID: w.tenantID, ObligationID: s.ObligationID, SettledByType: s.SettledByType,
		SettledByID: s.SettledByID, Amount: s.Amount.Neg(), BusinessDate: w.date, AllocationMode: s.AllocationMode,
		PostedAt: w.now, PostedBy: w.by, ReversalOf: &orig, CreatedAt: w.now,
	}
	if err := insertSettlement(ctx, w.q, r); err != nil {
		return Settlement{}, err
	}
	if err := moveSettled(ctx, w, ob, r.Amount); err != nil {
		return Settlement{}, err
	}
	return r, nil
}

func insertSettlement(ctx context.Context, q db.Querier, s Settlement) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO obligation_settlements (id, tenant_id, obligation_id, settled_by_type, settled_by_id, amount, business_date,
		                                    allocation_mode, posted_at, posted_by, reversal_of, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		s.ID, s.TenantID, s.ObligationID, s.SettledByType, s.SettledByID, s.Amount, s.BusinessDate, s.AllocationMode,
		s.PostedAt, s.PostedBy, s.ReversalOf, s.CreatedAt); err != nil {
		return fmt.Errorf("debt: insert settlement: %w", err)
	}
	return nil
}

// moveSettled moves the settled projection of ob by delta and derives its status.
func moveSettled(ctx context.Context, w writeCtx, ob *Obligation, delta money.Money) error {
	settled := ob.SettledAmount.Add(delta)
	if settled.IsNegative() || settled.Cmp(ob.Amount) > 0 {
		return fmt.Errorf("debt: obligation %s would be settled %s of %s", ob.ID, settled, ob.Amount)
	}
	status := ObligationOpen
	if settled.Equal(ob.Amount) {
		status = ObligationSettled
	}
	if _, err := w.q.ExecContext(ctx, `
		UPDATE obligations SET settled_amount = $2, status = $3, updated_at = $4, version = version + 1 WHERE id = $1`,
		ob.ID, settled, status, w.now); err != nil {
		return fmt.Errorf("debt: move obligation projection: %w", err)
	}
	ob.SettledAmount, ob.Status, ob.UpdatedAt = settled, status, w.now
	ob.Version++
	return nil
}

// moveAllocated moves the allocated projection of p by delta.
func moveAllocated(ctx context.Context, w writeCtx, p *Payment, delta money.Money) error {
	allocated := p.AllocatedAmount.Add(delta)
	if allocated.IsNegative() || allocated.Cmp(p.Amount) > 0 {
		return fmt.Errorf("debt: payment %s would be allocated %s of %s", p.ID, allocated, p.Amount)
	}
	if _, err := w.q.ExecContext(ctx, `
		UPDATE payments SET allocated_amount = $2, updated_at = $3, version = version + 1 WHERE id = $1`,
		p.ID, allocated, w.now); err != nil {
		return fmt.Errorf("debt: move payment projection: %w", err)
	}
	p.AllocatedAmount, p.UpdatedAt = allocated, w.now
	p.Version++
	return nil
}

// readOnlyZone dates reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// tenantToday is the business date of now in the tenant's time zone.
func tenantToday(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) (time.Time, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return time.Time{}, fmt.Errorf("debt: read tenant privilege: %w", err)
	}
	var name string
	if readsTenant {
		if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = $1`, tenantID).Scan(&name); err != nil {
			return time.Time{}, fmt.Errorf("debt: read tenant time zone: %w", err)
		}
	} else {
		var err error
		if name, err = db.ReadOnlyTimezone(ctx, q, readOnlyZone); err != nil {
			return time.Time{}, err
		}
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}

// partyRef is the name a document shows for a party.
type partyRef struct {
	ID    uuid.UUID `db:"id" json:"id"`
	Name  string    `db:"name" json:"name"`
	Phone *string   `db:"phone" json:"phone"`
	Email *string   `db:"email" json:"-"`
}

func getParty(ctx context.Context, q db.Querier, id uuid.UUID) (partyRef, error) {
	var p partyRef
	err := sqlx.GetContext(ctx, q, &p, `SELECT id, name, phone, email FROM parties WHERE id = $1`, id)
	if db.IsNoRows(err) {
		return partyRef{}, errField("party_not_found", "party_id", "not_found", "The party does not exist")
	}
	if err != nil {
		return partyRef{}, fmt.Errorf("debt: read party: %w", err)
	}
	return p, nil
}

// pathParty reads the party a URL names: one that does not exist (or belongs to another tenant)
// is not found, unlike a party named in a request body.
func pathParty(ctx context.Context, q db.Querier, id uuid.UUID) (partyRef, error) {
	p, err := getParty(ctx, q, id)
	if apperr.Is(err, "party_not_found") {
		return partyRef{}, errNotFound()
	}
	return p, err
}

// termsDays is the payment term of the party on a ledger: its customer or supplier profile, else
// the store default.
func termsDays(ctx context.Context, q db.Querier, tenantID, partyID uuid.UUID, direction string) (int, error) {
	profile := `SELECT payment_terms_days FROM customer_profiles WHERE party_id = $2`
	if direction == Payable {
		profile = `SELECT payment_terms_days FROM supplier_profiles WHERE party_id = $2`
	}
	var days int
	err := q.QueryRowxContext(ctx, `
		SELECT COALESCE((`+profile+`), (SELECT default_payment_terms_days FROM tenant_settings WHERE tenant_id = $1), 0)`,
		tenantID, partyID).Scan(&days)
	if err != nil {
		return 0, fmt.Errorf("debt: read payment terms: %w", err)
	}
	return days, nil
}

func uniqueIDs(in ...uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
