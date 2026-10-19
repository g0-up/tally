//go:build integration

package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// startLoyalty saves a store's first loyalty program: a point per 1,000đ of net revenue, each
// point worth 100đ.
func startLoyalty(t *testing.T, b *browser) {
	t.Helper()
	expect(t, b.doWith(http.MethodPut, v1+"/loyalty/program", map[string]any{
		"enabled": true, "points_per_amount": "0.001", "point_value": "100"}, http.Header{"If-Match": {`"0"`}}),
		http.StatusOK, "")
}

func newLoyaltyCustomer(t *testing.T, b *browser, name string) uuid.UUID {
	t.Helper()
	return send[withID](t, b, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": name}).ID
}

func loyaltyOf(t *testing.T, b *browser, party uuid.UUID) loyaltyOut {
	t.Helper()
	return send[loyaltyOut](t, b, http.MethodGet, "/customers/"+party.String()+"/loyalty", nil)
}

// pointsBy sums a customer's loyalty entries by source type.
func (l loyaltyOut) pointsBy(t *testing.T, sourceType string) decimal.Decimal {
	t.Helper()
	sum := decimal.Zero
	for _, e := range l.Entries {
		if e.SourceType == sourceType {
			sum = sum.Add(decimal.RequireFromString(e.Points))
		}
	}
	return sum
}

func wantPoints(t *testing.T, what string, got decimal.Decimal, want int64) {
	t.Helper()
	if !got.Equal(decimal.NewFromInt(want)) {
		t.Errorf("%s = %s points, want %d", what, got, want)
	}
}

func wantBalance(t *testing.T, l loyaltyOut, want int64) {
	t.Helper()
	wantPoints(t, "balance", decimal.RequireFromString(l.Balance), want)
}

func returnOne(t *testing.T, b *browser, st salesStore, sale salesDocOut) {
	t.Helper()
	expect(t, post(b, "/sales-returns", map[string]any{"sales_document_id": sale.ID, "reason_id": st.returnReason,
		"lines": []map[string]any{{"sales_document_line_id": sale.Lines[0].ID, "qty": "1"}}}), http.StatusCreated, "")
}

func reverseSale(t *testing.T, b *browser, sale salesDocOut) {
	t.Helper()
	confirmPassword(t, b)
	expect(t, post(b, "/sales-documents/"+sale.ID.String()+":reverse", map[string]any{"reason": "Bán nhầm khách"}),
		http.StatusCreated, "")
}

func TestLoyaltyProgramSavesOnlyOverTheVersionItWasRead(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	program := map[string]any{"enabled": true, "points_per_amount": "0.001", "point_value": "100"}
	put := func(ifMatch string) *http.Response {
		h := http.Header{}
		if ifMatch != "" {
			h.Set("If-Match", ifMatch)
		}
		return a.doWith(http.MethodPut, v1+"/loyalty/program", program, h).Result()
	}

	if got := put(""); got.StatusCode != http.StatusPreconditionRequired {
		t.Errorf("save without If-Match = %d, want 428", got.StatusCode)
	}
	first := put(`"0"`)
	if first.StatusCode != http.StatusOK || first.Header.Get("ETag") != `"1"` {
		t.Fatalf("first save = %d ETag %s, want 200 at version 1", first.StatusCode, first.Header.Get("ETag"))
	}
	if got := put(`"0"`); got.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("a second first save = %d, want 412", got.StatusCode)
	}
	read := a.do(http.MethodGet, v1+"/loyalty/program", nil)
	if got := put(read.Header().Get("ETag")); got.StatusCode != http.StatusOK || got.Header.Get("ETag") != `"2"` {
		t.Errorf("save over the read version = %d ETag %s, want 200 at version 2", got.StatusCode, got.Header.Get("ETag"))
	}
}

func TestLoyaltyPointsSettleWhicheverEventIsHandledFirst(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "TRA-XANH", "10000")
	st.receive(t, a, sku, nil, "50", "6000")
	startLoyalty(t, a)
	s.drainOutbox(t, st.tenant)
	// Pending sales are handled after everything else, as when the worker retries them later.
	postponeSales := func() {
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			_, err := q.ExecContext(context.Background(), `
				UPDATE outbox_events SET available_at = available_at + interval '1 hour'
				WHERE topic = 'sale.posted' AND processed_at IS NULL`)
			return err
		})
	}

	t.Run("a return handled before its sale still takes back its share", func(t *testing.T) {
		customer := newLoyaltyCustomer(t, a, "Anh Bình")
		sale := creditSale(t, a, customer, saleLine(sku, "10", nil))
		expect(t, post(a, "/sales-returns", map[string]any{"sales_document_id": sale.ID, "reason_id": st.returnReason,
			"lines": []map[string]any{{"sales_document_line_id": sale.Lines[0].ID, "qty": "5"}}}), http.StatusCreated, "")
		postponeSales()
		s.drainOutbox(t, st.tenant)
		got := loyaltyOf(t, a, customer)
		wantBalance(t, got, 50)
		wantPoints(t, "earned", got.pointsBy(t, "sales_document"), 100)
		wantPoints(t, "taken back by the return", got.pointsBy(t, "sales_return"), -50)
	})

	t.Run("a reversal handled before its sale leaves no points", func(t *testing.T) {
		customer := newLoyaltyCustomer(t, a, "Chị Hoa")
		sale := creditSale(t, a, customer, saleLine(sku, "3", nil))
		reverseSale(t, a, sale)
		postponeSales()
		s.drainOutbox(t, st.tenant)
		if got := loyaltyOf(t, a, customer); len(got.Entries) != 0 {
			t.Errorf("loyalty of a reversed sale = %+v, want no entries", got)
		}
	})

	t.Run("returning a sale in thirds takes back all its points", func(t *testing.T) {
		odd := st.newSku(t, a, "TRA-SEN", "3334")
		st.receive(t, a, odd, nil, "3", "2000")
		customer := newLoyaltyCustomer(t, a, "Cô Lan")
		sale := creditSale(t, a, customer, saleLine(odd, "3", nil))
		s.drainOutbox(t, st.tenant)
		wantBalance(t, loyaltyOf(t, a, customer), 10)
		for range 3 {
			returnOne(t, a, st, sale)
			s.drainOutbox(t, st.tenant)
		}
		got := loyaltyOf(t, a, customer)
		wantBalance(t, got, 0)
		wantPoints(t, "taken back by the returns", got.pointsBy(t, "sales_return"), -10)
	})
}

