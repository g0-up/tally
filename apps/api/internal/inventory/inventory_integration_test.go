//go:build integration

package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/platform/periodlock"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/testkit"
	"github.com/g0-up/tally/apps/api/migrations"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func key(v string) idempotency.Key {
	h := sha256.Sum256([]byte(v))
	return idempotency.Key{Value: v, RequestHash: h[:]}
}

func newKey() idempotency.Key { return key(ids.New().String()) }

// testHash is the payload hash the service-level tests bind their confirmations to.
var testHash = func() []byte { h := sha256.Sum256([]byte(`{"test":true}`)); return h[:] }()

type harness struct {
	d   *db.DB
	clk *clock.Fixed
	svc *Service
	exc *exception.Guard
}

func newHarness(t *testing.T) harness {
	t.Helper()
	d := testkit.DB(t)
	clk := testkit.Clock()
	rec := audit.NewRecorder(clk)
	coord, err := posting.NewCoordinator(posting.Deps{
		DB: d, Idempotency: idempotency.NewGuard(clk), Docs: docseq.NewIssuer(), Periods: periodlock.NewGuard(),
		Audit: rec, Outbox: outbox.NewPublisher(clk), Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterLockers(coord, clk)
	exc := exception.NewGuard(clk)
	svc := NewService(Deps{DB: d, Clock: clk, Audit: rec, Posting: coord, Exceptions: exc})
	return harness{d: d, clk: clk, svc: svc, exc: exc}
}

// store is a seeded tenant: one warehouse, the units cái and hộp, the colors xanh and đỏ, a
// plain SKU (1 hộp = 10 cái), a SKU sold by color and a SKU tracking lots and expiry, and the
// reasons the adjustments cite.
type store struct {
	tenantID, warehouse            uuid.UUID
	slug                           string
	owner                          audit.Actor
	cai, hop                       uuid.UUID
	xanh, do                       uuid.UUID
	plain, colored, lotted         uuid.UUID
	damage, disposal, varianceKind uuid.UUID
}

func (st store) scope() Scope { return Scope{TenantID: st.tenantID} }

func (h harness) newStore(t *testing.T) store {
	t.Helper()
	tn := testkit.NewTenant(t, h.d)
	st := store{
		tenantID: tn.ID, slug: tn.Slug, warehouse: ids.New(), cai: ids.New(), hop: ids.New(), xanh: ids.New(), do: ids.New(),
		plain: ids.New(), colored: ids.New(), lotted: ids.New(), damage: ids.New(), disposal: ids.New(), varianceKind: ids.New(),
		owner: audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb},
	}
	ctx := context.Background()
	now := h.clk.Now()
	exec := func(q db.Querier, sql string, args ...any) error {
		if _, err := q.ExecContext(ctx, sql, args...); err != nil {
			return fmt.Errorf("%s: %w", sql, err)
		}
		return nil
	}
	testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
		storeID := ids.New()
		if err := exec(q, `INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES ($1, $2, 'Cửa hàng', $3, $3)`,
			storeID, tn.ID, now); err != nil {
			return err
		}
		if err := exec(q, `INSERT INTO warehouses (id, tenant_id, store_id, name, created_at, updated_at) VALUES ($1, $2, $3, 'Kho', $4, $4)`,
			st.warehouse, tn.ID, storeID, now); err != nil {
			return err
		}
		for _, u := range []struct {
			id   uuid.UUID
			name string
		}{{st.cai, "cái"}, {st.hop, "hộp"}} {
			if err := exec(q, `INSERT INTO units (id, tenant_id, name, name_key, created_at) VALUES ($1, $2, $3, $3, $4)`,
				u.id, tn.ID, u.name, now); err != nil {
				return err
			}
		}
		for _, c := range []struct {
			id         uuid.UUID
			code, name string
		}{{st.xanh, "xanh", "Xanh"}, {st.do, "do", "Đỏ"}} {
			if err := exec(q, `INSERT INTO colors (id, tenant_id, code, name, hex, created_at) VALUES ($1, $2, $3, $4, '#123456', $5)`,
				c.id, tn.ID, c.code, c.name, now); err != nil {
				return err
			}
		}
		for _, s := range []struct {
			id       uuid.UUID
			code     string
			lot      bool
			minStock string
		}{{st.plain, "SP-PLAIN", false, "0"}, {st.colored, "SP-COLOR", false, "0"}, {st.lotted, "SP-LOT", true, "10"}} {
			product := ids.New()
			if err := exec(q, `INSERT INTO products (id, tenant_id, type, name, name_key, created_at, updated_at)
				VALUES ($1, $2, 'goods', $3, $3, $4, $4)`, product, tn.ID, s.code, now); err != nil {
				return err
			}
			if err := exec(q, `INSERT INTO skus (id, tenant_id, product_id, code, display_name, base_unit_id, track_lot, track_expiry,
				min_stock, created_at, updated_at) VALUES ($1, $2, $3, $4, $4, $5, $6, $6, $7, $8, $8)`,
				s.id, tn.ID, product, s.code, st.cai, s.lot, s.minStock, now); err != nil {
				return err
			}
			if err := exec(q, `INSERT INTO sku_units (tenant_id, sku_id, unit_id, factor, is_base, created_at, updated_at)
				VALUES ($1, $2, $3, 1, true, $4, $4)`, tn.ID, s.id, st.cai, now); err != nil {
				return err
			}
		}
		if err := exec(q, `INSERT INTO sku_units (tenant_id, sku_id, unit_id, factor, is_base, created_at, updated_at)
			VALUES ($1, $2, $3, 10, false, $4, $4)`, tn.ID, st.plain, st.hop, now); err != nil {
			return err
		}
		for _, r := range []struct {
			id   uuid.UUID
			kind string
		}{{st.damage, "damage"}, {st.disposal, "disposal"}, {st.varianceKind, "count_variance"}} {
			if err := exec(q, `INSERT INTO reasons (id, tenant_id, kind, name, name_key, created_at) VALUES ($1, $2, $3, $3, $3, $4)`,
				r.id, tn.ID, r.kind, now); err != nil {
				return err
			}
		}
		return nil
	})
	return st
}

// postedDoc is the part of a posted DC the tests read.
type postedDoc struct {
	ID     uuid.UUID `json:"id"`
	DocNo  string    `json:"doc_no"`
	Status string    `json:"status"`
	Lines  []struct {
		LotID *uuid.UUID `json:"lot_id"`
	} `json:"lines"`
}

func decodeDoc(t *testing.T, res posting.Response) postedDoc {
	t.Helper()
	var d postedDoc
	if err := json.Unmarshal(res.Body, &d); err != nil {
		t.Fatalf("decode %s: %v", res.Body, err)
	}
	return d
}

func (h harness) adjust(t *testing.T, st store, in AdjustmentInput) postedDoc {
	t.Helper()
	res, err := h.svc.PostAdjustment(context.Background(), st.owner, newKey(), in)
	if err != nil {
		t.Fatalf("post %s: %v", in.Kind, err)
	}
	if res.Status != http.StatusCreated {
		t.Fatalf("post %s = %d", in.Kind, res.Status)
	}
	return decodeDoc(t, res)
}

func opening(sku uuid.UUID, q, cost string) AdjustmentInput {
	c := vnd(cost)
	return AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{{SkuID: sku, Qty: qty(q), UnitCost: &c}}}
}

func damage(st store, sku uuid.UUID, q string) AdjustmentInput {
	return AdjustmentInput{Kind: KindDamage, ReasonID: &st.damage, Lines: []AdjustmentLineInput{{SkuID: sku, Qty: qty(q)}}}
}

func (h harness) stockOf(t *testing.T, st store, sku uuid.UUID) StockRow {
	t.Helper()
	d, err := h.svc.StockOf(context.Background(), st.scope(), sku)
	if err != nil {
		t.Fatal(err)
	}
	return d.StockRow
}

func wantQty(t *testing.T, what string, got money.Qty, want string) {
	t.Helper()
	if !got.Equal(qty(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func wantMoney(t *testing.T, what string, got money.Money, want string) {
	t.Helper()
	if !got.Equal(vnd(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// verifyClean fails when the ledger verifier finds a projection that disagrees with the moves.
func (h harness) verifyClean(t *testing.T, st store) {
	t.Helper()
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		issues, err := Verify(context.Background(), q)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			t.Errorf("ledger verify found %d mismatches: %v", len(issues), issues)
		}
		return nil
	})
}

func (h harness) confirm(t *testing.T, st store, kind exception.Kind, docType string, docID uuid.UUID, version int, hash []byte) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		c, err := h.exc.Create(context.Background(), q, exception.NewConfirmation{TenantID: st.tenantID, Kind: kind,
			DocType: docType, DocID: docID, DocVersion: version, PayloadHash: hash, ConfirmedBy: st.owner.ID})
		id = c.ID
		return err
	})
	return id
}

// issue takes stock out through the ledger the way a sale does.
func (h harness) issue(st store, ln IssueLine) error {
	ctx := context.Background()
	return h.d.WithTenantTx(ctx, st.tenantID, func(q db.Querier) error {
		today, err := tenantToday(ctx, q, st.tenantID, h.clk.Now())
		if err != nil {
			return err
		}
		_, err = h.svc.Ledger().Issue(ctx, &jobTx{Querier: q}, Posting{TenantID: st.tenantID, DocType: "invoice", DocID: ids.New(),
			BusinessDate: today, PostedAt: h.clk.Now(), PostedBy: st.owner.ID}, []IssueLine{ln})
		return err
	})
}

