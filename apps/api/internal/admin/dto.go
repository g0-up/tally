package admin

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/saas"
)

func optDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

// tenantDTO is a row of the admin tenant directory: store metadata, subscription and usage
// counters. It never carries business records or their values.
type tenantDTO struct {
	ID                 uuid.UUID  `json:"id"`
	Slug               string     `json:"slug"`
	DisplayName        string     `json:"display_name"`
	Status             string     `json:"status"`
	IsSandbox          bool       `json:"is_sandbox"`
	SandboxOf          *uuid.UUID `json:"sandbox_of"`
	LegalHold          bool       `json:"legal_hold"`
	Maintenance        bool       `json:"maintenance"`
	CreatedAt          time.Time  `json:"created_at"`
	SubscriptionStatus *string    `json:"subscription_status"`
	PlanCode           *string    `json:"plan_code"`
	PlanName           *string    `json:"plan_name"`
	PeriodEnd          *string    `json:"current_period_end"`
	UsageDate          *string    `json:"usage_date"`
	ActiveSKUs         *int       `json:"active_skus"`
	StorageBytes       *int64     `json:"storage_bytes"`
}

func toTenantDTO(r TenantRow) tenantDTO {
	return tenantDTO{
		ID: r.ID, Slug: r.Slug, DisplayName: r.DisplayName, Status: r.Status, IsSandbox: r.IsSandbox,
		SandboxOf: r.SandboxOf, LegalHold: r.LegalHold, Maintenance: r.Maintenance, CreatedAt: r.CreatedAt.UTC(),
		SubscriptionStatus: r.SubscriptionStatus, PlanCode: r.PlanCode, PlanName: r.PlanName,
		PeriodEnd: optDate(r.PeriodEnd), UsageDate: optDate(r.UsageDate), ActiveSKUs: r.ActiveSKUs, StorageBytes: r.StorageBytes,
	}
}

type usageDTO struct {
	TenantID        uuid.UUID `json:"tenant_id"`
	UsageDate       string    `json:"usage_date"`
	ActiveSKUs      int       `json:"active_skus"`
	DocumentsPosted int       `json:"documents_posted"`
	StorageBytes    int64     `json:"storage_bytes"`
}

func toUsageDTO(u saas.Usage) usageDTO {
	return usageDTO{
		TenantID: u.TenantID, UsageDate: u.UsageDate.Format(time.DateOnly), ActiveSKUs: u.ActiveSKUs,
		DocumentsPosted: u.DocumentsPosted, StorageBytes: u.StorageBytes,
	}
}

func toUsageDTOs(in []saas.Usage) []usageDTO {
	out := make([]usageDTO, len(in))
	for i, u := range in {
		out[i] = toUsageDTO(u)
	}
	return out
}

// usageRowDTO is a row of the usage list: a store's day with the store's name and slug.
type usageRowDTO struct {
	usageDTO
	TenantName string `json:"tenant_name"`
	Slug       string `json:"slug"`
}

type tenantDetailDTO struct {
	tenantDTO
	Subscription saas.SubscriptionViewDTO `json:"subscription"`
	Usage        []usageDTO               `json:"usage"`
	Closure      *saas.ClosureViewDTO     `json:"closure"`
}

type adminInvoiceDTO struct {
	saas.InvoiceDTO
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantSlug string    `json:"tenant_slug"`
	TenantName string    `json:"tenant_name"`
}

type incidentDTO struct {
	ID         uuid.UUID        `json:"id"`
	Title      string           `json:"title"`
	Status     string           `json:"status"`
	Impact     string           `json:"impact"`
	StartedAt  time.Time        `json:"started_at"`
	ResolvedAt *time.Time       `json:"resolved_at"`
	Updates    []IncidentUpdate `json:"updates"`
}

func toIncidentDTO(i Incident) incidentDTO {
	updates := []IncidentUpdate(i.Updates)
	if updates == nil {
		updates = []IncidentUpdate{}
	}
	return incidentDTO{
		ID: i.ID, Title: i.Title, Status: i.Status, Impact: i.Impact, StartedAt: i.StartedAt.UTC(),
		ResolvedAt: i.ResolvedAt, Updates: updates,
	}
}

func toIncidentDTOs(in []Incident) []incidentDTO {
	out := make([]incidentDTO, len(in))
	for i, v := range in {
		out[i] = toIncidentDTO(v)
	}
	return out
}

type statusDTO struct {
	Indicator string        `json:"indicator"`
	Incidents []incidentDTO `json:"incidents"`
}

