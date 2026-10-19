package pricing

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
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Promotion code checks per client IP: a steady one per second with room for a busy counter, so
// guessing codes stays slow.
const (
	codeCheckPerSecond = 1.0
	codeCheckBurst     = 30
)

// Handler serves the pricing endpoints of tenant hosts.
type Handler struct {
	svc         *Service
	codeLimiter *middleware.RateLimiter
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, codeLimiter: middleware.NewRateLimiter(codeCheckPerSecond, codeCheckBurst)}
}

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant-host group behind session, host guard, CSRF, entitlement and support
	// read-only.
	Tenant *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
}

// Register adds the pricing routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil {
		panic("pricing: Register needs the tenant group and the command router")
	}
	g := r.Tenant
	g.GET("/price-lists", h.listPriceLists)
	g.POST("/price-lists", h.createPriceList)
	g.GET("/price-lists/:id", h.getPriceList)
	g.PATCH("/price-lists/:id", h.updatePriceList)
	g.DELETE("/price-lists/:id", h.deletePriceList)
	g.GET("/price-lists/:id/items", h.listPriceListItems)
	g.PUT("/price-lists/:id/items", h.putPriceListItems)
	g.DELETE("/price-lists/:id/items/:itemId", h.deletePriceListItem)

	g.POST("/skus/:id/sale-price", h.setSalePrice)
	g.GET("/skus/:id/price-history", h.priceHistory)
	g.GET("/price-changes", h.priceChanges)

	g.POST("/pricing/quote", h.quote)
	g.POST("/pricing/calculate", h.calculate)

	g.GET("/promotions", h.listPromotions)
	g.POST("/promotions", h.createPromotion)
	g.GET("/promotions/validate-code", middleware.RateLimitByIP(h.codeLimiter), h.validateCode)
	g.GET("/promotions/:id", h.getPromotion)
	g.PATCH("/promotions/:id", h.updatePromotion)
	promo := r.Commands(g, "promotions")
	promo.Item("activate", h.setPromotionStatus(PromoActive))
	promo.Item("deactivate", h.setPromotionStatus(PromoInactive))
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

// bindKeyed is bind for a write that is safe to retry when the client sends an Idempotency-Key.
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

// afterTime reads the created_at half of a newest-first cursor.
func afterTime(c *gin.Context, p httpx.Page) (*time.Time, bool) {
	if p.After == nil {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
	if err != nil {
		httpx.RenderError(c, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"}))
		return nil, false
	}
	return &t, true
}

func timeCursor(t time.Time, id uuid.UUID) httpx.Cursor {
	return httpx.Cursor{SortKey: t.UTC().Format(time.RFC3339Nano), ID: id}
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

func (h *Handler) listPriceLists(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	active, ok := queryBool(c, "active")
	if !ok {
		return
	}
	out, err := h.svc.PriceLists(c.Request.Context(), scope, PriceListFilter{Kind: c.Query("kind"), ActiveOnly: active})
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toPriceListDTO)), err)
}

func (h *Handler) getPriceList(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.PriceList(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, v.Version, toPriceListDTO(v), err)
}

func (h *Handler) createPriceList(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[priceListRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.CreatePriceList(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, v.Version, toPriceListDTO(v), err)
}

func (h *Handler) updatePriceList(c *gin.Context) {
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
	req, ok := bind[priceListRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdatePriceList(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, v.Version, toPriceListDTO(v), err)
}

func (h *Handler) deletePriceList(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	noContent(c, h.svc.DeletePriceList(c.Request.Context(), actor, id))
}

func (h *Handler) listPriceListItems(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	pg, ok := page(c)
	if !ok {
		return
	}
	sku, ok := queryUUID(c, "sku_id")
	if !ok {
		return
	}
	f := ItemFilter{SkuID: sku, Limit: pg.FetchLimit()}
	if pg.After != nil {
		f.AfterKey, f.AfterID = &pg.After.SortKey, pg.After.ID
	}
	rows, err := h.svc.PriceListItems(c.Request.Context(), scope, id, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, pg, func(v PriceListItem) httpx.Cursor { return httpx.Cursor{SortKey: v.SkuCode, ID: v.ID} })
	c.JSON(http.StatusOK, httpx.List[priceListItemDTO]{Data: mapSlice(list.Data, toPriceListItemDTO), NextCursor: list.NextCursor})
}

func (h *Handler) putPriceListItems(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[priceListItemsRequest](c)
	if !ok {
		return
	}
	out, err := h.svc.PutPriceListItems(c.Request.Context(), actor, id, req.input())
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toPriceListItemDTO)), err)
}