func TestOpeningStockConvertsUnitsAndCompletesOnboarding(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()

	missing := AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("1")}}}
	_, err := h.svc.PostAdjustment(ctx, st.owner, newKey(), missing)
	if e := wantCode(t, err, apperr.Validation, "validation_failed"); e.Fields["lines.0.unit_cost"] != "required" {
		t.Errorf("fields = %v", e.Fields)
	}

	in := opening(st.plain, "2", "1000")
	in.Lines[0].UnitID = &st.hop
	h.adjust(t, st, in)
	s := h.stockOf(t, st, st.plain)
	wantQty(t, "on hand", s.OnHand, "20")
	wantMoney(t, "stock value", s.StockValue, "2000")
	wantMoney(t, "average cost", s.AvgCost, "100")

	var done bool
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT completed_at IS NOT NULL FROM onboarding_steps WHERE step_key = 'opening_stock'`).Scan(&done)
	})
	if !done {
		t.Error("the opening stock onboarding step is not completed")
	}
	h.verifyClean(t, st)
}

func TestIssueNeverSubstitutesAnotherColor(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	in := opening(st.colored, "12", "50")
	in.Lines[0].ColorID = &st.xanh
	h.adjust(t, st, in)

	red := damage(st, st.colored, "1")
	red.Lines[0].ColorID = &st.do
	_, err := h.svc.PostAdjustment(ctx, st.owner, newKey(), red)
	e := wantCode(t, err, apperr.Conflict, CodeInsufficientStock)
	want := map[string]string{"available.xanh": "12", "requested_color": "do", "requested_qty": "1", "sku": "SP-COLOR"}
	for k, v := range want {
		if e.Fields[k] != v {
			t.Errorf("fields[%s] = %q, want %q (fields %v)", k, e.Fields[k], v, e.Fields)
		}
	}

	_, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), damage(st, st.colored, "1"))
	wantErr(t, err, apperr.Validation, CodeColorRequired)

	tooMany := damage(st, st.colored, "13")
	tooMany.Lines[0].ColorID = &st.xanh
	_, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), tooMany)
	wantErr(t, err, apperr.Conflict, CodeInsufficientStock)

	ok := damage(st, st.colored, "2")
	ok.Lines[0].ColorID = &st.xanh
	h.adjust(t, st, ok)
	s := h.stockOf(t, st, st.colored)
	wantQty(t, "on hand", s.OnHand, "10")
	if len(s.Colors) != 1 || s.Colors[0].ColorID == nil || *s.Colors[0].ColorID != st.xanh || !s.Colors[0].OnHand.Equal(qty("10")) {
		t.Errorf("colors = %+v", s.Colors)
	}
	h.verifyClean(t, st)
}

func TestExpiredLotsAreBlockedAndFefoSkipsThem(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	cost := vnd("10")
	h.adjust(t, st, AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{
		{SkuID: st.lotted, Qty: qty("5"), UnitCost: &cost, LotNo: "L-SOON", ExpiryDate: dayPtr("2026-03-20")},
		{SkuID: st.lotted, Qty: qty("5"), UnitCost: &cost, LotNo: "L-LATE", ExpiryDate: dayPtr("2026-06-30")},
	}})
	lots, err := h.svc.Lots(ctx, st.scope(), LotFilter{SkuID: &st.lotted, Limit: 10})
	if err != nil || len(lots.Lots) != 2 {
		t.Fatalf("lots = %+v, %v", lots.Lots, err)
	}
	lotID := map[string]uuid.UUID{}
	for _, l := range lots.Lots {
		lotID[l.LotNo] = l.ID
	}

	// FEFO takes the first expiring lot.
	if err := h.issue(st, IssueLine{SkuID: st.lotted, Qty: qty("3")}); err != nil {
		t.Fatal(err)
	}
	balance := func(lot string) money.Qty {
		var q money.Qty
		testkit.InTenant(t, h.d, st.tenantID, func(tx db.Querier) error {
			return tx.QueryRowxContext(ctx, `SELECT qty FROM lot_balances WHERE lot_id = $1`, lotID[lot]).Scan(&q)
		})
		return q
	}
	wantQty(t, "L-SOON after FEFO issue", balance("L-SOON"), "2")
	wantQty(t, "L-LATE after FEFO issue", balance("L-LATE"), "5")

	// Nobody touched the SKU since L-SOON expired: the roller moves it to expired.
	h.clk.Set(time.Date(2026, 3, 25, 3, 0, 0, 0, time.UTC))
	n, err := h.svc.RollExpiry(ctx, st.tenantID)
	if err != nil || n != 1 {
		t.Fatalf("roll expiry = %d, %v; want 1 SKU", n, err)
	}
	if n, err := h.svc.RollExpiry(ctx, st.tenantID); err != nil || n != 0 {
		t.Errorf("second roll = %d, %v; want 0", n, err)
	}
	s := h.stockOf(t, st, st.lotted)
	wantQty(t, "expired", s.Expired, "2")
	wantQty(t, "sellable", s.Sellable, "5")

	soon := lotID["L-SOON"]
	err = h.issue(st, IssueLine{SkuID: st.lotted, Qty: qty("1"), LotID: &soon})
	wantErr(t, err, apperr.Conflict, CodeExpiredLotBlocked)
	// FEFO does not fall back on the expired lot: 5 sellable, 6 asked.
	err = h.issue(st, IssueLine{SkuID: st.lotted, Qty: qty("6")})
	wantErr(t, err, apperr.Conflict, CodeInsufficientStock)
	if err := h.issue(st, IssueLine{SkuID: st.lotted, Qty: qty("2")}); err != nil {
		t.Fatal(err)
	}
	wantQty(t, "L-SOON after the expired issue", balance("L-SOON"), "2")
	wantQty(t, "L-LATE after the expired issue", balance("L-LATE"), "3")
	s = h.stockOf(t, st, st.lotted)
	wantQty(t, "expired after the issue", s.Expired, "2")
	wantQty(t, "sellable after the issue", s.Sellable, "3")

	expired, err := h.svc.Lots(ctx, st.scope(), LotFilter{SkuID: &st.lotted, State: LotStateExpired, Limit: 10})
	if err != nil || len(expired.Lots) != 1 || expired.Lots[0].ID != soon {
		t.Errorf("expired lots = %+v, %v", expired.Lots, err)
	}
	trace, err := h.svc.TraceLot(ctx, st.scope(), soon, nil, 50)
	if err != nil || len(trace.Moves) != 2 || !trace.Moves[0].Qty.Equal(qty("5")) || !trace.Moves[1].Qty.Equal(qty("-3")) ||
		trace.Moves[0].DocNo == nil {
		t.Errorf("trace = %+v, %v", trace.Moves, err)
	}
	h.verifyClean(t, st)
}

func TestStockSignalsArePublishedOncePerDay(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	cost := vnd("10")
	// Below the minimum of 10 and expiring within the 30-day warning window.
	h.adjust(t, st, AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{
		{SkuID: st.lotted, Qty: qty("3"), UnitCost: &cost, LotNo: "L-1", ExpiryDate: dayPtr("2026-03-20")},
	}})
	n, err := h.svc.DetectSignals(ctx, st.tenantID)
	if err != nil || n < 1 {
		t.Fatalf("detect = %d, %v; want at least the expiring lot", n, err)
	}
	if n, err := h.svc.DetectSignals(ctx, st.tenantID); err != nil || n != 0 {
		t.Errorf("same-day re-run = %d, %v; want 0", n, err)
	}
	var topics []string
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		rows, err := q.QueryxContext(ctx, `SELECT DISTINCT topic FROM outbox_events WHERE topic IN ('stock.low', 'lot.expiring') ORDER BY topic`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			topics = append(topics, s)
		}
		return rows.Err()
	})
	if len(topics) != 2 {
		t.Errorf("signal topics = %v, want lot.expiring and stock.low", topics)
	}
}

func TestReservationsNeverExceedSellableUnderConcurrency(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))

	const workers = 20
	var wg sync.WaitGroup
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.plain, Qty: qty("1")})
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		if e, isApp := apperr.As(err); !isApp || e.Code != CodeOverReserve {
			t.Errorf("reservation error = %v, want %s", err, CodeOverReserve)
		}
	}
	if ok != 10 {
		t.Errorf("successful reservations = %d, want 10", ok)
	}
	list, err := h.svc.ListReservations(ctx, st.scope(), ReservationFilter{SkuID: &st.plain, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	total := money.ZeroQty
	for _, r := range list {
		total = total.Add(r.Open())
	}
	wantQty(t, "Σ reserved", total, "10")
	wantQty(t, "sku_stock.reserved", h.stockOf(t, st, st.plain).Reserved, "10")

	if _, err := h.svc.ReleaseReservation(ctx, st.owner, list[0].ID, ReleaseInput{Reason: "khách hủy"}); err != nil {
		t.Fatal(err)
	}
	wantQty(t, "reserved after a release", h.stockOf(t, st, st.plain).Reserved, "9")
	_, err = h.svc.ReleaseReservation(ctx, st.owner, list[0].ID, ReleaseInput{Reason: "khách hủy"})
	wantErr(t, err, apperr.Conflict, CodeReservationNotActive)
	h.verifyClean(t, st)
}

func TestAdjustmentReversalMirrorsTheMoves(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	dmg := h.adjust(t, st, damage(st, st.plain, "3"))
	wantQty(t, "on hand after damage", h.stockOf(t, st, st.plain).OnHand, "7")

	res, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), dmg.ID, ReverseInput{})
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("reverse = %d, %v", res.Status, err)
	}
	rev := decodeDoc(t, res)
	s := h.stockOf(t, st, st.plain)
	wantQty(t, "on hand after reversal", s.OnHand, "10")
	wantMoney(t, "stock value after reversal", s.StockValue, "1000")

	orig, err := h.svc.Adjustment(ctx, st.scope(), dmg.ID)
	if err != nil || orig.ReversedBy == nil || *orig.ReversedBy != rev.ID {
		t.Errorf("reversed by = %v, %v; want %s", orig.ReversedBy, err, rev.ID)
	}
	_, err = h.svc.ReverseAdjustment(ctx, st.owner, newKey(), dmg.ID, ReverseInput{})
	wantErr(t, err, apperr.Conflict, CodeAlreadyReversed)
	_, err = h.svc.ReverseAdjustment(ctx, st.owner, newKey(), rev.ID, ReverseInput{})
	wantErr(t, err, apperr.Conflict, "reversal_not_reversible")

	// The same key replays the stored answer instead of posting twice.
	k := newKey()
	first, err := h.svc.PostAdjustment(ctx, st.owner, k, damage(st, st.plain, "1"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.svc.PostAdjustment(ctx, st.owner, k, damage(st, st.plain, "1"))
	if err != nil || !again.Replayed || decodeDoc(t, again).ID != decodeDoc(t, first).ID {
		t.Errorf("replay = %+v, %v", again, err)
	}
	wantQty(t, "on hand after one damage", h.stockOf(t, st, st.plain).OnHand, "9")
	h.verifyClean(t, st)
}

func TestDisposalAndQuarantineReleaseNeedTheOwnersConfirmation(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))

	disposal := AdjustmentInput{Kind: KindDisposal, ReasonID: &st.disposal, PayloadHash: testHash,
		Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("1")}}}
	_, err := h.svc.PostAdjustment(ctx, st.owner, newKey(), disposal)
	if e := wantCode(t, err, apperr.ExceptionRequired, "stock_disposal"); e.ExceptionKind != "stock_disposal" {
		t.Errorf("exception kind = %q", e.ExceptionKind)
	}
	id := h.confirm(t, st, exception.StockDisposal, "sku", st.plain, 0, testHash)
	disposal.ExceptionConfirmationID = &id
	h.adjust(t, st, disposal)
	// A confirmation is spent once.
	_, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), disposal)
	if err == nil {
		t.Error("a spent confirmation was accepted twice")
	}

	h.adjust(t, st, AdjustmentInput{Kind: KindQuarantineIn, ReasonID: &st.damage,
		Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("4")}}})
	s := h.stockOf(t, st, st.plain)
	wantQty(t, "on hand", s.OnHand, "9")
	wantQty(t, "quarantine", s.Quarantine, "4")
	wantQty(t, "sellable", s.Sellable, "5")

	release := AdjustmentInput{Kind: KindQuarantineRelease, ReasonID: &st.damage, PayloadHash: testHash,
		Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("2")}}}
	_, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), release)
	wantErr(t, err, apperr.ExceptionRequired, "quarantine_release")
	id = h.confirm(t, st, exception.QuarantineRelease, "sku", st.plain, 0, testHash)
	release.ExceptionConfirmationID = &id
	h.adjust(t, st, release)
	s = h.stockOf(t, st, st.plain)
	wantQty(t, "quarantine after release", s.Quarantine, "2")
	wantQty(t, "sellable after release", s.Sellable, "7")
	h.verifyClean(t, st)
}

func countLine(t *testing.T, d CountDetail, sku uuid.UUID) CountLineView {
	t.Helper()
	for _, ln := range d.Lines {
		if ln.SkuID == sku {
			return ln
		}
	}
	t.Fatalf("no count line for %s", sku)
	return CountLineView{}
}

func TestStockCountBooksTheVarianceAtTheCutoff(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	in := opening(st.colored, "5", "50")
	in.Lines[0].ColorID = &st.xanh
	h.adjust(t, st, in)

	c, created, err := h.svc.StartCount(ctx, st.owner, nil, CountInput{Scope: ScopeSelection, SkuIDs: []uuid.UUID{st.plain, st.colored}, Blind: true})
	if err != nil || !created || c.Status != CountCounting || len(c.Lines) != 2 {
		t.Fatalf("start = %+v, %v, %v", c, created, err)
	}
	for _, ln := range toCountDTO(c).Lines {
		if ln.SystemQty != nil {
			t.Errorf("a blind count shows the system quantity %s while counting", ln.SystemQty)
		}
	}
	plain, colored := countLine(t, c, st.plain), countLine(t, c, st.colored)
	wantQty(t, "plain system qty", plain.SystemQty, "10")

	// A damage after the count started is not a variance: it moved the stock since the cutoff.
	h.adjust(t, st, damage(st, st.plain, "1"))

	_, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version+1, []CountLineInput{{LineID: &plain.ID, CountedQty: qtyPtr("8")}})
	wantErr(t, err, apperr.PreconditionFailed, "precondition_failed")
	c, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version,
		[]CountLineInput{{LineID: &plain.ID, CountedQty: qtyPtr("8"), ReasonID: &st.varianceKind}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.FinishCount(ctx, st.owner, c.ID, c.Version)
	wantErr(t, err, apperr.Validation, CodeCountIncomplete)
	c, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version, []CountLineInput{{LineID: &colored.ID, CountedQty: qtyPtr("5")}})
	if err != nil {
		t.Fatal(err)
	}
	c, err = h.svc.FinishCount(ctx, st.owner, c.ID, c.Version)
	if err != nil || c.Status != CountPendingPost {
		t.Fatalf("finish = %s, %v", c.Status, err)
	}
	plain = countLine(t, c, st.plain)
	wantQty(t, "moved since start", plain.MovedSinceStartQty, "-1")
	if plain.VarianceQty == nil || !plain.VarianceQty.Equal(qty("-1")) {
		t.Errorf("variance = %v, want -1 (counted 8 − system 10 − moved −1)", plain.VarianceQty)
	}
	if v := countLine(t, c, st.colored).VarianceQty; v == nil || !v.IsZero() {
		t.Errorf("colored variance = %v, want 0", v)
	}
	_, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version, []CountLineInput{{LineID: &plain.ID, CountedQty: qtyPtr("9")}})
	wantErr(t, err, apperr.Conflict, CodeCountNotCounting)

	// Recount reopens the line, then the sheet is finished again.
	c, err = h.svc.RecountCount(ctx, st.owner, c.ID, c.Version, []uuid.UUID{plain.ID})
	if err != nil || c.Status != CountCounting || countLine(t, c, st.plain).CountedQty != nil {
		t.Fatalf("recount = %s, %v", c.Status, err)
	}
	if c, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version,
		[]CountLineInput{{LineID: &plain.ID, CountedQty: qtyPtr("8"), ReasonID: &st.varianceKind}}); err != nil {
		t.Fatal(err)
	}
	if c, err = h.svc.FinishCount(ctx, st.owner, c.ID, c.Version); err != nil {
		t.Fatal(err)
	}

	post := CountPostInput{Version: c.Version, PayloadHash: testHash}
	_, err = h.svc.PostCount(ctx, st.owner, newKey(), c.ID, post)
	wantErr(t, err, apperr.ExceptionRequired, "count_variance")
	id := h.confirm(t, st, exception.CountVariance, DocStockCount, c.ID, c.Version, testHash)
	post.ExceptionConfirmationID = &id
	res, err := h.svc.PostCount(ctx, st.owner, newKey(), c.ID, post)
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("post count = %d, %v", res.Status, err)
	}
	wantQty(t, "on hand after the count", h.stockOf(t, st, st.plain).OnHand, "8")
	wantQty(t, "colored on hand after the count", h.stockOf(t, st, st.colored).OnHand, "5")
	posted, err := h.svc.Count(ctx, st.scope(), c.ID)
	if err != nil || posted.Status != CountPosted || posted.AdjustmentID == nil {
		t.Fatalf("posted count = %+v, %v", posted.StockCount, err)
	}
	adj, err := h.svc.Adjustment(ctx, st.scope(), *posted.AdjustmentID)
	if err != nil || adj.Kind != KindCount || len(adj.Lines) != 1 || adj.Lines[0].Direction != -1 {
		t.Errorf("count adjustment = %+v, %v", adj, err)
	}
	_, err = h.svc.PostCount(ctx, st.owner, newKey(), c.ID, CountPostInput{Version: posted.Version})
	wantErr(t, err, apperr.Conflict, CodeCountPosted)
	h.verifyClean(t, st)
}

func TestRecostReplaysABackdatedReceipt(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	dated := func(in AdjustmentInput, d string) AdjustmentInput { in.BusinessDate = dayPtr(d); return in }
	h.adjust(t, st, dated(opening(st.plain, "10", "100"), "2026-03-10"))
	h.adjust(t, st, dated(damage(st, st.plain, "5"), "2026-03-12"))
	// Posted late: had it been posted on its day the damage would have cost 150 a unit.
	h.adjust(t, st, dated(opening(st.plain, "10", "200"), "2026-03-11"))
	s := h.stockOf(t, st, st.plain)
	wantMoney(t, "stock value before recost", s.StockValue, "2500")

	_, err := h.svc.PreviewRecost(ctx, st.owner, nil, day("2026-03-10"))
	if e := wantCode(t, err, apperr.Validation, "validation_failed"); e.Fields["sku_ids"] != "required" {
		t.Errorf("fields = %v", e.Fields)
	}
	_, err = h.svc.PreviewRecost(ctx, st.owner, []uuid.UUID{st.plain}, day("2026-04-01"))
	wantErr(t, err, apperr.Validation, "validation_failed")

	run, err := h.svc.PreviewRecost(ctx, st.owner, []uuid.UUID{st.plain}, day("2026-03-10"))
	if err != nil || run == nil {
		t.Fatalf("preview = %v, %v", run, err)
	}
	wantMoney(t, "stock value delta", run.Impact.StockValueDelta, "-250")
	wantMoney(t, "COGS delta", run.Impact.CogsDelta, "250")
	if len(run.Impact.Skus) != 1 || !run.Impact.Skus[0].NewAvgCost.Equal(vnd("150")) {
		t.Errorf("impact = %+v", run.Impact.Skus)
	}

	if _, err := h.svc.ConfirmRecost(ctx, st.owner, newKey(), run.ID); err != nil {
		t.Fatal(err)
	}
	s = h.stockOf(t, st, st.plain)
	wantMoney(t, "stock value after recost", s.StockValue, "2250")
	wantMoney(t, "average cost after recost", s.AvgCost, "150")
	wantQty(t, "on hand after recost", s.OnHand, "15")
	applied, err := h.svc.Recost(ctx, st.scope(), run.ID)
	if err != nil || applied.Status != RecostApplied {
		t.Errorf("run = %s, %v", applied.Status, err)
	}
	_, err = h.svc.ConfirmRecost(ctx, st.owner, newKey(), run.ID)
	wantErr(t, err, apperr.Conflict, CodeRecostNotPending)
	if again, err := h.svc.PreviewRecost(ctx, st.owner, []uuid.UUID{st.plain}, day("2026-03-10")); err != nil || again != nil {
		t.Errorf("preview after apply = %+v, %v; want nothing to correct", again, err)
	}
	h.verifyClean(t, st)

	// A posting after the preview makes it stale.
	h.adjust(t, st, dated(opening(st.plain, "5", "300"), "2026-03-11"))
	stale, err := h.svc.PreviewRecost(ctx, st.owner, []uuid.UUID{st.plain}, day("2026-03-10"))
	if err != nil || stale == nil {
		t.Fatalf("second preview = %v, %v", stale, err)
	}
	h.adjust(t, st, dated(opening(st.plain, "1", "1000"), "2026-03-11"))
	_, err = h.svc.ConfirmRecost(ctx, st.owner, newKey(), stale.ID)
	wantErr(t, err, apperr.Conflict, CodeRecostStale)
	discarded, err := h.svc.DiscardRecost(ctx, st.owner, stale.ID)
	if err != nil || discarded.Status != RecostDiscarded {
		t.Fatalf("discard = %s, %v", discarded.Status, err)
	}
	_, err = h.svc.ConfirmRecost(ctx, st.owner, newKey(), stale.ID)
	wantErr(t, err, apperr.Conflict, CodeRecostNotPending)
	h.verifyClean(t, st)
}

func TestStockCardBalancesAndReconciles(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	dated := func(in AdjustmentInput, d string) AdjustmentInput { in.BusinessDate = dayPtr(d); return in }
	h.adjust(t, st, dated(opening(st.plain, "10", "100"), "2026-03-10"))
	h.adjust(t, st, dated(damage(st, st.plain, "3"), "2026-03-12"))
	h.adjust(t, st, dated(opening(st.plain, "5", "100"), "2026-03-14"))
	h.adjust(t, st, damage(st, st.plain, "1"))

	card, err := h.svc.StockCardOf(ctx, st.scope(), CardFilter{SkuID: st.plain, From: dayPtr("2026-03-11"), To: dayPtr("2026-03-15"), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	wantQty(t, "opening", card.Opening, "10")
	wantQty(t, "in", card.In, "5")
	wantQty(t, "out", card.Out, "4")
	wantQty(t, "closing", card.Closing, "11")
	wantQty(t, "on hand", card.OnHand, "11")
	if !card.Opening.Add(card.In).Sub(card.Out).Equal(card.Closing) || !card.Reconciled {
		t.Errorf("card = opening %s + in %s − out %s → closing %s, reconciled %v", card.Opening, card.In, card.Out, card.Closing, card.Reconciled)
	}
	if len(card.Rows) != 3 || !card.Rows[len(card.Rows)-1].Balance.Equal(qty("11")) {
		t.Errorf("rows = %+v", card.Rows)
	}

	past, err := h.svc.StockCardOf(ctx, st.scope(), CardFilter{SkuID: st.plain, From: dayPtr("2026-03-11"), To: dayPtr("2026-03-13"), Limit: 50})
	if err != nil || !past.Closing.Equal(qty("7")) || !past.Reconciled {
		t.Errorf("past card closing %s reconciled %v, %v", past.Closing, past.Reconciled, err)
	}

	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.sku_stock SET on_hand = on_hand + 1, sellable = sellable + 1 WHERE sku_id = $1`, st.plain); err != nil {
		t.Fatal(err)
	}
	card, err = h.svc.StockCardOf(ctx, st.scope(), CardFilter{SkuID: st.plain, Limit: 50})
	if err != nil || card.Reconciled {
		t.Errorf("a tampered projection is reconciled = %v, %v", card.Reconciled, err)
	}
}

