package debt

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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

// Handler serves the Công nợ endpoints of tenant hosts: balances, aging and forecast, payments
// and their verification queue, allocations, adjustments, offsets, refunds, statements and
// collection notes.
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
	// (Idempotency-Key required; the coordinator checks the entitlement).
	Posting *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
}

// Register adds the debt routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Posting == nil || r.Commands == nil || r.Reauth == nil {
		panic("debt: Register needs the tenant and posting groups, the command router and the re-auth guard")
	}
	g, p := r.Tenant, r.Posting
	g.GET("/debts/parties", h.listParties)
	g.GET("/debts/parties/:partyId", h.partyLedger)
	g.GET("/debts/aging", h.aging)
	g.GET("/debts/forecast", h.forecast)

	p.POST("/payments", h.postPayment)
	payments := r.Commands(p, "payments")
	payments.Item("confirm", h.confirmPayment)
	payments.Item("reverse", guarded(r.Reauth, h.reversePayment))
	g.GET("/payments", h.listPayments)
	g.GET("/payments/pending-verification", h.pendingVerification)
	g.GET("/payments/:id", h.getPayment)

	p.POST("/allocations", h.postAllocation)
	r.Commands(g, "allocations").Collection("suggest", h.suggestAllocation)
	// A write-off needs re-authentication; the kind is only known once the body is read.
	p.POST("/debt-adjustments", h.postAdjustment(r.Reauth))
	g.GET("/debt-adjustments", h.listAdjustments)
	g.GET("/debt-adjustments/:id", h.getAdjustment)
	p.POST("/debt-offsets", r.Reauth, h.postOffset)
	g.GET("/debt-offsets", h.listOffsets)
	g.GET("/debt-offsets/:id", h.getOffset)
	p.POST("/refunds", h.postRefund)

	g.GET("/statements", h.listStatements)
	g.POST("/statements", h.createStatement)
	g.GET("/statements/:id", h.getStatement)
	g.GET("/statements/:id/versions/:n", h.statementVersion)
	statements := r.Commands(g, "statements")
	statements.Item("generate", h.statementAction(h.svc.GenerateStatement))
	statements.Item("send", h.statementAction(h.svc.SendStatement))
	statements.Item("confirm", h.statementAction(h.svc.ConfirmStatement))
	statements.Item("dispute", h.disputeStatement)

	g.GET("/parties/:id/collection-notes", h.listNotes)
	g.POST("/parties/:id/collection-notes", h.addNote)
	g.POST("/parties/:id/reminders", h.remind)
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

func scopeFor(c *gin.Context) (Scope, bool) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return Scope{}, false
	}
	return Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}, true
}

func queryDate(c *gin.Context, name string, required bool) (time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		if required {
			httpx.RenderError(c, errInvalid(map[string]string{name: "required"}))
			return time.Time{}, false
		}
		return time.Time{}, true
	}
	var d date
	if err := d.UnmarshalJSON(strconv.AppendQuote(nil, raw)); err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid_date"}))
		return time.Time{}, false
	}
	return time.Time(d), true
}

func queryDirection(c *gin.Context) (string, bool) {
	d := c.Query("direction")
	if d != Receivable && d != Payable {
		httpx.RenderError(c, errInvalid(map[string]string{"direction": "invalid"}))
		return "", false
	}
	return d, true
}

