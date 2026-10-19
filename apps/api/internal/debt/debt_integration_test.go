//go:build integration

package debt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/platform/periodlock"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// at is 10:00 in Hà Nội on a day of 2026.
func at(month time.Month, day int) time.Time {
	return time.Date(2026, month, day, 3, 0, 0, 0, time.UTC)
}

func dayOf(month time.Month, day int) time.Time { return clock.Date(2026, month, day) }

func key(v string) idempotency.Key {
	h := sha256.Sum256([]byte(v))
	return idempotency.Key{Value: v, RequestHash: h[:]}
}

func newKey() idempotency.Key { return key(ids.New().String()) }

// fundBook is a CashBook on the fund tables, standing in for the cash domain (which this
// package may not import): it moves the fund balance by each entry and mirrors the entries of a
// reversed payment. Entry categories are the cash domain's business; the book files everything
// under other. The real adapter is exercised by the HTTP tests of the composition root.
type fundBook struct{}

func (fundBook) Method(ctx context.Context, q db.Querier, methodID uuid.UUID) (Method, error) {
	var m Method
	err := q.QueryRowxContext(ctx, `SELECT id, fund_account_id, code, requires_verification, active FROM payment_methods WHERE id = $1`,
		methodID).Scan(&m.ID, &m.FundAccountID, &m.Code, &m.RequiresVerification, &m.Active)
	if db.IsNoRows(err) {
		return Method{}, apperr.NewValidation("payment_method_not_found", "The payment method does not exist",
			map[string]string{"payment_method_id": "not_found"})
	}
	return m, err
}

func (fundBook) entry(ctx context.Context, q db.Querier, tenantID, fund uuid.UUID, amount money.Money, paymentID uuid.UUID,
	reversalOf *uuid.UUID, businessDate, at time.Time, by uuid.UUID,
) error {
	if _, err := q.ExecContext(ctx, `SELECT id FROM fund_accounts ORDER BY id FOR UPDATE`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE fund_accounts SET balance = balance + $2 WHERE id = $1`, fund, amount); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO cash_entries (id, tenant_id, fund_account_id, amount, category, payment_id, business_date, posted_at, posted_by,
		                          reversal_of, created_at)
		VALUES ($1, $2, $3, $4, 'other', $5, $6, $7, $8, $9, $7)`,
		ids.New(), tenantID, fund, amount, paymentID, businessDate, at, by, reversalOf)
	return err
}

func (b fundBook) Record(ctx context.Context, q db.Querier, tenantID uuid.UUID, e CashEntry) error {
	return b.entry(ctx, q, tenantID, e.FundAccountID, e.Amount, e.PaymentID, nil, e.BusinessDate, e.PostedAt, e.PostedBy)
}

func (b fundBook) ReversePayment(ctx context.Context, q db.Querier, tenantID, paymentID uuid.UUID, businessDate, at time.Time, by uuid.UUID) error {
	var open []struct {
		ID     uuid.UUID   `db:"id"`
		Fund   uuid.UUID   `db:"fund_account_id"`
		Amount money.Money `db:"amount"`
	}
	if err := sqlx.SelectContext(ctx, q, &open, `
		SELECT e.id, e.fund_account_id, e.amount FROM cash_entries e
		WHERE e.payment_id = $1 AND e.reversal_of IS NULL
		  AND NOT EXISTS (SELECT 1 FROM cash_entries r WHERE r.reversal_of = e.id)
		ORDER BY e.created_at, e.id`, paymentID); err != nil {
		return err
	}
	for _, e := range open {
		id := e.ID
		if err := b.entry(ctx, q, tenantID, e.Fund, e.Amount.Neg(), paymentID, &id, businessDate, at, by); err != nil {
			return err
		}
	}
	return nil
}

type harness struct {
	d     *db.DB
	clk   *clock.Fixed
	coord *posting.Coordinator
	svc   *Service
	exc   *exception.Guard
}

func newHarness(t *testing.T) harness {
	t.Helper()
	d := testkit.DB(t)
	clk := clock.NewFixed(at(time.September, 20))
	rec := audit.NewRecorder(clk)
	coord, err := posting.NewCoordinator(posting.Deps{
		DB: d, Idempotency: idempotency.NewGuard(clk), Docs: docseq.NewIssuer(), Periods: periodlock.NewGuard(),
		Audit: rec, Outbox: outbox.NewPublisher(clk), Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterLockers(coord)
	exc := exception.NewGuard(clk)
	svc := NewService(Deps{
		DB: d, Clock: clk, Audit: rec, Posting: coord, Cash: fundBook{}, Docs: docseq.NewIssuer(), Exceptions: exc,
	})
	return harness{d: d, clk: clk, coord: coord, svc: svc, exc: exc}
}

// store is a seeded tenant: its funds, methods and reasons, and a customer group for profiles.
type store struct {
	tenantID                     uuid.UUID
	owner                        audit.Actor
	cashMethod, transferMethod   uuid.UUID
	cashFund, bankFund           uuid.UUID
	adjustReason, writeOffReason uuid.UUID
	group                        uuid.UUID
}

func (st store) scope() Scope { return Scope{TenantID: st.tenantID} }

func (h harness) newStore(t *testing.T) store {
	t.Helper()
	tn := testkit.NewTenant(t, h.d)
	st := store{
		tenantID: tn.ID, group: ids.New(),
		owner: audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb},
	}
	ctx := context.Background()
	now := h.clk.Now()
	testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
		for _, m := range []struct {
			kind, code string
			verify     bool
		}{{"cash", "cash", false}, {"bank", "bank_transfer", true}} {
			fund := ids.New()
			if _, err := q.ExecContext(ctx, `INSERT INTO fund_accounts (id, tenant_id, kind, name, created_at, updated_at)
				VALUES ($1, $2, $3, $3, $4, $4)`, fund, tn.ID, m.kind, now); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO payment_methods (id, tenant_id, code, name, fund_account_id, requires_verification, created_at)
				VALUES ($1, $2, $3, $3, $4, $5, $6)`, ids.New(), tn.ID, m.code, fund, m.verify, now); err != nil {
				return err
			}
		}
		if err := (ReasonSeeder{}).SeedReasons(ctx, q, SeedContext{TenantID: tn.ID, Now: now}); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO customer_groups (id, tenant_id, name, name_key, base_price_kind, created_at, updated_at)
			VALUES ($1, $2, 'Khách sỉ', 'khách sỉ', 'wholesale', $3, $3)`, st.group, tn.ID, now); err != nil {
			return err
		}
		return q.QueryRowxContext(ctx, `SELECT
			(SELECT id FROM payment_methods WHERE code = 'cash'), (SELECT id FROM payment_methods WHERE code = 'bank_transfer'),
			(SELECT id FROM fund_accounts WHERE kind = 'cash'), (SELECT id FROM fund_accounts WHERE kind = 'bank'),
			(SELECT id FROM reasons WHERE kind = 'debt_adjustment' ORDER BY name_key LIMIT 1),
			(SELECT id FROM reasons WHERE kind = 'write_off')`).
			Scan(&st.cashMethod, &st.transferMethod, &st.cashFund, &st.bankFund, &st.adjustReason, &st.writeOffReason)
	})
	return st
}

