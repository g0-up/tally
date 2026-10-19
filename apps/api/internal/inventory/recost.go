package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// TopicRecostSuggested asks the worker to preview a backdate recost. The posting that changed
// an earlier cost only publishes it: building the preview locks the tenant's run list, which a
// posting transaction holding sku_stock locks must never wait for.
const TopicRecostSuggested = "cost_recalculation.suggested"

// Recost audit actions.
const (
	actionRecostPreview = "cost_recalculation.preview"
	actionRecostApply   = "cost_recalculation.apply"
	actionRecostDiscard = "cost_recalculation.discard"
)

// RecostRun is a cost_recalculation_runs row.
type RecostRun struct {
	ID          uuid.UUID    `db:"id"`
	SkuIDs      []uuid.UUID  `db:"-"`
	SkuIDsJSON  []byte       `db:"sku_ids_json"`
	FromDate    time.Time    `db:"from_date"`
	Status      string       `db:"status"`
	ImpactJSON  []byte       `db:"impact"`
	Impact      RecostImpact `db:"-"`
	RunVersion  int          `db:"run_version"`
	RequestedBy uuid.UUID    `db:"requested_by"`
	ConfirmedBy *uuid.UUID   `db:"confirmed_by"`
	CreatedAt   time.Time    `db:"created_at"`
	AppliedAt   *time.Time   `db:"applied_at"`
}

// RecostImpact is what applying a run changes: per SKU the stock value correction booked as a
// cost_adjustment move and the cost delta of quantities already issued booked as a COGS
// adjustment.
type RecostImpact struct {
	Skus            []SkuRecost `json:"skus"`
	StockValueDelta money.Money `json:"stock_value_delta"`
	CogsDelta       money.Money `json:"cogs_delta"`
}

// SkuRecost is the impact of a run on one SKU.
type SkuRecost struct {
	SkuID           uuid.UUID   `json:"sku_id"`
	SkuCode         string      `json:"sku_code"`
	OldAvgCost      money.Money `json:"old_avg_cost"`
	NewAvgCost      money.Money `json:"new_avg_cost"`
	OldStockValue   money.Money `json:"old_stock_value"`
	NewStockValue   money.Money `json:"new_stock_value"`
	StockValueDelta money.Money `json:"stock_value_delta"`
	CogsDelta       money.Money `json:"cogs_delta"`
}

func (s SkuRecost) zero() bool { return s.StockValueDelta.IsZero() && s.CogsDelta.IsZero() }

func (r *RecostRun) decode() error {
	if err := json.Unmarshal(r.SkuIDsJSON, &r.SkuIDs); err != nil {
		return fmt.Errorf("inventory: decode recost skus: %w", err)
	}
	if err := json.Unmarshal(r.ImpactJSON, &r.Impact); err != nil {
		return fmt.Errorf("inventory: decode recost impact: %w", err)
	}
	return nil
}

const recostColumns = `id, to_json(sku_ids) AS sku_ids_json, from_date, status, impact, run_version, requested_by, confirmed_by,
	created_at, applied_at`

// RecostPlanner previews and applies backdate cost recalculations. Moving-average costs depend
// on the order of movements, so a posting dated before later movements of the same SKU leaves
// those later issues valued at a stale average. The planner replays the SKU's ledger in business
// date order and measures the difference; nothing changes until the owner confirms the run.
type RecostPlanner struct{}

// suggest publishes TopicRecostSuggested when a SKU of the posting has movements dated after it.
func (RecostPlanner) suggest(ctx context.Context, tx Tx, p Posting, skuIDs []uuid.UUID) error {
	if p.DocType == DocCostRecalc || len(skuIDs) == 0 {
		return nil
	}
	var later []uuid.UUID
	if err := sqlx.SelectContext(ctx, tx, &later, `
		SELECT DISTINCT sku_id FROM stock_moves
		WHERE sku_id = ANY($1) AND business_date > $2 AND doc_type <> $3 ORDER BY sku_id`,
		skuIDs, p.BusinessDate, DocCostRecalc); err != nil {
		return fmt.Errorf("inventory: find later moves: %w", err)
	}
	if len(later) == 0 {
		return nil
	}
	tx.Publish(outbox.Event{Topic: TopicRecostSuggested, Payload: recostSuggestion{
		SkuIDs: later, FromDate: p.BusinessDate.Format(time.DateOnly), RequestedBy: p.PostedBy, DocType: p.DocType, DocID: p.DocID,
	}})
	return nil
}

