package org

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB          *db.DB
	Clock       clock.Clock
	Audit       *audit.Recorder
	Exceptions  *exception.Guard
	Provisioner *Provisioner
	// Objects stores attachments. It is required by the attachment use cases only.
	Objects objectstore.Store
	// AttachmentOwners are the records files may be attached to; without them no upload is
	// accepted.
	AttachmentOwners AttachmentOwners
	// AddressMoved is told a slug that now names another tenant (a reset sandbox), after the
	// change committed, so a host cache that resolved it forgets the old tenant. Optional.
	AddressMoved func(slug string)
	// Limits enforces the plan's storage limit on uploads; nil leaves storage unbounded.
	Limits LimitChecker
}

// LimitChecker answers 409 plan_limit_reached when used exceeds the tenant's plan limit key; q
// is the caller's tenant transaction.
type LimitChecker interface {
	Within(ctx context.Context, q db.Querier, tenantID uuid.UUID, key string, used int64) error
}

// LimitStorageMB is the plan limit on the stored attachment bytes, in MiB.
const LimitStorageMB = "storage_mb"

// Service implements the organisation use cases. Every method runs in its own tenant
// transaction; the tenant id always comes from the authenticated session.
type Service struct {
	db          *db.DB
	clock       clock.Clock
	audit       *audit.Recorder
	exceptions  *exception.Guard
	provisioner *Provisioner
	objects     objectstore.Store
	owners      AttachmentOwners
	// addressMoved may be nil.
	addressMoved func(slug string)
	limits       LimitChecker
}