// party adds a customer, with a credit limit when limit is not empty.
func (h harness) party(t *testing.T, st store, name, limit string) uuid.UUID {
	t.Helper()
	id := ids.New()
	var credit *money.Money
	if limit != "" {
		l := vnd(limit)
		credit = &l
	}
	now := h.clk.Now()
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		ctx := context.Background()
		if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5)`, id, st.tenantID, name, textnorm.Key(name), now); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `INSERT INTO customer_profiles (tenant_id, party_id, customer_group_id, credit_limit, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5)`, st.tenantID, id, st.group, credit, now)
		return err
	})
	return id
}

// sourceCommand stands for a document of another domain (a sale, a receipt, a return) that writes
// its debt rows through the recorders inside its own posting; prefix numbers the document itself
// (none when empty) and docs are the prefixes of the payments it records.
type sourceCommand struct {
	date   time.Time
	prefix string
	docs   []string
	apply  func(ctx context.Context, tx posting.Tx) error
}

func (c sourceCommand) ExtraDocPrefixes() []string { return c.docs }

func (sourceCommand) Scope() string             { return "test.source" }
func (c sourceCommand) BusinessDate() time.Time { return c.date }
func (c sourceCommand) DocPrefix() string       { return c.prefix }
func (sourceCommand) LockKeys() posting.LockSet { return posting.LockSet{} }

func (c sourceCommand) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	if err := c.apply(ctx, tx); err != nil {
		return posting.Result{}, err
	}
	return posting.Result{Body: map[string]bool{"ok": true}}, nil
}

// today is the store's business date (the tenants are in Asia/Ho_Chi_Minh).
func (h harness) today(t *testing.T) time.Time {
	t.Helper()
	loc, err := clock.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	return clock.BusinessDate(h.clk.Now(), loc)
}

func (h harness) source(t *testing.T, st store, fn func(ctx context.Context, tx posting.Tx) error) error {
	t.Helper()
	return h.sourceWith(t, st, nil, fn)
}

// sourceWith posts a source document that records payments numbered under docs.
func (h harness) sourceWith(t *testing.T, st store, docs []string, fn func(ctx context.Context, tx posting.Tx) error) error {
	t.Helper()
	_, err := h.coord.Post(context.Background(), st.owner, newKey(), sourceCommand{date: h.today(t), docs: docs, apply: fn})
	return err
}

func docNo(prefix string) string { return prefix + strings.ToUpper(ids.New().String()[:8]) }

// receivable records a sale of amount due on due.
func (h harness) receivable(t *testing.T, st store, party uuid.UUID, amount string, due time.Time) Obligation {
	t.Helper()
	var out Obligation
	if err := h.source(t, st, func(ctx context.Context, tx posting.Tx) error {
		r, err := h.svc.Receivables().Record(ctx, tx, ObligationInput{
			PartyID: party, SourceType: SourceSalesDocument, SourceID: ids.New(), SourceDocNo: docNo("BH"),
			Amount: vnd(amount), DueDate: &due,
		})
		out = r.Obligation
		return err
	}); err != nil {
		t.Fatalf("record receivable: %v", err)
	}
	return out
}

// payable records a goods receipt of amount due on due.
func (h harness) payable(t *testing.T, st store, party uuid.UUID, amount string, due time.Time) Obligation {
	t.Helper()
	var out Obligation
	if err := h.source(t, st, func(ctx context.Context, tx posting.Tx) error {
		r, err := h.svc.Payables().Record(ctx, tx, ObligationInput{
			PartyID: party, SourceType: SourceReceipt, SourceID: ids.New(), SourceDocNo: docNo("PN"),
			Amount: vnd(amount), DueDate: &due,
		})
		out = r.Obligation
		return err
	}); err != nil {
		t.Fatalf("record payable: %v", err)
	}
	return out
}

type settlementBody struct {
	ObligationID   uuid.UUID   `json:"obligation_id"`
	Amount         money.Money `json:"amount"`
	AllocationMode string      `json:"allocation_mode"`
	ReversalOf     *uuid.UUID  `json:"reversal_of"`
}

type paymentBody struct {
	ID              uuid.UUID        `json:"id"`
	DocNo           string           `json:"doc_no"`
	Purpose         string           `json:"purpose"`
	Status          string           `json:"status"`
	Amount          money.Money      `json:"amount"`
	AllocatedAmount money.Money      `json:"allocated_amount"`
	Unallocated     money.Money      `json:"unallocated"`
	RefundOf        *uuid.UUID       `json:"refund_of"`
	Settlements     []settlementBody `json:"settlements"`
}

// decoded unwraps a posting's response body, failing the test when the posting failed.
func decoded[T any](t *testing.T) func(posting.Response, error) T {
	return func(res posting.Response, err error) T {
		t.Helper()
		if err != nil {
			t.Fatalf("posting failed: %v", err)
		}
		var out T
		if err := json.Unmarshal(res.Body, &out); err != nil {
			t.Fatalf("decode %s: %v", res.Body, err)
		}
		return out
	}
}

func wantErr(t *testing.T, err error, status int, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || httpx.StatusOf(e.Kind) != status || e.Code != code {
		t.Fatalf("err = %v, want %d %s", err, status, code)
	}
}

func wantMoney(t *testing.T, what string, got money.Money, want string) {
	t.Helper()
	if !got.Equal(vnd(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// pay posts a payment in from the Công nợ screen.
func (h harness) pay(t *testing.T, st store, party, method uuid.UUID, purpose, amount string, lines ...Allocation) (posting.Response, error) {
	t.Helper()
	return h.svc.PostPayment(context.Background(), st.owner, newKey(), PaymentInput{
		Direction: In, PartyID: &party, Purpose: purpose, MethodID: method, Amount: vnd(amount), Allocations: lines,
	}, nil)
}

func line(ob Obligation, amount string) Allocation {
	return Allocation{ObligationID: ob.ID, Amount: vnd(amount), Mode: ModeManual}
}

type obligationRow struct {
	Settled money.Money `db:"settled_amount"`
	Status  string      `db:"status"`
}

func (h harness) obligation(t *testing.T, st store, id uuid.UUID) obligationRow {
	t.Helper()
	var out obligationRow
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT settled_amount, status FROM obligations WHERE id = $1`, id).StructScan(&out)
	})
	return out
}

type paymentRow struct {
	Allocated money.Money `db:"allocated_amount"`
	Status    string      `db:"status"`
}

func (h harness) payment(t *testing.T, st store, id uuid.UUID) paymentRow {
	t.Helper()
	var out paymentRow
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT allocated_amount, status FROM payments WHERE id = $1`, id).StructScan(&out)
	})
	return out
}

func (h harness) fundBalance(t *testing.T, st store, fund uuid.UUID) money.Money {
	t.Helper()
	var out money.Money
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT balance FROM fund_accounts WHERE id = $1`, fund).Scan(&out)
	})
	return out
}

func (h harness) count(t *testing.T, st store, query string, args ...any) int {
	t.Helper()
	var n int
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), query, args...).Scan(&n)
	})
	return n
}