// timeCursor decodes the sort key of a (created_at, id) keyset cursor.
func timeCursor(c *gin.Context, p httpx.Page) (*time.Time, uuid.UUID, bool) {
	if p.After == nil {
		return nil, uuid.Nil, true
	}
	t, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
	if err != nil {
		httpx.RenderError(c, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"}))
		return nil, uuid.Nil, false
	}
	return &t, p.After.ID, true
}

func render[T any](c *gin.Context, v T, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *Handler) listParties(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	direction, ok := queryDirection(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := PartyFilter{Direction: direction, Limit: page.FetchLimit()}
	if raw := c.Query("overdue"); raw != "" {
		if f.OverdueOnly, err = strconv.ParseBool(raw); err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"overdue": "invalid"}))
			return
		}
	}
	if page.After != nil {
		f.HasAfter, f.AfterNameKey, f.AfterID = true, page.After.SortKey, page.After.ID
	}
	rows, err := h.svc.Parties(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(p PartySummary) httpx.Cursor { return httpx.Cursor{SortKey: p.NameKey, ID: p.PartyID} })
	c.JSON(http.StatusOK, httpx.List[partySummaryDTO]{Data: mapSlice(list.Data, toPartySummaryDTO), NextCursor: list.NextCursor})
}

func (h *Handler) partyLedger(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "partyId")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	l, err := h.svc.Ledger(c.Request.Context(), scope, id)
	render(c, toPartyLedgerDTO(l), err)
}

func (h *Handler) aging(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	direction, ok := queryDirection(c)
	if !ok {
		return
	}
	asOf, ok := queryDate(c, "as_of", true)
	if !ok {
		return
	}
	r, err := h.svc.Aging(c.Request.Context(), scope, direction, asOf)
	render(c, toAgingDTO(r), err)
}

func (h *Handler) forecast(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	from, ok := queryDate(c, "from", true)
	if !ok {
		return
	}
	to, ok := queryDate(c, "to", true)
	if !ok {
		return
	}
	f, err := h.svc.Forecast(c.Request.Context(), scope, from, to)
	render(c, toForecastDTO(f), err)
}

func (h *Handler) pendingVerification(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	rows, err := h.svc.PendingVerification(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.List[pendingPaymentDTO]{Data: mapSlice(rows, toPendingPaymentDTO)})
}

// queryParty reads the optional party_id filter.
func queryParty(c *gin.Context) (*uuid.UUID, bool) {
	raw := c.Query("party_id")
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{"party_id": "invalid"}))
		return nil, false
	}
	return &id, true
}

// postedCursor is the (posted_at, id) keyset cursor of a document list row.
func postedCursor(postedAt time.Time, id uuid.UUID) httpx.Cursor {
	return httpx.Cursor{SortKey: postedAt.Format(time.RFC3339Nano), ID: id}
}

