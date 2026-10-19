//go:build integration

package cash

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func vnd(s string) money.Money {
	m, err := money.NewMoney(s)
	if err != nil {
		panic(err)
	}
	return m
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

type store struct {
	d        *db.DB
	clk      *clock.Fixed
	svc      *Service
	tenantID uuid.UUID
	owner    audit.Actor
	cashFund FundAccount
	bankFund FundAccount
}

func (st store) scope() Scope { return Scope{TenantID: st.tenantID} }

func newStore(t *testing.T, d *db.DB) store {
	t.Helper()
	clk := clock.NewFixed(time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC))
	tn := testkit.NewTenant(t, d)
	st := store{
		d: d, clk: clk, svc: NewService(Deps{DB: d, Clock: clk, Audit: audit.NewRecorder(clk)}), tenantID: tn.ID,
		owner: audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb},
	}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		return NewSeeder().SeedFunds(context.Background(), q, SeedContext{TenantID: tn.ID, Now: clk.Now()})
	})
	funds, err := st.svc.Funds(context.Background(), st.scope())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range funds {
		switch f.Kind {
		case FundCash:
			st.cashFund = f
		case FundBank:
			st.bankFund = f
		}
	}
	if st.cashFund.ID == uuid.Nil || st.bankFund.ID == uuid.Nil {
		t.Fatalf("seeded funds = %+v, want a cash and a bank fund", funds)
	}
	return st
}

// write appends one entry through the writer, as a posting would.
func (st store) write(t *testing.T, fund uuid.UUID, amount, category string, day int, reversalOf *uuid.UUID) Entry {
	t.Helper()
	var out Entry
	testkit.InTenant(t, st.d, st.tenantID, func(q db.Querier) (err error) {
		ctx := context.Background()
		if err := LockAllFunds(ctx, q); err != nil {
			return err
		}
		out, err = NewWriter().Write(ctx, q, st.tenantID, EntryInput{
			FundAccountID: fund, Amount: vnd(amount), Category: category, BusinessDate: clock.Date(2026, time.September, day),
			PostedAt: st.clk.Now(), PostedBy: st.owner.ID, ReversalOf: reversalOf,
		})
		return err
	})
	return out
}

func (st store) fund(t *testing.T, id uuid.UUID) FundAccount {
	t.Helper()
	f, err := st.svc.Fund(context.Background(), st.scope(), id)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (st store) verify(t *testing.T) []string {
	t.Helper()
	var out []string
	testkit.InTenant(t, st.d, st.tenantID, func(q db.Querier) (err error) {
		out, err = Verify(context.Background(), q)
		return err
	})
	return out
}

func TestSeededFundsAndMethods(t *testing.T) {
	d := testkit.DB(t)
	st := newStore(t, d)
	ctx := context.Background()
	methods, err := st.svc.Methods(ctx, st.scope())
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]PaymentMethod{}
	for _, m := range methods {
		byCode[m.Code] = m
	}
	if m := byCode[MethodCash]; m.RequiresVerification || m.FundAccountID != st.cashFund.ID {
		t.Errorf("cash method = %+v, want the cash fund without verification", m)
	}
	if m := byCode[MethodBankTransfer]; !m.RequiresVerification || m.FundAccountID != st.bankFund.ID {
		t.Errorf("transfer method = %+v, want the bank fund with verification", m)
	}

	testkit.InTenant(t, d, st.tenantID, func(q db.Querier) error {
		return NewSeeder().SeedFunds(ctx, q, SeedContext{TenantID: st.tenantID, Now: st.clk.Now()})
	})
	funds, err := st.svc.Funds(ctx, st.scope())
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.svc.Methods(ctx, st.scope())
	if err != nil || len(funds) != 2 || len(again) != len(methods) {
		t.Fatalf("after a second seed: %d funds, %d methods (was %d), %v", len(funds), len(again), len(methods), err)
	}
}

