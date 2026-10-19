package sales

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Handler serves the Bán hàng endpoints of tenant hosts: quotes (BG), invoices (SO), stock outs
// (PX), sales documents (BH/HD), the POS with its carts (GH) and offline sync, sales returns
// (TH), exchanges (DH) and print payloads.
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
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
	// Entitled is the entitlement guard (middleware.Entitlement) for the writes that do not post
	// but share a command dispatcher with postings on the Posting group.
	Entitled gin.HandlerFunc
}

// Register adds the sales routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Posting == nil || r.Commands == nil || r.Reauth == nil || r.Entitled == nil {
		panic("sales: Register needs the tenant and posting groups, the command router, the re-auth and entitlement guards")
	}
	g, p := r.Tenant, r.Posting

	g.GET("/quotes", h.listQuotes)
	g.POST("/quotes", h.createQuote)
	g.GET("/quotes/:id", h.getQuote)
	g.PATCH("/quotes/:id", h.updateQuote)
	g.DELETE("/quotes/:id", h.deleteQuote)
	g.GET("/quotes/:id/history", h.quoteHistory)
	quotes := r.Commands(g, "quotes")
	quotes.Item("send", h.sendQuote)
	quotes.Item("accept", h.quoteAction(h.svc.AcceptQuote))
	quotes.Item("reject", h.quoteAction(h.svc.RejectQuote))
	quotes.Item("revise", h.reviseQuote)
	quotes.Item("copy", h.copyQuote)
	quotes.Item("convert", h.convertQuote)

	g.GET("/invoices", h.listInvoices)
	g.POST("/invoices", h.createInvoice)
	g.GET("/invoices/:id", h.getInvoice)
	g.PATCH("/invoices/:id", h.updateInvoice)
	g.DELETE("/invoices/:id", h.deleteInvoice)
	g.PUT("/invoices/:id/lines", h.replaceInvoiceLines)
	g.GET("/invoices/:id/versions", h.invoiceVersions)
	p.PUT("/invoices/:id/paid-amount", h.setPaidAmount)
	p.POST("/invoices/:id/stock-outs", h.postStockOut)
	invoices := r.Commands(p, "invoices")
	invoices.Item("check", h.checkInvoice)
	invoices.Item("confirm", h.confirmInvoice)
	invoices.Item("revise", h.reviseInvoice)
	invoices.Item("reserve-more", h.reserveMore)
	invoices.Item("cancel-remainder", h.cancelRemainder)
	invoices.Item("copy", guarded(r.Entitled, h.copyInvoice))

	g.GET("/stock-outs/:id", h.getStockOut)
	// Reversing a stock out or a sale takes back its stock and its receivable: it needs
	// re-authentication.
	r.Commands(p, "stock-outs").Item("reverse", guarded(r.Reauth, h.reverseStockOut))

	g.GET("/sales-documents", h.listDocuments)
	g.GET("/sales-documents/:id", h.getDocument)
	g.GET("/sales-documents/:id/returnable-lines", h.returnableLines)
	r.Commands(p, "sales-documents").Item("reverse", guarded(r.Reauth, h.reverseDocument))

	g.GET("/pos/bootstrap", h.bootstrap)
	p.POST("/pos/checkout", h.checkout)
	g.GET("/pos/carts", h.listCarts)
	g.POST("/pos/carts", h.createCart)
	g.GET("/pos/carts/:id", h.getCart)
	g.PATCH("/pos/carts/:id", h.updateCart)
	g.DELETE("/pos/carts/:id", h.deleteCart)
	carts := r.Commands(p, "pos/carts")
	carts.Item("copy", guarded(r.Entitled, h.copyCart))
	carts.Item("checkout", h.checkoutCart)
	g.POST("/pos/offline-session", h.grantOfflineSession)
	// A sync posts many sales without one posting of its own: a read-only subscription refuses
	// the whole batch rather than parking every sale in it.
	p.POST("/pos/offline-sync", guarded(r.Entitled, h.syncOffline))
	g.GET("/pos/sync-exceptions", h.listSyncExceptions)
	syncExceptions := r.Commands(p, "pos/sync-exceptions")
	syncExceptions.Item("resolve", h.resolveSyncException)
	syncExceptions.Item("discard", guarded(r.Entitled, h.discardSyncException))

	g.GET("/sales-returns", h.listReturns)
	p.POST("/sales-returns", h.postReturn)

	g.GET("/exchanges", h.listExchanges)
	p.POST("/exchanges", h.postExchange)
	g.GET("/exchanges/:id", h.getExchange)
	// A preview only reads, so a read-only subscription may still work an exchange out.
	r.Commands(p, "exchanges").Collection("preview", h.previewExchange)
}

