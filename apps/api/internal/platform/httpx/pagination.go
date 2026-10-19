package httpx

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Page size limits for list endpoints.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Cursor is the keyset position after which the next page starts: the sort key of the last row
// (formatted by the repository, e.g. an RFC 3339 timestamp or a name key) and its id.
type Cursor struct {
	SortKey string
	ID      uuid.UUID
}

// Encode returns the opaque cursor string (base64url of the JSON pair [sort_key, id]).
func (c Cursor) Encode() string {
	raw, _ := json.Marshal([2]string{c.SortKey, c.ID.String()}) // strings always marshal
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses a cursor produced by Encode.
func DecodeCursor(s string) (Cursor, error) {
	invalid := apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, invalid
	}
	var pair [2]string
	if err := json.Unmarshal(raw, &pair); err != nil {
		return Cursor{}, invalid
	}
	id, err := uuid.Parse(pair[1])
	if err != nil || id == uuid.Nil {
		return Cursor{}, invalid
	}
	return Cursor{SortKey: pair[0], ID: id}, nil
}

// TimeCursor is the cursor of a row ordered by a timestamp and then its id.
func TimeCursor(t time.Time, id uuid.UUID) Cursor {
	return Cursor{SortKey: t.UTC().Format(time.RFC3339Nano), ID: id}
}

// Page is a parsed "?cursor=&limit=" request.
type Page struct {
	After *Cursor // nil for the first page
	Limit int
}

// FetchLimit is the number of rows a repository should read: one more than Limit, so the
// presence of a next page is known without a count query.
func (p Page) FetchLimit() int { return p.Limit + 1 }

// AfterTime reads the position of a TimeCursor; nil for the first page.
func (p Page) AfterTime() (*time.Time, uuid.UUID, error) {
	if p.After == nil {
		return nil, uuid.Nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
	if err != nil {
		return nil, uuid.Nil, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
	}
	return &t, p.After.ID, nil
}

// ParsePage reads cursor and limit query parameters (limit defaults to DefaultLimit, max MaxLimit).
func ParsePage(c *gin.Context) (Page, error) {
	p := Page{Limit: DefaultLimit}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > MaxLimit {
			return Page{}, apperr.NewValidation("invalid_limit", "limit must be between 1 and 200", map[string]string{"limit": "out_of_range"})
		}
		p.Limit = n
	}
	if raw := c.Query("cursor"); raw != "" {
		cur, err := DecodeCursor(raw)
		if err != nil {
			return Page{}, err
		}
		p.After = &cur
	}
	return p, nil
}

// List is the response envelope of list endpoints.
type List[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// NewList trims rows read with Page.FetchLimit to the page size and sets next_cursor from the
// last returned row when more rows exist.
func NewList[T any](rows []T, p Page, cursorOf func(T) Cursor) List[T] {
	if rows == nil {
		rows = []T{}
	}
	if len(rows) <= p.Limit {
		return List[T]{Data: rows}
	}
	rows = rows[:p.Limit]
	next := cursorOf(rows[len(rows)-1]).Encode()
	return List[T]{Data: rows, NextCursor: &next}
}

// NewListOf is NewList over rows answered in another shape: conv turns each returned row into it.
func NewListOf[R, T any](rows []R, p Page, cursorOf func(R) Cursor, conv func(R) T) List[T] {
	page := NewList(rows, p, cursorOf)
	out := List[T]{Data: make([]T, len(page.Data)), NextCursor: page.NextCursor}
	for i, r := range page.Data {
		out.Data[i] = conv(r)
	}
	return out
}