// recostSuggestion is the payload of TopicRecostSuggested.
type recostSuggestion struct {
	SkuIDs      []uuid.UUID `json:"sku_ids"`
	FromDate    string      `json:"from_date"`
	RequestedBy uuid.UUID   `json:"requested_by"`
	DocType     string      `json:"doc_type"`
	DocID       uuid.UUID   `json:"doc_id"`
}

// replayState is the running quantity and value of one SKU during a replay.
type replayState struct {
	qty   money.Qty
	value money.Money
	avg   money.Money
}

func (s *replayState) receive(qty money.Qty, value money.Money) error {
	s.qty, s.value = s.qty.Add(qty), s.value.Add(value)
	return s.reprice()
}

func (s *replayState) reprice() error {
	if !s.qty.IsPositive() || s.value.IsNegative() {
		return nil
	}
	avg, err := moneyOf(s.value.Decimal().DivRound(s.qty.Decimal(), money.MoneyScale))
	if err != nil {
		return err
	}
	s.avg = avg
	return nil
}

// issue takes qty out at the running average, with the ledger's rules: the issue that empties
// the stock takes the whole value and an issue never takes more value than there is. A quantity
// the replay does not hold yet (a sale dated before the receipt that covered it) is valued at the
// running average.
func (s *replayState) issue(qty money.Qty) money.Money {
	var v money.Money
	switch {
	case !s.qty.IsPositive():
		v = s.avg.MulQty(qty)
	case qty.Cmp(s.qty) >= 0:
		v = s.value.Add(s.avg.MulQty(qty.Sub(s.qty)))
	default:
		v = s.avg.MulQty(qty)
		if v.Cmp(s.value) > 0 {
			v = s.value
		}
	}
	s.qty, s.value = s.qty.Sub(qty), s.value.Sub(v)
	return v
}

// replayResult is the outcome of a replay.
type replayResult struct {
	state replayState
	// issuedDelta is replayed minus recorded value of every issue, net of reversals.
	issuedDelta money.Money
	// stranded is cost that met no stock (a cost adjustment on an empty replay stock).
	stranded money.Money
}

// replay revalues a SKU's moves in business date order (moves must come sorted). Receipts keep
// their recorded value, except count gains, which are valued at the running average as the ledger
// did; issues take the running average; quarantine pairs only re-price; earlier recost
// corrections are skipped (the replay recomputes what they corrected); reversals undo the
// replayed value of their original.
func replay(moves []StockMove) (replayResult, error) {
	var r replayResult
	byID := make(map[uuid.UUID]StockMove, len(moves))
	replayed := make(map[uuid.UUID]money.Money, len(moves))
	costOnly := func(delta money.Money) error {
		if !r.state.qty.IsPositive() {
			r.stranded = r.stranded.Add(delta)
			return nil
		}
		r.state.value = r.state.value.Add(delta)
		if r.state.value.IsNegative() {
			r.stranded, r.state.value = r.stranded.Add(r.state.value), money.ZeroMoney
		}
		return r.state.reprice()
	}
	for _, m := range moves {
		byID[m.ID] = m
		switch {
		case m.DocType == DocCostRecalc:
			continue
		case m.MoveType == MoveQuarantineIn || m.MoveType == MoveQuarantineOut:
			// A quarantine pair leaves quantity and value alone but re-prices, as in the ledger.
			if err := r.state.reprice(); err != nil {
				return r, err
			}
		case m.MoveType == MoveCostAdjustment:
			if err := costOnly(m.Value); err != nil {
				return r, err
			}
		case m.MoveType == MoveReversal:
			orig, ok := byID[*m.ReversalOf]
			if !ok || orig.DocType == DocCostRecalc {
				continue
			}
			if orig.MoveType == MoveQuarantineIn || orig.MoveType == MoveQuarantineOut {
				if err := r.state.reprice(); err != nil {
					return r, err
				}
				continue
			}
			value, ok := replayed[orig.ID]
			if !ok {
				value = orig.Value.Abs()
			}
			switch {
			case orig.MoveType == MoveCostAdjustment:
				if err := costOnly(m.Value); err != nil {
					return r, err
				}
			case m.Qty.IsPositive():
				// An issue put back: its replayed value returns, its recorded value is undone.
				r.issuedDelta = r.issuedDelta.Sub(value.Sub(orig.Value.Neg()))
				if err := r.state.receive(m.Qty, value); err != nil {
					return r, err
				}
			default:
				// A receipt taken back out, by the ledger's rule for it.
				q := m.Qty.Neg()
				if q.Cmp(r.state.qty) >= 0 || value.Cmp(r.state.value) > 0 {
					value = r.state.value
				}
				r.state.qty, r.state.value = r.state.qty.Sub(q), r.state.value.Sub(value)
				if err := r.state.reprice(); err != nil {
					return r, err
				}
			}
		case m.Qty.IsPositive():
			value := m.Value
			if m.MoveType == MoveCountGain {
				value = r.state.avg.MulQty(m.Qty)
			}
			replayed[m.ID] = value
			if err := r.state.receive(m.Qty, value); err != nil {
				return r, err
			}
		case m.Qty.IsNegative():
			v := r.state.issue(m.Qty.Neg())
			replayed[m.ID] = v
			r.issuedDelta = r.issuedDelta.Add(v.Sub(m.Value.Neg()))
		}
	}
	return r, nil
}

