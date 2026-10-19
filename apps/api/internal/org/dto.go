package org

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the organisation endpoints. Business dates travel as "YYYY-MM-DD".

type tenantDTO struct {
	ID                uuid.UUID  `json:"id"`
	Slug              string     `json:"slug"`
	LegalName         string     `json:"legal_name"`
	DisplayName       string     `json:"display_name"`
	TaxCode           *string    `json:"tax_code"`
	Address           *string    `json:"address"`
	Phone             *string    `json:"phone"`
	Currency          string     `json:"currency"`
	Timezone          string     `json:"timezone"`
	DateFormat        string     `json:"date_format"`
	Status            string     `json:"status"`
	IsSandbox         bool       `json:"is_sandbox"`
	SandboxOf         *uuid.UUID `json:"sandbox_of"`
	OpeningCutoffDate *string    `json:"opening_cutoff_date"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Version           int        `json:"version"`
}

func toTenantDTO(t Tenant) tenantDTO {
	return tenantDTO{
		ID: t.ID, Slug: t.Slug, LegalName: t.LegalName, DisplayName: t.DisplayName, TaxCode: t.TaxCode,
		Address: t.Address, Phone: t.Phone, Currency: t.Currency, Timezone: t.Timezone, DateFormat: DateFormat,
		Status: t.Status, IsSandbox: t.IsSandbox, SandboxOf: t.SandboxOf, OpeningCutoffDate: dateString(t.OpeningCutoffDate),
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Version: t.Version,
	}
}

type settingsDTO struct {
	PricesIncludeTax        bool        `json:"prices_include_tax"`
	RoundingMode            string      `json:"rounding_mode"`
	DefaultPaymentTermsDays int         `json:"default_payment_terms_days"`
	BlockExpiredLotSale     bool        `json:"block_expired_lot_sale"`
	ExpiryWarningDays       int         `json:"expiry_warning_days"`
	OfflinePOSEnabled       bool        `json:"offline_pos_enabled"`
	OfflinePOSMaxAmount     money.Money `json:"offline_pos_max_amount"`
	OfflinePOSMaxHours      int         `json:"offline_pos_max_hours"`
	AnalyticsAIEnabled      bool        `json:"analytics_ai_enabled"`
	ProductAnalyticsEnabled bool        `json:"product_analytics_enabled"`
	UpdatedAt               time.Time   `json:"updated_at"`
	Version                 int         `json:"version"`
}

func toSettingsDTO(s Settings) settingsDTO {
	return settingsDTO{
		PricesIncludeTax: s.PricesIncludeTax, RoundingMode: s.RoundingMode, DefaultPaymentTermsDays: s.DefaultPaymentTermsDays,
		BlockExpiredLotSale: s.BlockExpiredLotSale, ExpiryWarningDays: s.ExpiryWarningDays, OfflinePOSEnabled: s.OfflinePOSEnabled,
		OfflinePOSMaxAmount: s.OfflinePOSMaxAmount, OfflinePOSMaxHours: s.OfflinePOSMaxHours,
		AnalyticsAIEnabled: s.AnalyticsAIEnabled, ProductAnalyticsEnabled: s.ProductAnalyticsEnabled,
		UpdatedAt: s.UpdatedAt, Version: s.Version,
	}
}

type storeDTO struct {
	Store struct {
		ID      uuid.UUID `json:"id"`
		Name    string    `json:"name"`
		Address *string   `json:"address"`
		Phone   *string   `json:"phone"`
	} `json:"store"`
	Warehouse struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"warehouse"`
}

func toStoreDTO(s Store, w Warehouse) storeDTO {
	var out storeDTO
	out.Store.ID, out.Store.Name, out.Store.Address, out.Store.Phone = s.ID, s.Name, s.Address, s.Phone
	out.Warehouse.ID, out.Warehouse.Name = w.ID, w.Name
	return out
}

type periodLockDTO struct {
	ID            uuid.UUID `json:"id"`
	Action        string    `json:"action"`
	LockedThrough *string   `json:"locked_through"`
	Reason        *string   `json:"reason"`
	ActorID       uuid.UUID `json:"actor_id"`
	CreatedAt     time.Time `json:"created_at"`
}

func toPeriodLockDTO(ev PeriodLockEvent) periodLockDTO {
	return periodLockDTO{
		ID: ev.ID, Action: ev.Action, LockedThrough: dateString(ev.LockedThrough), Reason: ev.Reason,
		ActorID: ev.ActorID, CreatedAt: ev.CreatedAt,
	}
}

type periodLocksDTO struct {
	LockedThrough *string         `json:"locked_through"`
	Version       int             `json:"version"`
	Data          []periodLockDTO `json:"data"`
	NextCursor    *string         `json:"next_cursor"`
}

type lockRequest struct {
	LockedThrough string `json:"locked_through" validate:"required"`
}

// reopenRequest.LockedThrough must be present: a date, or null to unlock every period, so a
// client that forgets the field cannot unlock everything by accident.
type reopenRequest struct {
	LockedThrough           json.RawMessage `json:"locked_through" validate:"required"`
	Reason                  string          `json:"reason" validate:"required"`
	ExceptionConfirmationID uuid.UUID       `json:"exception_confirmation_id" validate:"required"`
}

