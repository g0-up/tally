package kits

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Handler serves the kits endpoints of tenant hosts: kit and combo recipes, assembly orders (LD)
// with their batches, disassemblies (TG), lot genealogy, labels, progress and budget suggestions.
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
	// Posting is the tenant-host group for endpoints that run through posting.Coordinator
	// (Idempotency-Key required; the coordinator checks the entitlement after the replay).
	Posting *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
}

// Register adds the kits routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Posting == nil || r.Commands == nil {
		panic("kits: Register needs the tenant and posting groups and the command router")
	}
	g, p := r.Tenant, r.Posting
	g.GET("/kits", h.listKits)
	g.POST("/kits", h.createKit)
	g.GET("/kits/progress", h.progress)
	g.GET("/kits/:id", h.getKit)
	g.PATCH("/kits/:id", h.updateKit)
	g.DELETE("/kits/:id", h.deleteKit)
	g.GET("/kits/:id/preview", h.kitPreview)
	g.POST("/kits/:id/recipes", h.newRecipe)
	g.GET("/kits/:id/recipes/:version", h.getRecipe)
	kits := r.Commands(g, "kits")
	kits.Item("publish", h.publishKit)
	kits.Collection("suggest", h.suggest)
	kits.Collection("preview", h.previewRecipe)

	g.GET("/assembly-orders", h.listOrders)
	g.POST("/assembly-orders", h.createOrder)
	r.Commands(g, "assembly-orders").Collection("preview", h.previewOrder)
	g.GET("/assembly-orders/:id", h.getOrder)
	g.PATCH("/assembly-orders/:id", h.updateOrder)
	g.GET("/assembly-orders/:id/label", h.label)
	orders := r.Commands(p, "assembly-orders")
	orders.Item("reserve", h.reserveOrder)
	orders.Item("close", h.closeOrder)
	p.POST("/assembly-orders/:id/substitutions", h.substitute)
	p.POST("/assembly-orders/:id/postings", h.postBatch)
	p.POST("/assembly-orders/:id/disassemblies", h.postDisassembly)

	g.GET("/disassemblies", h.listDisassemblies)
	g.GET("/lots/:id/genealogy", h.genealogy)
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

// bindKeyed is bind for a create that is safe to retry when the client sends an Idempotency-Key.
func bindKeyed[T any](c *gin.Context) (T, *idempotency.Key, bool) {
	var v T
	key, err := httpx.BindJSONKeyed(c, &v)
	if err != nil {
		httpx.RenderError(c, err)
		return v, nil, false
	}
	return v, key, true
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

func page(c *gin.Context) (httpx.Page, bool) {
	p, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return httpx.Page{}, false
	}
	return p, true
}

func errCursor() error {
	return apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
}

// afterTime reads the timestamp half of a newest-first cursor.
func afterTime(c *gin.Context, p httpx.Page) (*time.Time, bool) {
	if p.After == nil {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
	if err != nil {
		httpx.RenderError(c, errCursor())
		return nil, false
	}
	return &t, true
}

func timeCursor(t time.Time, id uuid.UUID) httpx.Cursor {
	return httpx.Cursor{SortKey: t.Format(time.RFC3339Nano), ID: id}
}

func queryUUID(c *gin.Context, name string) (*uuid.UUID, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return nil, false
	}
	return &id, true
}

func queryDate(c *gin.Context, name string) (*time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	var d date
	if err := d.UnmarshalJSON(strconv.AppendQuote(nil, raw)); err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid_date"}))
		return nil, false
	}
	return d.time(), true
}

func render[T, D any](c *gin.Context, status int, v T, err error, dto func(T) D) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(status, dto(v))
}

// kitResponse writes a kit with the version of its latest recipe as the ETag.
func kitResponse(c *gin.Context, status int, k Kit, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, k.Latest.Version)
	c.JSON(status, toKitDTO(k))
}

// orderResponse writes an assembly order with its version as the ETag.
func orderResponse(c *gin.Context, status int, o OrderDetail, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, o.Version)
	c.JSON(status, orderDTO(o))
}

