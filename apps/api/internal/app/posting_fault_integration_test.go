//go:build integration

package app_test

import (
	"maps"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/platform/fault"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// postingTrace counts every row a posting writes, so a failed posting can be shown to leave none.
var postingTrace = map[string]string{
	"sales documents":   `SELECT count(*) FROM sales_documents`,
	"sales lines":       `SELECT count(*) FROM sales_document_lines`,
	"stock moves":       `SELECT count(*) FROM stock_moves`,
	"payments":          `SELECT count(*) FROM payments`,
	"cash entries":      `SELECT count(*) FROM cash_entries`,
	"shifts":            `SELECT count(*) FROM shifts`,
	"audit events":      `SELECT count(*) FROM audit_events`,
	"outbox events":     `SELECT count(*) FROM outbox_events`,
	"idempotency keys":  `SELECT count(*) FROM idempotency_keys`,
	"numbers taken":     `SELECT COALESCE(sum(next_value), 0) FROM doc_sequences`,
	"on hand (x1000)":   `SELECT COALESCE(sum(on_hand * 1000), 0)::bigint FROM sku_stock`,
	"stock value (VND)": `SELECT COALESCE(sum(stock_value), 0)::bigint FROM sku_stock`,
}

func tracePostings(t *testing.T, tenant uuid.UUID) map[string]int {
	t.Helper()
	out := make(map[string]int, len(postingTrace))
	for name, query := range postingTrace {
		out[name] = countRows(t, tenant, query)
	}
	return out
}

// A server that fails in the middle of a posting, after every row, audit event, outbox event and
// the idempotency record were written, leaves nothing behind; the client's retry with the same
// Idempotency-Key then posts the sale exactly once, and later retries replay it.
func TestPostingThatFailsBeforeCommitIsAllOrNothing(t *testing.T) {
	faults, err := fault.Parse(fault.TestEnv, string(fault.PostingBeforeCommit)+"@"+sales.ScopePOSCheckout)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newSalesStack(t, app.WithFaults(faults))
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "AO-THUN", "120000")
	// The receipt is another scope: the armed fault waits for the checkout.
	st.receive(t, a, sku, nil, "5", "70000")
	if !faults.Armed() {
		t.Fatal("the receipt consumed the fault armed for the checkout")
	}

	before := tracePostings(t, st.tenant)
	body := st.cashSale("240000", saleLine(sku, "2", nil))
	key := http.Header{"Idempotency-Key": {ids.New().String()}}

	failed := a.doWith(http.MethodPost, v1+"/pos/checkout", body, key)
	expect(t, failed, http.StatusInternalServerError, "internal_error")
	if faults.Armed() {
		t.Fatal("the checkout did not reach the failure point")
	}
	if after := tracePostings(t, st.tenant); !maps.Equal(before, after) {
		t.Fatalf("a failed posting left rows behind:\nbefore %v\nafter  %v", before, after)
	}
	verifySalesLedgers(t, st.tenant)

	retried := a.doWith(http.MethodPost, v1+"/pos/checkout", body, key)
	if retried.Code != http.StatusCreated {
		t.Fatalf("retry with the same key = %d %s", retried.Code, retried.Body)
	}
	sale := testkit.DecodeJSON[posSaleOut](t, retried)
	replayed := a.doWith(http.MethodPost, v1+"/pos/checkout", body, key)
	if replayed.Code != http.StatusCreated ||
		testkit.DecodeJSON[posSaleOut](t, replayed).Document.ID != sale.Document.ID {
		t.Errorf("second retry = %d %s, want the stored response of %s", replayed.Code, replayed.Body, sale.Document.DocNo)
	}

	if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != 1 {
		t.Errorf("sales documents = %d, want exactly 1", n)
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM idempotency_keys WHERE scope = $1`, sales.ScopePOSCheckout); n != 1 {
		t.Errorf("checkout idempotency keys = %d, want 1", n)
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM stock_moves WHERE sku_id = $1 AND qty < 0`, sku); n != 1 {
		t.Errorf("issuing stock moves = %d, want 1", n)
	}
	// The failed attempt's document number rolled back with it: the retry took the first one.
	if n := countRows(t, st.tenant, `SELECT next_value - 1 FROM doc_sequences WHERE prefix = $1`,
		sale.Document.DocNo[:2]); n != 1 {
		t.Errorf("%s numbers taken = %d, want 1 (no gap left by the failed attempt)", sale.Document.DocNo, n)
	}
	if got := stockOf(t, st.tenant, sku); got.OnHand.String() != "3" {
		t.Errorf("on hand = %s, want 3", got.OnHand)
	}
	verifySalesLedgers(t, st.tenant)
}
