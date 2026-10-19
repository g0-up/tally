// Package org owns the tenant (store) and its organisation data: profile, the single store and
// warehouse, settings, document numbering, period locks, exception confirmations, the audit log
// reader, attachments and demo (sandbox) tenants. TenantProvisioner creates a tenant with its
// default data through the same code paths user actions use.
package org

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Tenant profile defaults.
const (
	DefaultCurrency = "VND"
	DefaultTimezone = "Asia/Ho_Chi_Minh"
	// DateFormat is the display date format of every tenant.
	DateFormat = "dd/MM/yyyy"
	// WarehouseName is the name of the single warehouse.
	WarehouseName = "Kho cửa hàng"
)

// Tenant statuses.
const (
	TenantActive   = "active"
	TenantReadOnly = "read_only"
	TenantClosing  = "closing"
	TenantClosed   = "closed"
	// TenantReplaced is a sandbox a reset superseded: it keeps its rows but no host or account.
	TenantReplaced = "replaced"
)

// OnboardingSteps are the checklist steps every tenant starts with.
var OnboardingSteps = []string{
	"store_profile", "catalog_setup", "opening_stock", "opening_debt", "opening_cash", "first_sale",
	"confirm_opening_balances",
}

// Tenant is a store account (tenants row).
type Tenant struct {
	ID                uuid.UUID  `db:"id"`
	Slug              string     `db:"slug"`
	LegalName         string     `db:"legal_name"`
	DisplayName       string     `db:"display_name"`
	TaxCode           *string    `db:"tax_code"`
	Address           *string    `db:"address"`
	Phone             *string    `db:"phone"`
	Currency          string     `db:"currency"`
	Timezone          string     `db:"timezone"`
	Status            string     `db:"status"`
	IsSandbox         bool       `db:"is_sandbox"`
	SandboxOf         *uuid.UUID `db:"sandbox_of"`
	OpeningCutoffDate *time.Time `db:"opening_cutoff_date"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
	Version           int        `db:"version"`
}

const tenantColumns = `id, slug, legal_name, display_name, tax_code, address, phone, currency, timezone, status,
	is_sandbox, sandbox_of, opening_cutoff_date, created_at, updated_at, version`

// Store is the single store of a tenant.
type Store struct {
	ID        uuid.UUID `db:"id"`
	TenantID  uuid.UUID `db:"tenant_id"`
	Name      string    `db:"name"`
	Address   *string   `db:"address"`
	Phone     *string   `db:"phone"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
	Version   int       `db:"version"`
}

// Warehouse is the single warehouse of a tenant.
type Warehouse struct {
	ID        uuid.UUID `db:"id"`
	TenantID  uuid.UUID `db:"tenant_id"`
	StoreID   uuid.UUID `db:"store_id"`
	Name      string    `db:"name"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Settings are the tenant settings (tenant_settings row).
type Settings struct {
	TenantID                uuid.UUID   `db:"tenant_id"`
	PricesIncludeTax        bool        `db:"prices_include_tax"`
	RoundingMode            string      `db:"rounding_mode"`
	DefaultPaymentTermsDays int         `db:"default_payment_terms_days"`
	BlockExpiredLotSale     bool        `db:"block_expired_lot_sale"`
	ExpiryWarningDays       int         `db:"expiry_warning_days"`
	OfflinePOSEnabled       bool        `db:"offline_pos_enabled"`
	OfflinePOSMaxAmount     money.Money `db:"offline_pos_max_amount"`
	OfflinePOSMaxHours      int         `db:"offline_pos_max_hours"`
	AnalyticsAIEnabled      bool        `db:"analytics_ai_enabled"`
	ProductAnalyticsEnabled bool        `db:"product_analytics_enabled"`
	UpdatedAt               time.Time   `db:"updated_at"`
	Version                 int         `db:"version"`
}

const settingsColumns = `tenant_id, prices_include_tax, rounding_mode, default_payment_terms_days, block_expired_lot_sale,
	expiry_warning_days, offline_pos_enabled, offline_pos_max_amount, offline_pos_max_hours, analytics_ai_enabled,
	product_analytics_enabled, updated_at, version`

// Period lock actions.
const (
	PeriodLockActionLock   = "lock"
	PeriodLockActionReopen = "reopen"
)

// PeriodLockEvent is one append-only period_locks row; the latest one is the current lock.
type PeriodLockEvent struct {
	ID       uuid.UUID `db:"id"`
	TenantID uuid.UUID `db:"tenant_id"`
	Action   string    `db:"action"`
	// LockedThrough is the lock date in effect after the event; nil after a reopen that unlocked
	// every period.
	LockedThrough *time.Time `db:"locked_through"`
	Reason        *string    `db:"reason"`
	ActorID       uuid.UUID  `db:"actor_id"`
	CreatedAt     time.Time  `db:"created_at"`
}

// AuditEvent is one audit_events row (before/after were redacted when recorded).
type AuditEvent struct {
	ID         uuid.UUID  `db:"id"`
	TenantID   uuid.UUID  `db:"tenant_id"`
	ActorType  string     `db:"actor_type"`
	ActorID    *uuid.UUID `db:"actor_id"`
	Action     string     `db:"action"`
	EntityType string     `db:"entity_type"`
	EntityID   *uuid.UUID `db:"entity_id"`
	DocNo      *string    `db:"doc_no"`
	Before     []byte     `db:"before"` // JSON, nil when absent
	After      []byte     `db:"after"`  // JSON, nil when absent
	Source     string     `db:"source"`
	RequestID  *string    `db:"request_id"`
	IP         *string    `db:"ip"`
	CreatedAt  time.Time  `db:"created_at"`
}

// Attachment scan statuses. A file is "clean" once the object store confirmed the declared size
// and type; "rejected" files never get a download URL.
const (
	ScanPending  = "pending"
	ScanClean    = "clean"
	ScanRejected = "rejected"
)

// Attachment is a file stored in the object store under the tenant prefix.
type Attachment struct {
	ID           uuid.UUID  `db:"id"`
	TenantID     uuid.UUID  `db:"tenant_id"`
	OwnerType    string     `db:"owner_type"`
	OwnerID      uuid.UUID  `db:"owner_id"`
	StorageKey   string     `db:"storage_key"`
	FileName     string     `db:"file_name"`
	ContentType  string     `db:"content_type"`
	SizeBytes    int64      `db:"size_bytes"`
	SHA256       []byte     `db:"sha256"`
	ScanStatus   string     `db:"scan_status"`
	VersionNo    int        `db:"version_no"`
	SupersedesID *uuid.UUID `db:"supersedes_id"`
	UploadedBy   uuid.UUID  `db:"uploaded_by"`
	CreatedAt    time.Time  `db:"created_at"`
}

const attachmentColumns = `id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type, size_bytes, sha256,
	scan_status, version_no, supersedes_id, uploaded_by, created_at`
