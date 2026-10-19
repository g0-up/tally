package inventory

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Handler serves the Kho endpoints of tenant hosts: the stock list, stock card and lots,
// reservations, stock adjustments (DC), stock counts (KK) and backdate cost recalculations.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant-host group behind session, host guard, CSRF, entitlement and support
	// read-only. Stock postings run on it too: the entitlement is checked before the coordinator.
	Tenant *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
}

// Register adds the inventory routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil || r.Reauth == nil {
		panic("inventory: Register needs the tenant group, the command router and the re-auth guard")
	}
	g := r.Tenant
	g.GET("/stock", h.listStock)
	g.GET("/stock/:skuId", h.getStock)
	g.GET("/stock-card", h.stockCard)
	g.GET("/lots", h.listLots)
	g.GET("/lots/:id/trace", h.traceLot)

	g.GET("/reservations", h.listReservations)
	g.POST("/reservations", h.createReservation)
	r.Commands(g, "reservations").Item("release", h.releaseReservation)

	g.GET("/stock-adjustments", h.listAdjustments)
	// Disposal and quarantine release need re-authentication; the kind is only known once the
	// body is read.
	g.POST("/stock-adjustments", h.postAdjustment(r.Reauth))
	g.GET("/stock-adjustments/:id", h.getAdjustment)
	r.Commands(g, "stock-adjustments").Item("reverse", guarded(r.Reauth, h.reverseAdjustment))

	g.GET("/stock-counts", h.listCounts)
	g.POST("/stock-counts", h.startCount)
	g.GET("/stock-counts/:id", h.getCount)
	g.PUT("/stock-counts/:id/lines", h.putCountLines)
	counts := r.Commands(g, "stock-counts")
	counts.Item("finish", h.finishCount)
	counts.Item("recount", h.recountCount)
	counts.Item("post", h.postCount)

	g.GET("/cost-recalculations", h.listRecosts)
	g.POST("/cost-recalculations", h.previewRecost)
	g.GET("/cost-recalculations/:id", h.getRecost)
	recosts := r.Commands(g, "cost-recalculations")
	recosts.Item("confirm", guarded(r.Reauth, h.confirmRecost))
	recosts.Item("discard", h.discardRecost)
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

// moveCursor reads a cursor in posting order ("business date|posted at").
func moveCursor(c *gin.Context, p httpx.Page) (*MoveCursor, bool) {
	if p.After == nil {
		return nil, true
	}
	day, at, ok := strings.Cut(p.After.SortKey, "|")
	d, err1 := time.Parse(time.DateOnly, day)
	t, err2 := time.Parse(time.RFC3339Nano, at)
	if !ok || err1 != nil || err2 != nil {
		httpx.RenderError(c, errCursor())
		return nil, false
	}
	return &MoveCursor{BusinessDate: d, PostedAt: t, ID: p.After.ID}, true
}

func moveCursorOf(m TraceMove) httpx.Cursor {
	return httpx.Cursor{SortKey: dateOf(m.BusinessDate) + "|" + m.PostedAt.Format(time.RFC3339Nano), ID: m.ID}
}

// nextMoves trims moves read with Page.FetchLimit to the page and returns the next cursor.
func nextMoves[T any](rows []T, p httpx.Page, cursorOf func(T) httpx.Cursor) ([]T, *string) {
	if len(rows) <= p.Limit {
		return rows, nil
	}
	rows = rows[:p.Limit]
	next := cursorOf(rows[len(rows)-1]).Encode()
	return rows, &next
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

func queryInt(c *gin.Context, name string) (*int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return nil, false
	}
	return &v, true
}

func render[T any](c *gin.Context, status int, v T, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(status, v)
}

// errAborted stops a posting whose guard already answered the request.
var errAborted = errors.New("inventory: request aborted by a guard")

// posted reads the body and the Idempotency-Key of a posting request, decodes the body into req
// and writes the coordinator's response of post.
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
		if !c.IsAborted() {
			httpx.RenderError(c, err)
		}
		return
	}
	httpx.WritePosted(c, res.Status, res.Body, res.Replayed)
}

