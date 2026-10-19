package crm

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the CRM endpoints. Money and points travel as decimal strings; business dates
// as "YYYY-MM-DD".

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
	t, err := parseDate(s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string " + s, Type: reflect.TypeFor[date]()}
	}
	*d = date(t)
	return nil
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, err
	}
	return clock.Date(t.Year(), t.Month(), t.Day()), nil
}

func (d *date) time() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

func dateOf(t time.Time) string { return t.Format(time.DateOnly) }

type activityDTO struct {
	ID        uuid.UUID  `json:"id"`
	PartyID   uuid.UUID  `json:"party_id"`
	Kind      string     `json:"kind"`
	RefType   *string    `json:"ref_type"`
	RefID     *uuid.UUID `json:"ref_id"`
	Summary   string     `json:"summary"`
	CreatedBy *uuid.UUID `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
}

func toActivityDTO(a Activity) activityDTO {
	return activityDTO(a)
}

type activityRequest struct {
	Kind    string `json:"kind" validate:"required"`
	Summary string `json:"summary" validate:"required"`
}

type consentDTO struct {
	ID         uuid.UUID `json:"id"`
	PartyID    uuid.UUID `json:"party_id"`
	Channel    string    `json:"channel"`
	Purpose    string    `json:"purpose"`
	Granted    bool      `json:"granted"`
	Source     string    `json:"source"`
	Evidence   *string   `json:"evidence"`
	RecordedBy uuid.UUID `json:"recorded_by"`
	RecordedAt time.Time `json:"recorded_at"`
}

func toConsentDTO(c Consent) consentDTO {
	return consentDTO(c)
}

type consentsDTO struct {
	Current []consentDTO `json:"current"`
	History []consentDTO `json:"history"`
}

type consentRequest struct {
	Channel  string  `json:"channel" validate:"required"`
	Purpose  string  `json:"purpose" validate:"required"`
	Granted  *bool   `json:"granted" validate:"required"`
	Source   string  `json:"source" validate:"required"`
	Evidence *string `json:"evidence"`
}

type careTaskDTO struct {
	ID               uuid.UUID  `json:"id"`
	PartyID          uuid.UUID  `json:"party_id"`
	PartyName        string     `json:"party_name"`
	DueOn            string     `json:"due_on"`
	Title            string     `json:"title"`
	SourceCampaignID *uuid.UUID `json:"source_campaign_id"`
	Status           string     `json:"status"`
	DoneAt           *time.Time `json:"done_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func toCareTaskDTO(t CareTask) careTaskDTO {
	return careTaskDTO{
		ID: t.ID, PartyID: t.PartyID, PartyName: t.PartyName, DueOn: dateOf(t.DueOn), Title: t.Title,
		SourceCampaignID: t.SourceCampaignID, Status: t.Status, DoneAt: t.DoneAt, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

type careTaskRequest struct {
	PartyID          uuid.UUID  `json:"party_id"`
	DueOn            *date      `json:"due_on" validate:"required"`
	Title            string     `json:"title" validate:"required"`
	SourceCampaignID *uuid.UUID `json:"source_campaign_id"`
}

func (r careTaskRequest) input() CareTaskInput {
	in := CareTaskInput{PartyID: r.PartyID, Title: r.Title, SourceCampaignID: r.SourceCampaignID}
	if t := r.DueOn.time(); t != nil {
		in.DueOn = *t
	}
	return in
}

type careTaskPatchRequest struct {
	DueOn  *date   `json:"due_on"`
	Title  *string `json:"title"`
	Status *string `json:"status"`
}

func (r careTaskPatchRequest) input() CareTaskPatch {
	return CareTaskPatch{DueOn: r.DueOn.time(), Title: r.Title, Status: r.Status}
}

type lastSeasonCustomerDTO struct {
	PartyID        uuid.UUID   `json:"party_id"`
	Name           string      `json:"name"`
	Phone          *string     `json:"phone"`
	Documents      int         `json:"documents"`
	NetRevenue     money.Money `json:"net_revenue"`
	LastPurchaseOn string      `json:"last_purchase_on"`
	OpenTasks      int         `json:"open_tasks"`
}

func toLastSeasonDTO(c LastSeasonCustomer) lastSeasonCustomerDTO {
	return lastSeasonCustomerDTO{
		PartyID: c.PartyID, Name: c.Name, Phone: c.Phone, Documents: c.Documents, NetRevenue: c.NetRevenue,
		LastPurchaseOn: dateOf(c.LastPurchaseOn), OpenTasks: c.OpenTasks,
	}
}

type lastSeasonDTO struct {
	CampaignID uuid.UUID               `json:"campaign_id"`
	Data       []lastSeasonCustomerDTO `json:"data"`
	NextCursor *string                 `json:"next_cursor"`
}

type reorderRequest struct {
	SalesDocumentID *uuid.UUID `json:"sales_document_id"`
}

type reorderLineDTO struct {
	SkuID   *uuid.UUID `json:"sku_id"`
	Code    *string    `json:"sku_code"`
	Name    string     `json:"name"`
	Kind    string     `json:"line_kind"`
	ColorID *uuid.UUID `json:"color_id"`
	UnitID  uuid.UUID  `json:"unit_id"`
	Qty     money.Qty  `json:"qty"`
	Copied  bool       `json:"copied"`
	Flag    *string    `json:"flag"`
}

func toReorderLineDTO(l ReorderLine) reorderLineDTO {
	d := reorderLineDTO{
		SkuID: l.SkuID, Code: l.Code, Name: l.Name, Kind: l.Kind, ColorID: l.ColorID, UnitID: l.UnitID, Qty: l.Qty, Copied: l.Copied,
	}
	if l.Flag != "" {
		flag := l.Flag
		d.Flag = &flag
	}
	return d
}

type reorderDTO struct {
	InvoiceID        uuid.UUID        `json:"invoice_id"`
	InvoiceDocNo     string           `json:"invoice_doc_no"`
	InvoiceVersion   int              `json:"invoice_version"`
	TotalAmount      money.Money      `json:"total_amount"`
	SourceDocumentID uuid.UUID        `json:"source_document_id"`
	SourceDocNo      string           `json:"source_doc_no"`
	Lines            []reorderLineDTO `json:"lines"`
}

func toReorderDTO(r Reorder) reorderDTO {
	return reorderDTO{
		InvoiceID: r.Draft.InvoiceID, InvoiceDocNo: r.Draft.DocNo, InvoiceVersion: r.Draft.Version, TotalAmount: r.Draft.TotalAmount,
		SourceDocumentID: r.SourceDocumentID, SourceDocNo: r.SourceDocNo, Lines: mapSlice(r.Lines, toReorderLineDTO),
	}
}

type programDTO struct {
	Enabled         bool        `json:"enabled"`
	PointsPerAmount money.Money `json:"points_per_amount"`
	PointValue      money.Money `json:"point_value"`
	UpdatedAt       *time.Time  `json:"updated_at"`
	Version         int         `json:"version"`
}

func toProgramDTO(p Program) programDTO {
	return programDTO(p)
}

type programRequest struct {
	Enabled         *bool        `json:"enabled" validate:"required"`
	PointsPerAmount *money.Money `json:"points_per_amount" validate:"required"`
	PointValue      *money.Money `json:"point_value" validate:"required"`
}

type loyaltyEntryDTO struct {
	ID           uuid.UUID  `json:"id"`
	PartyID      uuid.UUID  `json:"party_id"`
	Points       money.Qty  `json:"points"`
	Kind         string     `json:"kind"`
	SourceType   string     `json:"source_type"`
	SourceID     uuid.UUID  `json:"source_id"`
	ReversalOf   *uuid.UUID `json:"reversal_of"`
	BusinessDate string     `json:"business_date"`
	CreatedBy    *uuid.UUID `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toLoyaltyEntryDTO(e LoyaltyEntry) loyaltyEntryDTO {
	return loyaltyEntryDTO{
		ID: e.ID, PartyID: e.PartyID, Points: e.Points, Kind: e.Kind, SourceType: e.SourceType, SourceID: e.SourceID,
		ReversalOf: e.ReversalOf, BusinessDate: dateOf(e.BusinessDate), CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt,
	}
}

type loyaltyDTO struct {
	PartyID uuid.UUID         `json:"party_id"`
	Balance money.Qty         `json:"balance"`
	Entries []loyaltyEntryDTO `json:"entries"`
}

type adjustmentRequest struct {
	PartyID uuid.UUID  `json:"party_id"`
	Points  *money.Qty `json:"points" validate:"required"`
	Reason  string     `json:"reason" validate:"required"`
}