// TestLedgerStaysConsistentUnderRandomOperations runs a seeded random mix of receipts, issues,
// quarantines, releases, reservations and reversals and proves every projection still equals
// its ledger; a tampered projection is then caught.
func TestLedgerStaysConsistentUnderRandomOperations(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(20261017, 7)) //nolint:gosec // deterministic test data, not security
	skus := []uuid.UUID{st.plain, st.colored, st.lotted}
	colors := []*uuid.UUID{&st.xanh, &st.do}
	expiries := []string{"2026-04-01", "2026-05-15", "2026-07-30"}
	var docs, reservations []uuid.UUID
	quarantines := map[uuid.UUID]bool{}
	const ops = 520
	succeeded := 0
	line := func(sku uuid.UUID, q string) AdjustmentLineInput {
		ln := AdjustmentLineInput{SkuID: sku, Qty: qty(q)}
		if sku == st.colored {
			ln.ColorID = colors[rng.IntN(len(colors))]
		}
		return ln
	}
	for i := range ops {
		sku := skus[rng.IntN(len(skus))]
		n := fmt.Sprint(1 + rng.IntN(5))
		var err error
		switch op := rng.IntN(10); op {
		case 0, 1, 2:
			ln := line(sku, n)
			cost := money.NewMoneyFromInt(int64(10 + rng.IntN(500)))
			ln.UnitCost = &cost
			if sku == st.lotted {
				ln.LotNo = fmt.Sprintf("L-%d", rng.IntN(4))
				ln.ExpiryDate = dayPtr(expiries[rng.IntN(len(expiries))])
			}
			var res posting.Response
			if res, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{ln}}); err == nil {
				docs = append(docs, decodeDoc(t, res).ID)
			}
		case 3, 4:
			var res posting.Response
			in := AdjustmentInput{Kind: KindDamage, ReasonID: &st.damage, Lines: []AdjustmentLineInput{line(sku, n)}}
			if res, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), in); err == nil {
				docs = append(docs, decodeDoc(t, res).ID)
			}
		case 5:
			ln := IssueLine{SkuID: sku, Qty: qty(n)}
			if sku == st.colored {
				ln.ColorID = colors[rng.IntN(len(colors))]
			}
			err = h.issue(st, ln)
		case 6:
			var res posting.Response
			in := AdjustmentInput{Kind: KindQuarantineIn, ReasonID: &st.damage, Lines: []AdjustmentLineInput{line(st.plain, n)}}
			if res, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), in); err == nil {
				id := decodeDoc(t, res).ID
				docs = append(docs, id)
				quarantines[id] = true
			}
		case 7:
			id := h.confirm(t, st, exception.QuarantineRelease, "sku", st.plain, 0, testHash)
			_, err = h.svc.PostAdjustment(ctx, st.owner, newKey(), AdjustmentInput{Kind: KindQuarantineRelease, ReasonID: &st.damage,
				ExceptionConfirmationID: &id, PayloadHash: testHash, Lines: []AdjustmentLineInput{line(st.plain, "1")}})
		case 8:
			if len(reservations) > 0 && rng.IntN(2) == 0 {
				j := rng.IntN(len(reservations))
				_, err = h.svc.ReleaseReservation(ctx, st.owner, reservations[j], ReleaseInput{Reason: "khách hủy"})
				reservations = append(reservations[:j], reservations[j+1:]...)
			} else {
				var r Reservation
				if r, err = h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: sku, Qty: qty(n)}); err == nil {
					reservations = append(reservations, r.ID)
				}
			}
		case 9:
			if len(docs) == 0 {
				continue
			}
			j := rng.IntN(len(docs))
			rev := ReverseInput{}
			if quarantines[docs[j]] {
				id := h.confirm(t, st, exception.QuarantineRelease, DocStockAdjustment, docs[j], 0, testHash)
				rev = ReverseInput{ExceptionConfirmationID: &id, PayloadHash: testHash}
			}
			_, err = h.svc.ReverseAdjustment(ctx, st.owner, newKey(), docs[j], rev)
			docs = append(docs[:j], docs[j+1:]...)
		}
		if err != nil {
			// Only business refusals are expected: a shortage of stock, of sellable stock or of
			// quarantined stock to release.
			if e, ok := apperr.As(err); !ok || e.Kind != apperr.Conflict {
				t.Fatalf("op %d: unexpected error %v", i, err)
			}
			continue
		}
		succeeded++
		if i%100 == 99 {
			h.verifyClean(t, st)
		}
	}
	if succeeded < ops/2 {
		t.Errorf("only %d of %d operations succeeded; the mix no longer exercises the ledger", succeeded, ops)
	}
	h.verifyClean(t, st)

	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.sku_stock SET on_hand = on_hand + 1, sellable = sellable + 1 WHERE sku_id = $1`, st.plain); err != nil {
		t.Fatal(err)
	}
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		all, err := Verify(ctx, q)
		if err != nil {
			return err
		}
		one, err := VerifySkus(ctx, q, []uuid.UUID{st.plain})
		if err != nil {
			return err
		}
		other, err := VerifySkus(ctx, q, []uuid.UUID{st.colored})
		if err != nil {
			return err
		}
		if len(all) == 0 || len(one) == 0 || len(other) != 0 {
			t.Errorf("after tampering: verify %v, sku %v, other sku %v", all, one, other)
		}
		return nil
	})
}

func TestStockReaderShowsShelfStockByColor(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	cost := vnd("10")
	h.adjust(t, st, AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{
		{SkuID: st.colored, Qty: qty("3"), ColorID: &st.xanh, UnitCost: &cost},
		{SkuID: st.colored, Qty: qty("2"), ColorID: &st.do, UnitCost: &cost},
		{SkuID: st.plain, Qty: qty("4"), UnitCost: &cost},
	}})
	h.adjust(t, st, AdjustmentInput{Kind: KindQuarantineIn, ReasonID: &st.damage,
		Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("1")}}})

	// The reader runs in the caller's transaction on a pool of one connection: taking a second
	// connection would wait until the deadline instead of answering.
	single, err := db.Open(context.Background(), testkit.AppURL(t), db.Options{MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = single.Close() })
	read := func(readOnly bool, r StockReader, skuIDs ...uuid.UUID) (map[uuid.UUID][]ColorQty, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		inTx := single.WithTenantTx
		if readOnly {
			inTx = single.WithReadOnlyTenantTx
		}
		var got map[uuid.UUID][]ColorQty
		err := inTx(ctx, st.tenantID, func(q db.Querier) error {
			var err error
			got, err = r.OnHandByColor(ctx, q, skuIDs)
			return err
		})
		return got, err
	}
	got, err := read(true, h.svc.StockReader(), st.colored, st.plain, st.lotted)
	if err != nil {
		t.Fatal(err)
	}
	byColor := map[uuid.UUID]string{}
	for _, c := range got[st.colored] {
		byColor[*c.ColorID] = c.Qty.String()
	}
	if len(got[st.colored]) != 2 || byColor[st.xanh] != "3" || byColor[st.do] != "2" {
		t.Errorf("colored = %+v", got[st.colored])
	}
	// Quarantined stock is not on the shelf; a SKU without stock is absent.
	if p := got[st.plain]; len(p) != 1 || p[0].ColorID != nil || !p[0].Qty.Equal(qty("3")) {
		t.Errorf("plain = %+v", p)
	}
	if _, ok := got[st.lotted]; ok {
		t.Errorf("a SKU without stock is listed: %+v", got[st.lotted])
	}

	// Expired lots are not on the shelf either, even before the roller ran; the expiry day itself
	// still sells. The tenant's own zone decides, in a read-only support transaction too, which
	// carries the zone because its role may not read the tenants table.
	h.adjust(t, st, AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{
		{SkuID: st.lotted, Qty: qty("2"), UnitCost: &cost, LotNo: "L-SOON", ExpiryDate: dayPtr("2026-03-20")},
		{SkuID: st.lotted, Qty: qty("3"), UnitCost: &cost, LotNo: "L-LATE", ExpiryDate: dayPtr("2026-06-30")},
	}})
	if err := h.d.WithPlatformTx(context.Background(), func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `UPDATE tenants SET timezone = 'Asia/Tokyo' WHERE id = $1`, st.tenantID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		at       time.Time
		readOnly bool
		want     string
	}{
		{time.Date(2026, 3, 20, 14, 0, 0, 0, time.UTC), false, "5"}, // 23:00 on the expiry day in Tokyo
		{time.Date(2026, 3, 20, 15, 0, 0, 0, time.UTC), false, "3"}, // 00:00 the day after in Tokyo
		{time.Date(2026, 3, 20, 14, 0, 0, 0, time.UTC), true, "5"},
		{time.Date(2026, 3, 20, 16, 0, 0, 0, time.UTC), true, "3"}, // 01:00 the day after in Tokyo (Ho Chi Minh would still sell it)
	} {
		h.clk.Set(c.at)
		got, err := read(c.readOnly, h.svc.StockReader(), st.lotted)
		if err != nil {
			t.Fatal(err)
		}
		if l := got[st.lotted]; len(l) != 1 || !l[0].Qty.Equal(qty(c.want)) {
			t.Errorf("lotted at %s (read-only %v) = %+v, want %s", c.at, c.readOnly, l, c.want)
		}
	}

	if _, err := read(true, StockReader{}, st.plain); err == nil {
		t.Error("a stock reader not built by the ledger service answered")
	}
}

func TestTenantsSeeOnlyTheirOwnStock(t *testing.T) {
	h := newHarness(t)
	a, b := h.newStore(t), h.newStore(t)
	ctx := context.Background()
	doc := h.adjust(t, a, opening(a.plain, "10", "100"))

	rows, err := h.svc.Stock(ctx, b.scope(), StockFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.SkuID == a.plain {
			t.Error("tenant B lists the SKU of tenant A")
		}
	}
	_, err = h.svc.StockOf(ctx, b.scope(), a.plain)
	wantErr(t, err, apperr.NotFound, "not_found")
	_, err = h.svc.Adjustment(ctx, b.scope(), doc.ID)
	wantErr(t, err, apperr.NotFound, "not_found")
	if _, err := h.svc.PostAdjustment(ctx, b.owner, newKey(), damage(b, a.plain, "1")); err == nil {
		t.Error("tenant B issued the stock of tenant A")
	}
	if _, err := h.svc.ReverseAdjustment(ctx, b.owner, newKey(), doc.ID, ReverseInput{}); err == nil {
		t.Error("tenant B reversed a DC of tenant A")
	}
	_, err = h.svc.PreviewRecost(ctx, b.owner, []uuid.UUID{a.plain}, day("2026-03-10"))
	wantErr(t, err, apperr.NotFound, "not_found")
	if _, err := h.svc.CreateReservation(ctx, b.owner, ManualReservation{SkuID: a.plain, Qty: qty("1")}); err == nil {
		t.Error("tenant B reserved the stock of tenant A")
	}
	wantQty(t, "tenant A on hand", h.stockOf(t, a, a.plain).OnHand, "10")
	wantQty(t, "tenant A reserved", h.stockOf(t, a, a.plain).Reserved, "0")
	h.verifyClean(t, a)
	h.verifyClean(t, b)
}

type fakeHosts map[string]uuid.UUID

func (f fakeHosts) ResolveSlug(_ context.Context, slug string) (uuid.UUID, bool, error) {
	id, ok := f[slug]
	return id, ok, nil
}

type fakeSessions map[string]middleware.Principal

func (f fakeSessions) ResolveSession(_ context.Context, token string) (middleware.Principal, bool, error) {
	p, ok := f[token]
	return p, ok, nil
}

// api serves the inventory routes for st behind the platform's host, session and tenant
// middleware. The "fresh" session re-entered its password a minute ago, "stale" never did.
func (h harness) api(t *testing.T, st store) http.Handler {
	t.Helper()
	e, err := httpx.NewEngine(httpx.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reauthAt := h.clk.Now().Add(-time.Minute)
	sessions := fakeSessions{
		"fresh": {Kind: middleware.PrincipalOwner, SessionID: ids.New(), UserID: st.owner.ID, TenantID: st.tenantID, ReauthenticatedAt: &reauthAt},
		"stale": {Kind: middleware.PrincipalOwner, SessionID: ids.New(), UserID: st.owner.ID, TenantID: st.tenantID},
	}
	e.Use(middleware.HostContext("tally.test", fakeHosts{st.slug: st.tenantID}), middleware.Session(sessions, "tally_session"))
	g := e.Group("/api/v1", httpx.OnHosts(httpx.HostTenant), middleware.Tenant())
	idx := &httpx.CommandIndex{}
	NewHandler(h.svc).Register(Routes{
		Tenant: g,
		Commands: func(gr *gin.RouterGroup, res string) *httpx.CommandRouter {
			return httpx.NewCommandRouter(gr, res, idx)
		},
		Reauth: middleware.RequireRecentAuth(middleware.ReauthWindow, h.clk.Now),
	})
	return e
}

func call(t *testing.T, api http.Handler, st store, session, method, path string, body any, idemKey string) (int, []byte) {
	t.Helper()
	header := http.Header{}
	if idemKey != "" {
		header.Set(httpx.IdempotencyKeyHeader, idemKey)
	}
	var cookies []*http.Cookie
	if session != "" {
		cookies = []*http.Cookie{{Name: "tally_session", Value: session}}
	}
	rec := testkit.Do(t, api, testkit.Request{Method: method, URL: "http://" + st.slug + ".tally.test/api/v1" + path, Body: body,
		Header: header, Cookies: cookies})
	return rec.Code, rec.Body.Bytes()
}

func errorOf(t *testing.T, body []byte) httpx.ErrorPayload {
	t.Helper()
	var e httpx.ErrorBody
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error %s: %v", body, err)
	}
	return e.Error
}

func TestRoutes(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	api := h.api(t, st)

	if code, _ := call(t, api, st, "", http.MethodGet, "/stock", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("GET /stock without a session = %d, want 401", code)
	}

	openingBody := map[string]any{"kind": "opening", "lines": []map[string]any{{"sku_id": st.plain, "qty": "10", "unit_cost": "100"}}}
	code, body := call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", openingBody, "")
	if code != http.StatusPreconditionRequired && code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
		t.Errorf("posting without an Idempotency-Key = %d %s", code, body)
	}
	code, body = call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", openingBody, "open-1")
	if code != http.StatusCreated {
		t.Fatalf("POST opening = %d %s", code, body)
	}
	var doc postedDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var replayed postedDoc
	code, again := call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", openingBody, "open-1")
	if err := json.Unmarshal(again, &replayed); err != nil || code != http.StatusCreated || replayed.ID != doc.ID {
		t.Errorf("replayed opening = %d %s", code, again)
	}

	code, body = call(t, api, st, "stale", http.MethodGet, "/stock", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /stock = %d %s", code, body)
	}
	var list httpx.List[stockRowDTO]
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, r := range list.Data {
		listed = listed || (r.SkuID == st.plain && r.OnHand.Equal(qty("10")))
	}
	if len(list.Data) != 3 || !listed {
		t.Errorf("stock list = %s", body)
	}

	if code, body = call(t, api, st, "stale", http.MethodGet, "/stock-card?sku_id="+st.plain.String(), nil, ""); code != http.StatusOK {
		t.Errorf("GET /stock-card = %d %s", code, body)
	}
	var card struct {
		Reconciled bool `json:"reconciled"`
	}
	if err := json.Unmarshal(body, &card); err != nil || !card.Reconciled {
		t.Errorf("stock card %s, %v", body, err)
	}
	if code, body = call(t, api, st, "stale", http.MethodGet, "/lots?sku_id="+st.plain.String(), nil, ""); code != http.StatusOK {
		t.Errorf("GET /lots = %d %s", code, body)
	}
	if code, body = call(t, api, st, "stale", http.MethodGet, "/stock-adjustments/"+doc.ID.String(), nil, ""); code != http.StatusOK {
		t.Errorf("GET adjustment = %d %s", code, body)
	}

	// Disposal needs a recent password, then the owner's confirmation of this exact body.
	disposal := map[string]any{"kind": "disposal", "reason_id": st.disposal, "lines": []map[string]any{{"sku_id": st.plain, "qty": "1"}}}
	code, body = call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", disposal, "dispose-1")
	if code != http.StatusForbidden || errorOf(t, body).Code != "reauth_required" {
		t.Errorf("disposal without re-auth = %d %s", code, body)
	}
	code, body = call(t, api, st, "fresh", http.MethodPost, "/stock-adjustments", disposal, "dispose-2")
	if e := errorOf(t, body); code != http.StatusConflict || e.Code != "stock_disposal" || e.ExceptionKind != "stock_disposal" {
		t.Errorf("disposal without confirmation = %d %s", code, body)
	}
	raw, err := json.Marshal(disposal)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := exception.PayloadHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	disposal["exception_confirmation_id"] = h.confirm(t, st, exception.StockDisposal, "sku", st.plain, 0, hash)
	if code, body = call(t, api, st, "fresh", http.MethodPost, "/stock-adjustments", disposal, "dispose-3"); code != http.StatusCreated {
		t.Errorf("confirmed disposal = %d %s", code, body)
	}
	// A decrease citing a disposal reason is a disposal: it needs the recent password too.
	disposingDecrease := map[string]any{"kind": "decrease", "reason_id": st.disposal, "lines": []map[string]any{{"sku_id": st.plain, "qty": "1"}}}
	code, body = call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", disposingDecrease, "dispose-4")
	if code != http.StatusForbidden || errorOf(t, body).Code != "reauth_required" {
		t.Errorf("disposing decrease without re-auth = %d %s", code, body)
	}

	// Reversing a quarantine puts it back on sale: the owner confirms the reversal of that DC.
	quarantine := map[string]any{"kind": "quarantine_in", "reason_id": st.damage, "lines": []map[string]any{{"sku_id": st.plain, "qty": "1"}}}
	code, body = call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", quarantine, "quarantine-1")
	var qDoc postedDoc
	if err := json.Unmarshal(body, &qDoc); err != nil || code != http.StatusCreated {
		t.Fatalf("quarantine = %d %s", code, body)
	}
	code, body = call(t, api, st, "fresh", http.MethodPost, "/stock-adjustments/"+qDoc.ID.String()+":reverse", nil, "rev-q-1")
	if e := errorOf(t, body); code != http.StatusConflict || e.ExceptionKind != "quarantine_release" {
		t.Errorf("quarantine reversal without confirmation = %d %s", code, body)
	}
	emptyHash, err := exception.PayloadHash([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	reverseBody := map[string]any{"exception_confirmation_id": h.confirm(t, st, exception.QuarantineRelease, DocStockAdjustment, qDoc.ID, 0, emptyHash)}
	if code, body = call(t, api, st, "fresh", http.MethodPost, "/stock-adjustments/"+qDoc.ID.String()+":reverse", reverseBody, "rev-q-2"); code != http.StatusCreated {
		t.Errorf("confirmed quarantine reversal = %d %s", code, body)
	}

	if code, body = call(t, api, st, "stale", http.MethodPost, "/stock-adjustments/"+doc.ID.String()+":reverse", nil, "rev-1"); code != http.StatusForbidden {
		t.Errorf("reverse without re-auth = %d %s", code, body)
	}
	dmg := map[string]any{"kind": "damage", "reason_id": st.damage, "lines": []map[string]any{{"sku_id": st.plain, "qty": "2"}}}
	code, body = call(t, api, st, "stale", http.MethodPost, "/stock-adjustments", dmg, "dmg-1")
	if code != http.StatusCreated {
		t.Fatalf("damage = %d %s", code, body)
	}
	var dmgDoc postedDoc
	if err := json.Unmarshal(body, &dmgDoc); err != nil {
		t.Fatal(err)
	}
	if code, body = call(t, api, st, "fresh", http.MethodPost, "/stock-adjustments/"+dmgDoc.ID.String()+":reverse", nil, "rev-2"); code != http.StatusCreated {
		t.Errorf("reverse = %d %s", code, body)
	}

	code, body = call(t, api, st, "stale", http.MethodPost, "/reservations", map[string]any{"sku_id": st.plain, "qty": "20"}, "")
	if e := errorOf(t, body); code != http.StatusConflict || e.Code != CodeOverReserve {
		t.Errorf("over-reservation = %d %s", code, body)
	}
	code, body = call(t, api, st, "stale", http.MethodPost, "/reservations", map[string]any{"sku_id": st.plain, "qty": "2"}, "")
	if code != http.StatusCreated {
		t.Fatalf("reservation = %d %s", code, body)
	}
	var res struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if code, body = call(t, api, st, "stale", http.MethodPost, "/reservations/"+res.ID.String()+":release", map[string]any{"reason": "khách hủy"}, ""); code != http.StatusOK {
		t.Errorf("release = %d %s", code, body)
	}

	code, body = call(t, api, st, "stale", http.MethodPost, "/stock-counts", map[string]any{"scope": "selection", "sku_ids": []uuid.UUID{st.plain}, "blind": true}, "")
	if code != http.StatusCreated {
		t.Fatalf("start count = %d %s", code, body)
	}
	var count struct {
		Lines []struct {
			SystemQty *string `json:"system_qty"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &count); err != nil || len(count.Lines) != 1 || count.Lines[0].SystemQty != nil {
		t.Errorf("blind count = %s, %v", body, err)
	}

	code, body = call(t, api, st, "stale", http.MethodPost, "/cost-recalculations", map[string]any{"sku_ids": []uuid.UUID{st.plain}, "from_date": "2026-03-01"}, "")
	if code != http.StatusNoContent && code != http.StatusCreated {
		t.Errorf("recost preview = %d %s", code, body)
	}
	h.verifyClean(t, st)
}

