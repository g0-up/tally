package procurement

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

// Handler serves the Nhập hàng endpoints of tenant hosts: purchase orders (PO), receipts (PN),
// landed costs, supplier documents, purchase returns (XT), price adjustments (DG) and late orders.
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
	// (Idempotency-Key required; the coordinator checks the entitlement after the replay, so a
	// retry of a posting accepted before the tenant turned read-only still gets its response).
	Posting *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
}

// Register adds the procurement routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Posting == nil || r.Commands == nil || r.Reauth == nil {
		panic("procurement: Register needs the tenant and posting groups, the command router and the re-auth guard")
	}
	g, p := r.Tenant, r.Posting
	g.GET("/purchase-orders", h.listOrders)
	g.POST("/purchase-orders", h.createOrder)
	g.GET("/purchase-orders/:id", h.getOrder)
	g.PATCH("/purchase-orders/:id", h.updateOrder)
	g.DELETE("/purchase-orders/:id", h.deleteOrder)
	g.PUT("/purchase-orders/:id/lines", h.replaceLines)
	g.GET("/purchase-orders/:id/receivable-lines", h.receivableLines)
	orders := r.Commands(g, "purchase-orders")
	orders.Item("confirm", h.confirmOrder)
	orders.Item("close-short", h.closeShort)
	g.GET("/procurement/late-orders", h.lateOrders)

	g.GET("/receipts", h.listReceipts)
	p.POST("/receipts", h.postReceipt)
	g.GET("/receipts/:id", h.getReceipt)
	p.POST("/receipts/:id/landed-costs", h.postLandedCost)
	// Reversing a receipt takes back its stock and its payable: it needs re-authentication.
	r.Commands(p, "receipts").Item("reverse", guarded(r.Reauth, h.reverseReceipt))

	g.GET("/supplier-documents", h.listDocuments)
	g.POST("/supplier-documents", h.createDocument)
	g.GET("/supplier-documents/:id", h.getDocument)
	g.POST("/supplier-documents/:id/receipts", h.matchReceipts)

	g.GET("/purchase-returns", h.listReturns)
	p.POST("/purchase-returns", h.postReturn)
	g.GET("/purchase-returns/:id", h.getReturn)

	g.GET("/purchase-price-adjustments", h.listAdjustments)
	p.POST("/purchase-price-adjustments", h.postAdjustment)
	g.GET("/purchase-price-adjustments/:id", h.getAdjustment)
}

// guarded runs reauth before h on routes that share a dispatcher with unguarded verbs.
func guarded(reauth, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		reauth(c)
		if c.IsAborted() {
			return
		}
		h(c)
	}
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

// docFilter reads the filters shared by the supplier document, return and adjustment lists.
func docFilter(c *gin.Context, p httpx.Page) (DocFilter, bool) {
	f := DocFilter{Q: c.Query("q"), Status: c.Query("status"), Limit: p.FetchLimit()}
	var ok bool
	if f.SupplierID, ok = queryUUID(c, "supplier_id"); !ok {
		return f, false
	}
	if f.ReceiptID, ok = queryUUID(c, "receipt_id"); !ok {
		return f, false
	}
	if f.From, ok = queryDate(c, "from"); !ok {
		return f, false
	}
	if f.To, ok = queryDate(c, "to"); !ok {
		return f, false
	}
	if f.AfterAt, ok = afterTime(c, p); !ok {
		return f, false
	}
	if p.After != nil {
		f.AfterID = p.After.ID
	}
	return f, true
}

func render[T any](c *gin.Context, status int, v T, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(status, v)
}

// orderResponse writes a PO with its version as the ETag.
func orderResponse(c *gin.Context, status int, o OrderDetail, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, o.Version)
	c.JSON(status, toOrderDetailDTO(o))
}

// documentResponse writes a supplier document with its version as the ETag.
func documentResponse(c *gin.Context, status int, d DocumentDetail, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, d.Version)
	c.JSON(status, toDocumentDTO(d))
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

// Purchase orders.

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
	if f.SupplierID, ok = queryUUID(c, "supplier_id"); !ok {
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
	list := httpx.NewList(rows, p, func(r OrderRow) httpx.Cursor { return timeCursor(r.CreatedAt, r.ID) })
	c.JSON(http.StatusOK, httpx.List[orderDTO]{Data: mapSlice(list.Data, toOrderRowDTO), NextCursor: list.NextCursor})
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

func (h *Handler) deleteOrder(c *gin.Context) {
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
	if err := h.svc.DeleteOrder(c.Request.Context(), actor, id, version); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) replaceLines(c *gin.Context) {
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
	req, ok := bind[orderLinesRequest](c)
	if !ok {
		return
	}
	o, err := h.svc.ReplaceLines(c.Request.Context(), actor, id, version, mapSlice(req.Lines, orderLineRequest.input))
	orderResponse(c, http.StatusOK, o, err)
}

func (h *Handler) confirmOrder(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	o, err := h.svc.ConfirmOrder(c.Request.Context(), actor, httpx.CommandID(c), version)
	orderResponse(c, http.StatusOK, o, err)
}

func (h *Handler) closeShort(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[closeShortRequest](c)
	if !ok {
		return
	}
	o, err := h.svc.CloseShort(c.Request.Context(), actor, httpx.CommandID(c), version, CloseShortInput(req))
	orderResponse(c, http.StatusOK, o, err)
}

func (h *Handler) receivableLines(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	lines, err := h.svc.ReceivableLines(c.Request.Context(), scope, id)
	render(c, http.StatusOK, httpx.List[orderLineDTO]{Data: mapSlice(lines, toOrderLineDTO)}, err)
}

func (h *Handler) lateOrders(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := LateFilter{Limit: p.FetchLimit()}
	if f.SupplierID, ok = queryUUID(c, "supplier_id"); !ok {
		return
	}
	if p.After != nil {
		d, err := time.Parse(time.DateOnly, p.After.SortKey)
		if err != nil {
			httpx.RenderError(c, errCursor())
			return
		}
		f.AfterDate, f.AfterID = &d, p.After.ID
	}
	rows, err := h.svc.LateOrders(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r LateOrder) httpx.Cursor {
		// Late orders always have an expected date: that is what makes them late.
		return httpx.Cursor{SortKey: dateOf(*r.ExpectedDate), ID: r.ID}
	})
	c.JSON(http.StatusOK, httpx.List[orderDTO]{Data: mapSlice(list.Data, toLateOrderDTO), NextCursor: list.NextCursor})
}