type partnerDTO struct {
	ID                uuid.UUID     `json:"id"`
	Name              string        `json:"name"`
	ReferralCode      string        `json:"referral_code"`
	CommissionPercent money.Percent `json:"commission_percent"`
	Active            bool          `json:"active"`
	CreatedAt         time.Time     `json:"created_at"`
	Referrals         int           `json:"referrals"`
}

func toPartnerDTO(p Partner) partnerDTO {
	return partnerDTO{
		ID: p.ID, Name: p.Name, ReferralCode: p.ReferralCode, CommissionPercent: p.CommissionPercent,
		Active: p.Active, CreatedAt: p.CreatedAt.UTC(), Referrals: p.Referrals,
	}
}

type referralDTO struct {
	TenantID           uuid.UUID   `json:"tenant_id"`
	Slug               string      `json:"slug"`
	DisplayName        string      `json:"display_name"`
	ReferredAt         time.Time   `json:"referred_at"`
	SubscriptionStatus *string     `json:"subscription_status"`
	PaidInvoices       money.Money `json:"paid_invoices"`
	Commission         money.Money `json:"commission"`
}

type feedbackDTO struct {
	ID         uuid.UUID `json:"id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantSlug string    `json:"tenant_slug"`
	Kind       string    `json:"kind"`
	Score      *int      `json:"score"`
	Body       *string   `json:"body"`
	PagePath   *string   `json:"page_path"`
	CreatedAt  time.Time `json:"created_at"`
}

type loginRequest struct {
	Email    string `json:"email" validate:"required,max=254"`
	Password string `json:"password" validate:"required,max=1024"`
}

type totpRequest struct {
	Challenge string `json:"challenge" validate:"required,max=512"`
	Code      string `json:"code" validate:"required,max=12"`
}

type authResponse struct {
	Status    string     `json:"status"`
	Challenge *string    `json:"challenge,omitempty"`
	CSRFToken *string    `json:"csrf_token,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// adminRefDTO names a platform admin.
type adminRefDTO struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
	Email    string    `json:"email"`
}

// adminEntryDTO is a row of the admin directory.
type adminEntryDTO struct {
	adminRefDTO
	Active bool `json:"active"`
}

type flagListResponse struct {
	Data []featureflag.Flag `json:"data"`
	// KnownKeys are the flag keys the server reads.
	KnownKeys []string `json:"known_keys"`
}

type meResponse struct {
	Admin     adminRefDTO `json:"admin"`
	CSRFToken string      `json:"csrf_token"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type changePlanRequest struct {
	PlanID uuid.UUID `json:"plan_id" validate:"required"`
}

type extendRequest struct {
	Days int `json:"days" validate:"required"`
}

type setStatusRequest struct {
	Status    string `json:"status"`
	LegalHold *bool  `json:"legal_hold"`
}

type planRequest struct {
	Code          string           `json:"code"`
	Name          string           `json:"name"`
	Kind          string           `json:"kind"`
	BillingPeriod string           `json:"billing_period"`
	Price         money.Money      `json:"price"`
	Modules       []string         `json:"modules"`
	Limits        map[string]int64 `json:"limits"`
	Active        *bool            `json:"active"`
}

func (r planRequest) input() saas.PlanInput {
	active := r.Active == nil || *r.Active
	return saas.PlanInput{
		Code: r.Code, Name: r.Name, Kind: r.Kind, BillingPeriod: r.BillingPeriod, Price: r.Price,
		Modules: r.Modules, Limits: r.Limits, Active: active,
	}
}

type issueInvoiceRequest struct {
	TenantID uuid.UUID    `json:"tenant_id" validate:"required"`
	PlanID   uuid.UUID    `json:"plan_id" validate:"required"`
	Amount   *money.Money `json:"amount"`
}

type markPaidRequest struct {
	Reference string `json:"reference" validate:"max=120"`
}

type setFlagRequest struct {
	Key      string     `json:"key" validate:"required"`
	TenantID *uuid.UUID `json:"tenant_id"`
	Enabled  *bool      `json:"enabled" validate:"required"`
}

type incidentRequest struct {
	Title   string `json:"title"`
	Impact  string `json:"impact"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type incidentPatchRequest struct {
	Title   *string `json:"title"`
	Status  *string `json:"status"`
	Impact  *string `json:"impact"`
	Message string  `json:"message"`
}

type partnerRequest struct {
	Name              string        `json:"name"`
	ReferralCode      string        `json:"referral_code"`
	CommissionPercent money.Percent `json:"commission_percent"`
}

type feedbackRequest struct {
	Kind     string `json:"kind"`
	Score    *int   `json:"score"`
	Body     string `json:"body"`
	PagePath string `json:"page_path"`
}
