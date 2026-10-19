//go:build integration

package docseq_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func next(t *testing.T, d *db.DB, iss *docseq.Issuer, tenant testkit.Tenant, prefix string) string {
	t.Helper()
	var no string
	testkit.InTenant(t, d, tenant.ID, func(q db.Querier) error {
		var err error
		no, err = iss.Next(context.Background(), q, tenant.ID, prefix)
		return err
	})
	return no
}

func TestNextIsSequentialPerTenantAndPrefix(t *testing.T) {
	d := testkit.DB(t)
	iss := docseq.NewIssuer()
	a, b := testkit.NewTenant(t, d), testkit.NewTenant(t, d)
	got := []string{
		next(t, d, iss, a, "PN"), next(t, d, iss, a, "PN"), next(t, d, iss, a, "HD"),
		next(t, d, iss, b, "PN"), next(t, d, iss, a, "PN"),
	}
	want := []string{"PN-0001", "PN-0002", "HD-0001", "PN-0001", "PN-0003"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRolledBackNumberIsReissued(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	iss := docseq.NewIssuer()
	a := testkit.NewTenant(t, d)
	next(t, d, iss, a, "XK")
	boom := errors.New("posting failed")
	err := d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
		if no, err := iss.Next(ctx, q, a.ID, "XK"); err != nil || no != "XK-0002" {
			t.Fatalf("got %q, %v", no, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if no := next(t, d, iss, a, "XK"); no != "XK-0002" {
		t.Fatalf("after rollback got %q, want XK-0002 (gap-free)", no)
	}
}

func TestInvalidPrefix(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	err := d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
		_, err := docseq.NewIssuer().Next(ctx, q, a.ID, "pn")
		return err
	})
	if apperr.KindOf(err) != apperr.Validation {
		t.Fatalf("err = %v", err)
	}
}

func TestFiftyParallelCallsGetConsecutiveNumbers(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	iss := docseq.NewIssuer()
	a := testkit.NewTenant(t, d)

	const n = 50
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			errs[i] = d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
				no, err := iss.Next(ctx, q, a.ID, "BH")
				results[i] = no
				return err
			})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	sort.Strings(results)
	for i, no := range results {
		if want := fmt.Sprintf("BH-%04d", i+1); no != want {
			t.Fatalf("sorted result %d = %q, want %q (all: %v)", i, no, want, results)
		}
	}
}

func TestNextReportsDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	iss := docseq.NewIssuer()
	a := testkit.NewTenant(t, d)

	t.Run("read-only transaction cannot create the counter", func(t *testing.T) {
		err := d.WithReadOnlyTenantTx(ctx, a.ID, func(q db.Querier) error {
			_, err := iss.Next(ctx, q, a.ID, "RO")
			return err
		})
		if err == nil || apperr.KindOf(err) != apperr.Internal {
			t.Fatalf("err = %v, want an internal database error", err)
		}
	})

	t.Run("exhausted counter overflows", func(t *testing.T) {
		testkit.InTenant(t, d, a.ID, func(q db.Querier) error {
			_, err := q.ExecContext(ctx, `
				INSERT INTO doc_sequences (tenant_id, prefix, next_value) VALUES ($1, 'OV', 9223372036854775807)`, a.ID)
			return err
		})
		err := d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
			_, err := iss.Next(ctx, q, a.ID, "OV")
			return err
		})
		if err == nil || apperr.KindOf(err) != apperr.Internal {
			t.Fatalf("err = %v, want an internal database error", err)
		}
	})
}
