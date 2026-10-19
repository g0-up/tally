package partner

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the partner endpoints of tenant hosts.
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
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
}

// Register adds the partner routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil {
		panic("partner: Register needs the tenant group and the command router")
	}
	g := r.Tenant
	g.GET("/customers", h.listCustomers)
	g.POST("/customers", h.createCustomer)
	g.GET("/customers/:id", h.getCustomer)
	g.PATCH("/customers/:id", h.updateCustomer)
	g.DELETE("/customers/:id", h.deleteCustomer)

	g.GET("/suppliers", h.listSuppliers)
	g.POST("/suppliers", h.createSupplier)
	g.GET("/suppliers/:id", h.getSupplier)
	g.PATCH("/suppliers/:id", h.updateSupplier)
	g.DELETE("/suppliers/:id", h.deleteSupplier)
	sup := r.Commands(g, "suppliers")
	sup.Item("stop", h.setSupplierStatus(StatusInactive))
	sup.Item("reopen", h.setSupplierStatus(StatusActive))

	g.GET("/customer-groups", h.listGroups)
	g.POST("/customer-groups", h.createGroup)
	g.PATCH("/customer-groups/:id", h.updateGroup)
	g.DELETE("/customer-groups/:id", h.deleteGroup)

	g.GET("/parties/:id/contacts", h.listContacts)
	g.POST("/parties/:id/contacts", h.createContact)
	g.PATCH("/parties/:id/contacts/:contactId", h.updateContact)
	g.DELETE("/parties/:id/contacts/:contactId", h.deleteContact)
	g.GET("/parties/:id/addresses", h.listAddresses)
	g.POST("/parties/:id/addresses", h.createAddress)
	g.PATCH("/parties/:id/addresses/:addressId", h.updateAddress)
	g.DELETE("/parties/:id/addresses/:addressId", h.deleteAddress)
}

// Request helpers: each renders the error itself and reports whether the handler may go on.

func scopeFor(c *gin.Context) (Scope, bool) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return Scope{}, false
	}
	return Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}, true
}

func actorFor(c *gin.Context) (audit.Actor, bool) {
	a, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return audit.Actor{}, false
	}
	return a, true
}

func bind[T any](c *gin.Context) (T, bool) {
	var v T
	if err := httpx.BindJSON(c, &v); err != nil {
		httpx.RenderError(c, err)
		return v, false
	}
	return v, true
}

func pathID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := httpx.PathID(c, name)
	if err != nil {
		httpx.RenderError(c, err)
		return uuid.Nil, false
	}
	return id, true
}

func ifMatch(c *gin.Context) (int, bool) {
	v, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return 0, false
	}
	return v, true
}

// respond renders err, or body with status; a positive version becomes the ETag.
func respond(c *gin.Context, status int, version int, body any, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	if version > 0 {
		httpx.SetETag(c, version)
	}
	c.JSON(status, body)
}

