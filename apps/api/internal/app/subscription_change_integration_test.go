//go:build integration

package app_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/saas"
)

// addonAIQAPlanID is the seeded AI Q&A add-on plan.
const addonAIQAPlanID = "00000000-0000-7000-8000-000000000006"

// The owner buys an add-on with the plan change command: it needs a fresh password, a retry with
// the same Idempotency-Key answers the first invoice without issuing another, and the paid add-on
// enables its module.
func TestBuyingAnAddonNeedsReauthAndRunsOncePerKey(t *testing.T) {
	s := newControlStack(t)
	b := s.signIn(t, s.signUp(t))
	tenant := tenantOf(t, b)
	if before := send[saas.Entitlements](t, b, http.MethodGet, "/entitlements", nil); before.Modules[saas.ModuleAIQA] {
		t.Fatal("the trial already includes the AI Q&A")
	}

	body := map[string]any{"plan_id": addonAIQAPlanID, "accept_terms": true}
	quote := send[struct {
		Purpose string `json:"purpose"`
	}](t, b, http.MethodPost, "/subscription:preview-change", body)
	if quote.Purpose != saas.PurposeAddon {
		t.Fatalf("add-on quote purpose = %q, want %q", quote.Purpose, saas.PurposeAddon)
	}

	key := http.Header{"Idempotency-Key": {ids.New().String()}}
	s.clk.Advance(middleware.ReauthWindow + time.Minute)
	expect(t, b.doWith(http.MethodPost, v1+"/subscription:change", body, key), http.StatusForbidden, "reauth_required")
	expect(t, b.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")

	first := b.doWith(http.MethodPost, v1+"/subscription:change", body, key)
	expect(t, first, http.StatusCreated, "")
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Error("the first purchase is flagged as a replay")
	}
	inv := jsonOf(t, first)
	// The seeded add-on is free, so its invoice is paid at once.
	if inv["purpose"] != saas.PurposeAddon || inv["plan_id"] != addonAIQAPlanID || inv["status"] != saas.InvoicePaid {
		t.Fatalf("add-on invoice = %s", first.Body)
	}

	again := b.doWith(http.MethodPost, v1+"/subscription:change", body, key)
	expect(t, again, http.StatusCreated, "")
	if again.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("retry is not flagged as a replay: %v", again.Header())
	}
	if got := jsonOf(t, again); !reflect.DeepEqual(got, inv) {
		t.Errorf("replayed body differs:\n first: %s\nreplay: %s", first.Body, again.Body)
	}
	other := map[string]any{"plan_id": storePlanID, "accept_terms": true}
	expect(t, b.doWith(http.MethodPost, v1+"/subscription:change", other, key), http.StatusConflict, idempotency.CodeKeyReused)
	expect(t, b.doWith(http.MethodPost, v1+"/subscription:change", map[string]any{"plan_id": addonAIQAPlanID}, nil),
		http.StatusUnprocessableEntity, saas.CodeTermsNotAccepted)

	issued := `SELECT count(*) FROM audit_events WHERE action = 'saas_invoice.issued'`
	if n := countRows(t, tenant, issued); n != 1 {
		t.Errorf("issued audit events = %d, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM tally.saas_invoices WHERE tenant_id = $1 AND purpose = 'addon'`, tenant); n != 1 {
		t.Errorf("add-on invoices = %d, want 1", n)
	}
	if after := send[saas.Entitlements](t, b, http.MethodGet, "/entitlements", nil); !after.Modules[saas.ModuleAIQA] {
		t.Errorf("entitlements after the add-on = %+v, want the AI Q&A", after.Modules)
	}

	// Without a key the command stays usable, and every request issues.
	expect(t, b.do(http.MethodPost, v1+"/subscription:change", body), http.StatusCreated, saas.PurposeAddon)
	if n := countRows(t, tenant, issued); n != 2 {
		t.Errorf("issued audit events after an unkeyed purchase = %d, want 2", n)
	}
}