// guarded runs guard before h on routes that share a dispatcher with unguarded verbs.
func guarded(guard, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		guard(c)
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

func render[T any](c *gin.Context, status int, v T, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(status, v)
}

// versioned writes a draft document with its version as the ETag.
func versioned[T any](c *gin.Context, status int, v T, version func(T) int, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, version(v))
	c.JSON(status, v)
}

func quoteVersion(q Quote) int     { return q.Version }
func invoiceVersion(i Invoice) int { return i.Version }
func cartVersion(c Cart) int       { return c.Version }

// list writes one page of rows with the cursor of the row after it.
func list[T any](c *gin.Context, p httpx.Page, rows []T, cursorOf func(T) httpx.Cursor, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewList(rows, p, cursorOf))
}

// all writes rows that are never paged.
func all[T any](c *gin.Context, rows []T, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	if rows == nil {
		rows = []T{}
	}
	c.JSON(http.StatusOK, httpx.List[T]{Data: rows})
}

// posted reads the body and the Idempotency-Key of a posting request, decodes a non-empty body
// into req and writes the coordinator's response of post, which also gets the raw body.
func posted[R any](c *gin.Context, req *R, post func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error)) {
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
	res, err := post(actor, key, body)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.WritePosted(c, res.Status, res.Body, res.Replayed)
}

// Quotes.

func (h *Handler) listQuotes(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	f := QuoteFilter{Status: c.Query("status"), Page: lp}
	if f.CustomerID, ok = queryUUID(c, "customer_id"); !ok {
		return
	}
	if f.From, f.To, ok = dateRange(c); !ok {
		return
	}
	rows, err := h.svc.Quotes(c.Request.Context(), scope, f)
	list(c, p, rows, func(q Quote) httpx.Cursor { return timeCursor(q.CreatedAt, q.ID) }, err)
}

func (h *Handler) createQuote(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	in, key, ok := bindKeyed[QuoteInput](c)
	if !ok {
		return
	}
	q, replayed, err := h.svc.CreateQuote(c.Request.Context(), actor, key, in)
	httpx.MarkReplay(c, replayed)
	versioned(c, http.StatusCreated, q, quoteVersion, err)
}

func (h *Handler) getQuote(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	q, err := h.svc.Quote(c.Request.Context(), scope, id)
	versioned(c, http.StatusOK, q, quoteVersion, err)
}

func (h *Handler) updateQuote(c *gin.Context) {
	h.patchQuote(c, pathParam, h.svc.UpdateQuote)
}

func (h *Handler) reviseQuote(c *gin.Context) {
	h.patchQuote(c, commandParam, h.svc.ReviseQuote)
}

type quotePatchFunc func(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in QuotePatch) (Quote, error)

func (h *Handler) patchQuote(c *gin.Context, idOf func(*gin.Context) (uuid.UUID, bool), fn quotePatchFunc) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := idOf(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	in, ok := bind[QuotePatch](c)
	if !ok {
		return
	}
	q, err := fn(c.Request.Context(), actor, id, version, in)
	versioned(c, http.StatusOK, q, quoteVersion, err)
}

func (h *Handler) deleteQuote(c *gin.Context) {
	h.deleteVersioned(c, h.svc.DeleteQuote)
}

// quoteAction serves a status change of a quote at its If-Match version.
func (h *Handler) quoteAction(fn func(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Quote, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := actorFor(c)
		if !ok {
			return
		}
		version, ok := ifMatch(c)
		if !ok {
			return
		}
		q, err := fn(c.Request.Context(), actor, httpx.CommandID(c), version)
		versioned(c, http.StatusOK, q, quoteVersion, err)
	}
}