// NewService returns the service.
func NewService(d Deps) *Service {
	return &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, exceptions: d.Exceptions, provisioner: d.Provisioner, objects: d.Objects,
		owners: d.AttachmentOwners, addressMoved: d.AddressMoved, limits: d.Limits,
	}
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// read runs fn in a tenant transaction suited to scope.
func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// readTenant reads the tenant row; forUpdate serialises profile changes and period locks.
func readTenant(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Tenant, error) {
	query := `SELECT ` + tenantColumns + ` FROM tenants WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var t Tenant
	if err := q.QueryRowxContext(ctx, query, id).StructScan(&t); err != nil {
		if db.IsNoRows(err) {
			return Tenant{}, errNotFound()
		}
		return Tenant{}, fmt.Errorf("org: read tenant: %w", err)
	}
	return t, nil
}

// Tenant returns the tenant profile. The tenants table is a platform table the read-only support
// role cannot see, so it is always read in a regular tenant transaction (a single SELECT).
func (s *Service) Tenant(ctx context.Context, tenantID uuid.UUID) (Tenant, error) {
	var t Tenant
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var err error
		t, err = readTenant(ctx, q, tenantID, false)
		return err
	})
	return t, err
}

// TenantPatch changes the tenant profile; nil fields are kept and an empty string clears an
// optional field (tax code, address, phone).
type TenantPatch struct {
	LegalName   *string `json:"legal_name"`
	DisplayName *string `json:"display_name"`
	TaxCode     *string `json:"tax_code"`
	Address     *string `json:"address"`
	Phone       *string `json:"phone"`
	Timezone    *string `json:"timezone"`
	// OpeningCutoffDate is the onboarding cutoff (YYYY-MM-DD) the opening balances are stated at;
	// "" clears it. It cannot change once the opening balances are confirmed.
	OpeningCutoffDate *string `json:"opening_cutoff_date"`
}

var (
	// taxCodeFormat is a Vietnamese tax code (MST): 10 digits, or 13 with a branch suffix.
	taxCodeFormat = regexp.MustCompile(`^[0-9]{10}(-[0-9]{3})?$`)
	phoneFormat   = regexp.MustCompile(`^\+?[0-9][0-9 .-]{6,18}[0-9]$`)
)

const maxAddressLen = 500

func (p *TenantPatch) normalise() error {
	fields := map[string]string{}
	trim := func(v *string) {
		if v != nil {
			*v = strings.TrimSpace(*v)
		}
	}
	trim(p.LegalName)
	trim(p.DisplayName)
	trim(p.TaxCode)
	trim(p.Address)
	trim(p.Phone)
	trim(p.Timezone)
	trim(p.OpeningCutoffDate)
	for field, v := range map[string]*string{"legal_name": p.LegalName, "display_name": p.DisplayName} {
		if v == nil {
			continue
		}
		if err := ValidateName(field, *v); err != nil {
			e, _ := apperr.As(err)
			fields[field] = e.Fields[field]
		}
	}
	if p.TaxCode != nil && *p.TaxCode != "" && !taxCodeFormat.MatchString(*p.TaxCode) {
		fields["tax_code"] = "invalid"
	}
	if p.Phone != nil && *p.Phone != "" && !phoneFormat.MatchString(*p.Phone) {
		fields["phone"] = "invalid"
	}
	if p.Address != nil && (utf8.RuneCountInString(*p.Address) > maxAddressLen || strings.ContainsRune(*p.Address, 0)) {
		fields["address"] = "invalid"
	}
	if p.Timezone != nil {
		if _, err := clock.LoadLocation(*p.Timezone); err != nil {
			fields["timezone"] = "invalid"
		}
	}
	if p.OpeningCutoffDate != nil && *p.OpeningCutoffDate != "" {
		if _, err := time.Parse(time.DateOnly, *p.OpeningCutoffDate); err != nil {
			fields["opening_cutoff_date"] = "invalid"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// UpdateTenant applies patch when the tenant is still at version expected (If-Match).
func (s *Service) UpdateTenant(ctx context.Context, actor audit.Actor, expected int, patch TenantPatch) (Tenant, error) {
	if err := patch.normalise(); err != nil {
		return Tenant{}, err
	}
	var out Tenant
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readTenant(ctx, q, actor.TenantID, true)
		if err != nil {
			return err
		}
		var sets []db.Set
		changedBefore, changedAfter := map[string]any{}, map[string]any{}
		required := func(col string, cur string, v *string) {
			if v != nil && *v != cur {
				sets = append(sets, db.Set{Column: col, Value: *v})
				changedBefore[col], changedAfter[col] = cur, *v
			}
		}
		optional := func(col string, cur *string, v *string) {
			if v == nil {
				return
			}
			var next *string
			if *v != "" {
				next = v
			}
			if derefOr(cur) == derefOr(next) {
				return
			}
			sets = append(sets, db.Set{Column: col, Value: next})
			changedBefore[col], changedAfter[col] = cur, next
		}
		required("legal_name", before.LegalName, patch.LegalName)
		required("display_name", before.DisplayName, patch.DisplayName)
		optional("tax_code", before.TaxCode, patch.TaxCode)
		optional("address", before.Address, patch.Address)
		optional("phone", before.Phone, patch.Phone)
		required("timezone", before.Timezone, patch.Timezone)
		if before.Version != expected {
			return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
		}
		if patch.OpeningCutoffDate != nil {
			set, err := s.openingCutoffChange(ctx, q, before, *patch.OpeningCutoffDate)
			if err != nil {
				return err
			}
			if set != nil {
				sets = append(sets, *set)
				changedBefore["opening_cutoff_date"], changedAfter["opening_cutoff_date"] = dateString(before.OpeningCutoffDate), set.Value
			}
		}
		if len(sets) == 0 {
			out = before
			return nil
		}
		if _, err := db.UpdateWithVersion(ctx, q, "tenants", actor.TenantID, expected, s.clock.Now(), sets...); err != nil {
			return err
		}
		if out, err = readTenant(ctx, q, actor.TenantID, false); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "tenant.update", EntityType: "tenant", EntityID: actor.TenantID, Before: changedBefore, After: changedAfter,
		})
	})
	return out, err
}

// openingCutoffChange is the column change of a new opening cutoff ("" clears it), or nil when
// it does not change. The cutoff may not be after the store's business date, and it is frozen
// once the opening balances are confirmed, since the period lock was set from it. The tenant row
// is locked, which orders this against the confirmation.
func (s *Service) openingCutoffChange(ctx context.Context, q db.Querier, before Tenant, raw string) (*db.Set, error) {
	var next *string
	if raw != "" {
		next = &raw
	}
	if derefOr(dateString(before.OpeningCutoffDate)) == derefOr(next) {
		return nil, nil
	}
	if next != nil {
		loc, err := tenantLocation(before)
		if err != nil {
			return nil, err
		}
		day, _ := time.Parse(time.DateOnly, raw)
		if day.After(clock.BusinessDate(s.clock.Now(), loc)) {
			return nil, errInvalid(map[string]string{"opening_cutoff_date": "in_future"})
		}
	}
	var confirmed bool
	if err := q.QueryRowxContext(ctx, `
		SELECT completed_at IS NOT NULL FROM onboarding_steps WHERE tenant_id = $1 AND step_key = $2`,
		before.ID, StepConfirmOpenings).Scan(&confirmed); err != nil && !db.IsNoRows(err) {
		return nil, fmt.Errorf("org: read opening confirmation: %w", err)
	}
	if confirmed {
		return nil, apperr.NewConflict("opening_balances_confirmed", "The opening balances are confirmed; the cutoff date cannot change")
	}
	return &db.Set{Column: "opening_cutoff_date", Value: next}, nil
}

func derefOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// Settings returns the tenant settings.
func (s *Service) Settings(ctx context.Context, scope Scope) (Settings, error) {
	var out Settings
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = readSettings(ctx, q, scope.TenantID, false)
		return err
	})
	return out, err
}

func readSettings(ctx context.Context, q db.Querier, tenantID uuid.UUID, forUpdate bool) (Settings, error) {
	query := `SELECT ` + settingsColumns + ` FROM tenant_settings WHERE tenant_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var st Settings
	if err := q.QueryRowxContext(ctx, query, tenantID).StructScan(&st); err != nil {
		if db.IsNoRows(err) {
			return Settings{}, errNotFound()
		}
		return Settings{}, fmt.Errorf("org: read settings: %w", err)
	}
	return st, nil
}

