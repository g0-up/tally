package campaign

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
}

// Service implements campaigns, their checklists, attribution and demand tracking.
type Service struct {
	db    *db.DB
	clock clock.Clock
	audit *audit.Recorder
}

// NewService returns the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit}
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, action, entity string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: id, Before: before, After: after})
}

// Input is a new campaign. PriceListIDs and PromotionIDs are linked to it.
type Input struct {
	Name                string
	Kind                string
	StartsOn            time.Time
	EndsOn              time.Time
	Budget              *money.Money
	TargetRevenue       *money.Money
	TargetMarginPercent *money.Percent
	PriceListIDs        []uuid.UUID
	PromotionIDs        []uuid.UUID
}

// Opt is an optional patch field: Set false keeps the stored value; Set with a nil Value clears it.
type Opt[T any] struct {
	Set   bool
	Value *T
}

// Patch changes a campaign; nil fields and unset options keep the stored values. PriceListIDs
// and PromotionIDs, when given, replace the linked sets.
type Patch struct {
	Name                *string
	Kind                *string
	StartsOn            *time.Time
	EndsOn              *time.Time
	Budget              Opt[money.Money]
	TargetRevenue       Opt[money.Money]
	TargetMarginPercent Opt[money.Percent]
	PriceListIDs        *[]uuid.UUID
	PromotionIDs        *[]uuid.UUID
}

var hundred = money.MustPercent("100")

func validate(c Campaign) map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(c.Name) == "" {
		fields["name"] = "required"
	} else if len([]rune(c.Name)) > 200 {
		fields["name"] = "too_long"
	}
	if !validKind(c.Kind) {
		fields["kind"] = "invalid"
	}
	if c.StartsOn.IsZero() {
		fields["starts_on"] = "required"
	}
	if c.EndsOn.IsZero() {
		fields["ends_on"] = "required"
	} else if c.EndsOn.Before(c.StartsOn) {
		fields["ends_on"] = "before_starts_on"
	}
	if c.Budget != nil && c.Budget.IsNegative() {
		fields["budget_amount"] = "negative"
	}
	if c.TargetRevenue != nil && c.TargetRevenue.IsNegative() {
		fields["target_revenue"] = "negative"
	}
	if p := c.TargetMarginPercent; p != nil && (p.Decimal().IsNegative() || p.Cmp(hundred) > 0) {
		fields["target_margin_percent"] = "out_of_range"
	}
	return fields
}

// Create adds a campaign in planning with its seeded checklist and links its price lists and
// promotions.
func (s *Service) Create(ctx context.Context, scope Scope, actor audit.Actor, in Input) (Detail, error) {
	c := Campaign{
		ID: ids.New(), Name: strings.TrimSpace(in.Name), Kind: in.Kind, StartsOn: in.StartsOn, EndsOn: in.EndsOn,
		Budget: in.Budget, TargetRevenue: in.TargetRevenue, TargetMarginPercent: in.TargetMarginPercent,
		Status: StatusPlanning, Version: 1,
	}
	if c.Kind == "" {
		c.Kind = KindSeason
	}
	if fields := validate(c); len(fields) > 0 {
		return Detail{}, errInvalid(fields)
	}
	var out Detail
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		now := s.clock.Now()
		c.CreatedAt, c.UpdatedAt = now, now
		if err := insertCampaign(ctx, q, scope.TenantID, c); err != nil {
			if db.IsUniqueViolation(err, "ux_campaigns__name_key") {
				return errNameTaken()
			}
			return err
		}
		for i, it := range seedChecklist {
			if err := insertChecklistItem(ctx, q, scope.TenantID, c.ID, it.Phase, it.Kind, it.Title, (i+1)*10, now); err != nil {
				return err
			}
		}
		if err := linkPriceLists(ctx, q, c.ID, in.PriceListIDs, now); err != nil {
			return err
		}
		if err := linkPromotions(ctx, q, c.ID, in.PromotionIDs, now); err != nil {
			return err
		}
		d, err := loadDetail(ctx, q, c.ID)
		if err != nil {
			return err
		}
		out = d
		return s.record(ctx, q, actor, "campaign.created", "campaign", c.ID, nil, d)
	})
	return out, err
}