func (h *Handler) sendQuote(c *gin.Context) {
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
	q, replayed, err := h.svc.SendQuote(c.Request.Context(), actor, key, httpx.CommandID(c), version)
	if err == nil {
		httpx.MarkReplay(c, replayed)
	}
	versioned(c, http.StatusOK, q, quoteVersion, err)
}

func (h *Handler) copyQuote(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	q, err := h.svc.CopyQuote(c.Request.Context(), actor, httpx.CommandID(c))
	versioned(c, http.StatusCreated, q, quoteVersion, err)
}

func (h *Handler) convertQuote(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	inv, err := h.svc.ConvertQuote(c.Request.Context(), actor, httpx.CommandID(c), version)
	versioned(c, http.StatusCreated, inv, invoiceVersion, err)
}

func (h *Handler) quoteHistory(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	rows, err := h.svc.QuoteHistory(c.Request.Context(), scope, id)
	all(c, rows, err)
}

// Invoices.

func (h *Handler) listInvoices(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	f := InvoiceFilter{Status: c.Query("status"), Page: lp}
	if f.CustomerID, ok = queryUUID(c, "customer_id"); !ok {
		return
	}
	if f.From, f.To, ok = dateRange(c); !ok {
		return
	}
	rows, err := h.svc.Invoices(c.Request.Context(), scope, f)
	list(c, p, rows, func(i Invoice) httpx.Cursor { return timeCursor(i.CreatedAt, i.ID) }, err)
}

func (h *Handler) createInvoice(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	in, key, ok := bindKeyed[InvoiceInput](c)
	if !ok {
		return
	}
	inv, replayed, err := h.svc.CreateInvoice(c.Request.Context(), actor, key, in)
	httpx.MarkReplay(c, replayed)
	versioned(c, http.StatusCreated, inv, invoiceVersion, err)
}

func (h *Handler) getInvoice(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	inv, err := h.svc.Invoice(c.Request.Context(), scope, id)
	versioned(c, http.StatusOK, inv, invoiceVersion, err)
}

func (h *Handler) updateInvoice(c *gin.Context) {
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
	in, ok := bind[InvoicePatch](c)
	if !ok {
		return
	}
	inv, err := h.svc.UpdateInvoice(c.Request.Context(), actor, id, version, in)
	versioned(c, http.StatusOK, inv, invoiceVersion, err)
}

func (h *Handler) replaceInvoiceLines(c *gin.Context) {
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
	in, ok := bind[DraftInput](c)
	if !ok {
		return
	}
	inv, err := h.svc.ReplaceInvoiceLines(c.Request.Context(), actor, id, version, in)
	versioned(c, http.StatusOK, inv, invoiceVersion, err)
}

func (h *Handler) deleteInvoice(c *gin.Context) {
	h.deleteVersioned(c, h.svc.DeleteInvoice)
}

func (h *Handler) copyInvoice(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	inv, err := h.svc.CopyInvoice(c.Request.Context(), actor, httpx.CommandID(c))
	versioned(c, http.StatusCreated, inv, invoiceVersion, err)
}

func (h *Handler) invoiceVersions(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	rows, err := h.svc.InvoiceVersions(c.Request.Context(), scope, id)
	all(c, rows, err)
}

func (h *Handler) setPaidAmount(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req PaidAmountInput
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.SetPaidAmount(c.Request.Context(), actor, key, id, req)
	})
}

func (h *Handler) checkInvoice(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.CheckInvoice(c.Request.Context(), scope, httpx.CommandID(c))
	render(c, http.StatusOK, out, err)
}

func (h *Handler) confirmInvoice(c *gin.Context) {
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	var req confirmRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		exc, err := req.exceptions(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.ConfirmInvoice(c.Request.Context(), actor, key, httpx.CommandID(c), version, exc)
	})
}

func (h *Handler) reviseInvoice(c *gin.Context) {
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	var req reviseRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		exc, err := req.exceptions(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.ReviseInvoice(c.Request.Context(), actor, key, httpx.CommandID(c), version, req.ReviseInput, exc)
	})
}

func (h *Handler) reserveMore(c *gin.Context) {
	h.invoicePosting(c, h.svc.ReserveMore)
}