// impactOf replays one SKU against its current projection. Value the replay leaves behind an
// empty stock, and cost that met no stock, are issued cost. COGS adjustments of earlier recost
// runs are subtracted, so applying a run twice never books the same delta twice.
func impactOf(ctx context.Context, q db.Querier, skuID uuid.UUID, code string, current SkuStock) (SkuRecost, error) {
	var moves []StockMove
	if err := sqlx.SelectContext(ctx, q, &moves, `SELECT `+moveColumns+` FROM stock_moves
		WHERE sku_id = $1 ORDER BY business_date, posted_at, created_at, id`, skuID); err != nil {
		return SkuRecost{}, fmt.Errorf("inventory: read moves to replay: %w", err)
	}
	r, err := replay(moves)
	if err != nil {
		return SkuRecost{}, err
	}
	var prior money.Money
	if err := q.QueryRowxContext(ctx, `SELECT COALESCE(SUM(amount), 0) FROM cogs_adjustments
		WHERE sku_id = $1 AND source_doc_type = $2`, skuID, DocCostRecalc).Scan(&prior); err != nil {
		return SkuRecost{}, fmt.Errorf("inventory: read earlier recost cogs: %w", err)
	}
	out := SkuRecost{SkuID: skuID, SkuCode: code, OldAvgCost: current.AvgCost, NewAvgCost: current.AvgCost,
		OldStockValue: current.StockValue, NewStockValue: current.StockValue}
	cogs := r.issuedDelta.Add(r.stranded).Sub(prior)
	if current.OnHand.IsPositive() && r.state.qty.Equal(current.OnHand) && !r.state.value.IsNegative() {
		out.NewStockValue, out.NewAvgCost = r.state.value, r.state.avg
	} else {
		// Nothing left to carry a correction: every replayed value has been issued.
		cogs = cogs.Add(r.state.value)
	}
	out.StockValueDelta = out.NewStockValue.Sub(current.StockValue)
	out.CogsDelta = cogs
	return out, nil
}

// impacts computes the impact of a run over skuIDs from the current projections.
func impacts(ctx context.Context, q db.Querier, skuIDs []uuid.UUID, stocks map[uuid.UUID]SkuStock) (RecostImpact, error) {
	infos, err := readSkuInfos(ctx, q, skuIDs)
	if err != nil {
		return RecostImpact{}, err
	}
	out := RecostImpact{Skus: []SkuRecost{}}
	for _, id := range skuIDs {
		s, ok := stocks[id]
		if !ok {
			continue
		}
		imp, err := impactOf(ctx, q, id, infos[id].Code, s)
		if err != nil {
			return RecostImpact{}, err
		}
		if imp.zero() {
			continue
		}
		out.Skus = append(out.Skus, imp)
		out.StockValueDelta = out.StockValueDelta.Add(imp.StockValueDelta)
		out.CogsDelta = out.CogsDelta.Add(imp.CogsDelta)
	}
	return out, nil
}