func (h *Handler) listPayments(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := PaymentFilter{Direction: c.Query("direction"), Status: c.Query("status"), DocNo: c.Query("doc_no"), Limit: page.FetchLimit()}
	fields := map[string]string{}
	if f.Direction != "" && f.Direction != In && f.Direction != Out {
		fields["direction"] = "invalid"
	}
	if f.Status != "" && f.Status != PaymentPending && f.Status != PaymentConfirmed && f.Status != PaymentReversed {
		fields["status"] = "invalid"
	}
	if raw := c.Query("unallocated"); raw != "" {
		if f.Unallocated, err = strconv.ParseBool(raw); err != nil {
			fields["unallocated"] = "invalid"
		}
	}
	if len(fields) > 0 {
		httpx.RenderError(c, errInvalid(fields))
		return
	}
	if f.PartyID, ok = queryParty(c); !ok {
		return
	}
	if f.AfterPostedAt, f.AfterID, ok = timeCursor(c, page); !ok {
		return
	}
	rows, err := h.svc.ListPayments(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(p PendingPayment) httpx.Cursor { return postedCursor(p.PostedAt, p.ID) })
	c.JSON(http.StatusOK, httpx.List[pendingPaymentDTO]{Data: mapSlice(list.Data, toPendingPaymentDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getPayment(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	p, err := h.svc.PaymentByID(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, p.Version)
	c.JSON(http.StatusOK, toPaymentDetailDTO(p))
}

// documentFilter reads the party, document number and page of a DN or BT list.
func documentFilter(c *gin.Context) (DocumentFilter, httpx.Page, bool) {
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return DocumentFilter{}, page, false
	}
	f := DocumentFilter{DocNo: c.Query("doc_no"), Limit: page.FetchLimit()}
	var ok bool
	if f.PartyID, ok = queryParty(c); !ok {
		return f, page, false
	}
	if f.AfterPostedAt, f.AfterID, ok = timeCursor(c, page); !ok {
		return f, page, false
	}
	return f, page, true
}

func (h *Handler) listAdjustments(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	f, page, ok := documentFilter(c)
	if !ok {
		return
	}
	rows, err := h.svc.ListAdjustments(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(a AdjustmentRow) httpx.Cursor { return postedCursor(a.PostedAt, a.ID) })
	c.JSON(http.StatusOK, httpx.List[adjustmentRowDTO]{Data: mapSlice(list.Data, toAdjustmentRowDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getAdjustment(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	d, err := h.svc.AdjustmentByID(c.Request.Context(), scope, id)
	render(c, toAdjustmentDetailDTO(d), err)
}

func (h *Handler) listOffsets(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	f, page, ok := documentFilter(c)
	if !ok {
		return
	}
	rows, err := h.svc.ListOffsets(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(o OffsetRow) httpx.Cursor { return postedCursor(o.PostedAt, o.ID) })
	c.JSON(http.StatusOK, httpx.List[offsetRowDTO]{Data: mapSlice(list.Data, toOffsetRowDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getOffset(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	d, err := h.svc.OffsetByID(c.Request.Context(), scope, id)
	render(c, toOffsetDetailDTO(d), err)
}

// errAborted stops a posting whose guard already answered the request.
var errAborted = errors.New("debt: request aborted by a guard")

// posted reads the body and the Idempotency-Key of a posting request, decodes the body into req
// and writes the coordinator's response of post.
func posted[R any](c *gin.Context, req *R, post func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error)) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
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
	if req != nil {
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

func (h *Handler) postPayment(c *gin.Context) {
	var req paymentRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		in, openingDate, err := req.input()
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.PostPayment(c.Request.Context(), actor, key, in, openingDate)
	})
}

func (h *Handler) confirmPayment(c *gin.Context) {
	posted[struct{}](c, nil, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.ConfirmPayment(c.Request.Context(), actor, key, httpx.CommandID(c))
	})
}

func (h *Handler) reversePayment(c *gin.Context) {
	posted[struct{}](c, nil, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.ReversePayment(c.Request.Context(), actor, key, httpx.CommandID(c))
	})
}

func (h *Handler) postAllocation(c *gin.Context) {
	var req allocationRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		in, err := req.input()
		if err != nil {
			return posting.Response{}, err
		}
		return h.svc.PostAllocation(c.Request.Context(), actor, key, in)
	})
}

func (h *Handler) suggestAllocation(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	var req suggestRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	out, err := h.svc.SuggestAllocation(c.Request.Context(), scope, req.PartyID, req.Direction, req.Amount)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"allocations": mapSlice(out, toAllocationDTO)})
}

func (h *Handler) postAdjustment(reauth gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req adjustmentRequest
		posted(c, &req, func(actor audit.Actor, key idempotency.Key, body []byte) (posting.Response, error) {
			if req.Kind == AdjustWriteOff {
				if reauth(c); c.IsAborted() {
					return posting.Response{}, errAborted
				}
			}
			hash, err := exception.PayloadHash(body)
			if err != nil {
				return posting.Response{}, err
			}
			return h.svc.PostAdjustment(c.Request.Context(), actor, key, req.input(hash))
		})
	}
}

func (h *Handler) postOffset(c *gin.Context) {
	var req offsetRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.PostOffset(c.Request.Context(), actor, key, req.input())
	})
}

func (h *Handler) postRefund(c *gin.Context) {
	var req refundRequest
	posted(c, &req, func(actor audit.Actor, key idempotency.Key, _ []byte) (posting.Response, error) {
		return h.svc.PostRefund(c.Request.Context(), actor, key, req.input())
	})
}