// SettingsPatch changes tenant settings; nil fields are kept.
type SettingsPatch struct {
	PricesIncludeTax        *bool        `json:"prices_include_tax"`
	RoundingMode            *string      `json:"rounding_mode"`
	DefaultPaymentTermsDays *int         `json:"default_payment_terms_days"`
	BlockExpiredLotSale     *bool        `json:"block_expired_lot_sale"`
	ExpiryWarningDays       *int         `json:"expiry_warning_days"`
	OfflinePOSEnabled       *bool        `json:"offline_pos_enabled"`
	OfflinePOSMaxAmount     *money.Money `json:"offline_pos_max_amount"`
	OfflinePOSMaxHours      *int         `json:"offline_pos_max_hours"`
	AnalyticsAIEnabled      *bool        `json:"analytics_ai_enabled"`
	ProductAnalyticsEnabled *bool        `json:"product_analytics_enabled"`
}

// Settings limits beyond the database CHECKs.
const (
	maxTermsDays         = 365
	maxExpiryWarningDays = 365
	maxOfflineHours      = 72
)

func (p SettingsPatch) validate() error {
	fields := map[string]string{}
	if p.RoundingMode != nil && *p.RoundingMode != "line" && *p.RoundingMode != "document" {
		fields["rounding_mode"] = "invalid"
	}
	if p.DefaultPaymentTermsDays != nil && (*p.DefaultPaymentTermsDays < 0 || *p.DefaultPaymentTermsDays > maxTermsDays) {
		fields["default_payment_terms_days"] = "out_of_range"
	}
	if p.ExpiryWarningDays != nil && (*p.ExpiryWarningDays < 0 || *p.ExpiryWarningDays > maxExpiryWarningDays) {
		fields["expiry_warning_days"] = "out_of_range"
	}
	if p.OfflinePOSMaxAmount != nil && p.OfflinePOSMaxAmount.IsNegative() {
		fields["offline_pos_max_amount"] = "out_of_range"
	}
	if p.OfflinePOSMaxHours != nil && (*p.OfflinePOSMaxHours < 0 || *p.OfflinePOSMaxHours > maxOfflineHours) {
		fields["offline_pos_max_hours"] = "out_of_range"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// UpdateSettings applies patch when the settings are still at version expected (If-Match).
func (s *Service) UpdateSettings(ctx context.Context, actor audit.Actor, expected int, patch SettingsPatch) (Settings, error) {
	if err := patch.validate(); err != nil {
		return Settings{}, err
	}
	var out Settings
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readSettings(ctx, q, actor.TenantID, true)
		if err != nil {
			return err
		}
		if before.Version != expected {
			return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
		}
		after := before
		applyBool(&after.PricesIncludeTax, patch.PricesIncludeTax)
		if patch.RoundingMode != nil {
			after.RoundingMode = *patch.RoundingMode
		}
		applyInt(&after.DefaultPaymentTermsDays, patch.DefaultPaymentTermsDays)
		applyBool(&after.BlockExpiredLotSale, patch.BlockExpiredLotSale)
		applyInt(&after.ExpiryWarningDays, patch.ExpiryWarningDays)
		applyBool(&after.OfflinePOSEnabled, patch.OfflinePOSEnabled)
		if patch.OfflinePOSMaxAmount != nil {
			after.OfflinePOSMaxAmount = *patch.OfflinePOSMaxAmount
		}
		applyInt(&after.OfflinePOSMaxHours, patch.OfflinePOSMaxHours)
		applyBool(&after.AnalyticsAIEnabled, patch.AnalyticsAIEnabled)
		applyBool(&after.ProductAnalyticsEnabled, patch.ProductAnalyticsEnabled)
		if settingsEqual(before, after) {
			out = before
			return nil
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE tenant_settings SET prices_include_tax = $2, rounding_mode = $3, default_payment_terms_days = $4,
			  block_expired_lot_sale = $5, expiry_warning_days = $6, offline_pos_enabled = $7, offline_pos_max_amount = $8,
			  offline_pos_max_hours = $9, analytics_ai_enabled = $10, product_analytics_enabled = $11,
			  updated_at = $12, version = version + 1
			WHERE tenant_id = $1`,
			actor.TenantID, after.PricesIncludeTax, after.RoundingMode, after.DefaultPaymentTermsDays, after.BlockExpiredLotSale,
			after.ExpiryWarningDays, after.OfflinePOSEnabled, after.OfflinePOSMaxAmount, after.OfflinePOSMaxHours,
			after.AnalyticsAIEnabled, after.ProductAnalyticsEnabled, s.clock.Now()); err != nil {
			return fmt.Errorf("org: update settings: %w", err)
		}
		if out, err = readSettings(ctx, q, actor.TenantID, false); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "tenant_settings.update", EntityType: "tenant_settings", EntityID: actor.TenantID,
			Before: settingsSnapshot(before), After: settingsSnapshot(out),
		})
	})
	return out, err
}

func applyBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func applyInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}

func settingsEqual(a, b Settings) bool {
	return a.PricesIncludeTax == b.PricesIncludeTax && a.RoundingMode == b.RoundingMode &&
		a.DefaultPaymentTermsDays == b.DefaultPaymentTermsDays && a.BlockExpiredLotSale == b.BlockExpiredLotSale &&
		a.ExpiryWarningDays == b.ExpiryWarningDays && a.OfflinePOSEnabled == b.OfflinePOSEnabled &&
		a.OfflinePOSMaxAmount.Decimal().Equal(b.OfflinePOSMaxAmount.Decimal()) && a.OfflinePOSMaxHours == b.OfflinePOSMaxHours &&
		a.AnalyticsAIEnabled == b.AnalyticsAIEnabled && a.ProductAnalyticsEnabled == b.ProductAnalyticsEnabled
}

// settingsSnapshot is the audited form of the settings (without bookkeeping columns).
func settingsSnapshot(st Settings) map[string]any {
	return map[string]any{
		"prices_include_tax": st.PricesIncludeTax, "rounding_mode": st.RoundingMode,
		"default_payment_terms_days": st.DefaultPaymentTermsDays, "block_expired_lot_sale": st.BlockExpiredLotSale,
		"expiry_warning_days": st.ExpiryWarningDays, "offline_pos_enabled": st.OfflinePOSEnabled,
		"offline_pos_max_amount": st.OfflinePOSMaxAmount, "offline_pos_max_hours": st.OfflinePOSMaxHours,
		"analytics_ai_enabled": st.AnalyticsAIEnabled, "product_analytics_enabled": st.ProductAnalyticsEnabled,
	}
}

// StoreAndWarehouse returns the single store and warehouse of the tenant.
func (s *Service) StoreAndWarehouse(ctx context.Context, scope Scope) (Store, Warehouse, error) {
	var st Store
	var wh Warehouse
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := q.QueryRowxContext(ctx, `
			SELECT id, tenant_id, name, address, phone, created_at, updated_at, version FROM stores WHERE tenant_id = $1`,
			scope.TenantID).StructScan(&st); err != nil {
			if db.IsNoRows(err) {
				return errNotFound()
			}
			return fmt.Errorf("org: read store: %w", err)
		}
		if err := q.QueryRowxContext(ctx, `
			SELECT id, tenant_id, store_id, name, created_at, updated_at FROM warehouses WHERE tenant_id = $1`,
			scope.TenantID).StructScan(&wh); err != nil {
			if db.IsNoRows(err) {
				return errNotFound()
			}
			return fmt.Errorf("org: read warehouse: %w", err)
		}
		return nil
	})
	return st, wh, err
}

// tenantLocation returns the time zone of the tenant (business dates are civil dates there).
func tenantLocation(t Tenant) (*time.Location, error) {
	loc, err := clock.LoadLocation(t.Timezone)
	if err != nil {
		return nil, fmt.Errorf("org: tenant %s: %w", t.ID, err)
	}
	return loc, nil
}