// lockRunList serializes the writers of the tenant's recost runs (one open preview, run
// version numbers).
func lockRunList(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('cost_recalculation_runs:' || $1::text, 0))`,
		tenantID); err != nil {
		return fmt.Errorf("inventory: lock recost runs: %w", err)
	}
	return nil
}

func readRun(ctx context.Context, q db.Querier, where string, args ...any) (*RecostRun, error) {
	var r RecostRun
	err := q.QueryRowxContext(ctx, `SELECT `+recostColumns+` FROM cost_recalculation_runs WHERE `+where, args...).StructScan(&r)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read recost run: %w", err)
	}
	return &r, r.decode()
}

// preview builds the tenant's open recost preview over skuIDs from the business date from,
// merging it into the preview still open: a tenant has at most one, so the owner confirms one
// consistent set of corrections. A preview whose impact is zero is not kept. It returns the open
// run, nil when there is nothing to correct.
func (RecostPlanner) preview(ctx context.Context, tx Tx, tenantID, requestedBy uuid.UUID, skuIDs []uuid.UUID,
	from, now time.Time) (*RecostRun, error) {
	if err := lockRunList(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	open, err := readRun(ctx, tx, `status = $1 ORDER BY run_version DESC LIMIT 1 FOR UPDATE`, RecostPreviewed)
	if err != nil {
		return nil, err
	}
	skus := slices.Clone(skuIDs)
	if open != nil {
		skus = append(skus, open.SkuIDs...)
		if open.FromDate.Before(from) {
			from = open.FromDate
		}
	}
	skus = sortedUnique(skus)
	stocks, err := readSkuStock(ctx, tx, skus)
	if err != nil {
		return nil, err
	}
	imp, err := impacts(ctx, tx, skus, stocks)
	if err != nil {
		return nil, err
	}
	impactJSON, err := json.Marshal(imp)
	if err != nil {
		return nil, fmt.Errorf("inventory: encode recost impact: %w", err)
	}
	switch {
	case open == nil && len(imp.Skus) == 0:
		return nil, nil
	case open == nil:
		open = &RecostRun{ID: ids.New(), Status: RecostPreviewed, RequestedBy: requestedBy, CreatedAt: now}
		if err := tx.QueryRowxContext(ctx, `
			INSERT INTO cost_recalculation_runs (id, tenant_id, sku_ids, from_date, status, impact, run_version, requested_by, created_at)
			SELECT $1, $2, $3, $4, $5, $6, COALESCE(MAX(run_version), 0) + 1, $7, $8 FROM cost_recalculation_runs
			RETURNING run_version`,
			open.ID, tenantID, skus, from, RecostPreviewed, impactJSON, requestedBy, now).Scan(&open.RunVersion); err != nil {
			return nil, fmt.Errorf("inventory: insert recost run: %w", err)
		}
	case len(imp.Skus) == 0:
		if _, err := tx.ExecContext(ctx, `UPDATE cost_recalculation_runs SET status = $2, impact = $3 WHERE id = $1`,
			open.ID, RecostDiscarded, impactJSON); err != nil {
			return nil, fmt.Errorf("inventory: discard recost run: %w", err)
		}
		return nil, nil
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE cost_recalculation_runs SET sku_ids = $2, from_date = $3, impact = $4 WHERE id = $1`,
			open.ID, skus, from, impactJSON); err != nil {
			return nil, fmt.Errorf("inventory: update recost run: %w", err)
		}
	}
	open.SkuIDs, open.FromDate, open.Impact, open.ImpactJSON = skus, from, imp, impactJSON
	tx.Publish(outbox.Event{Topic: TopicRecostPreviewed, Payload: map[string]any{
		"run_id": open.ID, "run_version": open.RunVersion, "sku_ids": skus, "from_date": from.Format(time.DateOnly),
		"stock_value_delta": imp.StockValueDelta, "cogs_delta": imp.CogsDelta,
	}})
	return open, nil
}