type loyaltySaleOut struct {
	Document salesDocOut `json:"sales_document"`
	Loyalty  *struct {
		Points string `json:"points"`
		Amount string `json:"amount"`
	} `json:"loyalty_redemption"`
}

func TestLoyaltyPointsPayForPartOfASale(t *testing.T) {
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "BANH-QUY", "10000")
	st.receive(t, a, sku, nil, "20", "6000")
	startLoyalty(t, a)
	customer := newLoyaltyCustomer(t, a, "Anh Tùng")
	expect(t, post(a, "/loyalty/adjustments", map[string]any{"party_id": customer, "points": "500",
		"reason": "Điểm chuyển từ sổ cũ"}), http.StatusCreated, "")
	s.drainOutbox(t, st.tenant)
	sell := func(points string) map[string]any {
		return map[string]any{"customer_party_id": customer, "lines": []map[string]any{saleLine(sku, "2", nil)},
			"payments": []map[string]any{}, "loyalty_points": points}
	}

	t.Run("points that cannot be spent are refused and none are taken", func(t *testing.T) {
		walkIn := st.cashSale("20000", saleLine(sku, "2", nil))
		walkIn["loyalty_points"] = "10"
		expect(t, post(a, "/pos/checkout", walkIn), http.StatusUnprocessableEntity, "customer_required")
		expect(t, post(a, "/pos/checkout", sell("1.5")), http.StatusUnprocessableEntity, "must_be_positive_whole")
		expect(t, post(a, "/pos/checkout", sell("600")), http.StatusConflict, "insufficient_points")
		// 300 points are worth 30,000đ, more than the 20,000đ sale.
		expect(t, post(a, "/pos/checkout", sell("300")), http.StatusUnprocessableEntity, "exceeds_amount")
		if got := loyaltyOf(t, a, customer); got.Balance != "500" || len(got.Entries) != 1 {
			t.Errorf("loyalty after refused checkouts = %+v, want 500 points untouched", got)
		}
	})

	t.Run("spent points discount the sale once and a return gives back its share", func(t *testing.T) {
		key := http.Header{"Idempotency-Key": {ids.New().String()}}
		rec := a.doWith(http.MethodPost, v1+"/pos/checkout", sell("50"), key)
		expect(t, rec, http.StatusCreated, "")
		sale := testkit.DecodeJSON[loyaltySaleOut](t, rec)
		if sale.Loyalty == nil || sale.Loyalty.Points != "50" || !decimal.RequireFromString(sale.Loyalty.Amount).Equal(decimal.NewFromInt(5000)) {
			t.Fatalf("redemption = %+v, want 50 points worth 5000", sale.Loyalty)
		}
		wantPoints(t, "sale total", decimal.RequireFromString(sale.Document.TotalAmount), 15000)
		wantBalance(t, loyaltyOf(t, a, customer), 450)

		again := a.doWith(http.MethodPost, v1+"/pos/checkout", sell("50"), key)
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "true" || testkit.DecodeJSON[loyaltySaleOut](t, again).Document.ID != sale.Document.ID {
			t.Errorf("double submit = %s, want a replay of %s", again.Header(), sale.Document.ID)
		}
		s.drainOutbox(t, st.tenant)
		// The 15,000đ the customer paid earns 15 points.
		wantBalance(t, loyaltyOf(t, a, customer), 465)

		returnOne(t, a, st, sale.Document)
		s.drainOutbox(t, st.tenant)
		got := loyaltyOf(t, a, customer)
		wantPoints(t, "given back by the return", got.pointsBy(t, "sales_return_redemption"), 25)
		wantPoints(t, "taken back by the return", got.pointsBy(t, "sales_return"), -8)
		wantBalance(t, got, 482)
	})

	t.Run("a reversed sale gives back every point spent on it", func(t *testing.T) {
		before := decimal.RequireFromString(loyaltyOf(t, a, customer).Balance)
		sale := checkout(t, a, sell("100")).Document
		s.drainOutbox(t, st.tenant)
		reverseSale(t, a, sale)
		s.drainOutbox(t, st.tenant)
		got := loyaltyOf(t, a, customer)
		wantPoints(t, "given back by the reversal", got.pointsBy(t, "sales_document_reversal_redemption"), 100)
		wantPoints(t, "balance after the reversal", decimal.RequireFromString(got.Balance), before.IntPart())
	})
}
