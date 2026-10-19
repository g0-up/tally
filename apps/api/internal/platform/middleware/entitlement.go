package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// EntitlementReader answers whether a tenant's subscription currently allows writes.
type EntitlementReader interface {
	CanWrite(ctx context.Context, tenantID uuid.UUID) (bool, error)
}

// Entitlement blocks state-changing requests of a tenant whose subscription is read-only
// (403 subscription_read_only). Reads and exports stay available. Runs after Tenant.
func Entitlement(reader EntitlementReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if httpx.MethodIsSafe(c.Request.Method) {
			c.Next()
			return
		}
		ok, err := reader.CanWrite(c.Request.Context(), TenantID(c))
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		if !ok {
			httpx.RenderError(c, apperr.NewReadOnlyTenant())
			return
		}
		c.Next()
	}
}

// MaintenanceReader answers whether an operator holds a tenant in maintenance.
type MaintenanceReader interface {
	InMaintenance(ctx context.Context, tenantID uuid.UUID) (bool, error)
}

// Maintenance refuses every state-changing request of a tenant an operator holds in
// maintenance (423 tenant_maintenance), so nothing writes while a restore copies rows in. Reads
// stay available. Runs after Tenant.
func Maintenance(reader MaintenanceReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if httpx.MethodIsSafe(c.Request.Method) {
			c.Next()
			return
		}
		held, err := reader.InMaintenance(c.Request.Context(), TenantID(c))
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		if held {
			httpx.RenderError(c, apperr.NewTenantMaintenance())
			return
		}
		c.Next()
	}
}

// ModuleReader answers whether a tenant's entitlements include a module.
type ModuleReader interface {
	HasModule(ctx context.Context, tenantID uuid.UUID, module string) (bool, error)
}

// ModuleGate answers 403 module_not_in_plan to a request for a module the tenant's plan does
// not include, for every method: moduleOf names the module of the matched route ("" for none).
// Runs after Tenant.
func ModuleGate(reader ModuleReader, moduleOf func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		module := moduleOf(c)
		if module == "" {
			c.Next()
			return
		}
		ok, err := reader.HasModule(c.Request.Context(), TenantID(c), module)
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		if !ok {
			httpx.RenderError(c, apperr.NewForbidden("module_not_in_plan", "This feature is not included in the store's plan"))
			return
		}
		c.Next()
	}
}