// confirmRecost applies a previewed run after the owner confirmed it: it re-replays under the
// sku_stock locks, refuses a preview the ledger has moved past (recost_stale, preview again),
// books the stock value corrections as cost_adjustment moves and the issued cost deltas as
// cogs_adjustments, both on today's business date: a locked period is corrected in the open one.
type confirmRecost struct {
	ledger *StockLedger
	run    RecostRun
	today  time.Time
}

func (c confirmRecost) Scope() string           { return "cost_recalculation.apply" }
func (c confirmRecost) BusinessDate() time.Time { return c.today }
func (c confirmRecost) DocPrefix() string       { return "" }

func (c confirmRecost) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.run.SkuIDs...)
	return set
}

func (c confirmRecost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	run, err := readRun(ctx, tx, `id = $1 FOR UPDATE`, c.run.ID)
	if err != nil {
		return posting.Result{}, err
	}
	if run == nil {
		return posting.Result{}, errNotFound("Cost recalculation")
	}
	if run.Status != RecostPreviewed {
		return posting.Result{}, errRecostNotPending()
	}
	if !slices.Equal(run.SkuIDs, c.run.SkuIDs) {
		return posting.Result{}, errRecostStale()
	}
	stocks, err := readSkuStock(ctx, tx, run.SkuIDs)
	if err != nil {
		return posting.Result{}, err
	}
	imp, err := impacts(ctx, tx, run.SkuIDs, stocks)
	if err != nil {
		return posting.Result{}, err
	}
	if !sameImpact(imp, run.Impact) {
		return posting.Result{}, errRecostStale()
	}
	p := PostingOf(tx, DocCostRecalc, run.ID)
	var lines []CostLine
	for _, s := range imp.Skus {
		if !s.StockValueDelta.IsZero() {
			lines = append(lines, CostLine{SkuID: s.SkuID, Value: s.StockValueDelta})
		}
	}
	if len(lines) > 0 {
		if _, err := c.ledger.CostAdjust(ctx, tx, p, lines); err != nil {
			return posting.Result{}, err
		}
	}
	for _, s := range imp.Skus {
		if s.CogsDelta.IsZero() {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cogs_adjustments (id, tenant_id, sku_id, amount, source_doc_type, source_doc_id, business_date, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			ids.New(), tx.TenantID(), s.SkuID, s.CogsDelta, DocCostRecalc, run.ID, c.today, tx.Now()); err != nil {
			return posting.Result{}, fmt.Errorf("inventory: insert cogs adjustment: %w", err)
		}
	}
	now := tx.Now()
	actor := tx.Actor().ID
	if _, err := tx.ExecContext(ctx, `UPDATE cost_recalculation_runs SET status = $2, confirmed_by = $3, applied_at = $4 WHERE id = $1`,
		run.ID, RecostApplied, actor, now); err != nil {
		return posting.Result{}, fmt.Errorf("inventory: apply recost run: %w", err)
	}
	before := *run
	run.Status, run.ConfirmedBy, run.AppliedAt = RecostApplied, &actor, &now
	tx.Audit(audit.Event{Action: actionRecostApply, EntityType: "cost_recalculation", EntityID: run.ID,
		Before: map[string]any{"status": before.Status}, After: map[string]any{"status": run.Status, "impact": run.Impact}})
	return posting.Result{Status: http.StatusOK, Body: toRecostDTO(*run)}, nil
}

// sameImpact compares the deltas a run would book.
func sameImpact(a, b RecostImpact) bool {
	if len(a.Skus) != len(b.Skus) {
		return false
	}
	for i := range a.Skus {
		x, y := a.Skus[i], b.Skus[i]
		if x.SkuID != y.SkuID || !x.StockValueDelta.Equal(y.StockValueDelta) || !x.CogsDelta.Equal(y.CogsDelta) {
			return false
		}
	}
	return true
}

func errRecostNotPending() error {
	return apperr.NewConflict(CodeRecostNotPending, "This cost recalculation is no longer waiting for confirmation")
}

func errRecostStale() error {
	return apperr.NewConflict(CodeRecostStale, "Stock moved since this preview; preview the cost recalculation again")
}

// maxRecostSkus bounds the SKUs of a manual recost preview.
const maxRecostSkus = 200

// PreviewRecost builds (or widens) the tenant's open recost preview over skuIDs from the
// business date from, as a posting does when it changes an earlier cost. It returns nil when the
// ledger has nothing to correct.
func (s *Service) PreviewRecost(ctx context.Context, actor audit.Actor, skuIDs []uuid.UUID, from time.Time) (*RecostRun, error) {
	skus := sortedUnique(skuIDs)
	switch {
	case len(skus) == 0:
		return nil, errInvalid(map[string]string{"sku_ids": "required"})
	case len(skus) > maxRecostSkus:
		return nil, errInvalid(map[string]string{"sku_ids": "too_many"})
	}
	today, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return nil, err
	}
	if from.After(today) {
		return nil, errInvalid(map[string]string{"from_date": "in_future"})
	}
	var out *RecostRun
	err = s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		infos, err := readSkuInfos(ctx, q, skus)
		if err != nil {
			return err
		}
		if len(infos) != len(skus) {
			return errNotFound("SKU")
		}
		tx := &jobTx{Querier: q}
		run, err := s.ledger.recost.preview(ctx, tx, actor.TenantID, actor.ID, skus, from, s.clock.Now())
		if err != nil {
			return err
		}
		if run != nil {
			if err := s.record(ctx, q, actor, actionRecostPreview, DocCostRecalc, run.ID, nil,
				map[string]any{"sku_ids": run.SkuIDs, "from_date": run.FromDate.Format(time.DateOnly), "impact": run.Impact}); err != nil {
				return err
			}
		}
		out = run
		return s.flush(ctx, tx, actor.TenantID)
	})
	return out, err
}

