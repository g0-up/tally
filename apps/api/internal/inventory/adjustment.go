package inventory

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Idempotency scopes of the stock adjustment postings.
const (
	ScopeAdjustmentPost    = "stock_adjustment.post"
	ScopeAdjustmentReverse = "stock_adjustment.reverse"
)

// maxAdjustmentLines bounds one DC.
const maxAdjustmentLines = 200

// adjustmentColumns lists stock_adjustments columns.
const adjustmentColumns = `id, warehouse_id, doc_no, kind, reason_id, stock_count_id, business_date, note, status, idempotency_key,
	posted_at, posted_by, reversal_of, created_at`

const adjustmentLineColumns = `id, stock_adjustment_id, sku_id, sku_code_snap, name_snap, variant_label_snap, unit_id, unit_name_snap,
	unit_factor_snap, qty, base_qty, color_id, spec_text, direction, lot_id, lot_no, expiry_date, unit_cost`

// generalReasonKinds are the reasons an increase, a decrease or a quarantine may cite.
var generalReasonKinds = []string{"damage", "loss", "gift", "internal_use", "count_variance", "disposal"}

// ledgerSide is how a DC kind books its lines.
type ledgerSide int

const (
	sideReceive ledgerSide = iota
	sideIssue
	sideQuarantine
)

// AdjustmentPolicy is the rule set of one DC kind: how it books, which reasons it accepts, what
// a line may carry and which owner confirmation it consumes.
type AdjustmentPolicy struct {
	side ledgerSide
	move MoveType
	// direction is the sign of the line on sellable stock (stock_adjustment_lines.direction).
	direction int
	// reasonKinds lists the reason kinds accepted; empty means the kind takes no reason.
	reasonKinds []string
	// costRequired asks a unit cost on every line (opening stock).
	costRequired bool
	// release moves quarantined stock back (quarantine side only).
	release bool
	// exception is the confirmation the kind consumes ("" for none).
	exception exception.Kind
}

// PolicyOf returns the policy of a DC kind a user may post; count DCs are posted by stock
// counts only.
func PolicyOf(kind string) (AdjustmentPolicy, bool) {
	switch kind {
	case KindIncrease:
		return AdjustmentPolicy{side: sideReceive, move: MoveAdjustmentIn, direction: 1, reasonKinds: generalReasonKinds}, true
	case KindOpening:
		return AdjustmentPolicy{side: sideReceive, move: MoveOpening, direction: 1, costRequired: true}, true
	case KindDecrease:
		return AdjustmentPolicy{side: sideIssue, move: MoveAdjustmentOut, direction: -1, reasonKinds: generalReasonKinds}, true
	case KindDamage, KindLoss, KindGift, KindInternalUse:
		return AdjustmentPolicy{side: sideIssue, move: MoveAdjustmentOut, direction: -1, reasonKinds: []string{kind}}, true
	case KindDisposal:
		return AdjustmentPolicy{side: sideIssue, move: MoveAdjustmentOut, direction: -1, reasonKinds: []string{kind},
			exception: exception.StockDisposal}, true
	case KindQuarantineIn:
		return AdjustmentPolicy{side: sideQuarantine, direction: -1, reasonKinds: generalReasonKinds}, true
	case KindQuarantineRelease:
		return AdjustmentPolicy{side: sideQuarantine, direction: 1, release: true, reasonKinds: generalReasonKinds,
			exception: exception.QuarantineRelease}, true
	}
	return AdjustmentPolicy{}, false
}

// exceptionFor is the confirmation a DC of the policy consumes when it cites a reason of
// reasonKind: a decrease or a quarantine that cites a disposal reason disposes of stock just as a
// disposal DC does, so it needs the same owner confirmation.
func (p AdjustmentPolicy) exceptionFor(reasonKind string) exception.Kind {
	if p.exception == "" && p.direction < 0 && reasonKind == KindDisposal {
		return exception.StockDisposal
	}
	return p.exception
}

// NeedsReauth reports whether posting a DC of the kind citing a reason of reasonKind needs a
// recent re-authentication (the DCs that consume an owner confirmation).
func NeedsReauth(kind, reasonKind string) bool {
	p, ok := PolicyOf(kind)
	return ok && p.exceptionFor(reasonKind) != ""
}