// Get returns a campaign with its links and checklist.
func (s *Service) Get(ctx context.Context, scope Scope, id uuid.UUID) (Detail, error) {
	var out Detail
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = loadDetail(ctx, q, id)
		return err
	})
	return out, err
}

// Filter narrows the campaign list.
type Filter struct {
	Status string
	Kind   string
	Search string
}

// List returns campaigns newest period first.
func (s *Service) List(ctx context.Context, scope Scope, f Filter, page httpx.Page) ([]Campaign, error) {
	fields := map[string]string{}
	if f.Status != "" && !validStatus(f.Status) {
		fields["status"] = "invalid"
	}
	if f.Kind != "" && !validKind(f.Kind) {
		fields["kind"] = "invalid"
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	var after *listCursor
	if page.After != nil {
		t, err := time.Parse(time.DateOnly, page.After.SortKey)
		if err != nil {
			return nil, errCursor()
		}
		after = &listCursor{StartsOn: t, ID: page.After.ID}
	}
	var out []Campaign
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = listCampaigns(ctx, q, f, after, page.FetchLimit())
		return err
	})
	return out, err
}

// Update applies a patch under the version the client saw. A closed campaign is read only.
func (s *Service) Update(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID, version int, p Patch) (Detail, error) {
	var out Detail
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		before, err := loadDetail(ctx, q, id)
		if err != nil {
			return err
		}
		if before.Status == StatusClosed {
			return errClosed()
		}
		c := before.Campaign
		if p.Name != nil {
			c.Name = strings.TrimSpace(*p.Name)
		}
		if p.Kind != nil {
			c.Kind = *p.Kind
		}
		if p.StartsOn != nil {
			c.StartsOn = *p.StartsOn
		}
		if p.EndsOn != nil {
			c.EndsOn = *p.EndsOn
		}
		if p.Budget.Set {
			c.Budget = p.Budget.Value
		}
		if p.TargetRevenue.Set {
			c.TargetRevenue = p.TargetRevenue.Value
		}
		if p.TargetMarginPercent.Set {
			c.TargetMarginPercent = p.TargetMarginPercent.Value
		}
		if fields := validate(c); len(fields) > 0 {
			return errInvalid(fields)
		}
		now := s.clock.Now()
		if _, err := db.UpdateWithVersion(ctx, q, "campaigns", id, version, now,
			db.Set{Column: "name", Value: c.Name}, db.Set{Column: "name_key", Value: textnorm.Key(c.Name)},
			db.Set{Column: "kind", Value: c.Kind}, db.Set{Column: "starts_on", Value: c.StartsOn},
			db.Set{Column: "ends_on", Value: c.EndsOn}, db.Set{Column: "budget_amount", Value: c.Budget},
			db.Set{Column: "target_revenue", Value: c.TargetRevenue},
			db.Set{Column: "target_margin_percent", Value: c.TargetMarginPercent},
		); err != nil {
			if db.IsUniqueViolation(err, "ux_campaigns__name_key") {
				return errNameTaken()
			}
			return err
		}
		if p.PriceListIDs != nil {
			if err := unlinkPriceLists(ctx, q, id, *p.PriceListIDs, now); err != nil {
				return err
			}
			if err := linkPriceLists(ctx, q, id, *p.PriceListIDs, now); err != nil {
				return err
			}
		}
		if p.PromotionIDs != nil {
			if err := unlinkPromotions(ctx, q, id, *p.PromotionIDs, now); err != nil {
				return err
			}
			if err := linkPromotions(ctx, q, id, *p.PromotionIDs, now); err != nil {
				return err
			}
		}
		if out, err = loadDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "campaign.updated", "campaign", id, before, out)
	})
	return out, err
}