// costAdjust books a cost-only move the way a landed cost allocation does.
func (h harness) costAdjust(st store, sku uuid.UUID, value string) error {
	ctx := context.Background()
	return h.d.WithTenantTx(ctx, st.tenantID, func(q db.Querier) error {
		today, err := tenantToday(ctx, q, st.tenantID, h.clk.Now())
		if err != nil {
			return err
		}
		_, err = h.svc.Ledger().CostAdjust(ctx, &jobTx{Querier: q}, Posting{TenantID: st.tenantID, DocType: "landed_cost_allocation",
			DocID: ids.New(), BusinessDate: today, PostedAt: h.clk.Now(), PostedBy: st.owner.ID}, []CostLine{{SkuID: sku, Value: vnd(value)}})
		return err
	})
}

func TestStockLeavingTheShopForDisposalNeedsTheOwnersConfirmation(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))

	// A decrease or a quarantine citing a disposal reason disposes of stock like a disposal DC.
	for _, kind := range []string{KindDecrease, KindQuarantineIn} {
		in := AdjustmentInput{Kind: kind, ReasonID: &st.disposal, PayloadHash: testHash,
			Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("1")}}}
		_, err := h.svc.PostAdjustment(ctx, st.owner, newKey(), in)
		if e := wantCode(t, err, apperr.ExceptionRequired, "stock_disposal"); e.ExceptionKind != "stock_disposal" {
			t.Errorf("%s: exception kind = %q", kind, e.ExceptionKind)
		}
		if needs, err := h.svc.AdjustmentNeedsReauth(ctx, st.tenantID, kind, &st.disposal); err != nil || !needs {
			t.Errorf("%s with a disposal reason needs re-auth = %v, %v", kind, needs, err)
		}
		id := h.confirm(t, st, exception.StockDisposal, "sku", st.plain, 0, testHash)
		in.ExceptionConfirmationID = &id
		doc := h.adjust(t, st, in)

		// The audit trail names the confirmation and every SKU it covered.
		var after struct {
			ExceptionKind string      `json:"exception_kind"`
			Confirmation  uuid.UUID   `json:"exception_confirmation_id"`
			SkuIDs        []uuid.UUID `json:"sku_ids"`
		}
		testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
			var raw []byte
			if err := q.QueryRowxContext(ctx, `SELECT after FROM audit_events WHERE action = 'stock_adjustment.post' AND entity_id = $1`,
				doc.ID).Scan(&raw); err != nil {
				return err
			}
			return json.Unmarshal(raw, &after)
		})
		if after.ExceptionKind != "stock_disposal" || after.Confirmation != id || len(after.SkuIDs) != 1 || after.SkuIDs[0] != st.plain {
			t.Errorf("%s audit = %+v", kind, after)
		}
	}
	// Any other reason needs neither.
	h.adjust(t, st, AdjustmentInput{Kind: KindDecrease, ReasonID: &st.damage, Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("1")}}})
	if needs, err := h.svc.AdjustmentNeedsReauth(ctx, st.tenantID, KindDecrease, &st.damage); err != nil || needs {
		t.Errorf("decrease with a damage reason needs re-auth = %v, %v", needs, err)
	}
	s := h.stockOf(t, st, st.plain)
	wantQty(t, "on hand", s.OnHand, "8")
	wantQty(t, "quarantine", s.Quarantine, "1")
	h.verifyClean(t, st)
}