func noContent(c *gin.Context, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listOf is an unpaged list in the list envelope.
func listOf[T any](items []T) httpx.List[T] {
	if items == nil {
		items = []T{}
	}
	return httpx.List[T]{Data: items}
}

func queryUUID(c *gin.Context, name string) (*uuid.UUID, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	id, err := ids.Parse(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return nil, false
	}
	return &id, true
}

func queryBool(c *gin.Context, name string) (bool, bool) {
	raw := c.Query(name)
	if raw == "" {
		return false, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return false, false
	}
	return v, true
}

func (h *Handler) listCustomers(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	group, ok := queryUUID(c, "customer_group_id")
	if !ok {
		return
	}
	f := CustomerFilter{Q: c.Query("q"), Status: c.Query("status"), GroupID: group, Limit: page.FetchLimit()}
	if page.After != nil {
		f.AfterKey, f.AfterID = &page.After.SortKey, page.After.ID
	}
	rows, err := h.svc.Customers(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(v Customer) httpx.Cursor { return httpx.Cursor{SortKey: v.NameKey, ID: v.ID} })
	c.JSON(http.StatusOK, httpx.List[customerListItemDTO]{Data: mapSlice(list.Data, toCustomerListItemDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getCustomer(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.Customer(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, v.Version, toCustomerDTO(v), err)
}

func (h *Handler) createCustomer(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req customerRequest
	key, err := httpx.BindJSONKeyed(c, &req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	res, replayed, err := h.svc.CreateCustomer(c.Request.Context(), actor, key, req.input())
	if err == nil {
		httpx.MarkReplay(c, replayed)
	}
	respond(c, http.StatusCreated, res.Customer.Version, toCustomerResultDTO(res), err)
}

func (h *Handler) updateCustomer(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[customerRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.UpdateCustomer(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, res.Customer.Version, toCustomerResultDTO(res), err)
}

func (h *Handler) deleteCustomer(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteCustomer(c.Request.Context(), actor, id))
}

func (h *Handler) listSuppliers(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := SupplierFilter{Q: c.Query("q"), Status: c.Query("status"), Limit: page.FetchLimit()}
	if page.After != nil {
		f.AfterKey, f.AfterID = &page.After.SortKey, page.After.ID
	}
	rows, err := h.svc.Suppliers(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(v Supplier) httpx.Cursor { return httpx.Cursor{SortKey: v.NameKey, ID: v.ID} })
	c.JSON(http.StatusOK, httpx.List[supplierListItemDTO]{Data: mapSlice(list.Data, toSupplierListItemDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getSupplier(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.Supplier(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, v.Version, toSupplierDTO(v), err)
}

func (h *Handler) createSupplier(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[supplierRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.CreateSupplier(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, res.Supplier.Version, toSupplierResultDTO(res), err)
}

func (h *Handler) updateSupplier(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	expected, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[supplierRequest](c)
	if !ok {
		return
	}
	res, err := h.svc.UpdateSupplier(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, res.Supplier.Version, toSupplierResultDTO(res), err)
}

func (h *Handler) deleteSupplier(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteSupplier(c.Request.Context(), actor, id))
}

func (h *Handler) setSupplierStatus(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := actorFor(c)
		if !ok {
			return
		}
		v, err := h.svc.SetSupplierStatus(c.Request.Context(), actor, httpx.CommandID(c), status)
		respond(c, http.StatusOK, v.Version, toSupplierDTO(v), err)
	}
}

func (h *Handler) listGroups(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	active, ok := queryBool(c, "active")
	if !ok {
		return
	}
	out, err := h.svc.CustomerGroups(c.Request.Context(), scope, active)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toCustomerGroupDTO)), err)
}

func (h *Handler) createGroup(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[customerGroupRequest](c)
	if !ok {
		return
	}
	g, err := h.svc.CreateCustomerGroup(c.Request.Context(), actor, GroupInput(req))
	respond(c, http.StatusCreated, 0, toCustomerGroupDTO(g), err)
}

func (h *Handler) updateGroup(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[customerGroupRequest](c)
	if !ok {
		return
	}
	g, err := h.svc.UpdateCustomerGroup(c.Request.Context(), actor, id, GroupInput(req))
	respond(c, http.StatusOK, 0, toCustomerGroupDTO(g), err)
}

func (h *Handler) deleteGroup(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteCustomerGroup(c.Request.Context(), actor, id))
}

func (h *Handler) listContacts(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Contacts(c.Request.Context(), scope, partyID)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toContactDTO)), err)
}

func (h *Handler) createContact(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[contactRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.CreateContact(c.Request.Context(), actor, partyID, ContactInput(req))
	respond(c, http.StatusCreated, 0, toContactDTO(v), err)
}

func (h *Handler) updateContact(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	id, ok := pathID(c, "contactId")
	if !ok {
		return
	}
	req, ok := bind[contactRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdateContact(c.Request.Context(), actor, partyID, id, ContactInput(req))
	respond(c, http.StatusOK, 0, toContactDTO(v), err)
}

func (h *Handler) deleteContact(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	id, ok := pathID(c, "contactId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteContact(c.Request.Context(), actor, partyID, id))
}

func (h *Handler) listAddresses(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Addresses(c.Request.Context(), scope, partyID)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toAddressDTO)), err)
}

func (h *Handler) createAddress(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[addressRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.CreateAddress(c.Request.Context(), actor, partyID, AddressInput(req))
	respond(c, http.StatusCreated, 0, toAddressDTO(v), err)
}

func (h *Handler) updateAddress(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	id, ok := pathID(c, "addressId")
	if !ok {
		return
	}
	req, ok := bind[addressRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdateAddress(c.Request.Context(), actor, partyID, id, AddressInput(req))
	respond(c, http.StatusOK, 0, toAddressDTO(v), err)
}

func (h *Handler) deleteAddress(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	partyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	id, ok := pathID(c, "addressId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeleteAddress(c.Request.Context(), actor, partyID, id))
}
