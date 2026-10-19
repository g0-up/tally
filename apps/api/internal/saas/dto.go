package saas

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func dateText(t time.Time) string { return t.Format(time.DateOnly) }

func optDateText(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateText(*t)
	return &s
}

// PlanDTO is a plan as the tenant and admin APIs show it: limits and published terms apart.
type PlanDTO struct {
	ID            uuid.UUID        `json:"id"`
	Code          string           `json:"code"`
	Name          string           `json:"name"`
	Kind          string           `json:"kind"`
	BillingPeriod string           `json:"billing_period"`
	Price         money.Money      `json:"price"`
	Modules       []string         `json:"modules"`
	Limits        map[string]int64 `json:"limits"`
	Terms         map[string]int   `json:"terms"`
	Active        bool             `json:"active"`
}

// ToPlanDTO renders p.
func ToPlanDTO(p Plan) PlanDTO {
	out := PlanDTO{
		ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, BillingPeriod: p.BillingPeriod, Price: p.Price,
		Modules: append([]string{}, p.Modules...), Limits: map[string]int64{}, Terms: map[string]int{}, Active: p.Active,
	}
	for _, k := range entitlementLimits {
		if v, ok := p.Limits[k]; ok {
			out.Limits[k] = v
		}
	}
	for k := range termDefaults {
		out.Terms[k] = p.Term(k)
	}
	return out
}

// SubscriptionDTO is a subscription with its lifecycle dates.
type SubscriptionDTO struct {
	ID                 uuid.UUID `json:"id"`
	PlanID             uuid.UUID `json:"plan_id"`
	Status             string    `json:"status"`
	CurrentPeriodStart string    `json:"current_period_start"`
	CurrentPeriodEnd   string    `json:"current_period_end"`
	TrialEndsOn        *string   `json:"trial_ends_on"`
	GraceEndsOn        *string   `json:"grace_ends_on"`
	ReadOnlyEndsOn     *string   `json:"read_only_ends_on"`
	CancelAtPeriodEnd  bool      `json:"cancel_at_period_end"`
	Version            int       `json:"version"`
}

// ToSubscriptionDTO renders s.
func ToSubscriptionDTO(s Subscription) SubscriptionDTO {
	return SubscriptionDTO{
		ID: s.ID, PlanID: s.PlanID, Status: s.Status,
		CurrentPeriodStart: dateText(s.CurrentPeriodStart), CurrentPeriodEnd: dateText(s.CurrentPeriodEnd),
		TrialEndsOn: optDateText(s.TrialEndsOn), GraceEndsOn: optDateText(s.GraceEndsOn),
		ReadOnlyEndsOn: optDateText(s.ReadOnlyEndsOn), CancelAtPeriodEnd: s.CancelAtPeriodEnd, Version: s.Version,
	}
}

// SubscriptionViewDTO is GET /subscription: nil subscription and plan, and no add-ons, for a
// store without one.
type SubscriptionViewDTO struct {
	Subscription *SubscriptionDTO `json:"subscription"`
	Plan         *PlanDTO         `json:"plan"`
	Entitlements Entitlements     `json:"entitlements"`
	Addons       []ActiveAddonDTO `json:"addons"`
}

// ActiveAddonDTO is an add-on in force, its period as calendar days.
type ActiveAddonDTO struct {
	PlanID      uuid.UUID `json:"plan_id"`
	PlanName    string    `json:"plan_name"`
	Modules     []string  `json:"modules"`
	PeriodStart string    `json:"period_start"`
	PeriodEnd   string    `json:"period_end"`
}

// ToSubscriptionViewDTO renders v.
func ToSubscriptionViewDTO(v SubscriptionView) SubscriptionViewDTO {
	out := SubscriptionViewDTO{Entitlements: v.Entitlements, Addons: make([]ActiveAddonDTO, 0, len(v.Addons))}
	for _, a := range v.Addons {
		out.Addons = append(out.Addons, ActiveAddonDTO{
			PlanID: a.PlanID, PlanName: a.PlanName, Modules: a.Modules,
			PeriodStart: dateText(a.PeriodStart), PeriodEnd: dateText(a.PeriodEnd),
		})
	}
	if v.Subscription != nil {
		s := ToSubscriptionDTO(*v.Subscription)
		out.Subscription = &s
	}
	if v.Plan != nil {
		p := ToPlanDTO(*v.Plan)
		out.Plan = &p
	}
	return out
}