func TestReversingAQuarantineNeedsAReleaseConfirmation(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	q := h.adjust(t, st, AdjustmentInput{Kind: KindQuarantineIn, ReasonID: &st.damage,
		Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("4")}}})

	rev := ReverseInput{PayloadHash: testHash}
	_, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), q.ID, rev)
	if e := wantCode(t, err, apperr.ExceptionRequired, "quarantine_release"); e.ExceptionKind != "quarantine_release" {
		t.Errorf("exception kind = %q", e.ExceptionKind)
	}
	// A release confirmation of the SKU does not cover the reversal of this DC.
	other := h.confirm(t, st, exception.QuarantineRelease, "sku", st.plain, 0, testHash)
	rev.ExceptionConfirmationID = &other
	if _, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), q.ID, rev); err == nil {
		t.Error("a confirmation bound to another document reversed the quarantine")
	}
	wantQty(t, "quarantine before the confirmed reversal", h.stockOf(t, st, st.plain).Quarantine, "4")

	id := h.confirm(t, st, exception.QuarantineRelease, DocStockAdjustment, q.ID, 0, testHash)
	rev.ExceptionConfirmationID = &id
	res, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), q.ID, rev)
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("confirmed reversal = %d, %v", res.Status, err)
	}
	s := h.stockOf(t, st, st.plain)
	wantQty(t, "quarantine after the reversal", s.Quarantine, "0")
	wantQty(t, "sellable after the reversal", s.Sellable, "10")

	// Reversing any other DC needs no confirmation.
	dmg := h.adjust(t, st, damage(st, st.plain, "1"))
	if _, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), dmg.ID, ReverseInput{}); err != nil {
		t.Errorf("reverse damage = %v", err)
	}
	h.verifyClean(t, st)
}

