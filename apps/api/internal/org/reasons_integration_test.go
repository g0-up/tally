//go:build integration

package org

import (
	"context"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func strPtr(s string) *string { return &s }

// seedReasons runs the default reason seeder for the actor's tenant.
func (h harness) seedReasons(t *testing.T, a audit.Actor) {
	t.Helper()
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		return ReasonDefaults{}.SeedReasons(context.Background(), q, SeedContext{TenantID: a.TenantID, Now: testkit.Epoch})
	})
}

func TestReasonDefaultsAreSeededOnce(t *testing.T) {
	h := newHarness(t, nil)
	a := h.provision(t, TenantSpec{})
	h.seedReasons(t, a)
	h.seedReasons(t, a)
	all, err := h.svc.Reasons(context.Background(), scopeFor(a), ReasonFilter{})
	if err != nil || len(all) != len(defaultReasons) {
		t.Fatalf("reasons = %d (%v), want %d", len(all), err, len(defaultReasons))
	}
	returns, err := h.svc.Reasons(context.Background(), scopeFor(a), ReasonFilter{Kind: "sales_return"})
	if err != nil || len(returns) != 5 {
		t.Fatalf("sales return reasons = %+v, %v", returns, err)
	}
	for _, r := range returns {
		if !r.IsSystem || !r.Active || r.Kind != "sales_return" {
			t.Fatalf("seeded reason: %+v", r)
		}
	}
	_, err = h.svc.Reasons(context.Background(), scopeFor(a), ReasonFilter{Kind: "exchange"})
	if apperr.KindOf(err) != apperr.Validation {
		t.Fatalf("unknown kind filter: %v", err)
	}
}

func TestReasonLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a := h.provision(t, TenantSpec{})
	h.seedReasons(t, a)

	// Every kind the table accepts can be created.
	for _, kind := range ReasonKinds {
		if _, err := h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr(kind), Name: strPtr("Khác " + kind)}); err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
	}
	_, err := h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("exchange"), Name: strPtr("Đổi mẫu")})
	wantCode(t, err, "validation_failed")
	_, err = h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("damage"), Name: strPtr("  ")})
	wantCode(t, err, "validation_failed")
	// Names are unique per kind, accent- and case-insensitively; another kind may reuse one.
	_, err = h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("damage"), Name: strPtr("HONG")})
	wantCode(t, err, "name_taken")
	if _, err := h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("write_off"), Name: strPtr("Hỏng")}); err != nil {
		t.Fatal(err)
	}

	cracked, err := h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("damage"), Name: strPtr("Vỡ khi vận chuyển")})
	if err != nil || !cracked.Active || cracked.IsSystem {
		t.Fatalf("create: %+v, %v", cracked, err)
	}
	renamed, err := h.svc.UpdateReason(ctx, a, cracked.ID, ReasonInput{Name: strPtr("Vỡ khi giao"), Active: new(bool)})
	if err != nil || renamed.Name != "Vỡ khi giao" || renamed.Active {
		t.Fatalf("update: %+v, %v", renamed, err)
	}
	_, err = h.svc.UpdateReason(ctx, a, cracked.ID, ReasonInput{Kind: strPtr("loss")})
	wantCode(t, err, "validation_failed")
	active, _ := h.svc.Reasons(ctx, scopeFor(a), ReasonFilter{Kind: "damage", ActiveOnly: true})
	for _, r := range active {
		if r.ID == cracked.ID {
			t.Fatal("a deactivated reason is listed as active")
		}
	}

	// A built-in reason can be renamed and deactivated but never deleted.
	system, _ := h.svc.Reasons(ctx, scopeFor(a), ReasonFilter{Kind: "loss"})
	var lossID = system[0].ID
	for _, r := range system {
		if r.IsSystem {
			lossID = r.ID
		}
	}
	wantCode(t, h.svc.DeleteReason(ctx, a, lossID), "read_only")
	if _, err := h.svc.UpdateReason(ctx, a, lossID, ReasonInput{Active: new(bool)}); err != nil {
		t.Fatal(err)
	}

	// A reason a document uses is kept; an unused one goes.
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO stock_adjustments (id, tenant_id, warehouse_id, doc_no, kind, reason_id, business_date, idempotency_key,
			                               posted_at, posted_by, created_at)
			SELECT $1, $2, w.id, 'DC-0001', 'damage', $3, '2026-03-15', $4, $5, $6, $5 FROM warehouses w`,
			ids.New(), a.TenantID, cracked.ID, ids.New().String(), testkit.Epoch, a.ID)
		return err
	})
	wantCode(t, h.svc.DeleteReason(ctx, a, cracked.ID), "reason_in_use")
	unused, err := h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("gift"), Name: strPtr("Tặng đối tác")})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteReason(ctx, a, unused.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, h.svc.DeleteReason(ctx, a, unused.ID), "not_found")
}

func TestReasonsAreTenantIsolated(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	a, b := h.provision(t, TenantSpec{}), h.provision(t, TenantSpec{})
	r, err := h.svc.CreateReason(ctx, a, ReasonInput{Kind: strPtr("damage"), Name: strPtr("Vỡ")})
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := h.svc.Reasons(ctx, scopeFor(b), ReasonFilter{}); len(list) != 0 {
		t.Fatalf("tenant b sees %+v", list)
	}
	_, err = h.svc.UpdateReason(ctx, b, r.ID, ReasonInput{Name: strPtr("x")})
	wantCode(t, err, "not_found")
	wantCode(t, h.svc.DeleteReason(ctx, b, r.ID), "not_found")
	// The same name in another tenant is not a clash.
	if _, err := h.svc.CreateReason(ctx, b, ReasonInput{Kind: strPtr("damage"), Name: strPtr("Vỡ")}); err != nil {
		t.Fatal(err)
	}
}