// Receipts.

func (h *Handler) listReceipts(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := ReceiptFilter{Q: c.Query("q"), Status: c.Query("status"), Limit: p.FetchLimit()}
	if f.SupplierID, ok = queryUUID(c, "supplier_id"); !ok {
		return
	}
	if f.PurchaseOrderID, ok = queryUUID(c, "purchase_order_id"); !ok {
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
	rows, err := h.svc.Receipts(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r ReceiptRow) httpx.Cursor { return timeCursor(r.CreatedAt, r.ID) })
	c.JSON(http.StatusOK, httpx.List[receiptListItemDTO]{Data: mapSlice(list.Data, toReceiptListItemDTO), NextCursor: list.NextCursor})
}

func (h *Handler) postReceipt(c *gin.Context) {
	var req receiptRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.PostReceipt(c.Request.Context(), actor, key, req.input())
	})
}

func (h *Handler) getReceipt(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Receipt(c.Request.Context(), scope, id)
	render(c, http.StatusOK, toReceiptDTO(d), err)
}

func (h *Handler) reverseReceipt(c *gin.Context) {
	var req reverseRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.ReverseReceipt(c.Request.Context(), actor, key, httpx.CommandID(c), ReverseInput(req))
	})
}

func (h *Handler) postLandedCost(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req landedCostRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.PostLandedCost(c.Request.Context(), actor, key, id, req.input())
	})
}

// Supplier documents.

func (h *Handler) listDocuments(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f, ok := docFilter(c, p)
	if !ok {
		return
	}
	rows, err := h.svc.Documents(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(d SupplierDocument) httpx.Cursor { return timeCursor(d.CreatedAt, d.ID) })
	c.JSON(http.StatusOK, httpx.List[documentDTO]{Data: mapSlice(list.Data, toDocumentSummaryDTO), NextCursor: list.NextCursor})
}

func (h *Handler) createDocument(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, key, ok := bindKeyed[documentRequest](c)
	if !ok {
		return
	}
	d, replayed, err := h.svc.CreateDocument(c.Request.Context(), actor, key, req.input())
	httpx.MarkReplay(c, replayed)
	documentResponse(c, http.StatusCreated, d, err)
}

func (h *Handler) getDocument(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Document(c.Request.Context(), scope, id)
	documentResponse(c, http.StatusOK, d, err)
}

func (h *Handler) matchReceipts(c *gin.Context) {
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
	req, ok := bind[matchRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.MatchReceipts(c.Request.Context(), actor, id, version, req.ReceiptIDs)
	documentResponse(c, http.StatusOK, d, err)
}

// Purchase returns.

func (h *Handler) listReturns(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f, ok := docFilter(c, p)
	if !ok {
		return
	}
	rows, err := h.svc.Returns(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(x ReturnRow) httpx.Cursor { return timeCursor(x.CreatedAt, x.ID) })
	c.JSON(http.StatusOK, httpx.List[returnListItemDTO]{Data: mapSlice(list.Data, toReturnListItemDTO), NextCursor: list.NextCursor})
}

func (h *Handler) postReturn(c *gin.Context) {
	var req returnRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.PostReturn(c.Request.Context(), actor, key, req.input())
	})
}

func (h *Handler) getReturn(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Return(c.Request.Context(), scope, id)
	render(c, http.StatusOK, toReturnDTO(d), err)
}

// Price adjustments.

func (h *Handler) listAdjustments(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f, ok := docFilter(c, p)
	if !ok {
		return
	}
	rows, err := h.svc.Adjustments(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(a PriceAdjustment) httpx.Cursor { return timeCursor(a.CreatedAt, a.ID) })
	c.JSON(http.StatusOK, httpx.List[adjustmentDTO]{Data: mapSlice(list.Data, toAdjustmentSummaryDTO), NextCursor: list.NextCursor})
}

func (h *Handler) postAdjustment(c *gin.Context) {
	var req adjustmentRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key) (posting.Response, error) {
		return h.svc.PostAdjustment(c.Request.Context(), actor, key, req.input())
	})
}

func (h *Handler) getAdjustment(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Adjustment(c.Request.Context(), scope, id)
	render(c, http.StatusOK, toAdjustmentDTO(d), err)
}