// InvoiceDTO is a service invoice.
type InvoiceDTO struct {
	ID             uuid.UUID   `json:"id"`
	Number         string      `json:"number"`
	SubscriptionID *uuid.UUID  `json:"subscription_id"`
	PlanID         *uuid.UUID  `json:"plan_id"`
	Purpose        string      `json:"purpose"`
	PeriodStart    string      `json:"period_start"`
	PeriodEnd      string      `json:"period_end"`
	Amount         money.Money `json:"amount"`
	ProrationNote  *string     `json:"proration_note"`
	Status         string      `json:"status"`
	Provider       *string     `json:"provider"`
	ProviderRef    *string     `json:"provider_ref"`
	IssuedAt       time.Time   `json:"issued_at"`
	PaidAt         *time.Time  `json:"paid_at"`
}

// ToInvoiceDTO renders inv.
func ToInvoiceDTO(inv Invoice) InvoiceDTO {
	return InvoiceDTO{
		ID: inv.ID, Number: inv.Number, SubscriptionID: inv.SubscriptionID, PlanID: inv.PlanID, Purpose: inv.Purpose,
		PeriodStart: dateText(inv.PeriodStart), PeriodEnd: dateText(inv.PeriodEnd), Amount: inv.Amount,
		ProrationNote: inv.ProrationNote, Status: inv.Status, Provider: inv.Provider, ProviderRef: inv.ProviderRef,
		IssuedAt: inv.IssuedAt.UTC(), PaidAt: inv.PaidAt,
	}
}

type quoteDTO struct {
	Plan          PlanDTO     `json:"plan"`
	Purpose       string      `json:"purpose"`
	PeriodStart   string      `json:"period_start"`
	PeriodEnd     string      `json:"period_end"`
	Amount        money.Money `json:"amount"`
	ProrationNote string      `json:"proration_note"`
	Terms         string      `json:"terms"`
}

func toQuoteDTO(q Quote) quoteDTO {
	return quoteDTO{
		Plan: ToPlanDTO(q.Plan), Purpose: q.Purpose, PeriodStart: dateText(q.PeriodStart), PeriodEnd: dateText(q.PeriodEnd),
		Amount: q.Amount, ProrationNote: q.ProrationNote, Terms: q.Terms,
	}
}

type payInstructionsDTO struct {
	Invoice      InvoiceDTO `json:"invoice"`
	Provider     string     `json:"provider"`
	Instructions string     `json:"instructions"`
}

type closureRequestDTO struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	VerifiedAt   *time.Time `json:"verified_at"`
	ExportJobID  *uuid.UUID `json:"export_job_id"`
	WaitingUntil string     `json:"waiting_until"`
	LegalHold    bool       `json:"legal_hold"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toClosureRequestDTO(r ClosureRequest) closureRequestDTO {
	return closureRequestDTO{
		ID: r.ID, Status: r.Status, VerifiedAt: r.VerifiedAt, ExportJobID: r.ExportJobID,
		WaitingUntil: dateText(r.WaitingUntil), LegalHold: r.LegalHold, CreatedAt: r.CreatedAt.UTC(),
	}
}

type closureExportDTO struct {
	ID        uuid.UUID  `json:"id"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
	Final     bool       `json:"final"`
	// FailedAttempts counts the failed final exports of the current round.
	FailedAttempts int `json:"failed_attempts"`
}

// ClosureViewDTO is the JSON of a store's latest closure request and its export.
type ClosureViewDTO struct {
	Request *closureRequestDTO `json:"request"`
	Export  *closureExportDTO  `json:"export"`
}

// ToClosureViewDTO renders a closure view.
func ToClosureViewDTO(v ClosureView) ClosureViewDTO {
	var out ClosureViewDTO
	if v.Request != nil {
		r := toClosureRequestDTO(*v.Request)
		out.Request = &r
	}
	if e := v.Export; e != nil {
		out.Export = &closureExportDTO{ID: e.ID, Status: e.Status, ExpiresAt: e.ExpiresAt, Final: e.Final(),
			FailedAttempts: e.FailedAttempts}
	}
	return out
}

type changePlanRequest struct {
	PlanID      uuid.UUID `json:"plan_id" validate:"required"`
	AcceptTerms bool      `json:"accept_terms"`
}

type payRequest struct {
	Provider string `json:"provider" validate:"omitempty,max=40"`
}

type verifyClosureRequest struct {
	Token string `json:"token" validate:"required,max=200"`
}