// posted reads the body and the Idempotency-Key of a posting request, decodes the body into req
// and writes the coordinator's response of post.
func posted[R any](c *gin.Context, req *R, post func(actor audit.Actor, key idempotency.Key) (posting.Response, error)) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	body, err := httpx.ReadBody(c, 0)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	key, err := httpx.IdempotencyKey(c, body)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	if req != nil && len(body) > 0 {
		if err := httpx.DecodeJSON(body, req); err != nil {
			httpx.RenderError(c, err)
			return
		}
	}
	res, err := post(actor, key)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.WritePosted(c, res.Status, res.Body, res.Replayed)
}

// Kits and recipes.

func (h *Handler) listKits(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := KitFilter{Type: c.Query("type"), Status: c.Query("status"), Q: c.Query("q"), Limit: p.FetchLimit()}
	if p.After != nil {
		name := p.After.SortKey
		f.AfterName, f.AfterID = &name, p.After.ID
	}
	rows, err := h.svc.Kits(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r KitRow) httpx.Cursor { return httpx.Cursor{SortKey: r.Name, ID: r.ProductID} })
	c.JSON(http.StatusOK, httpx.List[kitRowDTO]{Data: mapSlice(list.Data, toKitRowDTO), NextCursor: list.NextCursor})
}

func (h *Handler) createKit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, key, ok := bindKeyed[kitRequest](c)
	if !ok {
		return
	}
	k, replayed, err := h.svc.CreateKit(c.Request.Context(), actor, key, req.input())
	httpx.MarkReplay(c, replayed)
	kitResponse(c, http.StatusCreated, k, err)
}

func (h *Handler) getKit(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	k, err := h.svc.Kit(c.Request.Context(), scope, id)
	kitResponse(c, http.StatusOK, k, err)
}

func (h *Handler) updateKit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[kitPatchRequest](c)
	if !ok {
		return
	}
	k, err := h.svc.UpdateKit(c.Request.Context(), actor, id, version, req.input())
	kitResponse(c, http.StatusOK, k, err)
}

func (h *Handler) deleteKit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	res, err := h.svc.DeleteKit(c.Request.Context(), actor, id, version)
	render(c, http.StatusOK, res, err, func(r DeleteResult) deleteDTO { return deleteDTO(r) })
}

func (h *Handler) kitPreview(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.KitPreview(c.Request.Context(), scope, id)
	render(c, http.StatusOK, v, err, toCostViewDTO)
}

func (h *Handler) newRecipe(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[recipeRequest](c)
	if !ok {
		return
	}
	k, err := h.svc.NewRecipeVersion(c.Request.Context(), actor, id, version, req.input())
	kitResponse(c, http.StatusCreated, k, err)
}

func (h *Handler) getRecipe(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version <= 0 {
		httpx.RenderError(c, apperr.NewNotFound("not_found", "Not found"))
		return
	}
	r, err := h.svc.RecipeVersion(c.Request.Context(), scope, id, version)
	render(c, http.StatusOK, r, err, func(r RecipeDetail) recipeDTO { return toRecipeDTO(r.Recipe, r.Items) })
}