func TestReversingAQuarantineKeepsTheCostAddedWhileQuarantined(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	q := h.adjust(t, st, AdjustmentInput{Kind: KindQuarantineIn, ReasonID: &st.damage,
		Lines: []AdjustmentLineInput{{SkuID: st.plain, Qty: qty("10")}}})
	// A landed cost arrives while every unit is quarantined.
	if err := h.costAdjust(st, st.plain, "50"); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "stock value with the landed cost", h.stockOf(t, st, st.plain).StockValue, "1050")

	id := h.confirm(t, st, exception.QuarantineRelease, DocStockAdjustment, q.ID, 0, testHash)
	if _, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), q.ID, ReverseInput{ExceptionConfirmationID: &id, PayloadHash: testHash}); err != nil {
		t.Fatal(err)
	}
	s := h.stockOf(t, st, st.plain)
	wantQty(t, "on hand", s.OnHand, "10")
	wantQty(t, "quarantine", s.Quarantine, "0")
	wantMoney(t, "stock value after the reversal", s.StockValue, "1050")
	wantMoney(t, "average cost after the reversal", s.AvgCost, "105")
	h.verifyClean(t, st)

	// The verifier catches a quarantine reversal that drops value: the leg out of the twin lot
	// took 1 050 instead of the 1 000 the quarantine put in, and the projection agrees with it.
	super := testkit.SuperDB(t)
	if _, err := super.Exec(`UPDATE tally.stock_moves SET value_amount = -1050 WHERE reversal_of =
		(SELECT id FROM tally.stock_moves WHERE doc_id = $1 AND move_type = 'quarantine_in')`, q.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(`UPDATE tally.sku_stock SET stock_value = 1000, avg_cost = 100 WHERE sku_id = $1`, st.plain); err != nil {
		t.Fatal(err)
	}
	testkit.InTenant(t, h.d, st.tenantID, func(tx db.Querier) error {
		issues, err := VerifySkus(ctx, tx, []uuid.UUID{st.plain})
		if err != nil {
			return err
		}
		if len(issues) != 1 || !strings.HasPrefix(issues[0], "reversal mirror ") {
			t.Errorf("tampered quarantine reversal: verify = %v, want one reversal mirror mismatch", issues)
		}
		return nil
	})
}