// payloadHash is the hash an exception confirmation binds to (an empty body hashes as {}).
func payloadHash(body []byte) ([]byte, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	return exception.PayloadHash(body)
}

// Stock.

func (h *Handler) listStock(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := StockFilter{Q: c.Query("q"), Limit: p.FetchLimit()}
	if f.CategoryID, ok = queryUUID(c, "category_id"); !ok {
		return
	}
	if f.ColorID, ok = queryUUID(c, "color_id"); !ok {
		return
	}
	if f.ColoredOnly, ok = queryBool(c, "colored_only"); !ok {
		return
	}
	if f.Uncolored, ok = queryBool(c, "uncolored"); !ok {
		return
	}
	if f.BelowMin, ok = queryBool(c, "below_min"); !ok {
		return
	}
	if p.After != nil {
		f.AfterCode, f.AfterID = &p.After.SortKey, p.After.ID
	}
	rows, err := h.svc.Stock(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r StockRow) httpx.Cursor { return httpx.Cursor{SortKey: r.Code, ID: r.SkuID} })
	c.JSON(http.StatusOK, httpx.List[stockRowDTO]{Data: mapSlice(list.Data, toStockRowDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getStock(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "skuId")
	if !ok {
		return
	}
	d, err := h.svc.StockOf(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStockDetailDTO(d))
}

func (h *Handler) stockCard(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := CardFilter{UnitMode: c.Query("unit_mode"), Limit: p.FetchLimit()}
	sku, ok := queryUUID(c, "sku_id")
	if !ok {
		return
	}
	if sku == nil {
		httpx.RenderError(c, errInvalid(map[string]string{"sku_id": "required"}))
		return
	}
	f.SkuID = *sku
	if f.ColorID, ok = queryUUID(c, "color_id"); !ok {
		return
	}
	if f.Uncolored, ok = queryBool(c, "uncolored"); !ok {
		return
	}
	if f.From, ok = queryDate(c, "from"); !ok {
		return
	}
	if f.To, ok = queryDate(c, "to"); !ok {
		return
	}
	if f.UnitID, ok = queryUUID(c, "unit_id"); !ok {
		return
	}
	if f.After, ok = moveCursor(c, p); !ok {
		return
	}
	card, err := h.svc.StockCardOf(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var next *string
	card.Rows, next = nextMoves(card.Rows, p, func(r CardRow) httpx.Cursor { return moveCursorOf(r.TraceMove) })
	c.JSON(http.StatusOK, toStockCardDTO(card, next))
}

func (h *Handler) listLots(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := LotFilter{State: c.Query("state"), Limit: p.FetchLimit()}
	if f.SkuID, ok = queryUUID(c, "sku_id"); !ok {
		return
	}
	switch kind := c.Query("kind"); kind {
	case "":
	case "kit":
		f.KitOnly = true
	default:
		httpx.RenderError(c, errInvalid(map[string]string{"kind": "invalid"}))
		return
	}
	if f.ExpiringWithin, ok = queryInt(c, "expiring_within"); !ok {
		return
	}
	if p.After != nil {
		expiry, received, found := strings.Cut(p.After.SortKey, "|")
		e, err1 := time.Parse(time.DateOnly, expiry)
		r, err2 := time.Parse(time.DateOnly, received)
		if !found || err1 != nil || err2 != nil {
			httpx.RenderError(c, errCursor())
			return
		}
		f.AfterExpiry, f.AfterReceived, f.AfterID = &e, r, p.After.ID
	}
	lots, err := h.svc.Lots(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(lots.Lots, p, func(l LotView) httpx.Cursor {
		expiry := "9999-12-31"
		if l.ExpiryDate != nil {
			expiry = dateOf(*l.ExpiryDate)
		}
		return httpx.Cursor{SortKey: expiry + "|" + dateOf(l.ReceivedOn), ID: l.ID}
	})
	c.JSON(http.StatusOK, httpx.List[lotDTO]{Data: mapSlice(list.Data, lotDTOOn(lots.Today, lots.WarnDays)), NextCursor: list.NextCursor})
}

func (h *Handler) traceLot(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	after, ok := moveCursor(c, p)
	if !ok {
		return
	}
	t, err := h.svc.TraceLot(c.Request.Context(), scope, id, after, p.FetchLimit())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	moves, next := nextMoves(t.Moves, p, moveCursorOf)
	c.JSON(http.StatusOK, lotTraceDTO{Lot: lotDTOOn(t.Today, t.WarnDays)(t.Lot), Moves: mapSlice(moves, toMoveDTO), NextCursor: next})
}

// Reservations.

func (h *Handler) listReservations(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := ReservationFilter{Status: c.Query("status"), SourceType: c.Query("source_type"), Limit: p.FetchLimit()}
	if f.SkuID, ok = queryUUID(c, "sku_id"); !ok {
		return
	}
	if f.AfterAt, ok = afterTime(c, p); !ok {
		return
	}
	if p.After != nil {
		f.AfterID = p.After.ID
	}
	rows, err := h.svc.ListReservations(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r Reservation) httpx.Cursor { return timeCursor(r.CreatedAt, r.ID) })
	c.JSON(http.StatusOK, httpx.List[reservationDTO]{Data: mapSlice(list.Data, toReservationDTO), NextCursor: list.NextCursor})
}

func (h *Handler) createReservation(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[reservationRequest](c)
	if !ok {
		return
	}
	r, err := h.svc.CreateReservation(c.Request.Context(), actor, req.input())
	render(c, http.StatusCreated, toReservationDTO(r), err)
}

func (h *Handler) releaseReservation(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[releaseRequest](c)
	if !ok {
		return
	}
	r, err := h.svc.ReleaseReservation(c.Request.Context(), actor, httpx.CommandID(c), ReleaseInput(req))
	render(c, http.StatusOK, toReservationDTO(r), err)
}

// Stock adjustments.

func (h *Handler) listAdjustments(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := AdjustmentFilter{Kind: c.Query("kind"), Status: c.Query("status"), DocNo: c.Query("doc_no"), Limit: p.FetchLimit()}
	if f.SkuID, ok = queryUUID(c, "sku_id"); !ok {
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
	rows, err := h.svc.Adjustments(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(a AdjustmentSummary) httpx.Cursor { return timeCursor(a.PostedAt, a.ID) })
	c.JSON(http.StatusOK, httpx.List[adjustmentDTO]{Data: mapSlice(list.Data, toAdjustmentListDTO), NextCursor: list.NextCursor})
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

func (h *Handler) postAdjustment(reauth gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req adjustmentRequest
		posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
			needs, err := h.svc.AdjustmentNeedsReauth(c.Request.Context(), actor.TenantID, req.Kind, req.ReasonID)
			if err != nil {
				return posting.Response{}, err
			}
			if needs {
				if reauth(c); c.IsAborted() {
					return posting.Response{}, errAborted
				}
			}
			hash, err := payloadHash(body)
			if err != nil {
				return posting.Response{}, err
			}
			return h.svc.PostAdjustment(c.Request.Context(), actor, key, req.input(hash))
		})
	}
}

func (h *Handler) reverseAdjustment(c *gin.Context) {
	var req reverseRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		hash, err := payloadHash(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.ReverseAdjustment(c.Request.Context(), actor, key, httpx.CommandID(c),
			ReverseInput{ExceptionConfirmationID: req.ExceptionConfirmationID, PayloadHash: hash})
	})
}

// Stock counts.

func (h *Handler) listCounts(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := CountFilter{Status: c.Query("status"), DocNo: c.Query("doc_no"), Limit: p.FetchLimit()}
	if f.AfterAt, ok = afterTime(c, p); !ok {
		return
	}
	if p.After != nil {
		f.AfterID = p.After.ID
	}
	rows, err := h.svc.Counts(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(s CountSummary) httpx.Cursor { return timeCursor(s.CreatedAt, s.ID) })
	c.JSON(http.StatusOK, httpx.List[countDTO]{Data: mapSlice(list.Data, toCountListDTO), NextCursor: list.NextCursor})
}

// countResponse writes a count with its version as the ETag.
func countResponse(c *gin.Context, status int, d CountDetail, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, d.Version)
	c.JSON(status, toCountDTO(d))
}