// verify runs the debt verifier and checks every fund balance against its entries; it fails on
// any mismatch.
func (h harness) verify(t *testing.T, st store) {
	t.Helper()
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		ctx := context.Background()
		mismatches, err := Verify(ctx, q)
		if err != nil {
			return err
		}
		if len(mismatches) > 0 {
			t.Errorf("debt verify: %v", mismatches)
		}
		var funds int
		if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM fund_accounts f
			WHERE f.balance <> f.opening_balance + COALESCE((SELECT SUM(e.amount) FROM cash_entries e WHERE e.fund_account_id = f.id), 0)`).
			Scan(&funds); err != nil {
			return err
		}
		if funds > 0 {
			t.Errorf("%d funds differ from opening + entries", funds)
		}
		return nil
	})
}

// denied runs a write the application role must not be granted, under a savepoint so the refusal
// leaves the surrounding transaction usable.
func denied(ctx context.Context, q db.Querier, query string, args ...any) error {
	if _, err := q.ExecContext(ctx, `SAVEPOINT denied`); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, query, args...)
	if _, rerr := q.ExecContext(ctx, `ROLLBACK TO SAVEPOINT denied`); rerr != nil {
		return rerr
	}
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		return fmt.Errorf("got %v, want permission denied", err)
	}
	return nil
}

func (h harness) partySummary(t *testing.T, st store, party uuid.UUID, ledger string) PartySummary {
	t.Helper()
	rows, err := h.svc.Parties(context.Background(), st.scope(), PartyFilter{Direction: ledger, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.PartyID == party {
			return r
		}
	}
	return PartySummary{}
}

func TestAllocationSuggestsOldestDueFirstAndRejectsMoreThanOpen(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Lan", "")
	first := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	second := h.receivable(t, st, party, "4000000", dayOf(time.October, 15))

	plan, err := h.svc.SuggestAllocation(ctx, st.scope(), party, Receivable, vnd("5000000"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].ObligationID != first.ID || !plan[0].Amount.Equal(vnd("3000000")) ||
		plan[1].ObligationID != second.ID || !plan[1].Amount.Equal(vnd("2000000")) || plan[0].Mode != ModeSuggested {
		t.Fatalf("suggestion = %+v, want 3,000,000 to the first and 2,000,000 to the second", plan)
	}

	_, err = h.pay(t, st, party, st.cashMethod, PurposeSettlement, "6000000", line(second, "6000000"))
	wantErr(t, err, 422, "allocation_exceeds_open")
	if n := h.count(t, st, `SELECT count(*) FROM payments`); n != 0 {
		t.Fatalf("a rejected payment left %d payments", n)
	}

	p := decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeSettlement, "5000000", plan...))
	if p.Status != PaymentConfirmed || len(p.Settlements) != 2 || !p.Unallocated.IsZero() || !strings.HasPrefix(p.DocNo, "PT") {
		t.Fatalf("payment = %+v", p)
	}
	if got := h.obligation(t, st, first.ID); got.Status != ObligationSettled || !got.Settled.Equal(vnd("3000000")) {
		t.Errorf("first = %+v, want settled", got)
	}
	if got := h.obligation(t, st, second.ID); got.Status != ObligationOpen || !got.Settled.Equal(vnd("2000000")) {
		t.Errorf("second = %+v, want 2,000,000 settled", got)
	}
	wantMoney(t, "cash fund", h.fundBalance(t, st, st.cashFund), "5000000")

	t.Run("an advance is allocated later, within what it has left", func(t *testing.T) {
		adv := decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeAdvance, "1500000"))
		_, err := h.svc.PostAllocation(ctx, st.owner, newKey(), AllocationInput{PaymentID: adv.ID, Allocations: []Allocation{line(second, "1600000")}})
		wantErr(t, err, 422, "allocation_exceeds_payment")
		got := decoded[paymentBody](t)(h.svc.PostAllocation(ctx, st.owner, newKey(), AllocationInput{
			PaymentID: adv.ID, Allocations: []Allocation{line(second, "1000000")},
		}))
		wantMoney(t, "unallocated", got.Unallocated, "500000")
		wantMoney(t, "second settled", h.obligation(t, st, second.ID).Settled, "3000000")
	})

	t.Run("documents of another party or ledger are refused", func(t *testing.T) {
		other := h.party(t, st, "Anh Minh", "")
		theirs := h.receivable(t, st, other, "1000000", dayOf(time.October, 1))
		_, err := h.pay(t, st, party, st.cashMethod, PurposeSettlement, "1000000", line(theirs, "1000000"))
		wantErr(t, err, 422, "allocation_party_mismatch")
		bill := h.payable(t, st, party, "1000000", dayOf(time.October, 1))
		_, err = h.pay(t, st, party, st.cashMethod, PurposeSettlement, "1000000", line(bill, "1000000"))
		wantErr(t, err, 422, "allocation_direction_mismatch")
		_, err = h.pay(t, st, party, st.cashMethod, PurposeSettlement, "1000000", line(first, "1000000"))
		wantErr(t, err, 422, "obligation_settled")
	})
	h.verify(t, st)
}

func TestPendingTransferMovesNothingUntilConfirmedOnce(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Cửa hàng Bình An", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))

	p := decoded[paymentBody](t)(h.pay(t, st, party, st.transferMethod, PurposeSettlement, "3000000", line(ob, "3000000")))
	if p.Status != PaymentPending || len(p.Settlements) != 0 || !p.AllocatedAmount.IsZero() {
		t.Fatalf("transfer = %+v, want pending with nothing allocated", p)
	}
	if n := h.count(t, st, `SELECT count(*) FROM cash_entries WHERE payment_id = $1`, p.ID); n != 0 {
		t.Fatalf("pending transfer wrote %d cash entries", n)
	}
	wantMoney(t, "open before confirm", h.obligation(t, st, ob.ID).Settled, "0")
	pending, err := h.svc.PendingVerification(ctx, st.scope())
	if err != nil || len(pending) != 1 || pending[0].ID != p.ID {
		t.Fatalf("queue = %+v, %v", pending, err)
	}
	ledger, err := h.svc.Ledger(ctx, st.scope(), party)
	if err != nil || len(ledger.Pending) != 1 || !ledger.Receivable.Equal(vnd("3000000")) {
		t.Fatalf("ledger = %+v, %v", ledger, err)
	}
	_, err = h.svc.PostAllocation(ctx, st.owner, newKey(), AllocationInput{PaymentID: p.ID, Allocations: []Allocation{line(ob, "1000")}})
	wantErr(t, err, 422, "payment_pending_verification")

	h.clk.Set(at(time.September, 22))
	confirmKey := newKey()
	confirmed := decoded[paymentBody](t)(h.svc.ConfirmPayment(ctx, st.owner, confirmKey, p.ID))
	if confirmed.Status != PaymentConfirmed || len(confirmed.Settlements) != 1 {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	replay, err := h.svc.ConfirmPayment(ctx, st.owner, confirmKey, p.ID)
	if err != nil || !replay.Replayed {
		t.Fatalf("replayed confirm = %+v, %v", replay, err)
	}
	_, err = h.svc.ConfirmPayment(ctx, st.owner, newKey(), p.ID)
	wantErr(t, err, 409, "payment_not_pending")

	if n := h.count(t, st, `SELECT count(*) FROM cash_entries WHERE payment_id = $1 AND fund_account_id = $2`, p.ID, st.bankFund); n != 1 {
		t.Errorf("cash entries = %d, want 1 in the bank fund", n)
	}
	if n := h.count(t, st, `SELECT count(*) FROM obligation_settlements WHERE settled_by_id = $1`, p.ID); n != 1 {
		t.Errorf("settlements = %d, want 1", n)
	}
	wantMoney(t, "bank fund", h.fundBalance(t, st, st.bankFund), "3000000")
	if got := h.obligation(t, st, ob.ID); got.Status != ObligationSettled {
		t.Errorf("obligation = %+v, want settled", got)
	}
	h.verify(t, st)
}

func TestReversalWaitsForTheRefundOfItsAdvanceAndRestoresBalances(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Hoa", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	before := h.partySummary(t, st, party, Receivable)

	p := decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeSettlement, "5000000", line(ob, "3000000")))
	wantMoney(t, "advance left", p.Unallocated, "2000000")
	refund := decoded[paymentBody](t)(h.svc.PostRefund(ctx, st.owner, newKey(), RefundInput{PaymentID: &p.ID, MethodID: st.cashMethod}))
	if refund.Purpose != PurposeAdvanceRefund || refund.RefundOf == nil || *refund.RefundOf != p.ID || !refund.Amount.Equal(vnd("2000000")) {
		t.Fatalf("refund = %+v", refund)
	}
	wantMoney(t, "cash after refund", h.fundBalance(t, st, st.cashFund), "3000000")
	_, err := h.svc.PostRefund(ctx, st.owner, newKey(), RefundInput{PaymentID: &p.ID, MethodID: st.cashMethod})
	wantErr(t, err, 422, "insufficient_advance")

	_, err = h.svc.ReversePayment(ctx, st.owner, newKey(), p.ID)
	wantErr(t, err, 409, "payment_has_successor")

	h.clk.Set(at(time.September, 25))
	if res, err := h.svc.ReversePayment(ctx, st.owner, newKey(), refund.ID); err != nil || res.Status != 200 {
		t.Fatalf("reverse refund = %+v, %v", res, err)
	}
	rev := decoded[paymentBody](t)(h.svc.ReversePayment(ctx, st.owner, newKey(), p.ID))
	if rev.Status != PaymentReversed || len(rev.Settlements) != 1 || !rev.Settlements[0].Amount.Equal(vnd("-3000000")) {
		t.Fatalf("reversal = %+v", rev)
	}
	_, err = h.svc.ReversePayment(ctx, st.owner, newKey(), p.ID)
	wantErr(t, err, 409, "payment_reversed")

	if got := h.obligation(t, st, ob.ID); got.Status != ObligationOpen || !got.Settled.IsZero() {
		t.Errorf("obligation = %+v, want open again", got)
	}
	wantMoney(t, "cash fund", h.fundBalance(t, st, st.cashFund), "0")
	after := h.partySummary(t, st, party, Receivable)
	if !after.Open.Equal(before.Open) || !after.Advance.Equal(before.Advance) {
		t.Errorf("summary after = open %s advance %s, before = open %s advance %s", after.Open, after.Advance, before.Open, before.Advance)
	}
	if n := h.count(t, st, `SELECT count(*) FROM cash_entries WHERE reversal_of IS NOT NULL`); n != 2 {
		t.Errorf("mirror cash entries = %d, want 2", n)
	}
	h.verify(t, st)
}

func TestAgingAsOfADateIgnoresLaterPostings(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Đại lý Thành Công", "")
	late := h.receivable(t, st, party, "2000000", dayOf(time.September, 25))
	first := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	h.receivable(t, st, party, "4000000", dayOf(time.October, 15))

	render := func(r AgingReport) string {
		var b strings.Builder
		for _, row := range append(r.Rows, AgingRow{Name: "total", Buckets: r.Totals}) {
			x := row.Buckets
			fmt.Fprintf(&b, "%s %s %s %s %s %s|", row.Name, x.Current, x.D1To30, x.D31To60, x.D61To90, x.Over90)
		}
		return b.String()
	}
	aging := func(asOf time.Time) AgingReport {
		r, err := h.svc.Aging(ctx, st.scope(), Receivable, asOf)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	h.clk.Set(at(time.October, 3))
	before := aging(dayOf(time.September, 30))
	wantMoney(t, "1-30 days on 30/09", before.Totals.D1To30, "2000000")
	wantMoney(t, "current on 30/09", before.Totals.Current, "7000000")

	h.clk.Set(at(time.October, 5))
	decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeSettlement, "5000000", line(late, "2000000"), line(first, "3000000")))
	if got := render(aging(dayOf(time.September, 30))); got != render(before) {
		t.Errorf("aging on 30/09 changed after a payment dated 05/10:\nbefore %s\nafter  %s", render(before), got)
	}
	now := aging(dayOf(time.October, 5))
	wantMoney(t, "current on 05/10", now.Totals.Current, "4000000")
	wantMoney(t, "1-30 days on 05/10", now.Totals.D1To30, "0")
}

func TestStatementVersionsFreezeWhatWasSent(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Mai", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))

	s, _, err := h.svc.CreateStatement(ctx, st.owner, nil, StatementInput{
		PartyID: party, Direction: Receivable, PeriodFrom: dayOf(time.September, 1), PeriodTo: dayOf(time.September, 20),
	})
	if err != nil || s.CurrentVersion != 1 || s.Status != StatementDraft {
		t.Fatalf("statement = %+v, %v", s, err)
	}
	if s, err = h.svc.SendStatement(ctx, st.owner, s.ID, s.Version); err != nil || s.Status != StatementSent {
		t.Fatalf("send = %+v, %v", s, err)
	}
	v1, err := h.svc.StatementVersion(ctx, st.scope(), s.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "v1 closing", v1.Version.ClosingBalance, "3000000")

	_, err = h.svc.ConfirmStatement(ctx, st.owner, s.ID, s.Version-1)
	wantErr(t, err, 412, "precondition_failed")
	_, err = h.svc.SendStatement(ctx, st.owner, s.ID, s.Version)
	wantErr(t, err, 409, "statement_invalid_state")

	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		err := denied(ctx, q, `UPDATE statement_versions SET closing_balance = 0 WHERE statement_id = $1`, s.ID)
		if err != nil {
			t.Errorf("update of a statement version: %v", err)
		}
		return nil
	})

	decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeSettlement, "1000000", line(ob, "1000000")))
	if s, err = h.svc.GenerateStatement(ctx, st.owner, s.ID, s.Version); err != nil || s.CurrentVersion != 2 || s.Status != StatementDraft {
		t.Fatalf("generate = %+v, %v", s, err)
	}
	old, err := h.svc.StatementVersion(ctx, st.scope(), s.ID, 1)
	if err != nil || old.Status != StatementSuperseded || string(old.Version.Snapshot) != string(v1.Version.Snapshot) {
		t.Fatalf("v1 after regenerate = %s %s, %v", old.Status, old.Version.Snapshot, err)
	}
	v2, err := h.svc.StatementVersion(ctx, st.scope(), s.ID, 2)
	if err != nil || v2.Status != StatementDraft {
		t.Fatalf("v2 = %+v, %v", v2, err)
	}
	wantMoney(t, "v2 closing", v2.Version.ClosingBalance, "2000000")

	if s, err = h.svc.SendStatement(ctx, st.owner, s.ID, s.Version); err != nil {
		t.Fatal(err)
	}
	if s, err = h.svc.ConfirmStatement(ctx, st.owner, s.ID, s.Version); err != nil || s.Status != StatementConfirmed {
		t.Fatalf("confirm = %+v, %v", s, err)
	}
	_, err = h.svc.GenerateStatement(ctx, st.owner, s.ID, s.Version)
	wantErr(t, err, 409, "statement_confirmed")

	list, err := h.svc.Statements(ctx, st.scope(), StatementFilter{PartyID: &party, Limit: 10})
	if err != nil || len(list) != 1 || list[0].SentVersion == nil || *list[0].SentVersion != 2 {
		t.Fatalf("statements = %+v, %v", list, err)
	}
}

func TestCreditLimitCountsOpenUndeliveredAndEarmarkedDeposits(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Đại lý Phúc Lộc", "8000000")
	free := h.party(t, st, "Khách không hạn mức", "")
	h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	h.receivable(t, st, party, "4000000", dayOf(time.October, 15))

	order := ids.New()
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO invoices (id, tenant_id, doc_no, customer_party_id, payer_party_id, invoice_date, created_at, updated_at)
			VALUES ($1, $2, 'SO000001', $3, $3, $4, $5, $5)`, order, st.tenantID, party, dayOf(time.October, 2), h.clk.Now())
		return err
	})
	check := func(in CreditCheckInput) CreditCheck {
		t.Helper()
		var out CreditCheck
		testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) (err error) {
			out, err = h.svc.CreditLimit().Check(ctx, q, in)
			return err
		})
		return out
	}

	c := check(CreditCheckInput{PartyID: party, OrderID: &order, Undelivered: vnd("2000000"), AsOf: dayOf(time.October, 2)})
	wantMoney(t, "exposure", c.Exposure, "9000000")
	wantMoney(t, "overdue", c.OverdueAmount, "3000000")
	if !c.Exceeded || !c.Overdue || c.OldestOverdueDue == nil || !c.OldestOverdueDue.Equal(dayOf(time.October, 1)) {
		t.Fatalf("check = %+v, want exceeded and overdue since 01/10", c)
	}
	e, ok := apperr.As(c.Err())
	if !ok || e.Kind != apperr.ExceptionRequired || e.ExceptionKind != string(exception.CreditLimitExceeded) {
		t.Fatalf("err = %v, want the credit_limit_exceeded exception", c.Err())
	}

	// A general advance is not counted; a deposit earmarked to this order is.
	decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeAdvance, "5000000"))
	decoded[paymentBody](t)(h.svc.PostPayment(ctx, st.owner, newKey(), PaymentInput{
		Direction: In, PartyID: &party, Purpose: PurposeDeposit, MethodID: st.cashMethod, Amount: vnd("1500000"),
		SourceType: PaymentSourceInvoice, SourceID: &order, EarmarkedInvoiceID: &order,
	}, nil))
	c = check(CreditCheckInput{PartyID: party, OrderID: &order, Undelivered: vnd("2000000"), AsOf: dayOf(time.September, 30)})
	wantMoney(t, "earmarked", c.EarmarkedAdvance, "1500000")
	wantMoney(t, "exposure with deposit", c.Exposure, "7500000")
	if c.Exceeded || c.Overdue || c.Err() != nil {
		t.Fatalf("check = %+v, want within the limit and nothing overdue on 30/09", c)
	}

	if c := check(CreditCheckInput{PartyID: free, Undelivered: vnd("900000000"), AsOf: dayOf(time.October, 2)}); c.Limit != nil || c.Exceeded {
		t.Fatalf("no-limit check = %+v", c)
	}
}