func (h *Handler) listStatements(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := StatementFilter{Status: c.Query("status"), Limit: page.FetchLimit()}
	if raw := c.Query("party_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"party_id": "invalid"}))
			return
		}
		f.PartyID = &id
	}
	if f.AfterCreatedAt, f.AfterID, ok = timeCursor(c, page); !ok {
		return
	}
	rows, err := h.svc.Statements(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(s StatementRow) httpx.Cursor {
		return httpx.Cursor{SortKey: s.CreatedAt.Format(time.RFC3339Nano), ID: s.ID}
	})
	c.JSON(http.StatusOK, httpx.List[statementRowDTO]{Data: mapSlice(list.Data, toStatementRowDTO), NextCursor: list.NextCursor})
}

func (h *Handler) getStatement(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	st, err := h.svc.Statement(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, st.Version)
	c.JSON(http.StatusOK, toStatementRowDTO(st))
}

func (h *Handler) createStatement(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req statementRequest
	key, err := httpx.BindJSONKeyed(c, &req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	st, replayed, err := h.svc.CreateStatement(c.Request.Context(), actor, key, req.input())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.MarkReplay(c, replayed)
	httpx.SetETag(c, st.Version)
	c.JSON(http.StatusCreated, toStatementDTO(st))
}

func (h *Handler) statementVersion(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil || n < 1 {
		httpx.RenderError(c, errNotFound())
		return
	}
	v, err := h.svc.StatementVersion(c.Request.Context(), scope, id, n)
	render(c, toStatementVersionDTO(v), err)
}

type statementFunc func(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Statement, error)

// statementAction serves a body-less statement transition guarded by If-Match.
func (h *Handler) statementAction(fn statementFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		h.transition(c, func(actor audit.Actor, id uuid.UUID, version int) (Statement, error) {
			return fn(c.Request.Context(), actor, id, version)
		})
	}
}

func (h *Handler) disputeStatement(c *gin.Context) {
	var req disputeRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.transition(c, func(actor audit.Actor, id uuid.UUID, version int) (Statement, error) {
		return h.svc.DisputeStatement(c.Request.Context(), actor, id, version, req.Note)
	})
}

func (h *Handler) transition(c *gin.Context, fn func(actor audit.Actor, id uuid.UUID, version int) (Statement, error)) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	version, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	st, err := fn(actor, httpx.CommandID(c), version)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, st.Version)
	c.JSON(http.StatusOK, toStatementDTO(st))
}

func (h *Handler) listNotes(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := NoteFilter{PartyID: id, Limit: page.FetchLimit()}
	if f.AfterCreatedAt, f.AfterID, ok = timeCursor(c, page); !ok {
		return
	}
	rows, err := h.svc.Notes(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, func(n CollectionNote) httpx.Cursor {
		return httpx.Cursor{SortKey: n.CreatedAt.Format(time.RFC3339Nano), ID: n.ID}
	})
	c.JSON(http.StatusOK, httpx.List[collectionNoteDTO]{Data: mapSlice(list.Data, toCollectionNoteDTO), NextCursor: list.NextCursor})
}

func (h *Handler) addNote(c *gin.Context) {
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
	var req noteRequest
	key, err := httpx.BindJSONKeyed(c, &req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	n, replayed, err := h.svc.AddNote(c.Request.Context(), actor, key, req.input(id))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.MarkReplay(c, replayed)
	c.JSON(http.StatusCreated, toCollectionNoteDTO(n))
}

func (h *Handler) remind(c *gin.Context) {
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
	var req reminderRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	r, err := h.svc.Remind(c.Request.Context(), actor, id, req.Channel)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	status := http.StatusCreated
	if r.Duplicate {
		status = http.StatusOK
	}
	c.JSON(status, toReminderDTO(r))
}