// Activate moves a campaign in planning to active.
func (s *Service) Activate(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID, version int) (Detail, error) {
	var out Detail
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		before, err := loadDetail(ctx, q, id)
		if err != nil {
			return err
		}
		if before.Status != StatusPlanning {
			return errStatus(before.Status, StatusActive)
		}
		if _, err := db.UpdateWithVersion(ctx, q, "campaigns", id, version, s.clock.Now(),
			db.Set{Column: "status", Value: StatusActive}); err != nil {
			return err
		}
		if out, err = loadDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "campaign.activated", "campaign", id,
			map[string]string{"status": before.Status}, map[string]string{"status": out.Status})
	})
	return out, err
}

// CloseResult is a closed campaign and the state of its checklist at close.
type CloseResult struct {
	Campaign Detail
	Summary  ChecklistSummary
}

// Close marks an active campaign closed. Stock, cash and debt are left as they are: the
// checklist summary reminds what is still open, nothing is reset or written off.
func (s *Service) Close(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID, version int) (CloseResult, error) {
	var out CloseResult
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		before, err := loadDetail(ctx, q, id)
		if err != nil {
			return err
		}
		if before.Status != StatusActive {
			return errStatus(before.Status, StatusClosed)
		}
		now := s.clock.Now()
		if _, err := db.UpdateWithVersion(ctx, q, "campaigns", id, version, now,
			db.Set{Column: "status", Value: StatusClosed}, db.Set{Column: "closed_at", Value: now}); err != nil {
			return err
		}
		d, err := loadDetail(ctx, q, id)
		if err != nil {
			return err
		}
		out = CloseResult{Campaign: d, Summary: summarize(d.Checklist)}
		return s.record(ctx, q, actor, "campaign.closed", "campaign", id,
			map[string]string{"status": before.Status},
			map[string]any{"status": d.Status, "checklist_open": len(out.Summary.Open)})
	})
	return out, err
}

// Checklist returns the checklist of a campaign.
func (s *Service) Checklist(ctx context.Context, scope Scope, id uuid.UUID) ([]ChecklistItem, error) {
	var out []ChecklistItem
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := loadCampaign(ctx, q, id, false); err != nil {
			return err
		}
		var err error
		out, err = listChecklist(ctx, q, id)
		return err
	})
	return out, err
}

// ItemChange ticks or unticks a checklist item.
type ItemChange struct {
	ID   uuid.UUID
	Done bool
}

// NewItem is a custom checklist item.
type NewItem struct {
	Phase string
	Title string
}

// ChecklistPatch ticks items and adds custom ones.
type ChecklistPatch struct {
	Items []ItemChange
	Add   []NewItem
}

// UpdateChecklist applies a checklist patch. Items stay editable after close: the owner may
// finish the closing reminders later.
func (s *Service) UpdateChecklist(ctx context.Context, scope Scope, actor audit.Actor, id uuid.UUID, p ChecklistPatch) ([]ChecklistItem, error) {
	fields := map[string]string{}
	for i, a := range p.Add {
		if !validPhase(a.Phase) {
			fields[fmt.Sprintf("add.%d.phase", i)] = "invalid"
		}
		if t := strings.TrimSpace(a.Title); t == "" {
			fields[fmt.Sprintf("add.%d.title", i)] = "required"
		} else if len([]rune(t)) > 200 {
			fields[fmt.Sprintf("add.%d.title", i)] = "too_long"
		}
	}
	if len(p.Items) == 0 && len(p.Add) == 0 {
		fields["items"] = "required"
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	var out []ChecklistItem
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		if _, err := loadCampaign(ctx, q, id, true); err != nil {
			return err
		}
		before, err := listChecklist(ctx, q, id)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		for i, it := range p.Items {
			ok, err := setItemDone(ctx, q, id, it.ID, it.Done, now, actor.ID)
			if err != nil {
				return err
			}
			if !ok {
				return errInvalid(map[string]string{fmt.Sprintf("items.%d.id", i): "not_found"})
			}
		}
		next := 0
		for _, it := range before {
			next = max(next, it.SortOrder)
		}
		for _, a := range p.Add {
			next += 10
			if err := insertChecklistItem(ctx, q, scope.TenantID, id, a.Phase, ItemCustom, strings.TrimSpace(a.Title), next, now); err != nil {
				return err
			}
		}
		if out, err = listChecklist(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "campaign.checklist_updated", "campaign", id, before, out)
	})
	return out, err
}