// ConfirmRecost applies the previewed run id through the coordinator (see confirmRecost).
func (s *Service) ConfirmRecost(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID) (posting.Response, error) {
	today, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var run *RecostRun
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		run, err = readRun(ctx, q, `id = $1`, id)
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	if run == nil {
		return posting.Response{}, errNotFound("Cost recalculation")
	}
	return s.posting.Post(ctx, actor, key, confirmRecost{ledger: s.ledger, run: *run, today: today})
}

// DiscardRecost drops the previewed run id without booking anything.
func (s *Service) DiscardRecost(ctx context.Context, actor audit.Actor, id uuid.UUID) (RecostRun, error) {
	var out RecostRun
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if err := lockRunList(ctx, q, actor.TenantID); err != nil {
			return err
		}
		run, err := readRun(ctx, q, `id = $1 FOR UPDATE`, id)
		if err != nil {
			return err
		}
		if run == nil {
			return errNotFound("Cost recalculation")
		}
		if run.Status != RecostPreviewed {
			return errRecostNotPending()
		}
		if _, err := q.ExecContext(ctx, `UPDATE cost_recalculation_runs SET status = $2 WHERE id = $1`, id, RecostDiscarded); err != nil {
			return fmt.Errorf("inventory: discard recost run: %w", err)
		}
		run.Status = RecostDiscarded
		out = *run
		return s.record(ctx, q, actor, actionRecostDiscard, DocCostRecalc, id,
			map[string]any{"status": RecostPreviewed}, map[string]any{"status": RecostDiscarded})
	})
	return out, err
}

// Recost returns one run.
func (s *Service) Recost(ctx context.Context, scope Scope, id uuid.UUID) (RecostRun, error) {
	var out RecostRun
	err := s.read(ctx, scope, func(q db.Querier) error {
		run, err := readRun(ctx, q, `id = $1`, id)
		if err != nil {
			return err
		}
		if run == nil {
			return errNotFound("Cost recalculation")
		}
		out = *run
		return nil
	})
	return out, err
}

// RecostFilter narrows the run list (newest first).
type RecostFilter struct {
	Status  string
	AfterAt *time.Time
	AfterID uuid.UUID
	Limit   int
}

// Recosts lists the tenant's recost runs.
func (s *Service) Recosts(ctx context.Context, scope Scope, f RecostFilter) ([]RecostRun, error) {
	out := []RecostRun{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+recostColumns+` FROM cost_recalculation_runs
			WHERE ($1 = '' OR status = $1) AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3::uuid))
			ORDER BY created_at DESC, id DESC LIMIT $4`, f.Status, f.AfterAt, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("inventory: list recost runs: %w", err)
		}
		for i := range out {
			if err := out[i].decode(); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
