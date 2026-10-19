package campaign

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the campaign endpoints. Money, quantities and percents travel as decimal
// strings; business dates as "YYYY-MM-DD".

func mapSlice[T, D any](in []T, fn func(T) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

// date is a civil date on the wire ("YYYY-MM-DD"), held as midnight UTC.
type date time.Time

// UnmarshalJSON reads a "YYYY-MM-DD" string.
func (d *date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: reflect.TypeFor[date]()}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string " + s, Type: reflect.TypeFor[date]()}
	}
	*d = date(clock.Date(t.Year(), t.Month(), t.Day()))
	return nil
}

func (d *date) time() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

func dateOf(t time.Time) string { return t.Format(time.DateOnly) }

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateOf(*t)
	return &s
}

// opt is a patch field that may also be cleared: set when given, value nil when given as null.
type opt[T any] struct {
	set   bool
	value *T
}

// UnmarshalJSON marks the field as given; JSON null clears it.
func (o *opt[T]) UnmarshalJSON(data []byte) error {
	o.set = true
	if string(bytes.TrimSpace(data)) == "null" {
		o.value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.value = &v
	return nil
}

func (o opt[T]) patch() Opt[T] { return Opt[T]{Set: o.set, Value: o.value} }

// Requests.

type campaignRequest struct {
	Name                string         `json:"name" validate:"required,max=200"`
	Kind                string         `json:"kind" validate:"omitempty,oneof=tet season promotion other"`
	StartsOn            date           `json:"starts_on"`
	EndsOn              date           `json:"ends_on"`
	BudgetAmount        *money.Money   `json:"budget_amount"`
	TargetRevenue       *money.Money   `json:"target_revenue"`
	TargetMarginPercent *money.Percent `json:"target_margin_percent"`
	PriceListIDs        []uuid.UUID    `json:"price_list_ids" validate:"max=100"`
	PromotionIDs        []uuid.UUID    `json:"promotion_ids" validate:"max=100"`
}

func (r campaignRequest) input() Input {
	return Input{
		Name: r.Name, Kind: r.Kind, StartsOn: time.Time(r.StartsOn), EndsOn: time.Time(r.EndsOn),
		Budget: r.BudgetAmount, TargetRevenue: r.TargetRevenue, TargetMarginPercent: r.TargetMarginPercent,
		PriceListIDs: r.PriceListIDs, PromotionIDs: r.PromotionIDs,
	}
}

type campaignPatchRequest struct {
	Name                *string            `json:"name" validate:"omitempty,max=200"`
	Kind                *string            `json:"kind" validate:"omitempty,oneof=tet season promotion other"`
	StartsOn            *date              `json:"starts_on"`
	EndsOn              *date              `json:"ends_on"`
	BudgetAmount        opt[money.Money]   `json:"budget_amount"`
	TargetRevenue       opt[money.Money]   `json:"target_revenue"`
	TargetMarginPercent opt[money.Percent] `json:"target_margin_percent"`
	PriceListIDs        *[]uuid.UUID       `json:"price_list_ids" validate:"omitempty,max=100"`
	PromotionIDs        *[]uuid.UUID       `json:"promotion_ids" validate:"omitempty,max=100"`
}

func (r campaignPatchRequest) input() Patch {
	return Patch{
		Name: r.Name, Kind: r.Kind, StartsOn: r.StartsOn.time(), EndsOn: r.EndsOn.time(),
		Budget: r.BudgetAmount.patch(), TargetRevenue: r.TargetRevenue.patch(),
		TargetMarginPercent: r.TargetMarginPercent.patch(), PriceListIDs: r.PriceListIDs, PromotionIDs: r.PromotionIDs,
	}
}

type checklistItemRequest struct {
	ID   uuid.UUID `json:"id" validate:"required"`
	Done bool      `json:"done"`
}

type checklistAddRequest struct {
	Phase string `json:"phase" validate:"required,oneof=before_peak closing"`
	Title string `json:"title" validate:"required,max=200"`
}

type checklistPatchRequest struct {
	Items []checklistItemRequest `json:"items" validate:"max=100,dive"`
	Add   []checklistAddRequest  `json:"add" validate:"max=50,dive"`
}

func (r checklistPatchRequest) input() ChecklistPatch {
	return ChecklistPatch{
		Items: mapSlice(r.Items, func(i checklistItemRequest) ItemChange { return ItemChange(i) }),
		Add:   mapSlice(r.Add, func(a checklistAddRequest) NewItem { return NewItem(a) }),
	}
}

type attributionRequest struct {
	CampaignID uuid.UUID  `json:"campaign_id" validate:"required"`
	DocType    string     `json:"doc_type" validate:"required"`
	DocID      uuid.UUID  `json:"doc_id" validate:"required"`
	DocLineID  *uuid.UUID `json:"doc_line_id"`
	Role       string     `json:"role" validate:"required"`
}

// Responses.

type checklistItemDTO struct {
	ID        uuid.UUID  `json:"id"`
	Phase     string     `json:"phase"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Done      bool       `json:"done"`
	DoneAt    *time.Time `json:"done_at"`
	DoneBy    *uuid.UUID `json:"done_by"`
	SortOrder int        `json:"sort_order"`
}

func toChecklistItemDTO(it ChecklistItem) checklistItemDTO {
	return checklistItemDTO{
		ID: it.ID, Phase: it.Phase, Kind: it.Kind, Title: it.Title, Done: it.DoneAt != nil, DoneAt: it.DoneAt,
		DoneBy: it.DoneBy, SortOrder: it.SortOrder,
	}
}

type checklistDTO struct {
	Data []checklistItemDTO `json:"data"`
}

func toChecklistDTO(items []ChecklistItem) checklistDTO {
	return checklistDTO{Data: mapSlice(items, toChecklistItemDTO)}
}

type campaignDTO struct {
	ID                  uuid.UUID      `json:"id"`
	Name                string         `json:"name"`
	Kind                string         `json:"kind"`
	StartsOn            string         `json:"starts_on"`
	EndsOn              string         `json:"ends_on"`
	BudgetAmount        *money.Money   `json:"budget_amount"`
	TargetRevenue       *money.Money   `json:"target_revenue"`
	TargetMarginPercent *money.Percent `json:"target_margin_percent"`
	Status              string         `json:"status"`
	ClosedAt            *time.Time     `json:"closed_at"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	Version             int            `json:"version"`
}

func toCampaignDTO(c Campaign) campaignDTO {
	return campaignDTO{
		ID: c.ID, Name: c.Name, Kind: c.Kind, StartsOn: dateOf(c.StartsOn), EndsOn: dateOf(c.EndsOn),
		BudgetAmount: c.Budget, TargetRevenue: c.TargetRevenue, TargetMarginPercent: c.TargetMarginPercent,
		Status: c.Status, ClosedAt: c.ClosedAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version,
	}
}

type campaignDetailDTO struct {
	campaignDTO
	PriceListIDs []uuid.UUID        `json:"price_list_ids"`
	PromotionIDs []uuid.UUID        `json:"promotion_ids"`
	Checklist    []checklistItemDTO `json:"checklist"`
}

func toDetailDTO(d Detail) campaignDetailDTO {
	return campaignDetailDTO{
		campaignDTO: toCampaignDTO(d.Campaign), PriceListIDs: d.PriceListIDs, PromotionIDs: d.PromotionIDs,
		Checklist: mapSlice(d.Checklist, toChecklistItemDTO),
	}
}

type checklistSummaryDTO struct {
	Total int                `json:"total"`
	Done  int                `json:"done"`
	Open  []checklistItemDTO `json:"open"`
}

type closeDTO struct {
	Campaign  campaignDetailDTO   `json:"campaign"`
	Checklist checklistSummaryDTO `json:"checklist"`
}

func toCloseDTO(r CloseResult) closeDTO {
	return closeDTO{
		Campaign: toDetailDTO(r.Campaign),
		Checklist: checklistSummaryDTO{
			Total: r.Summary.Total, Done: r.Summary.Done, Open: mapSlice(r.Summary.Open, toChecklistItemDTO),
		},
	}
}

type attributionDTO struct {
	ID         uuid.UUID  `json:"id"`
	CampaignID uuid.UUID  `json:"campaign_id"`
	DocType    string     `json:"doc_type"`
	DocID      uuid.UUID  `json:"doc_id"`
	DocLineID  *uuid.UUID `json:"doc_line_id"`
	Role       string     `json:"role"`
	CreatedBy  uuid.UUID  `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toAttributionDTO(a Attribution) attributionDTO {
	return attributionDTO(a)
}

type reattributeDTO struct {
	Attributions []attributionDTO `json:"attributions"`
	Warnings     []string         `json:"warnings"`
}

func toReattributeDTO(r ReattributeResult) reattributeDTO {
	return reattributeDTO{Attributions: mapSlice(r.Attributions, toAttributionDTO), Warnings: r.Warnings}
}

type demandDayDTO struct {
	Date      string    `json:"date"`
	Demand    money.Qty `json:"demand"`
	Incoming  money.Qty `json:"incoming"`
	Projected money.Qty `json:"projected"`
	Shortage  money.Qty `json:"shortage"`
}

type demandRowDTO struct {
	SkuID           uuid.UUID      `json:"sku_id"`
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	Available       money.Qty      `json:"available"`
	Backlog         money.Qty      `json:"backlog"`
	UndatedIncoming money.Qty      `json:"undated_incoming"`
	FirstShortageOn *string        `json:"first_shortage_on"`
	MaxShortage     money.Qty      `json:"max_shortage"`
	Days            []demandDayDTO `json:"days"`
}

type demandDTO struct {
	CampaignID uuid.UUID      `json:"campaign_id"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	Data       []demandRowDTO `json:"data"`
	NextCursor *string        `json:"next_cursor"`
}

func toDemandRowDTO(r DemandRow) demandRowDTO {
	return demandRowDTO{
		SkuID: r.SkuID, Code: r.Code, Name: r.Name, Available: r.Available, Backlog: r.Backlog,
		UndatedIncoming: r.UndatedIncoming, FirstShortageOn: dateString(r.FirstShortageOn), MaxShortage: r.MaxShortage,
		Days: mapSlice(r.Days, func(d DemandDay) demandDayDTO {
			return demandDayDTO{Date: dateOf(d.Date), Demand: d.Demand, Incoming: d.Incoming, Projected: d.Projected, Shortage: d.Shortage}
		}),
	}
}
