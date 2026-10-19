package crm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Error codes of the CRM module.
const (
	CodeNoPurchase      = "no_purchase"
	CodeNothingToOrder  = "nothing_to_reorder"
	CodeNoClosedSeason  = "no_closed_campaign"
	CodeReorderDisabled = "reorder_unavailable"
)

// Idempotency scopes of the CRM creates.
const (
	ScopeConsentRecord = "consent.record"
	ScopeReorderCreate = "reorder.create"
)

// Re-order line flags: why a line of the source sale was not copied.
const (
	FlagDiscontinued = "discontinued"
	FlagNeedsChoice  = "needs_choice"
)

const careTaskColumns = `t.id, t.party_id, p.name AS party_name, t.due_on, t.title, t.source_campaign_id, t.status,
	t.done_at, t.created_at, t.updated_at`

// CareTaskInput schedules a follow-up.
type CareTaskInput struct {
	PartyID          uuid.UUID
	DueOn            time.Time
	Title            string
	SourceCampaignID *uuid.UUID
}

// CareTaskPatch changes a task; nil fields are kept. Done or skipped stamps done_at.
type CareTaskPatch struct {
	DueOn  *time.Time
	Title  *string
	Status *string
}

// CareTaskFilter narrows the task list. DueBefore keeps tasks due on or before the date.
type CareTaskFilter struct {
	Status    string
	PartyID   *uuid.UUID
	DueBefore *time.Time
}

// CareTaskCursor is the position after the last task of a page (due date, then id).
type CareTaskCursor struct {
	DueOn time.Time
	ID    uuid.UUID
}

func validTitle(fields map[string]string, title string) {
	switch t := strings.TrimSpace(title); {
	case t == "":
		fields["title"] = "required"
	case len([]rune(t)) > 300:
		fields["title"] = "too_long"
	}
}

// CreateCareTask schedules a follow-up with a customer.
func (s *Service) CreateCareTask(ctx context.Context, actor audit.Actor, in CareTaskInput) (CareTask, error) {
	fields := map[string]string{}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if in.DueOn.IsZero() {
		fields["due_on"] = "required"
	}
	validTitle(fields, in.Title)
	if len(fields) > 0 {
		return CareTask{}, errInvalid(fields)
	}
	var out CareTask
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if err := partyExists(ctx, q, in.PartyID); err != nil {
			return apperr.NewValidation("validation_failed", "The customer does not exist", map[string]string{"party_id": "not_found"})
		}
		if in.SourceCampaignID != nil {
			var ok bool
			if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, *in.SourceCampaignID).Scan(&ok); err != nil {
				return fmt.Errorf("crm: read campaign: %w", err)
			}
			if !ok {
				return errInvalid(map[string]string{"source_campaign_id": "not_found"})
			}
		}
		id, now := ids.New(), s.clock.Now()
		if _, err := q.ExecContext(ctx, `
			INSERT INTO customer_care_tasks (id, tenant_id, party_id, due_on, title, source_campaign_id, status, created_at, updated_at)
			VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, 'open', $6, $6)`,
			id, in.PartyID, in.DueOn, strings.TrimSpace(in.Title), in.SourceCampaignID, now); err != nil {
			return fmt.Errorf("crm: insert care task: %w", err)
		}
		var err error
		if out, err = loadCareTask(ctx, q, id); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{Action: "care_task.created", EntityType: "care_task", EntityID: id, After: out})
	})
	return out, err
}