func (h *Handler) cancelRemainder(c *gin.Context) {
	h.invoicePosting(c, h.svc.CancelRemainder)
}

// invoicePosting serves a bodiless posting on an invoice at its If-Match version.
func (h *Handler) invoicePosting(c *gin.Context,
	fn func(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, version int) (posting.Response, error),
) {
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	posted[struct{}](c, nil, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return fn(c.Request.Context(), actor, key, httpx.CommandID(c), version)
	})
}

// Stock outs and sales documents.

func (h *Handler) postStockOut(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req StockOutInput
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.PostStockOut(c.Request.Context(), actor, key, id, req)
	})
}

func (h *Handler) getStockOut(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.StockOut(c.Request.Context(), scope, id)
	render(c, http.StatusOK, out, err)
}

func (h *Handler) reverseStockOut(c *gin.Context) {
	var req ReverseInput
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.ReverseStockOut(c.Request.Context(), actor, key, httpx.CommandID(c), req)
	})
}

func (h *Handler) listDocuments(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	f := DocumentFilter{Source: c.Query("source"), Status: c.Query("status"), Page: lp}
	if f.CustomerID, ok = queryUUID(c, "customer_id"); !ok {
		return
	}
	if f.InvoiceID, ok = queryUUID(c, "invoice_id"); !ok {
		return
	}
	if f.ShiftID, ok = queryUUID(c, "shift_id"); !ok {
		return
	}
	if f.From, f.To, ok = dateRange(c); !ok {
		return
	}
	rows, err := h.svc.SalesDocuments(c.Request.Context(), scope, f)
	list(c, p, rows, func(d SalesDocument) httpx.Cursor { return timeCursor(d.PostedAt, d.ID) }, err)
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
	out, err := h.svc.SalesDocument(c.Request.Context(), scope, id)
	render(c, http.StatusOK, out, err)
}

func (h *Handler) returnableLines(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	rows, err := h.svc.ReturnableLines(c.Request.Context(), scope, id)
	all(c, rows, err)
}

func (h *Handler) reverseDocument(c *gin.Context) {
	var req ReverseInput
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.ReverseDocument(c.Request.Context(), actor, key, httpx.CommandID(c), req)
	})
}

// POS.

func (h *Handler) bootstrap(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	out, err := h.svc.Bootstrap(c.Request.Context(), scope)
	render(c, http.StatusOK, out, err)
}

func (h *Handler) checkout(c *gin.Context) {
	var req checkoutRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		exc, err := req.exceptions(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.Checkout(c.Request.Context(), actor, key, req.CheckoutInput, exc)
	})
}

func (h *Handler) listCarts(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	rows, err := h.svc.Carts(c.Request.Context(), scope, CartFilter{Status: c.Query("status"), Page: lp})
	list(c, p, rows, func(x Cart) httpx.Cursor { return timeCursor(x.CreatedAt, x.ID) }, err)
}

func (h *Handler) createCart(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	in, ok := bind[CartInput](c)
	if !ok {
		return
	}
	out, err := h.svc.CreateCart(c.Request.Context(), actor, in)
	versioned(c, http.StatusCreated, out, cartVersion, err)
}

func (h *Handler) getCart(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Cart(c.Request.Context(), scope, id)
	versioned(c, http.StatusOK, out, cartVersion, err)
}

func (h *Handler) updateCart(c *gin.Context) {
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
	in, ok := bind[CartInput](c)
	if !ok {
		return
	}
	out, err := h.svc.UpdateCart(c.Request.Context(), actor, id, version, in)
	versioned(c, http.StatusOK, out, cartVersion, err)
}

func (h *Handler) deleteCart(c *gin.Context) {
	h.deleteVersioned(c, h.svc.DeleteCart)
}

func (h *Handler) copyCart(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	out, err := h.svc.CopyCart(c.Request.Context(), actor, httpx.CommandID(c))
	versioned(c, http.StatusCreated, out, cartVersion, err)
}

func (h *Handler) checkoutCart(c *gin.Context) {
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	var req cartCheckoutRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		exc, err := req.exceptions(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.CheckoutCart(c.Request.Context(), actor, key, httpx.CommandID(c), version, req.CartCheckoutInput, exc)
	})
}