// startCount opens a count. An Idempotency-Key is optional: a retry with the same key returns
// the count the first request opened.
func (h *Handler) startCount(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[countRequest](c)
	if !ok {
		return
	}
	var key *string
	if v := strings.TrimSpace(c.GetHeader(httpx.IdempotencyKeyHeader)); v != "" {
		key = &v
	}
	d, created, err := h.svc.StartCount(c.Request.Context(), actor, key, req.input())
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	countResponse(c, status, d, err)
}

func (h *Handler) getCount(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Count(c.Request.Context(), scope, id)
	countResponse(c, http.StatusOK, d, err)
}

func (h *Handler) putCountLines(c *gin.Context) {
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
	req, ok := bind[countLinesRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.UpdateCountLines(c.Request.Context(), actor, id, version, req.input())
	countResponse(c, http.StatusOK, d, err)
}

func (h *Handler) finishCount(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	d, err := h.svc.FinishCount(c.Request.Context(), actor, httpx.CommandID(c), version)
	countResponse(c, http.StatusOK, d, err)
}

func (h *Handler) recountCount(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[recountRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.RecountCount(c.Request.Context(), actor, httpx.CommandID(c), version, req.LineIDs)
	countResponse(c, http.StatusOK, d, err)
}

func (h *Handler) postCount(c *gin.Context) {
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	var req countPostRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
		hash, err := payloadHash(body)
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.PostCount(c.Request.Context(), actor, key, httpx.CommandID(c), CountPostInput{
			Version: version, ExceptionConfirmationID: req.ExceptionConfirmationID, PayloadHash: hash,
		})
	})
}

