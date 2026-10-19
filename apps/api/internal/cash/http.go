package cash

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the fund endpoints of tenant hosts. There is no cash screen: payments are
// recorded by the documents that take or pay money; these routes list the funds and methods and
// let the owner set up a fund.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant-host group behind session, host guard, CSRF, entitlement and support
	// read-only.
	Tenant *gin.RouterGroup
}

// Register adds the fund routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil {
		panic("cash: Register needs the tenant group")
	}
	r.Tenant.GET("/funds", h.listFunds)
	r.Tenant.PATCH("/funds/:id", h.updateFund)
	r.Tenant.GET("/payment-methods", h.listMethods)
}

func scopeFor(c *gin.Context) (Scope, bool) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return Scope{}, false
	}
	return Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}, true
}

func listOf[T any](items []T) httpx.List[T] {
	if items == nil {
		items = []T{}
	}
	return httpx.List[T]{Data: items}
}

func mapSlice[A, B any](in []A, fn func(A) B) []B {
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

func (h *Handler) listFunds(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.Funds(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, listOf(mapSlice(out, toFundDTO)))
}

func (h *Handler) listMethods(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.Methods(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, listOf(mapSlice(out, toMethodDTO)))
}

func (h *Handler) updateFund(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	version, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req fundPatch
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	in, err := req.input()
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f, err := h.svc.UpdateFund(c.Request.Context(), actor, id, version, in)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, f.Version)
	c.JSON(http.StatusOK, toFundDTO(f))
}