func (h *Handler) deletePriceListItem(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	itemID, ok := pathID(c, "itemId")
	if !ok {
		return
	}
	noContent(c, h.svc.DeletePriceListItem(c.Request.Context(), actor, id, itemID, c.Query("source")))
}

func (h *Handler) setSalePrice(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, key, ok := bindKeyed[salePriceRequest](c)
	if !ok {
		return
	}
	if !req.Price.Set {
		httpx.RenderError(c, errInvalid(map[string]string{"price": "required"}))
		return
	}
	out, replayed, err := h.svc.SetSalePrice(c.Request.Context(), actor, key, req.command(id))
	httpx.MarkReplay(c, replayed)
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toPriceChangeDTO)), err)
}

func (h *Handler) priceHistory(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	h.historyPage(c, func(f HistoryFilter) ([]PriceChange, error) {
		return h.svc.PriceHistory(c.Request.Context(), scope, id, f)
	})
}

func (h *Handler) priceChanges(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	h.historyPage(c, func(f HistoryFilter) ([]PriceChange, error) {
		return h.svc.PriceChanges(c.Request.Context(), scope, f)
	})
}

// historyPage reads the page cursor, runs read and renders one page of price history.
func (h *Handler) historyPage(c *gin.Context, read func(HistoryFilter) ([]PriceChange, error)) {
	pg, ok := page(c)
	if !ok {
		return
	}
	after, ok := afterTime(c, pg)
	if !ok {
		return
	}
	f := HistoryFilter{AfterTime: after, Limit: pg.FetchLimit()}
	if pg.After != nil {
		f.AfterID = pg.After.ID
	}
	rows, err := read(f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, pg, func(v PriceChange) httpx.Cursor { return timeCursor(v.CreatedAt, v.ID) })
	c.JSON(http.StatusOK, httpx.List[priceChangeDTO]{Data: mapSlice(list.Data, toPriceChangeDTO), NextCursor: list.NextCursor})
}

func (h *Handler) quote(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	req, ok := bind[quoteRequest](c)
	if !ok {
		return
	}
	out, err := h.svc.Quote(c.Request.Context(), scope, req.input())
	respond(c, http.StatusOK, 0, listOf(mapSlice(out, toResolvedPriceDTO)), err)
}

func (h *Handler) calculate(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	req, ok := bind[calculateRequest](c)
	if !ok {
		return
	}
	out, err := h.svc.Calculate(c.Request.Context(), scope, req.draft())
	respond(c, http.StatusOK, 0, toCalculatedDocumentDTO(out), err)
}

func (h *Handler) listPromotions(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	pg, ok := page(c)
	if !ok {
		return
	}
	after, ok := afterTime(c, pg)
	if !ok {
		return
	}
	f := PromotionFilter{Status: c.Query("status"), Q: c.Query("q"), AfterTime: after, Limit: pg.FetchLimit()}
	if pg.After != nil {
		f.AfterID = pg.After.ID
	}
	rows, err := h.svc.Promotions(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, pg, func(v Promotion) httpx.Cursor { return timeCursor(v.CreatedAt, v.ID) })
	c.JSON(http.StatusOK, httpx.List[promotionDTO]{Data: mapSlice(list.Data, toPromotionDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getPromotion(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.Promotion(c.Request.Context(), scope, id)
	respond(c, http.StatusOK, v.Version, toPromotionDTO(v), err)
}

func (h *Handler) createPromotion(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[promotionRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.CreatePromotion(c.Request.Context(), actor, req.input())
	respond(c, http.StatusCreated, v.Version, toPromotionDTO(v), err)
}

func (h *Handler) updatePromotion(c *gin.Context) {
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
	req, ok := bind[promotionRequest](c)
	if !ok {
		return
	}
	v, err := h.svc.UpdatePromotion(c.Request.Context(), actor, id, expected, req.input())
	respond(c, http.StatusOK, v.Version, toPromotionDTO(v), err)
}

func (h *Handler) setPromotionStatus(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := actorFor(c)
		if !ok {
			return
		}
		v, err := h.svc.SetPromotionStatus(c.Request.Context(), actor, httpx.CommandID(c), status)
		respond(c, http.StatusOK, v.Version, toPromotionDTO(v), err)
	}
}

func (h *Handler) validateCode(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	party, ok := queryUUID(c, "party_id")
	if !ok {
		return
	}
	group, ok := queryUUID(c, "customer_group_id")
	if !ok {
		return
	}
	day, ok := queryDate(c, "business_date")
	if !ok {
		return
	}
	out, err := h.svc.ValidateCode(c.Request.Context(), scope, CodeCheckInput{
		Code: c.Query("code"), PartyID: party, CustomerGroupID: group, BusinessDate: day,
	})
	respond(c, http.StatusOK, 0, toCodeCheckDTO(out), err)
}
