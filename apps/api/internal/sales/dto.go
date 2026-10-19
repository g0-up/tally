package sales

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// Wire formats of the sales endpoints. The service inputs and models carry their own JSON names
// (money and quantities as decimal strings, business dates as "YYYY-MM-DD"); the requests below
// add what only the HTTP layer reads: the owner's exception confirmations.

// confirmationIDs is the exception_confirmation_ids field of a command that may need the owner's
// confirmation. It is left out of the payload hash the confirmations bind (see PayloadHash).
type confirmationIDs struct {
	IDs []uuid.UUID `json:"exception_confirmation_ids"`
}

// exceptions binds the confirmation ids of a request to the hash of its body.
func (c confirmationIDs) exceptions(body []byte) (Exceptions, error) {
	hash, err := PayloadHash(body)
	if err != nil {
		return Exceptions{}, err
	}
	return Exceptions{IDs: c.IDs, Hash: hash}, nil
}

type confirmRequest struct {
	confirmationIDs
}

type reviseRequest struct {
	ReviseInput
	confirmationIDs
}

type checkoutRequest struct {
	CheckoutInput
	confirmationIDs
}

type cartCheckoutRequest struct {
	CartCheckoutInput
	confirmationIDs
}

type resolveRequest struct {
	ResolveInput
	confirmationIDs
}

type exchangeRequest struct {
	ExchangeInput
	confirmationIDs
}

// Query parameters.

func errCursor() error {
	return apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
}

// listPage reads the newest-first keyset page of a list request: the cursor's sort key is the
// creation time of the last row returned.
func listPage(c *gin.Context) (httpx.Page, ListPage, bool) {
	p, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return httpx.Page{}, ListPage{}, false
	}
	lp := ListPage{Limit: p.FetchLimit()}
	if p.After != nil {
		t, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
		if err != nil {
			httpx.RenderError(c, errCursor())
			return httpx.Page{}, ListPage{}, false
		}
		lp.AfterAt, lp.AfterID = &t, p.After.ID
	}
	return p, lp, true
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
	t, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid_date"}))
		return nil, false
	}
	d := clock.Date(t.Year(), t.Month(), t.Day())
	return &d, true
}

// dateRange reads the from and to business dates of a list.
func dateRange(c *gin.Context) (from, to *time.Time, ok bool) {
	if from, ok = queryDate(c, "from"); !ok {
		return nil, nil, false
	}
	if to, ok = queryDate(c, "to"); !ok {
		return nil, nil, false
	}
	return from, to, true
}