// UpdateCareTask reschedules, renames or closes a task.
func (s *Service) UpdateCareTask(ctx context.Context, actor audit.Actor, id uuid.UUID, p CareTaskPatch) (CareTask, error) {
	fields := map[string]string{}
	if p.Title != nil {
		validTitle(fields, *p.Title)
	}
	if p.Status != nil && !validTaskStatus(*p.Status) {
		fields["status"] = "invalid"
	}
	if p.DueOn != nil && p.DueOn.IsZero() {
		fields["due_on"] = "required"
	}
	if len(fields) > 0 {
		return CareTask{}, errInvalid(fields)
	}
	var out CareTask
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := loadCareTask(ctx, q, id)
		if err != nil {
			return err
		}
		after, now := before, s.clock.Now()
		if p.Title != nil {
			after.Title = strings.TrimSpace(*p.Title)
		}
		if p.DueOn != nil {
			after.DueOn = *p.DueOn
		}
		if p.Status != nil && *p.Status != before.Status {
			after.Status, after.DoneAt = *p.Status, nil
			if *p.Status != TaskOpen {
				after.DoneAt = &now
			}
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE customer_care_tasks SET title = $2, due_on = $3, status = $4, done_at = $5, updated_at = $6 WHERE id = $1`,
			id, after.Title, after.DueOn, after.Status, after.DoneAt, now); err != nil {
			return fmt.Errorf("crm: update care task: %w", err)
		}
		if out, err = loadCareTask(ctx, q, id); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{Action: "care_task.updated", EntityType: "care_task", EntityID: id, Before: before, After: out})
	})
	return out, err
}

func loadCareTask(ctx context.Context, q db.Querier, id uuid.UUID) (CareTask, error) {
	var t CareTask
	if err := q.QueryRowxContext(ctx, `SELECT `+careTaskColumns+`
		FROM customer_care_tasks t JOIN parties p ON p.id = t.party_id WHERE t.id = $1`, id).StructScan(&t); err != nil {
		if db.IsNoRows(err) {
			return CareTask{}, errNotFound("care task")
		}
		return CareTask{}, fmt.Errorf("crm: load care task: %w", err)
	}
	return t, nil
}

// CareTasks lists tasks by due date, earliest first.
func (s *Service) CareTasks(ctx context.Context, scope Scope, f CareTaskFilter, after *CareTaskCursor, limit int) ([]CareTask, error) {
	if f.Status != "" && !validTaskStatus(f.Status) {
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	var out []CareTask
	err := s.read(ctx, scope, func(q db.Querier) error {
		var due *time.Time
		var id *uuid.UUID
		if after != nil {
			due, id = &after.DueOn, &after.ID
		}
		return sqlxSelect(ctx, q, &out, `SELECT `+careTaskColumns+`
			FROM customer_care_tasks t JOIN parties p ON p.id = t.party_id
			WHERE ($1 = '' OR t.status = $1) AND ($2::uuid IS NULL OR t.party_id = $2)
				AND ($3::date IS NULL OR t.due_on <= $3)
				AND ($4::date IS NULL OR (t.due_on, t.id) > ($4, $5::uuid))
			ORDER BY t.due_on, t.id
			LIMIT $6`, f.Status, f.PartyID, f.DueBefore, due, id, limit)
	})
	return out, err
}

// LastSeasonCursor is the position after the last customer of a page (name key, then id).
type LastSeasonCursor struct {
	NameKey string
	ID      uuid.UUID
}

// LastSeason lists the customers whose sales lines are attributed (primary) to a campaign —
// by default the latest closed one — with their net purchases in it. A return of an attributed
// line counts against it; reversed documents drop out.
func (s *Service) LastSeason(ctx context.Context, scope Scope, campaignID *uuid.UUID, after *LastSeasonCursor, limit int) (uuid.UUID, []LastSeasonCustomer, error) {
	var out []LastSeasonCustomer
	var id uuid.UUID
	err := s.read(ctx, scope, func(q db.Querier) error {
		if campaignID != nil {
			var ok bool
			if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, *campaignID).Scan(&ok); err != nil {
				return fmt.Errorf("crm: read campaign: %w", err)
			}
			if !ok {
				return errNotFound("campaign")
			}
			id = *campaignID
		} else {
			err := q.QueryRowxContext(ctx, `
				SELECT id FROM campaigns WHERE status = 'closed' ORDER BY ends_on DESC, id DESC LIMIT 1`).Scan(&id)
			if db.IsNoRows(err) {
				return apperr.NewValidation(CodeNoClosedSeason, "No campaign is closed yet", map[string]string{"campaign_id": "required"})
			}
			if err != nil {
				return fmt.Errorf("crm: read last campaign: %w", err)
			}
		}
		var key *string
		var pid *uuid.UUID
		if after != nil {
			key, pid = &after.NameKey, &after.ID
		}
		return sqlxSelect(ctx, q, &out, `
			WITH lines AS (
				SELECT d.customer_party_id AS party_id, d.id AS doc_id, d.business_date,
					l.line_amount - l.tax_amount AS net
				FROM campaign_attributions a
					JOIN sales_document_lines l ON l.id = a.doc_line_id
					JOIN sales_documents d ON d.id = l.sales_document_id
				WHERE a.campaign_id = $1 AND a.role = 'primary' AND a.doc_type = 'sales_document'
					AND d.status IN ('posted', 'partially_reversed') AND d.reversal_of IS NULL
					AND d.customer_party_id IS NOT NULL AND l.line_kind NOT IN ('mix_pack', 'combo_component')
			), returned AS (
				SELECT d.customer_party_id AS party_id,
					-(rl.line_amount - round(rl.line_amount * COALESCE(l.tax_amount / NULLIF(l.line_amount, 0), 0))) AS net
				FROM campaign_attributions a
					JOIN sales_document_lines l ON l.id = a.doc_line_id
					JOIN sales_documents d ON d.id = l.sales_document_id
					JOIN sales_return_lines rl ON rl.sales_document_line_id = l.id
					JOIN sales_returns r ON r.id = rl.sales_return_id
				WHERE a.campaign_id = $1 AND a.role = 'primary' AND a.doc_type = 'sales_document'
					AND d.status IN ('posted', 'partially_reversed') AND d.reversal_of IS NULL
					AND d.customer_party_id IS NOT NULL AND r.status = 'posted' AND r.reversal_of IS NULL
			), per_party AS (
				SELECT party_id, count(DISTINCT doc_id) AS documents, sum(net) AS net, max(business_date) AS last_on
				FROM lines GROUP BY party_id
			)
			SELECT p.id AS party_id, p.name, p.name_key, p.phone, pp.documents,
				pp.net + COALESCE((SELECT sum(r.net) FROM returned r WHERE r.party_id = pp.party_id), 0) AS net_revenue,
				pp.last_on AS last_purchase_on,
				(SELECT count(*) FROM customer_care_tasks t WHERE t.party_id = p.id AND t.status = 'open') AS open_tasks
			FROM per_party pp JOIN parties p ON p.id = pp.party_id
			WHERE $2::text IS NULL OR (p.name_key, p.id) > ($2, $3::uuid)
			ORDER BY p.name_key, p.id
			LIMIT $4`, id, key, pid, limit)
	})
	return id, out, err
}

// ReorderInput asks for a draft invoice repeating a customer's purchase; without a document the
// latest one is used.
type ReorderInput struct {
	SalesDocumentID *uuid.UUID
}

// ReorderLine is one line of the source sale and what happened to it.
type ReorderLine struct {
	SkuID   *uuid.UUID `db:"sku_id"`
	Code    *string    `db:"sku_code_snap"`
	Name    string     `db:"name_snap"`
	Kind    string     `db:"line_kind"`
	ColorID *uuid.UUID `db:"color_id"`
	UnitID  uuid.UUID  `db:"unit_id"`
	Qty     money.Qty  `db:"qty"`
	BaseQty money.Qty  `db:"base_qty"`
	// SkuActive and UnitActive tell whether the SKU is still sold and the unit still offered.
	SkuActive  bool       `db:"sku_active"`
	UnitActive bool       `db:"unit_active"`
	BaseUnitID *uuid.UUID `db:"base_unit_id"`
	// Copied is set when the line is on the draft; Flag says why it is not.
	Copied bool   `db:"-"`
	Flag   string `db:"-"`
}

// Reorder is the draft invoice of a re-order with the lines of its source sale.
type Reorder struct {
	SourceDocumentID uuid.UUID
	SourceDocNo      string
	Draft            Draft
	Lines            []ReorderLine
}

// Reorder builds a draft invoice from a customer's past sale at today's prices, units and pack
// sizes: discontinued SKUs are flagged and left out, a unit no longer offered falls back to the
// base unit, mix packs and combos are flagged for the owner to choose again, gifts are left to
// the promotions in force. Nothing is confirmed; the owner reviews the draft. The draft and its
// timeline row commit together; a key replays the first result of the request it identifies.
func (s *Service) Reorder(ctx context.Context, actor audit.Actor, key *idempotency.Key, partyID uuid.UUID, in ReorderInput) (Reorder, bool, error) {
	if s.drafts == nil {
		return Reorder{}, false, apperr.NewConflict(CodeReorderDisabled, "Re-order drafts are not available")
	}
	var out Reorder
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeReorderCreate, key, http.StatusCreated, &out, func() error {
			return s.reorder(ctx, q, actor, partyID, in, &out)
		})
		return err
	})
	if err != nil {
		return Reorder{}, false, err
	}
	return out, replayed, nil
}

func (s *Service) reorder(ctx context.Context, q db.Querier, actor audit.Actor, partyID uuid.UUID, in ReorderInput, out *Reorder) error {
	if err := partyExists(ctx, q, partyID); err != nil {
		return err
	}
	err := q.QueryRowxContext(ctx, `
		SELECT id, doc_no FROM sales_documents
		WHERE customer_party_id = $1 AND status IN ('posted', 'partially_reversed') AND reversal_of IS NULL
			AND ($2::uuid IS NULL OR id = $2)
		ORDER BY business_date DESC, posted_at DESC, id DESC LIMIT 1`, partyID, in.SalesDocumentID).
		Scan(&out.SourceDocumentID, &out.SourceDocNo)
	if db.IsNoRows(err) {
		if in.SalesDocumentID != nil {
			return errInvalid(map[string]string{"sales_document_id": "not_found"})
		}
		return apperr.NewConflict(CodeNoPurchase, "The customer has no purchase to repeat")
	}
	if err != nil {
		return fmt.Errorf("crm: read source sale: %w", err)
	}
	if err := sqlxSelect(ctx, q, &out.Lines, `
		SELECT l.sku_id, l.sku_code_snap, l.name_snap, l.line_kind, l.color_id, l.unit_id, l.qty, l.base_qty,
			COALESCE(s.status = 'active', false) AS sku_active,
			COALESCE((SELECT u.active FROM sku_units u WHERE u.sku_id = l.sku_id AND u.unit_id = l.unit_id), false) AS unit_active,
			s.base_unit_id
		FROM sales_document_lines l LEFT JOIN skus s ON s.id = l.sku_id
		WHERE l.sales_document_id = $1 AND l.parent_line_id IS NULL AND l.line_kind <> 'gift'
		ORDER BY l.line_no`, out.SourceDocumentID); err != nil {
		return err
	}
	lines := planReorder(out.Lines)
	if len(lines) == 0 {
		return apperr.NewConflict(CodeNothingToOrder, "No line of the sale can be ordered again")
	}
	if out.Draft, err = s.drafts.DraftInvoice(ctx, q, actor, partyID, lines, "Đặt lại theo "+out.SourceDocNo); err != nil {
		return err
	}
	ref, by := RefInvoice, actor.ID
	return insertActivity(ctx, q, Activity{
		ID: ids.New(), PartyID: partyID, Kind: ActivityReorder, RefType: &ref, RefID: &out.Draft.InvoiceID,
		Summary: fmt.Sprintf("Tạo đơn nháp %s theo %s", out.Draft.DocNo, out.SourceDocNo), CreatedBy: &by, CreatedAt: s.clock.Now(),
	})
}

// planReorder decides which source lines go on the draft and flags the rest.
func planReorder(lines []ReorderLine) []DraftLine {
	var out []DraftLine
	for i := range lines {
		l := &lines[i]
		switch {
		case l.SkuID == nil || l.Kind == "mix_pack" || l.Kind == "combo":
			l.Flag = FlagNeedsChoice
		case !l.SkuActive:
			l.Flag = FlagDiscontinued
		default:
			d := DraftLine{SkuID: *l.SkuID, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty}
			if !l.UnitActive && l.BaseUnitID != nil {
				d.UnitID, d.Qty = *l.BaseUnitID, l.BaseQty
			}
			l.Copied = true
			out = append(out, d)
		}
	}
	return out
}