// AdjustmentNeedsReauth reports whether posting a DC of the kind citing reasonID needs a recent
// re-authentication. An unknown reason does not: the posting rejects it.
func (s *Service) AdjustmentNeedsReauth(ctx context.Context, tenantID uuid.UUID, kind string, reasonID *uuid.UUID) (bool, error) {
	reasonKind := ""
	if reasonID != nil {
		err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
			err := q.QueryRowxContext(ctx, `SELECT kind FROM reasons WHERE id = $1`, *reasonID).Scan(&reasonKind)
			if db.IsNoRows(err) {
				return nil
			}
			return err
		})
		if err != nil {
			return false, fmt.Errorf("inventory: read reason kind: %w", err)
		}
	}
	return NeedsReauth(kind, reasonKind), nil
}

// AdjustmentLineInput is one line of a new DC. Qty is in UnitID (the base unit when nil);
// UnitCost is per UnitID.
type AdjustmentLineInput struct {
	SkuID      uuid.UUID
	UnitID     *uuid.UUID
	Qty        money.Qty
	ColorID    *uuid.UUID
	Uncolored  bool
	LotID      *uuid.UUID
	LotNo      string
	ExpiryDate *time.Time
	UnitCost   *money.Money
}

// AdjustmentInput is a new DC.
type AdjustmentInput struct {
	Kind     string
	ReasonID *uuid.UUID
	// BusinessDate defaults to today; it may not be in the future.
	BusinessDate *time.Time
	Note         *string
	// ExceptionConfirmationID and PayloadHash bind the owner's confirmation (disposal,
	// quarantine release) to this exact request.
	ExceptionConfirmationID *uuid.UUID
	PayloadHash             []byte
	Lines                   []AdjustmentLineInput
}