type confirmationRequest struct {
	Kind        string          `json:"kind" validate:"required"`
	DocType     string          `json:"doc_type" validate:"required"`
	DocID       uuid.UUID       `json:"doc_id" validate:"required"`
	DocVersion  int             `json:"doc_version" validate:"gte=0"`
	PayloadHash string          `json:"payload_hash" validate:"required"`
	Impact      json.RawMessage `json:"impact"`
	Note        string          `json:"note"`
}

type confirmationDTO struct {
	ID          uuid.UUID       `json:"id"`
	Kind        exception.Kind  `json:"kind"`
	DocType     string          `json:"doc_type"`
	DocID       uuid.UUID       `json:"doc_id"`
	DocVersion  int             `json:"doc_version"`
	PayloadHash string          `json:"payload_hash"`
	Impact      json.RawMessage `json:"impact"`
	Note        *string         `json:"note"`
	ConfirmedBy uuid.UUID       `json:"confirmed_by"`
	ConfirmedAt time.Time       `json:"confirmed_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
}

func toConfirmationDTO(c exception.Confirmation) confirmationDTO {
	return confirmationDTO{
		ID: c.ID, Kind: c.Kind, DocType: c.DocType, DocID: c.DocID, DocVersion: c.DocVersion,
		PayloadHash: hex.EncodeToString(c.PayloadHash), Impact: c.Impact, Note: c.Note, ConfirmedBy: c.ConfirmedBy,
		ConfirmedAt: c.ConfirmedAt, ExpiresAt: c.ExpiresAt(),
	}
}

type auditEventDTO struct {
	ID         uuid.UUID       `json:"id"`
	ActorType  string          `json:"actor_type"`
	ActorID    *uuid.UUID      `json:"actor_id"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   *uuid.UUID      `json:"entity_id"`
	DocNo      *string         `json:"doc_no"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	Source     string          `json:"source"`
	RequestID  *string         `json:"request_id"`
	IP         *string         `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
}

func toAuditEventDTO(ev AuditEvent) auditEventDTO {
	return auditEventDTO{
		ID: ev.ID, ActorType: ev.ActorType, ActorID: ev.ActorID, Action: ev.Action, EntityType: ev.EntityType,
		EntityID: ev.EntityID, DocNo: ev.DocNo, Before: rawJSON(ev.Before), After: rawJSON(ev.After), Source: ev.Source,
		RequestID: ev.RequestID, IP: ev.IP, CreatedAt: ev.CreatedAt,
	}
}

type attachmentRequest struct {
	OwnerType   string    `json:"owner_type" validate:"required"`
	OwnerID     uuid.UUID `json:"owner_id" validate:"required"`
	FileName    string    `json:"file_name" validate:"required"`
	ContentType string    `json:"content_type" validate:"required"`
	SizeBytes   int64     `json:"size_bytes" validate:"gt=0"`
	SHA256      string    `json:"sha256" validate:"required"`
}

type attachmentDTO struct {
	ID          uuid.UUID `json:"id"`
	OwnerType   string    `json:"owner_type"`
	OwnerID     uuid.UUID `json:"owner_id"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	ScanStatus  string    `json:"scan_status"`
	VersionNo   int       `json:"version_no"`
	UploadedBy  uuid.UUID `json:"uploaded_by"`
	CreatedAt   time.Time `json:"created_at"`
}

func toAttachmentDTO(a Attachment) attachmentDTO {
	return attachmentDTO{
		ID: a.ID, OwnerType: a.OwnerType, OwnerID: a.OwnerID, FileName: a.FileName, ContentType: a.ContentType,
		SizeBytes: a.SizeBytes, SHA256: hex.EncodeToString(a.SHA256), ScanStatus: a.ScanStatus, VersionNo: a.VersionNo,
		UploadedBy: a.UploadedBy, CreatedAt: a.CreatedAt,
	}
}

type uploadDTO struct {
	Attachment attachmentDTO `json:"attachment"`
	Upload     struct {
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
		ExpiresAt time.Time         `json:"expires_at"`
	} `json:"upload"`
}

func toUploadDTO(u Upload) uploadDTO {
	out := uploadDTO{Attachment: toAttachmentDTO(u.Attachment)}
	out.Upload.Method, out.Upload.URL, out.Upload.ExpiresAt = u.Request.Method, u.Request.URL, u.Request.ExpiresAt
	// Every header the presigned request was signed with must be sent with the upload.
	out.Upload.Headers = make(map[string]string, len(u.Request.Headers))
	for k, v := range u.Request.Headers {
		out.Upload.Headers[k] = strings.Join(v, ", ")
	}
	return out
}

type attachmentURLDTO struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// parseDate reads a "YYYY-MM-DD" business date; field names the 422 detail.
func parseDate(field, v string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return time.Time{}, errInvalid(map[string]string{field: "invalid_date"})
	}
	return clock.Date(t.Year(), t.Month(), t.Day()), nil
}

// parseOptionalDate reads a JSON date string, or null (nil).
func parseOptionalDate(field string, raw json.RawMessage) (*time.Time, error) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errInvalid(map[string]string{field: "invalid_date"})
	}
	t, err := parseDate(field, v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// parseHash reads a lower- or upper-case hex SHA-256 digest.
func parseHash(field, v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 32 {
		return nil, errInvalid(map[string]string{field: "invalid_sha256"})
	}
	return b, nil
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}