func TestCountGainOfAnUntrackedLotIsBookedIntoTheLotTheCounterRecords(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))

	// SP-LOT tracks lots and expiry and has never had stock: its line has no lot.
	c, _, err := h.svc.StartCount(ctx, st.owner, nil, CountInput{Scope: ScopeSelection, SkuIDs: []uuid.UUID{st.lotted, st.plain}})
	if err != nil {
		t.Fatal(err)
	}
	lotted, plain := countLine(t, c, st.lotted), countLine(t, c, st.plain)
	if lotted.LotID != nil || !lotted.TrackExpiry {
		t.Fatalf("lotted line = %+v", lotted)
	}
	lotNo := "L-FOUND"
	_, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version, []CountLineInput{{LineID: &plain.ID, CountedQty: qtyPtr("10"), LotNo: &lotNo}})
	if e := wantCode(t, err, apperr.Validation, "validation_failed"); e.Fields["lines.0.lot_no"] != "not_tracked" {
		t.Errorf("lot on an untracked SKU: fields = %v", e.Fields)
	}
	c, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version, []CountLineInput{
		{LineID: &lotted.ID, CountedQty: qtyPtr("4"), ReasonID: &st.varianceKind},
		{LineID: &plain.ID, CountedQty: qtyPtr("10")},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Finishing names the line whose gain has no lot to go into.
	_, err = h.svc.FinishCount(ctx, st.owner, c.ID, c.Version)
	e := wantCode(t, err, apperr.Validation, CodeCountLotRequired)
	if e.Fields["lines."+lotted.ID.String()+".lot_no"] != "required" || e.Fields["lines."+lotted.ID.String()+".expiry_date"] != "required" {
		t.Errorf("fields = %v", e.Fields)
	}
	c, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version, []CountLineInput{
		{LineID: &lotted.ID, CountedQty: qtyPtr("4"), ReasonID: &st.varianceKind, LotNo: &lotNo, ExpiryDate: dayPtr("2026-09-30")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ln := countLine(t, c, st.lotted); ln.LotNo == nil || *ln.LotNo != lotNo || ln.ExpiryDate == nil || !ln.ExpiryDate.Equal(day("2026-09-30")) {
		t.Errorf("recorded line = %+v", ln)
	}
	if c, err = h.svc.FinishCount(ctx, st.owner, c.ID, c.Version); err != nil {
		t.Fatal(err)
	}
	post := CountPostInput{Version: c.Version, PayloadHash: testHash}
	id := h.confirm(t, st, exception.CountVariance, DocStockCount, c.ID, c.Version, testHash)
	post.ExceptionConfirmationID = &id
	if res, err := h.svc.PostCount(ctx, st.owner, newKey(), c.ID, post); err != nil || res.Status != http.StatusOK {
		t.Fatalf("post count = %d, %v", res.Status, err)
	}
	lots, err := h.svc.Lots(ctx, st.scope(), LotFilter{SkuID: &st.lotted, Limit: 10})
	if err != nil || len(lots.Lots) != 1 || lots.Lots[0].LotNo != lotNo || lots.Lots[0].ExpiryDate == nil ||
		!lots.Lots[0].ExpiryDate.Equal(day("2026-09-30")) {
		t.Fatalf("lots = %+v, %v", lots.Lots, err)
	}
	wantQty(t, "lotted on hand", h.stockOf(t, st, st.lotted).OnHand, "4")

	// A line that counts a known lot cannot name another one.
	c, _, err = h.svc.StartCount(ctx, st.owner, nil, CountInput{Scope: ScopeSelection, SkuIDs: []uuid.UUID{st.lotted}})
	if err != nil {
		t.Fatal(err)
	}
	known := countLine(t, c, st.lotted)
	_, err = h.svc.UpdateCountLines(ctx, st.owner, c.ID, c.Version, []CountLineInput{{LineID: &known.ID, CountedQty: qtyPtr("4"),
		LotNo: &lotNo, ExpiryDate: dayPtr("2026-10-31")}})
	if e := wantCode(t, err, apperr.Validation, "validation_failed"); e.Fields["lines.0.lot_no"] != "not_allowed" ||
		e.Fields["lines.0.expiry_date"] != "not_allowed" {
		t.Errorf("lot on a known-lot line: fields = %v", e.Fields)
	}
	h.verifyClean(t, st)
}

func TestExpiredManualHoldsAreReleasedOnce(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	at := func(d time.Duration) *time.Time { v := h.clk.Now().Add(d); return &v }
	soon, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.plain, Qty: qty("4"), ExpiresAt: at(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.plain, Qty: qty("2"), ExpiresAt: at(5 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.plain, Qty: qty("1")}); err != nil {
		t.Fatal(err)
	}
	if n, err := h.svc.ReleaseExpiredReservations(ctx, st.tenantID); err != nil || n != 0 {
		t.Errorf("release before any hold expired = %d, %v", n, err)
	}

	h.clk.Set(h.clk.Now().Add(2 * time.Hour))
	if n, err := h.svc.ReleaseExpiredReservations(ctx, st.tenantID); err != nil || n != 1 {
		t.Fatalf("release = %d, %v; want the expired hold", n, err)
	}
	if n, err := h.svc.ReleaseExpiredReservations(ctx, st.tenantID); err != nil || n != 0 {
		t.Errorf("second run = %d, %v; want nothing left to release", n, err)
	}
	wantQty(t, "reserved", h.stockOf(t, st, st.plain).Reserved, "3")
	list, err := h.svc.ListReservations(ctx, st.scope(), ReservationFilter{SkuID: &st.plain, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.ID == soon.ID && (r.Status != ReservationReleased || !r.Open().IsZero()) {
			t.Errorf("expired hold = %+v", r)
		}
	}
	var audited int
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM audit_events WHERE action = 'reservation.expire' AND entity_id = $1
			AND actor_type = 'system' AND source = 'worker' AND before IS NOT NULL AND after IS NOT NULL`, soon.ID).Scan(&audited)
	})
	if audited != 1 {
		t.Errorf("reservation.expire audit rows = %d, want 1", audited)
	}
	h.verifyClean(t, st)
}

func TestReservingCountsLotsExpiredSinceTheLastRoll(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	cost := vnd("10")
	h.adjust(t, st, AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{
		{SkuID: st.lotted, Qty: qty("5"), UnitCost: &cost, LotNo: "L-SOON", ExpiryDate: dayPtr("2026-03-20")},
		{SkuID: st.lotted, Qty: qty("3"), UnitCost: &cost, LotNo: "L-LATE", ExpiryDate: dayPtr("2026-06-30")},
	}})
	// L-SOON expired and the roller has not run: the projection still says 8 sellable.
	h.clk.Set(time.Date(2026, 3, 25, 3, 0, 0, 0, time.UTC))
	_, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.lotted, Qty: qty("4")})
	wantErr(t, err, apperr.Conflict, CodeOverReserve)
	if _, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.lotted, Qty: qty("3")}); err != nil {
		t.Fatal(err)
	}
	s := h.stockOf(t, st, st.lotted)
	wantQty(t, "reserved", s.Reserved, "3")
	wantQty(t, "expired", s.Expired, "5")
	h.verifyClean(t, st)
}

// TestABackorderedLineHoldsMoreAfterItsReservationSettled raises a consumed invoice line
// reservation (the line is still on backorder) and proves it becomes active again within
// sellable, while a manual hold that ended stays ended.
func TestABackorderedLineHoldsMoreAfterItsReservationSettled(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	rs := h.svc.Reservations()
	run := func(fn func(tx Tx) (Reservation, error)) (Reservation, error) {
		var out Reservation
		err := h.d.WithTenantTx(ctx, st.tenantID, func(q db.Querier) error {
			var err error
			out, err = fn(&jobTx{Querier: q})
			return err
		})
		return out, err
	}

	line, err := run(func(tx Tx) (Reservation, error) {
		return rs.Reserve(ctx, tx, st.tenantID, h.clk.Now(), ReserveRequest{SkuID: st.plain, Qty: qty("4"),
			SourceType: ReserveInvoiceLine, SourceID: ids.New()})
	})
	if err != nil {
		t.Fatal(err)
	}
	if line, err = run(func(tx Tx) (Reservation, error) {
		return rs.Consume(ctx, tx, st.tenantID, h.clk.Now(), line.ID, qty("4"))
	}); err != nil {
		t.Fatal(err)
	}
	if line.Status != ReservationConsumed {
		t.Fatalf("status after consuming everything = %s, want %s", line.Status, ReservationConsumed)
	}
	if err := h.issue(st, IssueLine{SkuID: st.plain, Qty: qty("4")}); err != nil {
		t.Fatal(err)
	}

	manual, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.plain, Qty: qty("1")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReleaseReservation(ctx, st.owner, manual.ID, ReleaseInput{Reason: "khách hủy"}); err != nil {
		t.Fatal(err)
	}
	_, err = run(func(tx Tx) (Reservation, error) {
		return rs.Adjust(ctx, tx, st.tenantID, h.clk.Now(), manual.ID, qty("2"))
	})
	wantErr(t, err, apperr.Conflict, CodeReservationNotActive)

	_, err = run(func(tx Tx) (Reservation, error) {
		return rs.Adjust(ctx, tx, st.tenantID, h.clk.Now(), line.ID, qty("11"))
	})
	wantErr(t, err, apperr.Conflict, CodeOverReserve)
	_, err = run(func(tx Tx) (Reservation, error) {
		return rs.Adjust(ctx, tx, st.tenantID, h.clk.Now(), line.ID, qty("3"))
	})
	if e := wantCode(t, err, apperr.Validation, "validation_failed"); e.Fields["qty"] != "below_settled" {
		t.Errorf("fields = %v, want qty below_settled", e.Fields)
	}

	more, err := run(func(tx Tx) (Reservation, error) {
		return rs.Adjust(ctx, tx, st.tenantID, h.clk.Now(), line.ID, qty("10"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if more.Status != ReservationActive {
		t.Errorf("status after holding more = %s, want %s", more.Status, ReservationActive)
	}
	wantQty(t, "open", more.Open(), "6")
	wantQty(t, "reserved", h.stockOf(t, st, st.plain).Reserved, "6")

	h.verifyClean(t, st)
}

// TestVerifySkusReadsOnlyTheAskedSkusRows runs every check for one SKU under EXPLAIN ANALYZE and
// proves no scan of stock_moves or reservations returns a row of another SKU: checking one SKU
// costs that SKU's rows, not the tenant's whole ledger.
func TestVerifySkusReadsOnlyTheAskedSkusRows(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	ctx := context.Background()
	h.adjust(t, st, opening(st.plain, "10", "100"))
	dmg := h.adjust(t, st, damage(st, st.plain, "1"))
	if _, err := h.svc.ReverseAdjustment(ctx, st.owner, newKey(), dmg.ID, ReverseInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.plain, Qty: qty("1")}); err != nil {
		t.Fatal(err)
	}
	const plainMoves, plainHolds = 3, 1
	cost := vnd("10")
	for i := range 6 {
		h.adjust(t, st, AdjustmentInput{Kind: KindOpening, Lines: []AdjustmentLineInput{{SkuID: st.lotted, Qty: qty("2"),
			UnitCost: &cost, LotNo: fmt.Sprintf("L-%d", i), ExpiryDate: dayPtr("2026-09-30")}}})
		if _, err := h.svc.CreateReservation(ctx, st.owner, ManualReservation{SkuID: st.lotted, Qty: qty("1")}); err != nil {
			t.Fatal(err)
		}
	}

	type node struct {
		Relation string  `json:"Relation Name"`
		Rows     float64 `json:"Actual Rows"`
		Plans    []node  `json:"Plans"`
	}
	limits := map[string]float64{"stock_moves": plainMoves, "reservations": plainHolds}
	var walk func(check string, n node)
	walk = func(check string, n node) {
		if limit, ok := limits[n.Relation]; ok && n.Rows > limit {
			t.Errorf("%s: a scan of %s returns %v rows per loop, more than the %v of the SKU checked", check, n.Relation, n.Rows, limit)
		}
		for _, c := range n.Plans {
			walk(check, c)
		}
	}
	testkit.InTenant(t, h.d, st.tenantID, func(q db.Querier) error {
		for _, c := range verifyChecks {
			var raw []byte
			query := "EXPLAIN (ANALYZE, FORMAT JSON) " + skuFilterRe.ReplaceAllString(c.query, "$1 = ANY($$1)")
			if err := q.QueryRowxContext(ctx, query, []uuid.UUID{st.plain}).Scan(&raw); err != nil {
				return fmt.Errorf("%s: %w", c.name, err)
			}
			var plans []struct {
				Plan node `json:"Plan"`
			}
			if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
				return fmt.Errorf("%s: plan %s: %w", c.name, raw, err)
			}
			walk(c.name, plans[0].Plan)
		}
		return nil
	})
	h.verifyClean(t, st)
}

func TestRollingBackMaxStockRefusesAppliedCostRecalculations(t *testing.T) {
	h := newHarness(t)
	st := h.newStore(t)
	// Later migrations replaced the COGS source check this down drops, so undo them first, newest
	// first, as a real rollback to before max stock would.
	var down []byte
	for _, name := range []string{"000042_cogs_adjustment_disassembly_source.down.sql",
		"000039_procurement_return_cogs_adjustment.down.sql", "000031_inventory_max_stock_recost.down.sql"} {
		b, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		down = append(down, b...)
		down = append(down, '\n')
	}
	super := testkit.SuperDB(t)
	run := func(prepare string, args ...any) error {
		tx, err := super.Beginx()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }() // never keep the rollback: other tests share the schema
		if _, err := tx.Exec(prepare, args...); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(string(down))
		return err
	}

	err := run(`INSERT INTO tally.cogs_adjustments (id, tenant_id, sku_id, amount, source_doc_type, source_doc_id, business_date, created_at)
		VALUES ($1, $2, $3, 250, 'cost_recalculation', $4, '2026-03-15', now())`, ids.New(), st.tenantID, st.plain, ids.New())
	if err == nil || !strings.Contains(err.Error(), "ck_cogs_adjustments__down_refused_cost_recalculation_applied") {
		t.Errorf("down over an applied recalculation = %v, want the refusal constraint named", err)
	}
	if err := run(`DELETE FROM tally.cogs_adjustments WHERE source_doc_type = 'cost_recalculation'`); err != nil {
		t.Errorf("down without applied recalculations = %v", err)
	}
}