// sessionFor is the login session of the request, which an offline window is granted to.
func sessionFor(c *gin.Context) (uuid.UUID, bool) {
	p, ok := middleware.PrincipalOf(c)
	if !ok || p.SessionID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return uuid.Nil, false
	}
	return p.SessionID, true
}

func (h *Handler) grantOfflineSession(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	session, ok := sessionFor(c)
	if !ok {
		return
	}
	out, err := h.svc.GrantOfflineSession(c.Request.Context(), actor, session)
	render(c, http.StatusOK, out, err)
}

// syncOffline posts a batch of offline sales. Each sale is idempotent by its offline client id,
// so the batch needs no Idempotency-Key: a device sends it again until every sale is settled.
func (h *Handler) syncOffline(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	session, ok := sessionFor(c)
	if !ok {
		return
	}
	in, ok := bind[OfflineSyncInput](c)
	if !ok {
		return
	}
	out, err := h.svc.SyncOffline(c.Request.Context(), actor, session, in)
	render(c, http.StatusOK, out, err)
}

func (h *Handler) listSyncExceptions(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	rows, err := h.svc.SyncExceptions(c.Request.Context(), scope, SyncExceptionFilter{Status: c.Query("status"), Page: lp})
	list(c, p, rows, func(x SyncException) httpx.Cursor { return timeCursor(x.CreatedAt, x.ID) }, err)
}

func (h *Handler) resolveSyncException(c *gin.Context) {
	var req resolveRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		exc, err := req.exceptions(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.ResolveSyncException(c.Request.Context(), actor, key, httpx.CommandID(c), req.ResolveInput, exc)
	})
}

func (h *Handler) discardSyncException(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	out, err := h.svc.DiscardSyncException(c.Request.Context(), actor, httpx.CommandID(c))
	render(c, http.StatusOK, out, err)
}

// Returns and exchanges.

func (h *Handler) listReturns(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	f := ReturnFilter{Page: lp}
	if f.SalesDocumentID, ok = queryUUID(c, "sales_document_id"); !ok {
		return
	}
	if f.CustomerID, ok = queryUUID(c, "customer_id"); !ok {
		return
	}
	rows, err := h.svc.SalesReturns(c.Request.Context(), scope, f)
	list(c, p, rows, func(r SalesReturn) httpx.Cursor { return timeCursor(r.PostedAt, r.ID) }, err)
}

func (h *Handler) postReturn(c *gin.Context) {
	var req ReturnInput
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.PostReturn(c.Request.Context(), actor, key, req)
	})
}

func (h *Handler) listExchanges(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, lp, ok := listPage(c)
	if !ok {
		return
	}
	f := ExchangeFilter{Page: lp}
	if f.OriginalDocumentID, ok = queryUUID(c, "original_sales_document_id"); !ok {
		return
	}
	if f.CustomerID, ok = queryUUID(c, "customer_id"); !ok {
		return
	}
	rows, err := h.svc.Exchanges(c.Request.Context(), scope, f)
	list(c, p, rows, func(x Exchange) httpx.Cursor { return timeCursor(x.CreatedAt, x.ID) }, err)
}

func (h *Handler) getExchange(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.Exchange(c.Request.Context(), scope, id)
	render(c, http.StatusOK, out, err)
}

func (h *Handler) previewExchange(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	in, ok := bind[ExchangeInput](c)
	if !ok {
		return
	}
	out, err := h.svc.PreviewExchange(c.Request.Context(), scope, in)
	render(c, http.StatusOK, out, err)
}

func (h *Handler) postExchange(c *gin.Context) {
	var req exchangeRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		exc, err := req.exceptions(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.PostExchange(c.Request.Context(), actor, key, req.ExchangeInput, exc)
	})
}

// Shared shapes.

// pathParam reads the :id path parameter; commandParam the id of an item verb.
func pathParam(c *gin.Context) (uuid.UUID, bool) { return pathID(c, "id") }

func commandParam(c *gin.Context) (uuid.UUID, bool) { return httpx.CommandID(c), true }

// deleteVersioned serves the DELETE of a draft at its If-Match version.
func (h *Handler) deleteVersioned(c *gin.Context, fn func(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) error) {
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
	if err := fn(c.Request.Context(), actor, id, version); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