func TestOpeningBalanceIsEditableOnlyBeforeTheFirstEntry(t *testing.T) {
	d := testkit.DB(t)
	st := newStore(t, d)
	ctx := context.Background()
	opening := vnd("2000000")
	day := clock.Date(2026, time.September, 1)

	f, err := st.svc.UpdateFund(ctx, st.owner, st.cashFund.ID, st.cashFund.Version, FundInput{
		OpeningBalance: &opening, OpeningDate: Opt[time.Time]{Set: true, Value: &day},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "opening", f.OpeningBalance, "2000000")
	wantMoney(t, "balance", f.Balance, "2000000")
	if f.Version != st.cashFund.Version+1 {
		t.Errorf("version = %d, want %d", f.Version, st.cashFund.Version+1)
	}

	name := "Két quầy"
	_, err = st.svc.UpdateFund(ctx, st.owner, st.cashFund.ID, st.cashFund.Version, FundInput{Name: &name})
	wantErr(t, err, 412, "precondition_failed")
	bank := "VCB"
	_, err = st.svc.UpdateFund(ctx, st.owner, st.cashFund.ID, f.Version, FundInput{BankName: Opt[string]{Set: true, Value: &bank}})
	wantErr(t, err, 422, "validation_failed")

	st.write(t, st.cashFund.ID, "500000", CategorySales, 2, nil)
	more := vnd("3000000")
	_, err = st.svc.UpdateFund(ctx, st.owner, st.cashFund.ID, f.Version, FundInput{OpeningBalance: &more})
	wantErr(t, err, 409, "fund_has_entries")
	if f, err = st.svc.UpdateFund(ctx, st.owner, st.cashFund.ID, f.Version, FundInput{Name: &name}); err != nil || f.Name != name {
		t.Fatalf("rename after entries = %+v, %v", f, err)
	}
	wantMoney(t, "balance", st.fund(t, st.cashFund.ID).Balance, "2500000")
	if m := st.verify(t); len(m) != 0 {
		t.Errorf("verify: %v", m)
	}
}

func TestCashFlowByCategoryAndDate(t *testing.T) {
	d := testkit.DB(t)
	st := newStore(t, d)
	ctx := context.Background()
	opening := vnd("1000000")
	if _, err := st.svc.UpdateFund(ctx, st.owner, st.cashFund.ID, st.cashFund.Version, FundInput{OpeningBalance: &opening}); err != nil {
		t.Fatal(err)
	}
	st.write(t, st.cashFund.ID, "400000", CategorySales, 5, nil)
	sale := st.write(t, st.cashFund.ID, "700000", CategorySales, 10, nil)
	st.write(t, st.cashFund.ID, "-300000", CategorySupplierPayment, 11, nil)
	st.write(t, st.cashFund.ID, "-700000", CategorySales, 12, &sale.ID)
	st.write(t, st.bankFund.ID, "2000000", CategoryDebtCollection, 12, nil)

	flows, err := st.svc.CashFlow(ctx, st.scope(), clock.Date(2026, time.September, 10), clock.Date(2026, time.September, 12))
	if err != nil {
		t.Fatal(err)
	}
	byFund := map[uuid.UUID]FundFlow{}
	for _, f := range flows {
		byFund[f.Fund.ID] = f
	}
	c := byFund[st.cashFund.ID]
	wantMoney(t, "cash opening", c.Opening, "1400000")
	wantMoney(t, "cash inflow", c.Inflow, "700000")
	wantMoney(t, "cash outflow", c.Outflow, "1000000")
	wantMoney(t, "cash closing", c.Closing, "1100000")
	nets := map[string]string{}
	for _, cat := range c.Categories {
		nets[cat.Category] = cat.Net.String()
	}
	if nets[CategorySales] != vnd("0").String() || nets[CategorySupplierPayment] != vnd("-300000").String() {
		t.Errorf("categories = %v, want sales netted to 0 and supplier payments -300,000", nets)
	}
	wantMoney(t, "closing equals the fund balance", c.Closing, st.fund(t, st.cashFund.ID).Balance.String())
	wantMoney(t, "bank closing", byFund[st.bankFund.ID].Closing, "2000000")

	_, err = st.svc.CashFlow(ctx, st.scope(), clock.Date(2026, time.September, 12), clock.Date(2026, time.September, 10))
	wantErr(t, err, 422, "validation_failed")
}

func TestVerifierAndAppendOnlyEntries(t *testing.T) {
	d := testkit.DB(t)
	st := newStore(t, d)
	ctx := context.Background()
	sale := st.write(t, st.cashFund.ID, "500000", CategorySales, 5, nil)
	if m := st.verify(t); len(m) != 0 {
		t.Fatalf("verify on a clean store: %v", m)
	}

	testkit.InTenant(t, d, st.tenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `SAVEPOINT denied`); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE cash_entries SET amount = 1 WHERE id = $1`, sale.ID)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("update of a cash entry = %v, want permission denied", err)
		}
		_, err = q.ExecContext(ctx, `ROLLBACK TO SAVEPOINT denied`)
		return err
	})

	st.write(t, st.cashFund.ID, "-400000", CategorySales, 6, &sale.ID)
	testkit.InTenant(t, d, st.tenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `UPDATE fund_accounts SET balance = balance + 1 WHERE id = $1`, st.bankFund.ID)
		return err
	})
	got := strings.Join(st.verify(t), "\n")
	for _, want := range []string{
		fmt.Sprintf("fund %s", st.bankFund.ID),
		"does not mirror its original",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("verify = %q, want a line with %q", got, want)
		}
	}
}

func TestFundsBelongToOneTenant(t *testing.T) {
	d := testkit.DB(t)
	a, b := newStore(t, d), newStore(t, d)
	ctx := context.Background()
	_, err := b.svc.Fund(ctx, b.scope(), a.cashFund.ID)
	wantErr(t, err, 404, "not_found")
	name := "Két của người khác"
	_, err = b.svc.UpdateFund(ctx, b.owner, a.cashFund.ID, a.cashFund.Version, FundInput{Name: &name})
	wantErr(t, err, 404, "not_found")
	if f := a.fund(t, a.cashFund.ID); f.Name == name {
		t.Error("tenant b renamed tenant a's fund")
	}
}