// Cost recalculations.

func (h *Handler) listRecosts(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	f := RecostFilter{Status: c.Query("status"), Limit: p.FetchLimit()}
	if f.AfterAt, ok = afterTime(c, p); !ok {
		return
	}
	if p.After != nil {
		f.AfterID = p.After.ID
	}
	rows, err := h.svc.Recosts(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r RecostRun) httpx.Cursor { return timeCursor(r.CreatedAt, r.ID) })
	c.JSON(http.StatusOK, httpx.List[recostDTO]{Data: mapSlice(list.Data, toRecostDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getRecost(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	r, err := h.svc.Recost(c.Request.Context(), scope, id)
	render(c, http.StatusOK, toRecostDTO(r), err)
}

// previewRecost answers 201 with the open preview, or 204 when the ledger has nothing to
// correct.
func (h *Handler) previewRecost(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[recostRequest](c)
	if !ok {
		return
	}
	if req.FromDate == nil {
		httpx.RenderError(c, errInvalid(map[string]string{"from_date": "required"}))
		return
	}
	run, err := h.svc.PreviewRecost(c.Request.Context(), actor, req.SkuIDs, *req.FromDate.time())
	switch {
	case err != nil:
		httpx.RenderError(c, err)
	case run == nil:
		c.Status(http.StatusNoContent)
	default:
		c.JSON(http.StatusCreated, toRecostDTO(*run))
	}
}

func (h *Handler) confirmRecost(c *gin.Context) {
	posted[struct{}](c, nil, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.ConfirmRecost(c.Request.Context(), actor, key, httpx.CommandID(c))
	})
}

func (h *Handler) discardRecost(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	r, err := h.svc.DiscardRecost(c.Request.Context(), actor, httpx.CommandID(c))
	render(c, http.StatusOK, toRecostDTO(r), err)
}