func TestCreditNoteBeyondTheDebtBecomesARefundDueAndAdvancesAreNeverNetted(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Thu", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeAdvance, "2000000"))

	sum := h.partySummary(t, st, party, Receivable)
	wantMoney(t, "open beside an advance", sum.Open, "3000000")
	wantMoney(t, "advance", sum.Advance, "2000000")

	var res CreditNoteResult
	returnID := ids.New()
	if err := h.source(t, st, func(ctx context.Context, tx posting.Tx) (err error) {
		res, err = h.svc.CreditNotes().Record(ctx, tx, CreditNoteInput{
			PartyID: party, Direction: Receivable, SourceType: CreditSalesReturn, SourceID: returnID,
			SourceDocNo: docNo("TH"), Amount: vnd("4000000"), Targets: []uuid.UUID{ob.ID},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if res.RefundDue == nil || res.RefundDue.Direction != Payable || !res.RefundDue.Amount.Equal(vnd("1000000")) {
		t.Fatalf("credit note = %+v, want a refund due of 1,000,000 on the payable ledger", res)
	}
	if got := h.obligation(t, st, ob.ID); got.Status != ObligationSettled {
		t.Errorf("sale = %+v, want settled by the return", got)
	}
	ledger, err := h.svc.Ledger(ctx, st.scope(), party)
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "receivable", ledger.Receivable, "0")
	wantMoney(t, "payable (refund due)", ledger.Payable, "1000000")
	if len(ledger.Advances) != 1 || !ledger.Advances[0].Amount.Equal(vnd("2000000")) {
		t.Errorf("advances = %+v, want the 2,000,000 advance untouched", ledger.Advances)
	}

	t.Run("replaying the same source writes nothing", func(t *testing.T) {
		var again CreditNoteResult
		if err := h.source(t, st, func(ctx context.Context, tx posting.Tx) (err error) {
			again, err = h.svc.CreditNotes().Record(ctx, tx, CreditNoteInput{
				PartyID: party, Direction: Receivable, SourceType: CreditSalesReturn, SourceID: returnID,
				SourceDocNo: res.Note.SourceDocNo, Amount: vnd("4000000"), Targets: []uuid.UUID{ob.ID},
			})
			return err
		}); err != nil || !again.Replayed || again.Note.ID != res.Note.ID {
			t.Fatalf("replay = %+v, %v", again, err)
		}
	})

	_, err = h.svc.PostRefund(ctx, st.owner, newKey(), RefundInput{ObligationID: &ob.ID, MethodID: st.cashMethod})
	wantErr(t, err, 422, "refund_not_due")
	refund := decoded[paymentBody](t)(h.svc.PostRefund(ctx, st.owner, newKey(), RefundInput{ObligationID: &res.RefundDue.ID, MethodID: st.cashMethod}))
	if refund.Purpose != PurposeRefund || !refund.Amount.Equal(vnd("1000000")) || !strings.HasPrefix(refund.DocNo, "PC") {
		t.Fatalf("refund = %+v", refund)
	}
	if got := h.obligation(t, st, res.RefundDue.ID); got.Status != ObligationSettled {
		t.Errorf("refund due = %+v, want settled", got)
	}
	wantMoney(t, "cash fund", h.fundBalance(t, st, st.cashFund), "1000000")
	if n := h.count(t, st, `SELECT count(*) FROM cash_entries WHERE payment_id = $1 AND amount < 0`, refund.ID); n != 1 {
		t.Errorf("refund cash entries = %d, want 1 outflow", n)
	}
	h.verify(t, st)
}

func TestWriteOffNeedsTheOwnersConfirmationAndOffsetSettlesBothLedgers(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Anh Tuấn", "")
	sale := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	bill := h.payable(t, st, party, "2000000", dayOf(time.October, 1))

	in := AdjustmentInput{PartyID: party, Direction: Receivable, Kind: AdjustWriteOff, Amount: vnd("500000"),
		TargetObligationID: &sale.ID, ReasonID: st.writeOffReason}
	_, err := h.svc.PostAdjustment(ctx, st.owner, newKey(), in)
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.ExceptionRequired || e.ExceptionKind != string(exception.DebtWriteOff) {
		t.Fatalf("write-off without confirmation = %v", err)
	}
	hash := sha256.Sum256([]byte("write-off"))
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) (err error) {
		c, err := h.exc.Create(ctx, q, exception.NewConfirmation{TenantID: st.tenantID, Kind: exception.DebtWriteOff, DocType: "obligation",
			DocID: sale.ID, DocVersion: sale.Version, PayloadHash: hash[:], ConfirmedBy: st.owner.ID})
		in.ConfirmationID, in.PayloadHash = c.ID, hash[:]
		return err
	})
	if _, err := h.svc.PostAdjustment(ctx, st.owner, newKey(), in); err != nil {
		t.Fatalf("confirmed write-off: %v", err)
	}
	wantMoney(t, "written off", h.obligation(t, st, sale.ID).Settled, "500000")

	attachment := ids.New()
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO attachments (id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type,
			size_bytes, sha256, scan_status, uploaded_by, created_at)
			VALUES ($1, $2, 'party', $3, $4, 'bien-ban.pdf', 'application/pdf', 10, $5, 'clean', $6, $7)`,
			attachment, st.tenantID, party, "tenants/"+st.tenantID.String()+"/"+attachment.String(), hash[:], st.owner.ID, h.clk.Now())
		return err
	})
	off := OffsetInput{PartyID: party, ReceivableObligationID: sale.ID, PayableObligationID: bill.ID, Amount: vnd("2500001"), AttachmentID: attachment}
	_, err = h.svc.PostOffset(ctx, st.owner, newKey(), off)
	wantErr(t, err, 422, "allocation_exceeds_open")
	off.Amount = vnd("2000000")
	if _, err := h.svc.PostOffset(ctx, st.owner, newKey(), off); err != nil {
		t.Fatalf("offset: %v", err)
	}
	ledger, err := h.svc.Ledger(ctx, st.scope(), party)
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "receivable after offset", ledger.Receivable, "500000")
	wantMoney(t, "payable after offset", ledger.Payable, "0")
	h.verify(t, st)
}

func TestPostingsAreIdempotentAndAtomic(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Ngọc", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	in := PaymentInput{Direction: In, PartyID: &party, Purpose: PurposeSettlement, MethodID: st.cashMethod,
		Amount: vnd("1000000"), Allocations: []Allocation{line(ob, "1000000")}}

	k := key("pay-once")
	first, err := h.svc.PostPayment(ctx, st.owner, k, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.svc.PostPayment(ctx, st.owner, k, in, nil)
	if err != nil || !again.Replayed {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	if a, b := decoded[paymentBody](t)(first, nil), decoded[paymentBody](t)(again, nil); a.ID != b.ID || a.DocNo != b.DocNo {
		t.Fatalf("replay answered payment %s %s, want %s %s", b.ID, b.DocNo, a.ID, a.DocNo)
	}
	_, err = h.svc.PostPayment(ctx, st.owner, idempotency.Key{Value: k.Value, RequestHash: key("other body").RequestHash}, in, nil)
	wantErr(t, err, 409, "idempotency_key_reused")
	if n := h.count(t, st, `SELECT count(*) FROM payments`); n != 1 {
		t.Fatalf("payments = %d, want 1", n)
	}
	wantMoney(t, "settled once", h.obligation(t, st, ob.ID).Settled, "1000000")

	t.Run("a failed line rolls the whole payment back", func(t *testing.T) {
		other := h.receivable(t, st, party, "500000", dayOf(time.October, 2))
		_, err := h.pay(t, st, party, st.cashMethod, PurposeSettlement, "2000000", line(ob, "1000000"), line(other, "1000000"))
		wantErr(t, err, 422, "allocation_exceeds_open")
		if n := h.count(t, st, `SELECT count(*) FROM payments`); n != 1 {
			t.Errorf("payments = %d, want 1", n)
		}
		wantMoney(t, "first document", h.obligation(t, st, ob.ID).Settled, "1000000")
		wantMoney(t, "cash fund", h.fundBalance(t, st, st.cashFund), "1000000")
	})

	t.Run("a document failing after its debt rows leaves none of them", func(t *testing.T) {
		boom := errors.New("stock out failed")
		saleID := ids.New()
		err := h.sourceWith(t, st, []string{docseq.PrefixCashReceipt}, func(ctx context.Context, tx posting.Tx) error {
			r, err := h.svc.Receivables().Record(ctx, tx, ObligationInput{PartyID: party, SourceType: SourceSalesDocument,
				SourceID: saleID, SourceDocNo: docNo("BH"), Amount: vnd("700000")})
			if err != nil {
				return err
			}
			if _, err := h.svc.Payments().Record(ctx, tx, PaymentInput{Direction: In, PartyID: &party, Purpose: PurposeSettlement,
				MethodID: st.cashMethod, Amount: vnd("700000"), SourceType: PaymentSourcePOS, SourceID: &saleID,
				Allocations: []Allocation{{ObligationID: r.Obligation.ID, Amount: vnd("700000"), Mode: ModeManual}}}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if n := h.count(t, st, `SELECT count(*) FROM obligations WHERE source_id = $1`, saleID); n != 0 {
			t.Errorf("obligations = %d, want 0", n)
		}
		if n := h.count(t, st, `SELECT count(*) FROM payments WHERE source_id = $1`, saleID); n != 0 {
			t.Errorf("payments = %d, want 0", n)
		}
		wantMoney(t, "cash fund", h.fundBalance(t, st, st.cashFund), "1000000")
	})

	t.Run("recorders replay the same source and refuse a changed one", func(t *testing.T) {
		saleID := ids.New()
		record := func(amount string) (ObligationResult, error) {
			var r ObligationResult
			err := h.source(t, st, func(ctx context.Context, tx posting.Tx) (err error) {
				r, err = h.svc.Receivables().Record(ctx, tx, ObligationInput{PartyID: party, SourceType: SourceSalesDocument,
					SourceID: saleID, SourceDocNo: "BH-REPLAY", Amount: vnd(amount)})
				return err
			})
			return r, err
		}
		a, err := record("900000")
		if err != nil {
			t.Fatal(err)
		}
		b, err := record("900000")
		if err != nil || !b.Replayed || b.Obligation.ID != a.Obligation.ID {
			t.Fatalf("replay = %+v, %v", b, err)
		}
		_, err = record("950000")
		wantErr(t, err, 409, "obligation_exists")
	})
	h.verify(t, st)
}

func TestVerifierReportsATamperedProjection(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Lan", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeSettlement, "1000000", line(ob, "1000000")))
	h.verify(t, st)

	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `UPDATE obligations SET settled_amount = settled_amount + 1 WHERE id = $1`, ob.ID); err != nil {
			return err
		}
		mismatches, err := Verify(ctx, q)
		if err != nil {
			return err
		}
		if len(mismatches) != 1 || !strings.Contains(mismatches[0], ob.SourceDocNo) {
			t.Errorf("mismatches = %v, want the tampered document", mismatches)
		}
		if err := denied(ctx, q, `UPDATE obligation_settlements SET amount = 0 WHERE obligation_id = $1`, ob.ID); err != nil {
			t.Errorf("update of a settlement: %v", err)
		}
		return nil
	})
}

func TestTenantsCannotReachEachOthersDebts(t *testing.T) {
	h := newHarness(t)
	a, b := h.newStore(t), h.newStore(t)
	ctx := context.Background()
	party := h.party(t, a, "Chị Lan", "")
	ob := h.receivable(t, a, party, "3000000", dayOf(time.October, 1))
	p := decoded[paymentBody](t)(h.pay(t, a, party, a.cashMethod, PurposeAdvance, "1000000"))

	_, err := h.svc.Ledger(ctx, b.scope(), party)
	wantErr(t, err, 404, "not_found")
	_, err = h.svc.SuggestAllocation(ctx, b.scope(), party, Receivable, vnd("1000"))
	wantErr(t, err, 422, "party_not_found")
	_, err = h.svc.ReversePayment(ctx, b.owner, newKey(), p.ID)
	wantErr(t, err, 404, "not_found")
	_, err = h.svc.PostAllocation(ctx, b.owner, newKey(), AllocationInput{PaymentID: p.ID, Allocations: []Allocation{line(ob, "1000")}})
	if err == nil {
		t.Fatal("tenant b allocated tenant a's payment")
	}
	_, err = h.svc.PostPayment(ctx, b.owner, newKey(), PaymentInput{Direction: In, PartyID: &party, Purpose: PurposeAdvance,
		MethodID: b.cashMethod, Amount: vnd("1000")}, nil)
	wantErr(t, err, 422, "party_not_found")
	if rows, err := h.svc.Parties(ctx, b.scope(), PartyFilter{Direction: Receivable, Limit: 50}); err != nil || len(rows) != 0 {
		t.Fatalf("tenant b parties = %+v, %v", rows, err)
	}
	wantMoney(t, "tenant a advance", h.payment(t, a, p.ID).Allocated, "0")
}

func TestCollectionNotesAndReminders(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Lan", "")
	h.receivable(t, st, party, "3000000", dayOf(time.October, 1))

	promised := vnd("1000000")
	day := dayOf(time.October, 1)
	if _, _, err := h.svc.AddNote(ctx, st.owner, nil, NoteInput{PartyID: party, Direction: Receivable, Kind: NotePromise,
		Body: "Hứa trả đầu tháng", PromisedDate: &day, PromisedAmount: &promised, Channel: ChannelPhone}); err != nil {
		t.Fatal(err)
	}
	r, err := h.svc.Remind(ctx, st.owner, party, ChannelZalo)
	if err != nil || r.Duplicate || !strings.Contains(r.Text, "3.000.000") {
		t.Fatalf("remind = %+v, %v", r, err)
	}
	again, err := h.svc.Remind(ctx, st.owner, party, ChannelZalo)
	if err != nil || !again.Duplicate || again.Note.ID != r.Note.ID {
		t.Fatalf("second reminder today = %+v, %v", again, err)
	}
	notes, err := h.svc.Notes(ctx, st.scope(), NoteFilter{PartyID: party, Limit: 10})
	if err != nil || len(notes) != 2 {
		t.Fatalf("notes = %+v, %v", notes, err)
	}
	f, err := h.svc.Forecast(ctx, st.scope(), dayOf(time.September, 20), dayOf(time.October, 31))
	if err != nil {
		t.Fatal(err)
	}
	var due, promise money.Money
	for _, d := range f.Days {
		due, promise = due.Add(d.ReceivableDue), promise.Add(d.Promised)
	}
	wantMoney(t, "forecast receivable due", due, "3000000")
	wantMoney(t, "forecast promised", promise, "1000000")
}

// invoice adds a draft invoice of party, which deposits can be earmarked to.
func (h harness) invoice(t *testing.T, st store, party uuid.UUID) uuid.UUID {
	t.Helper()
	id := ids.New()
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `INSERT INTO invoices (id, tenant_id, doc_no, customer_party_id, payer_party_id,
			invoice_date, created_at, updated_at) VALUES ($1, $2, $3, $4, $4, $5, $6, $6)`,
			id, st.tenantID, "SO"+strings.ToUpper(id.String()[24:]), party, h.today(t), h.clk.Now())
		return err
	})
	return id
}

func TestEarmarkedDepositReachesItsInvoiceWhicheverIsConfirmedFirst(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Đại lý Phúc Lộc", "")
	deposit := func(order, method uuid.UUID, amount string) paymentBody {
		t.Helper()
		return decoded[paymentBody](t)(h.svc.PostPayment(ctx, st.owner, newKey(), PaymentInput{
			Direction: In, PartyID: &party, Purpose: PurposeDeposit, MethodID: method, Amount: vnd(amount),
			SourceType: PaymentSourceInvoice, SourceID: &order, EarmarkedInvoiceID: &order,
		}, nil))
	}
	invoiced := func(order uuid.UUID, amount string) ObligationResult {
		t.Helper()
		var r ObligationResult
		if err := h.source(t, st, func(ctx context.Context, tx posting.Tx) (err error) {
			r, err = h.svc.Receivables().Record(ctx, tx, ObligationInput{PartyID: party, SourceType: SourceSalesDocument,
				SourceID: ids.New(), SourceDocNo: docNo("BH"), Amount: vnd(amount), EarmarkedInvoiceID: &order})
			return err
		}); err != nil {
			t.Fatalf("record the invoice's receivable: %v", err)
		}
		return r
	}

	t.Run("a deposit confirmed before the invoice is posted is applied with it", func(t *testing.T) {
		order := h.invoice(t, st, party)
		d := deposit(order, st.cashMethod, "1000000")
		r := invoiced(order, "3000000")
		if len(r.Settlements) != 1 || r.Settlements[0].AllocationMode != ModeAutoEarmarked || !r.Settlements[0].Amount.Equal(vnd("1000000")) {
			t.Fatalf("settlements = %+v, want the deposit applied automatically", r.Settlements)
		}
		wantMoney(t, "invoice settled", h.obligation(t, st, r.Obligation.ID).Settled, "1000000")
		wantMoney(t, "deposit allocated", h.payment(t, st, d.ID).Allocated, "1000000")
	})

	t.Run("a transfer confirmed after the invoice is posted is applied on confirmation", func(t *testing.T) {
		order := h.invoice(t, st, party)
		d := deposit(order, st.transferMethod, "1500000")
		if d.Status != PaymentPending {
			t.Fatalf("transfer = %+v, want pending", d)
		}
		r := invoiced(order, "1000000")
		if len(r.Settlements) != 0 {
			t.Fatalf("settlements = %+v, want none before the transfer is confirmed", r.Settlements)
		}
		wantMoney(t, "invoice settled before confirmation", h.obligation(t, st, r.Obligation.ID).Settled, "0")

		confirmed := decoded[paymentBody](t)(h.svc.ConfirmPayment(ctx, st.owner, newKey(), d.ID))
		if len(confirmed.Settlements) != 1 || confirmed.Settlements[0].AllocationMode != ModeAutoEarmarked ||
			confirmed.Settlements[0].ObligationID != r.Obligation.ID || !confirmed.Settlements[0].Amount.Equal(vnd("1000000")) {
			t.Fatalf("confirmed = %+v, want the invoice settled automatically", confirmed)
		}
		if got := h.obligation(t, st, r.Obligation.ID); got.Status != ObligationSettled {
			t.Errorf("invoice = %+v, want settled", got)
		}
		wantMoney(t, "deposit allocated", h.payment(t, st, d.ID).Allocated, "1000000")
		wantMoney(t, "deposit left as an advance", confirmed.Unallocated, "500000")
	})

	t.Run("pending deposits share what the confirmed ones left open", func(t *testing.T) {
		order := h.invoice(t, st, party)
		deposit(order, st.cashMethod, "400000")
		a := deposit(order, st.transferMethod, "500000")
		b := deposit(order, st.transferMethod, "500000")
		r := invoiced(order, "1000000")
		wantMoney(t, "invoice settled by the cash deposit", h.obligation(t, st, r.Obligation.ID).Settled, "400000")
		decoded[paymentBody](t)(h.svc.ConfirmPayment(ctx, st.owner, newKey(), b.ID))
		decoded[paymentBody](t)(h.svc.ConfirmPayment(ctx, st.owner, newKey(), a.ID))
		if got := h.obligation(t, st, r.Obligation.ID); got.Status != ObligationSettled {
			t.Errorf("invoice = %+v, want settled", got)
		}
		allocated := h.payment(t, st, a.ID).Allocated.Add(h.payment(t, st, b.ID).Allocated)
		wantMoney(t, "transfers allocated", allocated, "600000")
	})
	h.verify(t, st)
}

func TestPaymentRecorderTakesTheReservedNumberAndRefusesAChangedReplay(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Ngọc", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	sale := ids.New()
	in := PaymentInput{Direction: In, PartyID: &party, Purpose: PurposeSettlement, MethodID: st.cashMethod, Amount: vnd("1000000"),
		SourceType: PaymentSourcePOS, SourceID: &sale, Allocations: []Allocation{line(ob, "1000000")}, Key: "pos:" + sale.String()}
	record := func(docs []string, in PaymentInput) (PaymentResult, error) {
		t.Helper()
		var r PaymentResult
		err := h.sourceWith(t, st, docs, func(ctx context.Context, tx posting.Tx) (err error) {
			r, err = h.svc.Payments().Record(ctx, tx, in)
			return err
		})
		return r, err
	}
	receipt := []string{in.DocPrefix()}

	if _, err := record(nil, in); err == nil || !strings.Contains(err.Error(), "ExtraDocPrefixes") {
		t.Fatalf("record without a reserved number: %v", err)
	}
	first, err := record(receipt, in)
	if err != nil || first.Replayed || !strings.HasPrefix(first.Payment.DocNo, docseq.PrefixCashReceipt) {
		t.Fatalf("first = %+v, %v", first.Payment, err)
	}
	again, err := record(nil, in)
	if err != nil || !again.Replayed || again.Payment.ID != first.Payment.ID {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	if _, err := record(receipt, in); err == nil || !strings.Contains(err.Error(), "did not take the reserved document numbers") {
		t.Fatalf("a replay under a reserved number: %v", err)
	}

	changes := map[string]func(*PaymentInput){
		"amount":      func(c *PaymentInput) { c.Amount, c.Allocations = vnd("900000"), []Allocation{line(ob, "900000")} },
		"method":      func(c *PaymentInput) { c.MethodID = st.transferMethod },
		"allocations": func(c *PaymentInput) { c.Allocations = []Allocation{line(ob, "500000")} },
		"source":      func(c *PaymentInput) { other := ids.New(); c.SourceID = &other },
	}
	for name, change := range changes {
		t.Run("another "+name, func(t *testing.T) {
			changed := in
			change(&changed)
			_, err := record(receipt, changed)
			wantErr(t, err, 409, "payment_exists")
		})
	}
	if n := h.count(t, st, `SELECT count(*) FROM payments`); n != 1 {
		t.Fatalf("payments = %d, want 1", n)
	}

	decoded[paymentBody](t)(h.svc.ReversePayment(ctx, st.owner, newKey(), first.Payment.ID))
	_, err = record(nil, in)
	wantErr(t, err, 409, "payment_reversed")
	h.verify(t, st)
}

func TestVerifierChecksPaymentsAgainstTheirCashEntries(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Chị Lan", "")
	ob := h.receivable(t, st, party, "3000000", dayOf(time.October, 1))
	paid := decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeSettlement, "1000000", line(ob, "1000000")))
	reversed := decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeAdvance, "200000"))
	decoded[paymentBody](t)(h.svc.ReversePayment(ctx, st.owner, newKey(), reversed.ID))
	pending := decoded[paymentBody](t)(h.pay(t, st, party, st.transferMethod, PurposeAdvance, "300000"))
	h.verify(t, st)

	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		for _, p := range []uuid.UUID{paid.ID, pending.ID} {
			if err := (fundBook{}).entry(ctx, q, st.tenantID, st.cashFund, vnd("1000"), p, nil, h.today(t), h.clk.Now(), st.owner.ID); err != nil {
				return err
			}
		}
		mismatches, err := Verify(ctx, q)
		if err != nil {
			return err
		}
		if len(mismatches) != 2 || !strings.Contains(mismatches[0]+mismatches[1], paid.DocNo) ||
			!strings.Contains(mismatches[0]+mismatches[1], pending.DocNo) {
			t.Errorf("mismatches = %v, want the confirmed and the pending payment", mismatches)
		}
		return nil
	})
}

func TestForecastCountsMoneyOnTheDayTheFundReceivedIt(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	party := h.party(t, st, "Cửa hàng Bình An", "")
	transfer := decoded[paymentBody](t)(h.pay(t, st, party, st.transferMethod, PurposeAdvance, "3000000"))
	cash := decoded[paymentBody](t)(h.pay(t, st, party, st.cashMethod, PurposeAdvance, "500000"))
	h.clk.Set(at(time.September, 22))
	decoded[paymentBody](t)(h.svc.ConfirmPayment(ctx, st.owner, newKey(), transfer.ID))
	h.clk.Set(at(time.September, 23))
	decoded[paymentBody](t)(h.svc.ReversePayment(ctx, st.owner, newKey(), cash.ID))

	f, err := h.svc.Forecast(ctx, st.scope(), dayOf(time.September, 20), dayOf(time.September, 30))
	if err != nil {
		t.Fatal(err)
	}
	got := map[time.Time]money.Money{}
	for _, d := range f.Days {
		got[d.Date] = d.ConfirmedIn
	}
	wantMoney(t, "received on 20/09", got[dayOf(time.September, 20)], "500000")
	wantMoney(t, "received on 22/09", got[dayOf(time.September, 22)], "3000000")
	wantMoney(t, "received on 23/09", got[dayOf(time.September, 23)], "-500000")
	h.verify(t, st)
}

// reverse posts a reversing document numbered under prefix (none when empty) that reverses the
// obligation of ref.
func (h harness) reverse(t *testing.T, st store, prefix string, ref SourceRef, reason string) (ObligationReversal, error) {
	t.Helper()
	var out ObligationReversal
	_, err := h.coord.Post(context.Background(), st.owner, newKey(), sourceCommand{date: h.today(t), prefix: prefix,
		apply: func(ctx context.Context, tx posting.Tx) (err error) {
			out, err = h.svc.Obligations().ReverseBySource(ctx, tx, ref, reason)
			return err
		}})
	return out, err
}

func TestReversingASourceTurnsItsPaymentsIntoAdvancesAndKeepsEverySettlement(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	customer := h.party(t, st, "Anh Khoa", "")
	supplier := h.party(t, st, "Xưởng Minh Phát", "")
	refOf := func(ob Obligation) SourceRef {
		return SourceRef{Direction: ob.Direction, SourceType: ob.SourceType, SourceID: ob.SourceID}
	}

	t.Run("an unpaid receipt is closed by one reversal settlement", func(t *testing.T) {
		ob := h.payable(t, st, supplier, "5000000", dayOf(time.October, 20))
		r, err := h.reverse(t, st, "XN", refOf(ob), "Nhập nhầm nhà cung cấp")
		if err != nil {
			t.Fatal(err)
		}
		if r.Replayed || len(r.Released) != 0 || r.Closing.SettledByType != SettledByReversal ||
			r.Closing.SettledByID != ob.ID || !r.Closing.Amount.Equal(vnd("5000000")) {
			t.Fatalf("reversal = %+v, want one closing settlement of the whole amount", r)
		}
		if r.Obligation.ReversedOn == nil || r.Obligation.ReversalDocNo == nil || !strings.HasPrefix(*r.Obligation.ReversalDocNo, "XN-") ||
			r.Obligation.Status != ObligationSettled {
			t.Fatalf("obligation = %+v, want settled, dated and numbered by the reversal", r.Obligation)
		}
		ledger, err := h.svc.Ledger(ctx, st.scope(), supplier)
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "payable after the reversal", ledger.Payable, "0")

		again, err := h.reverse(t, st, "", refOf(ob), "Nhập nhầm nhà cung cấp")
		if err != nil || !again.Replayed || again.Obligation.ID != ob.ID {
			t.Fatalf("second reversal = %+v, %v, want a replay", again, err)
		}
		err = h.source(t, st, func(ctx context.Context, tx posting.Tx) error {
			_, err := h.svc.Payables().Record(ctx, tx, ObligationInput{PartyID: supplier, SourceType: SourceReceipt,
				SourceID: ob.SourceID, SourceDocNo: ob.SourceDocNo, Amount: ob.Amount, DueDate: &ob.DueDate})
			return err
		})
		wantErr(t, err, 409, "obligation_reversed")
		h.verify(t, st)
	})

	t.Run("what was paid on a reversed sale becomes the customer's advance", func(t *testing.T) {
		ob := h.receivable(t, st, customer, "3000000", dayOf(time.October, 5))
		p := decoded[paymentBody](t)(h.pay(t, st, customer, st.cashMethod, PurposeSettlement, "1000000", line(ob, "1000000")))
		r, err := h.reverse(t, st, "", refOf(ob), "Khách trả lại toàn bộ đơn")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Released) != 1 || r.Released[0].SettledByID != p.ID || !r.Released[0].Amount.Equal(vnd("-1000000")) ||
			r.Released[0].ReversalOf == nil || !r.Closing.Amount.Equal(vnd("3000000")) || r.Obligation.ReversalDocNo != nil {
			t.Fatalf("reversal = %+v, want the payment released and the whole amount closed", r)
		}
		if got := h.payment(t, st, p.ID); got.Status != PaymentConfirmed || !got.Allocated.IsZero() {
			t.Errorf("payment = %+v, want confirmed and unallocated", got)
		}
		if n := h.count(t, st, `SELECT count(*) FROM obligation_settlements WHERE obligation_id = $1`, ob.ID); n != 3 {
			t.Errorf("settlements = %d, want the payment, its mirror and the closing one", n)
		}
		sum := h.partySummary(t, st, customer, Receivable)
		wantMoney(t, "open", sum.Open, "0")
		wantMoney(t, "advance", sum.Advance, "1000000")
		wantMoney(t, "cash fund keeps the money", h.fundBalance(t, st, st.cashFund), "1000000")

		testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
			snap, err := buildSnapshot(ctx, q, Statement{PartyID: customer, Direction: Receivable,
				PeriodFrom: h.today(t), PeriodTo: h.today(t)})
			if err != nil {
				return err
			}
			var kinds []string
			for _, l := range snap.Lines {
				kinds = append(kinds, l.Kind+" "+l.DocNo)
			}
			if !snap.ClosingBalance.IsZero() || !slices.Contains(kinds, SettledByReversal+" "+ob.SourceDocNo) {
				t.Errorf("statement closing %s, lines %v, want 0 with the reversal under the sale's number", snap.ClosingBalance, kinds)
			}
			return nil
		})
		h.verify(t, st)
	})

	t.Run("a document settled by a credit note cannot be reversed", func(t *testing.T) {
		ob := h.receivable(t, st, customer, "2000000", dayOf(time.October, 5))
		if err := h.source(t, st, func(ctx context.Context, tx posting.Tx) error {
			_, err := h.svc.CreditNotes().Record(ctx, tx, CreditNoteInput{
				PartyID: customer, Direction: Receivable, SourceType: CreditSalesReturn, SourceID: ids.New(),
				SourceDocNo: docNo("TH"), Amount: vnd("500000"), Targets: []uuid.UUID{ob.ID}, OnlyTargets: true,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		_, err := h.reverse(t, st, "", refOf(ob), "Huỷ đơn")
		wantErr(t, err, 409, "obligation_has_credits")
		if got := h.obligation(t, st, ob.ID); got.Status != ObligationOpen {
			t.Errorf("obligation = %+v, want untouched", got)
		}
		h.verify(t, st)
	})

	t.Run("bad references are refused", func(t *testing.T) {
		ob := h.receivable(t, st, customer, "100000", dayOf(time.October, 5))
		_, err := h.reverse(t, st, "", SourceRef{Direction: Receivable, SourceType: SourceSalesDocument, SourceID: ids.New()}, "x")
		wantErr(t, err, 422, "obligation_not_found")
		_, err = h.reverse(t, st, "", SourceRef{Direction: Receivable, SourceType: SourceDebtAdjustment, SourceID: ob.SourceID}, "x")
		wantErr(t, err, 422, "validation_failed")
		_, err = h.reverse(t, st, "", SourceRef{Direction: Payable, SourceType: SourceSalesDocument, SourceID: ob.SourceID}, "x")
		wantErr(t, err, 422, "validation_failed")
		_, err = h.reverse(t, st, "", refOf(ob), "  ")
		wantErr(t, err, 422, "validation_failed")
	})

	t.Run("the verifier catches a reversal mark without its settlement", func(t *testing.T) {
		ob := h.receivable(t, st, customer, "700000", dayOf(time.October, 5))
		testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
			if _, err := q.ExecContext(ctx, `UPDATE obligations SET reversed_on = business_date, reversal_reason = 'x',
				settled_amount = amount, status = 'settled' WHERE id = $1`, ob.ID); err != nil {
				return err
			}
			mismatches, err := Verify(ctx, q)
			if err != nil {
				return err
			}
			found := slices.ContainsFunc(mismatches, func(m string) bool {
				return strings.Contains(m, ob.SourceDocNo) && strings.Contains(m, "is reversed but not closed")
			})
			if !found {
				t.Errorf("mismatches = %v, want the reversal without its settlement", mismatches)
			}
			return nil
		})
	})
}

func TestSupplierAdvanceIsAllocatedToAReceiptInsideItsPosting(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	supplier := h.party(t, st, "Xưởng Minh Phát", "")
	adv := decoded[paymentBody](t)(h.svc.PostPayment(ctx, st.owner, newKey(), PaymentInput{
		Direction: Out, PartyID: &supplier, Purpose: PurposeAdvance, MethodID: st.cashMethod, Amount: vnd("2000000"),
	}, nil))
	receipt := func(amount, use string) (Obligation, PaymentResult, error) {
		var ob Obligation
		var used PaymentResult
		err := h.source(t, st, func(ctx context.Context, tx posting.Tx) error {
			r, err := h.svc.Payables().Record(ctx, tx, ObligationInput{PartyID: supplier, SourceType: SourceReceipt,
				SourceID: ids.New(), SourceDocNo: docNo("PN"), Amount: vnd(amount)})
			if err != nil {
				return err
			}
			ob = r.Obligation
			used, err = h.svc.Allocations().Record(ctx, tx, AllocationInput{PaymentID: adv.ID,
				Allocations: []Allocation{{ObligationID: ob.ID, Amount: vnd(use)}}})
			return err
		})
		return ob, used, err
	}

	ob, used, err := receipt("3000000", "1500000")
	if err != nil {
		t.Fatal(err)
	}
	if len(used.Settlements) != 1 || used.Settlements[0].AllocationMode != ModeManual || !used.Payment.AllocatedAmount.Equal(vnd("1500000")) {
		t.Fatalf("allocation = %+v, want one manual settlement and 1.500.000 allocated", used)
	}
	if got := h.obligation(t, st, ob.ID); got.Status != ObligationOpen || !got.Settled.Equal(vnd("1500000")) {
		t.Errorf("receipt payable = %+v, want 1.500.000 settled and still open", got)
	}

	_, _, err = receipt("1000000", "800000")
	wantErr(t, err, 422, "allocation_exceeds_payment")
	if n := h.count(t, st, `SELECT count(*) FROM obligations WHERE party_id = $1`, supplier); n != 1 {
		t.Errorf("payables = %d, want the refused receipt rolled back with its allocation", n)
	}
	_, _, err = receipt("1000000", "0")
	wantErr(t, err, 422, "validation_failed")

	sum := h.partySummary(t, st, supplier, Payable)
	wantMoney(t, "open payable", sum.Open, "1500000")
	wantMoney(t, "advance left", sum.Advance, "500000")
	wantMoney(t, "cash fund paid the advance once", h.fundBalance(t, st, st.cashFund), "-2000000")
	h.verify(t, st)
}