// validate checks what does not need the database.
func (in AdjustmentInput) validate(p AdjustmentPolicy) error {
	fields := map[string]string{}
	if len(p.reasonKinds) == 0 && in.ReasonID != nil {
		fields["reason_id"] = "not_allowed"
	}
	if len(p.reasonKinds) > 0 && in.ReasonID == nil {
		fields["reason_id"] = "required"
	}
	if in.Note != nil && utf8.RuneCountInString(*in.Note) > maxNoteLen {
		fields["note"] = "too_long"
	}
	switch {
	case len(in.Lines) == 0:
		fields["lines"] = "required"
	case len(in.Lines) > maxAdjustmentLines:
		fields["lines"] = "too_many"
	}
	for i, ln := range in.Lines {
		if ln.SkuID == uuid.Nil {
			fields[lineField(i, "sku_id")] = "required"
		}
		if !ln.Qty.IsPositive() {
			fields[lineField(i, "qty")] = "must_be_positive"
		}
		if ln.ColorID != nil && ln.Uncolored {
			fields[lineField(i, "uncolored")] = "conflicts_with_color"
		}
		if ln.UnitCost != nil && ln.UnitCost.IsNegative() {
			fields[lineField(i, "unit_cost")] = "must_not_be_negative"
		}
		if p.side != sideReceive {
			if ln.UnitCost != nil {
				fields[lineField(i, "unit_cost")] = "not_allowed"
			}
			if strings.TrimSpace(ln.LotNo) != "" {
				fields[lineField(i, "lot_no")] = "not_allowed"
			}
			if ln.ExpiryDate != nil {
				fields[lineField(i, "expiry_date")] = "not_allowed"
			}
		} else if ln.Uncolored {
			fields[lineField(i, "uncolored")] = "not_allowed"
		}
		if p.costRequired && ln.UnitCost == nil {
			fields[lineField(i, "unit_cost")] = "required"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// AdjustmentDetail is a DC with its lines and the DC that reversed it.
type AdjustmentDetail struct {
	StockAdjustment
	Lines      []StockAdjustmentLine
	ReversedBy *uuid.UUID
}

// PostAdjustment posts a DC of a user kind through the coordinator.
func (s *Service) PostAdjustment(ctx context.Context, actor audit.Actor, key idempotency.Key, in AdjustmentInput) (posting.Response, error) {
	p, ok := PolicyOf(in.Kind)
	if !ok {
		return posting.Response{}, errInvalid(map[string]string{"kind": "invalid"})
	}
	if err := in.validate(p); err != nil {
		return posting.Response{}, err
	}
	today, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	date := today
	if in.BusinessDate != nil {
		if in.BusinessDate.After(today) {
			return posting.Response{}, errInvalid(map[string]string{"business_date": "in_future"})
		}
		date = *in.BusinessDate
	}
	return s.posting.Post(ctx, actor, key, adjustmentPost{s: s, date: date, policy: p, in: in,
		key: ScopeAdjustmentPost + ":" + key.Value})
}

type adjustmentPost struct {
	s      *Service
	date   time.Time
	policy AdjustmentPolicy
	in     AdjustmentInput
	key    string
}

func (c adjustmentPost) Scope() string           { return ScopeAdjustmentPost }
func (c adjustmentPost) BusinessDate() time.Time { return c.date }
func (c adjustmentPost) DocPrefix() string       { return docseq.PrefixStockAdjustment }

func (c adjustmentPost) LockKeys() posting.LockSet {
	var set posting.LockSet
	for _, ln := range c.in.Lines {
		set.Add(posting.LockSkuStock, ln.SkuID)
	}
	return set
}

// lineUnit is the unit a DC line was entered in.
type lineUnit struct {
	ID     uuid.UUID
	Name   string
	Factor money.Factor
}

// resolveUnits returns the unit of every line: the named active unit of the SKU, or its base
// unit.
func resolveUnits(ctx context.Context, q db.Querier, lines []AdjustmentLineInput, infos map[uuid.UUID]skuInfo) ([]lineUnit, error) {
	skuIDs := make([]uuid.UUID, 0, len(lines))
	for _, ln := range lines {
		skuIDs = append(skuIDs, ln.SkuID)
	}
	var rows []struct {
		SkuID  uuid.UUID    `db:"sku_id"`
		UnitID uuid.UUID    `db:"unit_id"`
		Name   string       `db:"name"`
		Factor money.Factor `db:"factor"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT su.sku_id, su.unit_id, u.name, su.factor FROM sku_units su JOIN units u ON u.id = su.unit_id
		WHERE su.sku_id = ANY($1) AND su.active`, sortedUnique(skuIDs)); err != nil {
		return nil, fmt.Errorf("inventory: read sku units: %w", err)
	}
	type key struct{ sku, unit uuid.UUID }
	units := make(map[key]lineUnit, len(rows))
	for _, r := range rows {
		units[key{r.SkuID, r.UnitID}] = lineUnit{ID: r.UnitID, Name: r.Name, Factor: r.Factor}
	}
	out := make([]lineUnit, len(lines))
	fields := map[string]string{}
	for i, ln := range lines {
		info := infos[ln.SkuID]
		if ln.UnitID == nil || *ln.UnitID == info.BaseUnitID {
			out[i] = lineUnit{ID: info.BaseUnitID, Name: info.BaseUnitName, Factor: money.MustFactor("1")}
			continue
		}
		u, ok := units[key{ln.SkuID, *ln.UnitID}]
		if !ok {
			fields[lineField(i, "unit_id")] = "not_a_unit_of_sku"
			continue
		}
		out[i] = u
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	return out, nil
}

// checkReason verifies that id names an active reason of one of kinds and returns its kind
// ("" when id is nil).
func checkReason(ctx context.Context, q db.Querier, id *uuid.UUID, kinds []string, field string) (string, error) {
	if id == nil {
		return "", nil
	}
	var kind string
	var active bool
	err := q.QueryRowxContext(ctx, `SELECT kind, active FROM reasons WHERE id = $1`, *id).Scan(&kind, &active)
	if db.IsNoRows(err) {
		return "", errInvalid(map[string]string{field: "not_found"})
	}
	if err != nil {
		return "", fmt.Errorf("inventory: read reason: %w", err)
	}
	switch {
	case !active:
		return "", errInvalid(map[string]string{field: "inactive"})
	case !slices.Contains(kinds, kind):
		return "", errInvalid(map[string]string{field: "wrong_kind"})
	}
	return kind, nil
}

func (c adjustmentPost) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	in, p := c.in, c.policy
	skuIDs := make([]uuid.UUID, len(in.Lines))
	for i, ln := range in.Lines {
		skuIDs[i] = ln.SkuID
	}
	infos, err := readSkuInfos(ctx, tx, sortedUnique(skuIDs))
	if err != nil {
		return posting.Result{}, err
	}
	fields := map[string]string{}
	for i, ln := range in.Lines {
		info, ok := infos[ln.SkuID]
		switch {
		case !ok:
			fields[lineField(i, "sku_id")] = "not_found"
		case info.ProductType == "combo":
			fields[lineField(i, "sku_id")] = "not_stockable"
		}
	}
	if len(fields) > 0 {
		return posting.Result{}, errInvalid(fields)
	}
	reasonKind, err := checkReason(ctx, tx, in.ReasonID, p.reasonKinds, "reason_id")
	if err != nil {
		return posting.Result{}, err
	}
	units, err := resolveUnits(ctx, tx, in.Lines, infos)
	if err != nil {
		return posting.Result{}, err
	}
	id := ids.New()
	// The confirmation is created before the DC exists, so it names the first SKU; its payload
	// hash covers every line, and the audit below lists every SKU it was used for.
	exc := p.exceptionFor(reasonKind)
	var confirmation uuid.UUID
	if exc != "" {
		if in.ExceptionConfirmationID != nil {
			confirmation = *in.ExceptionConfirmationID
		}
		if err := c.s.exceptions.Consume(ctx, tx, tx.TenantID(), exception.Expectation{ConfirmationID: confirmation,
			Kind: exc, DocType: "sku", DocID: in.Lines[0].SkuID, PayloadHash: in.PayloadHash,
			UsedByDocType: DocStockAdjustment, UsedByDocID: id}); err != nil {
			return posting.Result{}, err
		}
	}
	lines := make([]StockAdjustmentLine, len(in.Lines))
	for i, ln := range in.Lines {
		info, u := infos[ln.SkuID], units[i]
		base, err := money.BaseQty(ln.Qty, u.Factor)
		if err != nil {
			return posting.Result{}, errInvalid(map[string]string{lineField(i, "qty"): "too_precise"})
		}
		var lotNo *string
		if no := strings.TrimSpace(ln.LotNo); no != "" {
			lotNo = &no
		}
		lines[i] = StockAdjustmentLine{ID: ids.New(), StockAdjustmentID: id, SkuID: ln.SkuID, SkuCodeSnap: info.Code,
			NameSnap: info.DisplayName, VariantLabelSnap: info.VariantLabel, UnitID: u.ID, UnitNameSnap: u.Name,
			UnitFactorSnap: u.Factor, Qty: ln.Qty, BaseQty: base, ColorID: ln.ColorID, SpecText: info.SpecText,
			Direction: p.direction, LotID: ln.LotID, LotNo: lotNo, ExpiryDate: ln.ExpiryDate, UnitCost: ln.UnitCost}
	}
	posted, err := c.book(ctx, tx, id, lines)
	if err != nil {
		return posting.Result{}, err
	}
	fillLots(lines, posted, p.side)
	adj := StockAdjustment{ID: id, DocNo: tx.DocNo(), Kind: in.Kind, ReasonID: in.ReasonID, BusinessDate: c.date, Note: in.Note,
		Status: AdjustmentPosted, IdempotencyKey: c.key, PostedAt: tx.Now(), PostedBy: tx.Actor().ID, CreatedAt: tx.Now()}
	if adj.WarehouseID, err = warehouseID(ctx, tx); err != nil {
		return posting.Result{}, err
	}
	if err := insertAdjustment(ctx, tx, tx.TenantID(), adj, lines); err != nil {
		return posting.Result{}, err
	}
	if in.Kind == KindOpening {
		if err := completeOpeningStock(ctx, tx, tx.TenantID(), tx.Now(), tx.Actor().ID); err != nil {
			return posting.Result{}, err
		}
	}
	detail := AdjustmentDetail{StockAdjustment: adj, Lines: lines}
	var after any = toAdjustmentDTO(detail)
	if exc != "" {
		after = confirmedAdjustmentAudit{adjustmentDTO: toAdjustmentDTO(detail), ExceptionKind: exc,
			ExceptionConfirmationID: confirmation, SkuIDs: sortedUnique(skuIDs)}
	}
	tx.Audit(audit.Event{Action: "stock_adjustment.post", EntityType: DocStockAdjustment, EntityID: id, After: after})
	return posting.Result{Status: http.StatusCreated, Body: toAdjustmentDTO(detail)}, nil
}

// book posts the lines on the ledger as the policy says.
func (c adjustmentPost) book(ctx context.Context, tx posting.Tx, id uuid.UUID, lines []StockAdjustmentLine) ([]PostedMove, error) {
	pst := PostingOf(tx, DocStockAdjustment, id)
	ledger := c.s.ledger
	switch c.policy.side {
	case sideReceive:
		in := make([]ReceiveLine, len(lines))
		for i, ln := range lines {
			r := ReceiveLine{DocLineID: &lines[i].ID, SkuID: ln.SkuID, Qty: ln.BaseQty, MoveType: c.policy.move,
				ColorID: ln.ColorID, ExpiryDate: ln.ExpiryDate, LotID: ln.LotID, AtAverageCost: ln.UnitCost == nil}
			if ln.LotNo != nil {
				r.LotNo = *ln.LotNo
			}
			if ln.UnitCost != nil {
				cost, err := moneyOf(ln.UnitCost.Decimal().DivRound(ln.UnitFactorSnap.Decimal(), money.MoneyScale))
				if err != nil {
					return nil, err
				}
				r.UnitCost = cost
			}
			in[i] = r
		}
		return ledger.Receive(ctx, tx, pst, in)
	case sideIssue:
		in := make([]IssueLine, len(lines))
		for i, ln := range lines {
			in[i] = IssueLine{DocLineID: &lines[i].ID, SkuID: ln.SkuID, Qty: ln.BaseQty, MoveType: c.policy.move,
				ColorID: ln.ColorID, Uncolored: c.in.Lines[i].Uncolored, LotID: ln.LotID}
		}
		return ledger.Issue(ctx, tx, pst, in)
	default:
		in := make([]QuarantineLine, len(lines))
		for i, ln := range lines {
			in[i] = QuarantineLine{DocLineID: &lines[i].ID, SkuID: ln.SkuID, Qty: ln.BaseQty, ColorID: ln.ColorID,
				Uncolored: c.in.Lines[i].Uncolored, LotID: ln.LotID, Release: c.policy.release}
		}
		return ledger.Quarantine(ctx, tx, pst, in)
	}
}

// fillLots records on each line the lot it touched when it touched exactly one (the source lot
// of a quarantine pair), with its color, number and expiry, and the unit cost a receipt was
// valued at.
func fillLots(lines []StockAdjustmentLine, moves []PostedMove, side ledgerSide) {
	for i := range lines {
		ln := &lines[i]
		var lots []PostedMove
		for _, m := range moves {
			if m.DocLineID == nil || *m.DocLineID != ln.ID || (side == sideQuarantine && m.MoveType != MoveQuarantineOut) {
				continue
			}
			if !slices.ContainsFunc(lots, func(o PostedMove) bool { return o.LotID == m.LotID }) {
				lots = append(lots, m)
			}
		}
		if len(lots) != 1 {
			continue
		}
		m := lots[0]
		lotID := m.LotID
		ln.LotID, ln.ColorID, ln.ExpiryDate = &lotID, m.ColorID, m.ExpiryDate
		ln.LotNo = nil
		if m.LotNo != "" {
			no := m.LotNo
			ln.LotNo = &no
		}
		if side == sideReceive && ln.UnitCost == nil {
			cost := m.UnitCost.MulFactor(ln.UnitFactorSnap)
			ln.UnitCost = &cost
		}
	}
}

func insertAdjustment(ctx context.Context, q db.Querier, tenantID uuid.UUID, a StockAdjustment, lines []StockAdjustmentLine) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO stock_adjustments (id, tenant_id, warehouse_id, doc_no, kind, reason_id, stock_count_id, business_date, note, status,
		  idempotency_key, posted_at, posted_by, reversal_of, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		a.ID, tenantID, a.WarehouseID, a.DocNo, a.Kind, a.ReasonID, a.StockCountID, a.BusinessDate, a.Note, a.Status,
		a.IdempotencyKey, a.PostedAt, a.PostedBy, a.ReversalOf, a.CreatedAt); err != nil {
		return fmt.Errorf("inventory: insert stock adjustment: %w", err)
	}
	for _, ln := range lines {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO stock_adjustment_lines (id, tenant_id, stock_adjustment_id, sku_id, sku_code_snap, name_snap, variant_label_snap,
			  unit_id, unit_name_snap, unit_factor_snap, qty, base_qty, color_id, spec_text, direction, lot_id, lot_no, expiry_date, unit_cost)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
			ln.ID, tenantID, a.ID, ln.SkuID, ln.SkuCodeSnap, ln.NameSnap, ln.VariantLabelSnap, ln.UnitID, ln.UnitNameSnap,
			ln.UnitFactorSnap, ln.Qty, ln.BaseQty, ln.ColorID, ln.SpecText, ln.Direction, ln.LotID, ln.LotNo, ln.ExpiryDate,
			ln.UnitCost); err != nil {
			return fmt.Errorf("inventory: insert stock adjustment line: %w", err)
		}
	}
	return nil
}

// completeOpeningStock ticks the opening_stock onboarding step on the first opening DC.
func completeOpeningStock(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time, by uuid.UUID) error {
	var byID *uuid.UUID
	if by != uuid.Nil {
		byID = &by
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO onboarding_steps (tenant_id, step_key, completed_at, completed_by) VALUES ($1, 'opening_stock', $2, $3)
		ON CONFLICT ON CONSTRAINT pk_onboarding_steps DO UPDATE SET completed_at = EXCLUDED.completed_at, completed_by = EXCLUDED.completed_by
		WHERE onboarding_steps.completed_at IS NULL`, tenantID, now, byID); err != nil {
		return fmt.Errorf("inventory: complete opening stock step: %w", err)
	}
	return nil
}

// ReverseInput is a DC reversal request. Reversing a quarantine DC puts the stock back on sale,
// so it consumes the owner's quarantine release confirmation bound to the reversed DC and to this
// exact request, like a quarantine release DC does.
type ReverseInput struct {
	ExceptionConfirmationID *uuid.UUID
	PayloadHash             []byte
}

// ReverseAdjustment reverses a posted DC with a new DC that mirrors its moves (reversal_of on
// the document and on every move). The reversal is dated today.
func (s *Service) ReverseAdjustment(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, in ReverseInput) (posting.Response, error) {
	today, err := s.today(ctx, actor.TenantID)
	if err != nil {
		return posting.Response{}, err
	}
	var skus []uuid.UUID
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if _, err := readAdjustment(ctx, q, id, false); err != nil {
			return err
		}
		return sqlx.SelectContext(ctx, q, &skus, `SELECT DISTINCT sku_id FROM stock_adjustment_lines WHERE stock_adjustment_id = $1`, id)
	}); err != nil {
		return posting.Response{}, err
	}
	return s.posting.Post(ctx, actor, key, adjustmentReverse{s: s, date: today, id: id, skus: skus, in: in,
		key: ScopeAdjustmentReverse + ":" + key.Value})
}

type adjustmentReverse struct {
	s    *Service
	date time.Time
	id   uuid.UUID
	skus []uuid.UUID
	in   ReverseInput
	key  string
}

func (c adjustmentReverse) Scope() string           { return ScopeAdjustmentReverse }
func (c adjustmentReverse) BusinessDate() time.Time { return c.date }
func (c adjustmentReverse) DocPrefix() string       { return docseq.PrefixStockAdjustment }

func (c adjustmentReverse) LockKeys() posting.LockSet {
	var set posting.LockSet
	set.Add(posting.LockSkuStock, c.skus...)
	return set
}

func (c adjustmentReverse) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	orig, err := readAdjustment(ctx, tx, c.id, true)
	if err != nil {
		return posting.Result{}, err
	}
	switch {
	case orig.ReversalOf != nil:
		return posting.Result{}, apperr.NewConflict("reversal_not_reversible", "A reversal cannot be reversed; post a new adjustment")
	case orig.Status != AdjustmentPosted:
		return posting.Result{}, errAlreadyReversed()
	}
	lines, err := adjustmentLines(ctx, tx, c.id)
	if err != nil {
		return posting.Result{}, err
	}
	id := ids.New()
	if orig.Kind == KindQuarantineIn {
		var confirmation uuid.UUID
		if c.in.ExceptionConfirmationID != nil {
			confirmation = *c.in.ExceptionConfirmationID
		}
		if err := c.s.exceptions.Consume(ctx, tx, tx.TenantID(), exception.Expectation{ConfirmationID: confirmation,
			Kind: exception.QuarantineRelease, DocType: DocStockAdjustment, DocID: c.id, PayloadHash: c.in.PayloadHash,
			UsedByDocType: DocStockAdjustment, UsedByDocID: id}); err != nil {
			return posting.Result{}, err
		}
	}
	if _, err := c.s.ledger.Reverse(ctx, tx, PostingOf(tx, DocStockAdjustment, id), DocStockAdjustment, c.id); err != nil {
		return posting.Result{}, err
	}
	origID := c.id
	rev := StockAdjustment{ID: id, WarehouseID: orig.WarehouseID, DocNo: tx.DocNo(), Kind: orig.Kind, ReasonID: orig.ReasonID,
		StockCountID: orig.StockCountID, BusinessDate: c.date, Note: orig.Note, Status: AdjustmentPosted, IdempotencyKey: c.key,
		PostedAt: tx.Now(), PostedBy: tx.Actor().ID, ReversalOf: &origID, CreatedAt: tx.Now()}
	revLines := make([]StockAdjustmentLine, len(lines))
	for i, ln := range lines {
		ln.ID, ln.StockAdjustmentID, ln.Direction = ids.New(), id, -ln.Direction
		revLines[i] = ln
	}
	if err := insertAdjustment(ctx, tx, tx.TenantID(), rev, revLines); err != nil {
		return posting.Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stock_adjustments SET status = $2 WHERE id = $1`, c.id, AdjustmentReversed); err != nil {
		return posting.Result{}, fmt.Errorf("inventory: mark stock adjustment reversed: %w", err)
	}
	tx.Audit(audit.Event{Action: "stock_adjustment.reverse", EntityType: DocStockAdjustment, EntityID: c.id, DocNo: orig.DocNo,
		Before: map[string]any{"status": orig.Status}, After: map[string]any{"status": AdjustmentReversed, "reversal_id": id,
			"reversal_doc_no": rev.DocNo}})
	return posting.Result{Status: http.StatusCreated, Body: toAdjustmentDTO(AdjustmentDetail{StockAdjustment: rev, Lines: revLines})}, nil
}

func readAdjustment(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (StockAdjustment, error) {
	query := `SELECT ` + adjustmentColumns + ` FROM stock_adjustments WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var a StockAdjustment
	err := q.QueryRowxContext(ctx, query, id).StructScan(&a)
	if db.IsNoRows(err) {
		return StockAdjustment{}, errNotFound("Stock adjustment")
	}
	if err != nil {
		return StockAdjustment{}, fmt.Errorf("inventory: read stock adjustment: %w", err)
	}
	return a, nil
}

func adjustmentLines(ctx context.Context, q db.Querier, id uuid.UUID) ([]StockAdjustmentLine, error) {
	out := []StockAdjustmentLine{}
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+adjustmentLineColumns+` FROM stock_adjustment_lines
		WHERE stock_adjustment_id = $1 ORDER BY sku_code_snap, id`, id); err != nil {
		return nil, fmt.Errorf("inventory: read stock adjustment lines: %w", err)
	}
	return out, nil
}

// Adjustment returns one DC with its lines.
func (s *Service) Adjustment(ctx context.Context, scope Scope, id uuid.UUID) (AdjustmentDetail, error) {
	var out AdjustmentDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		a, err := readAdjustment(ctx, q, id, false)
		if err != nil {
			return err
		}
		lines, err := adjustmentLines(ctx, q, id)
		if err != nil {
			return err
		}
		out = AdjustmentDetail{StockAdjustment: a, Lines: lines}
		var rev uuid.UUID
		err = q.QueryRowxContext(ctx, `SELECT id FROM stock_adjustments WHERE reversal_of = $1`, id).Scan(&rev)
		switch {
		case err == nil:
			out.ReversedBy = &rev
		case !db.IsNoRows(err):
			return fmt.Errorf("inventory: read reversal: %w", err)
		}
		return nil
	})
	return out, err
}

// AdjustmentFilter narrows the DC list (newest first). DocNo finds one document by its exact number.
type AdjustmentFilter struct {
	Kind    string
	Status  string
	SkuID   *uuid.UUID
	DocNo   string
	From    *time.Time
	To      *time.Time
	AfterAt *time.Time
	AfterID uuid.UUID
	Limit   int
}

// adjustmentPreviewLines bounds the lines a DC of the list shows.
const adjustmentPreviewLines = 3

// AdjustmentLinePreview is one of the first lines of a DC of the list.
type AdjustmentLinePreview struct {
	StockAdjustmentID uuid.UUID  `db:"stock_adjustment_id"`
	ID                uuid.UUID  `db:"id"`
	NameSnap          string     `db:"name_snap"`
	VariantLabelSnap  string     `db:"variant_label_snap"`
	ColorID           *uuid.UUID `db:"color_id"`
	LotNo             *string    `db:"lot_no"`
}

// AdjustmentSummary is a DC of the list with what its lines add up to. ValueIn and ValueOut are
// the inventory value the posting added and removed, read from the ledger, so a decrease is
// valued at the cost it took out and a quarantine move shows the value it moved once each way.
type AdjustmentSummary struct {
	StockAdjustment
	LineCount    int         `db:"line_count"`
	TotalBaseQty money.Qty   `db:"total_base_qty"`
	ValueIn      money.Money `db:"value_in"`
	ValueOut     money.Money `db:"value_out"`
	Preview      []AdjustmentLinePreview
}

// Adjustments lists DCs with a summary of their lines and their first lines.
func (s *Service) Adjustments(ctx context.Context, scope Scope, f AdjustmentFilter) ([]AdjustmentSummary, error) {
	out := []AdjustmentSummary{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+adjustmentColumns+`, l.line_count, l.total_base_qty, m.value_in, m.value_out
			FROM stock_adjustments a
			CROSS JOIN LATERAL (SELECT count(*) AS line_count, coalesce(sum(x.base_qty), 0) AS total_base_qty
				FROM stock_adjustment_lines x WHERE x.stock_adjustment_id = a.id) l
			CROSS JOIN LATERAL (SELECT coalesce(sum(v.value_amount) FILTER (WHERE v.value_amount > 0), 0) AS value_in,
					coalesce(-sum(v.value_amount) FILTER (WHERE v.value_amount < 0), 0) AS value_out
				FROM stock_moves v WHERE v.doc_type = $9 AND v.doc_id = a.id) m
			WHERE ($1 = '' OR kind = $1) AND ($2 = '' OR status = $2)
			  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM stock_adjustment_lines x WHERE x.stock_adjustment_id = a.id AND x.sku_id = $3))
			  AND ($4::date IS NULL OR business_date >= $4) AND ($5::date IS NULL OR business_date <= $5)
			  AND ($6::timestamptz IS NULL OR (posted_at, id) < ($6, $7::uuid))
			  AND ($10 = '' OR doc_no = $10)
			ORDER BY posted_at DESC, id DESC LIMIT $8`,
			f.Kind, f.Status, f.SkuID, f.From, f.To, f.AfterAt, f.AfterID, f.Limit, DocStockAdjustment, strings.TrimSpace(f.DocNo)); err != nil {
			return fmt.Errorf("inventory: list stock adjustments: %w", err)
		}
		if len(out) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		var previews []AdjustmentLinePreview
		if err := sqlx.SelectContext(ctx, q, &previews, `SELECT stock_adjustment_id, id, name_snap, variant_label_snap, color_id, lot_no
			FROM (SELECT x.stock_adjustment_id, x.id, x.name_snap, x.variant_label_snap, x.color_id, coalesce(x.lot_no, NULLIF(lo.lot_no, '')) AS lot_no,
					row_number() OVER (PARTITION BY x.stock_adjustment_id ORDER BY x.id) AS n
				FROM stock_adjustment_lines x LEFT JOIN lots lo ON lo.id = x.lot_id
				WHERE x.stock_adjustment_id = ANY($1::uuid[])) p
			WHERE n <= $2 ORDER BY stock_adjustment_id, id`, ids, adjustmentPreviewLines); err != nil {
			return fmt.Errorf("inventory: list stock adjustment lines: %w", err)
		}
		at := make(map[uuid.UUID]int, len(out))
		for i := range out {
			at[out[i].ID] = i
		}
		for _, l := range previews {
			i := at[l.StockAdjustmentID]
			out[i].Preview = append(out[i].Preview, l)
		}
		return nil
	})
	return out, err
}