func (h *Handler) publishKit(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	key, err := httpx.OptionalCommandKey(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	k, replayed, err := h.svc.PublishKit(c.Request.Context(), actor, key, httpx.CommandID(c), version)
	if err == nil {
		httpx.MarkReplay(c, replayed)
	}
	kitResponse(c, http.StatusOK, k, err)
}

func (h *Handler) previewRecipe(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	req, ok := bind[recipePreviewRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.PreviewRecipe(c.Request.Context(), scope, req.input(), req.KitPrice)
	render(c, http.StatusOK, v, err, toCostViewDTO)
}

func (h *Handler) suggest(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	req, ok := bind[suggestRequest](c)
	if !ok {
		return
	}
	s, err := h.svc.Suggest(c.Request.Context(), scope, req.input())
	render(c, http.StatusOK, s, err, toSuggestionDTO)
}

func (h *Handler) progress(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	rows, err := h.svc.Progress(c.Request.Context(), scope)
	render(c, http.StatusOK, rows, err, func(rows []Progress) httpx.List[progressDTO] {
		return httpx.List[progressDTO]{Data: mapSlice(rows, toProgressDTO)}
	})
}

// Assembly orders.

func (h *Handler) listOrders(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := OrderFilter{Status: c.Query("status"), Q: c.Query("q"), Limit: p.FetchLimit()}
	if f.ProductID, ok = queryUUID(c, "product_id"); !ok {
		return
	}
	if f.AfterAt, ok = afterTime(c, p); !ok {
		return
	}
	if p.After != nil {
		f.AfterID = p.After.ID
	}
	rows, err := h.svc.Orders(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(o AssemblyOrder) httpx.Cursor { return timeCursor(o.CreatedAt, o.ID) })
	c.JSON(http.StatusOK, httpx.List[orderRowDTO]{Data: mapSlice(list.Data, toOrderRowDTO), NextCursor: list.NextCursor})
}

func (h *Handler) createOrder(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, key, ok := bindKeyed[orderRequest](c)
	if !ok {
		return
	}
	o, replayed, err := h.svc.CreateOrder(c.Request.Context(), actor, key, req.input())
	httpx.MarkReplay(c, replayed)
	orderResponse(c, http.StatusCreated, o, err)
}

func (h *Handler) previewOrder(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	req, ok := bind[orderRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.PreviewOrder(c.Request.Context(), scope, req.input())
	render(c, http.StatusOK, v, err, toOrderPreviewDTO)
}

func (h *Handler) getOrder(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	o, err := h.svc.Order(c.Request.Context(), scope, id)
	orderResponse(c, http.StatusOK, o, err)
}

func (h *Handler) updateOrder(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[orderPatchRequest](c)
	if !ok {
		return
	}
	o, err := h.svc.UpdateOrder(c.Request.Context(), actor, id, version, req.input())
	orderResponse(c, http.StatusOK, o, err)
}

func (h *Handler) label(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	l, err := h.svc.Label(c.Request.Context(), scope, id)
	render(c, http.StatusOK, l, err, toLabelDTO)
}

func (h *Handler) reserveOrder(c *gin.Context) {
	id := httpx.CommandID(c)
	posted[struct{}](c, nil, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.ReserveOrder(c.Request.Context(), actor, key, id)
	})
}

func (h *Handler) closeOrder(c *gin.Context) {
	id := httpx.CommandID(c)
	posted[struct{}](c, nil, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.CloseOrder(c.Request.Context(), actor, key, id)
	})
}

func (h *Handler) substitute(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req substituteRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.Substitute(c.Request.Context(), actor, key, id, SubstituteInput(req))
	})
}

func (h *Handler) postBatch(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req batchRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.PostBatch(c.Request.Context(), actor, key, id, req.input())
	})
}

func (h *Handler) postDisassembly(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req disassemblyRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.PostDisassembly(c.Request.Context(), actor, key, id, req.input())
	})
}

// Disassemblies and traceability.

func (h *Handler) listDisassemblies(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := DisassemblyFilter{Q: c.Query("q"), Limit: p.FetchLimit()}
	if f.OrderID, ok = queryUUID(c, "order_id"); !ok {
		return
	}
	if f.From, ok = queryDate(c, "from"); !ok {
		return
	}
	if f.To, ok = queryDate(c, "to"); !ok {
		return
	}
	if f.AfterAt, ok = afterTime(c, p); !ok {
		return
	}
	if p.After != nil {
		f.AfterID = p.After.ID
	}
	rows, err := h.svc.Disassemblies(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(d Disassembly) httpx.Cursor { return timeCursor(d.CreatedAt, d.ID) })
	c.JSON(http.StatusOK, httpx.List[disassemblyOutDTO]{Data: mapSlice(list.Data, disassemblyDTO), NextCursor: list.NextCursor})
}

func (h *Handler) genealogy(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	g, err := h.svc.Genealogy(c.Request.Context(), scope, id)
	render(c, http.StatusOK, g, err, toGenealogyDTO)
}
