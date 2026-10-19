// Package idempotency makes posting and payment requests safe to retry. A retry of a request that
// already completed replays the stored response (Replay, checked before any business guard, so a
// period lock or a read-only subscription that arrived after the commit cannot turn a success into
// an error). A new request claims its key row inside the posting transaction (Begin); a different
// request with the same key is rejected. Only committed 2xx responses are stored: a failed command
// rolls back its key row, so the client may retry.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// TTL is how long a key is remembered.
const TTL = 7 * 24 * time.Hour

// Error codes.
const (
	CodeKeyReused  = "idempotency_key_reused"
	CodeInProgress = "idempotency_key_in_progress"
)

// Key is the client's Idempotency-Key bound to the hash of the request it was sent with.
type Key struct {
	Value       string
	RequestHash []byte
}

// Valid reports a usable key.
func (k Key) Valid() bool { return k.Value != "" && len(k.RequestHash) == sha256.Size }

// RequestHash is SHA-256 over method, path and the canonical JSON body (object keys sorted,
// insignificant whitespace removed), so a resent request hashes identically whatever the client's
// key order. An empty body hashes as no body.
func RequestHash(method, path string, body []byte) ([]byte, error) {
	canonical, err := canonicalJSON(body)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(canonical)
	return h.Sum(nil), nil
}

func canonicalJSON(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // keep numbers verbatim, no float rounding
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonical body: %w", err)
	}
	if dec.More() {
		return nil, errors.New("canonical body: trailing data")
	}
	out, err := json.Marshal(v) // maps marshal with sorted keys
	if err != nil {
		return nil, fmt.Errorf("canonical body: %w", err)
	}
	return out, nil
}

// Stored is a response recorded for a key.
type Stored struct {
	Status int
	Body   json.RawMessage
}

// Guard reads and writes idempotency_keys through the caller's querier, so the key joins the
// posting transaction.
type Guard struct {
	clock clock.Clock
}

// NewGuard returns a guard using clk for timestamps.
func NewGuard(clk clock.Clock) *Guard { return &Guard{clock: clk} }

// Replay returns the stored response when the same request (same key, same request hash) already
// completed and has not expired, and (nil, nil) otherwise. It takes no lock and claims nothing:
// callers run it before their guards and fall back to Begin, which settles every other case
// (new key, key in progress, key reused for a different request, expired key).
func (g *Guard) Replay(ctx context.Context, q db.Querier, tenantID uuid.UUID, scope string, key Key) (*Stored, error) {
	if !key.Valid() || scope == "" {
		return nil, errors.New("idempotency: key and scope are required")
	}
	var row struct {
		ResponseStatus int    `db:"response_status"`
		ResponseBody   []byte `db:"response_body"`
	}
	err := q.QueryRowxContext(ctx, `
		SELECT response_status, response_body
		FROM idempotency_keys
		WHERE tenant_id = $1 AND scope = $2 AND key = $3 AND request_hash = $4
		  AND response_status IS NOT NULL AND expires_at > $5`,
		tenantID, scope, key.Value, key.RequestHash, g.clock.Now()).StructScan(&row)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replay idempotency key: %w", err)
	}
	return &Stored{Status: row.ResponseStatus, Body: row.ResponseBody}, nil
}

// Begin claims key for scope. It returns (nil, nil) when this request owns the key and must run
// the command; the stored response when the same request already completed; Conflict
// idempotency_key_reused when the key was used for a different request. A concurrent request
// with the same key blocks on the key row until the first transaction ends.
func (g *Guard) Begin(ctx context.Context, q db.Querier, tenantID uuid.UUID, scope string, key Key) (*Stored, error) {
	if !key.Valid() || scope == "" {
		return nil, errors.New("idempotency: key and scope are required")
	}
	now := g.clock.Now()
	res, err := q.ExecContext(ctx, `
		INSERT INTO idempotency_keys (tenant_id, scope, key, request_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, scope, key) DO NOTHING`,
		tenantID, scope, key.Value, key.RequestHash, now, now.Add(TTL))
	if err != nil {
		return nil, fmt.Errorf("claim idempotency key: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, fmt.Errorf("claim idempotency key: %w", err)
	} else if n == 1 {
		return nil, nil
	}

	var row struct {
		RequestHash    []byte    `db:"request_hash"`
		ResponseStatus *int      `db:"response_status"`
		ResponseBody   []byte    `db:"response_body"` // jsonb, NULL until completed
		ExpiresAt      time.Time `db:"expires_at"`
	}
	err = q.QueryRowxContext(ctx, `
		SELECT request_hash, response_status, response_body, expires_at
		FROM idempotency_keys WHERE tenant_id = $1 AND scope = $2 AND key = $3
		FOR UPDATE`, tenantID, scope, key.Value).StructScan(&row)
	if err != nil {
		return nil, fmt.Errorf("read idempotency key: %w", err)
	}

	if !row.ExpiresAt.After(now) {
		// Expired but not purged yet: the key is free again.
		if _, err := q.ExecContext(ctx, `
			UPDATE idempotency_keys
			SET request_hash = $4, response_status = NULL, response_body = NULL, created_at = $5, expires_at = $6
			WHERE tenant_id = $1 AND scope = $2 AND key = $3`,
			tenantID, scope, key.Value, key.RequestHash, now, now.Add(TTL)); err != nil {
			return nil, fmt.Errorf("reclaim idempotency key: %w", err)
		}
		return nil, nil
	}
	if !bytes.Equal(row.RequestHash, key.RequestHash) {
		return nil, apperr.NewConflict(CodeKeyReused, "This Idempotency-Key was already used for a different request")
	}
	if row.ResponseStatus == nil {
		return nil, apperr.NewConflict(CodeInProgress, "A request with this Idempotency-Key has not completed")
	}
	return &Stored{Status: *row.ResponseStatus, Body: row.ResponseBody}, nil
}

// Complete stores the successful response of the request that owns key. Only 2xx responses are
// stored; anything else must roll the transaction back instead.
func (g *Guard) Complete(ctx context.Context, q db.Querier, tenantID uuid.UUID, scope string, key Key, status int, body json.RawMessage) error {
	if status < 200 || status > 299 {
		return fmt.Errorf("idempotency: refusing to store non-2xx status %d", status)
	}
	var stored any
	if len(body) > 0 {
		if !json.Valid(body) {
			return errors.New("idempotency: response body is not valid JSON")
		}
		stored = []byte(body)
	}
	res, err := q.ExecContext(ctx, `
		UPDATE idempotency_keys SET response_status = $5, response_body = $6
		WHERE tenant_id = $1 AND scope = $2 AND key = $3 AND request_hash = $4 AND response_status IS NULL`,
		tenantID, scope, key.Value, key.RequestHash, status, stored)
	if err != nil {
		return fmt.Errorf("store idempotent response: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store idempotent response: %w", err)
	} else if n != 1 {
		return errors.New("idempotency: key is not claimed by this request")
	}
	return nil
}

// Purge deletes expired keys visible to q (call it inside WithTenantTx, once per tenant).
func (g *Guard) Purge(ctx context.Context, q db.Querier) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE expires_at <= $1`, g.clock.Now())
	if err != nil {
		return 0, fmt.Errorf("purge idempotency keys: %w", err)
	}
	return res.RowsAffected()
}

// Once runs create, the body of a create command, at most once per key inside the caller's
// transaction q; create sets *out. Without a key it only runs create. With one it claims the key
// first (Begin): when the same request already completed, *out is decoded from the stored
// response and replayed is true; a different request under the key fails with
// idempotency_key_reused, one still running with idempotency_key_in_progress. Otherwise *out is
// stored as the response with status once create succeeds. A failed create rolls the transaction
// back with the key, so the client may retry. T must survive a JSON round trip unchanged.
func Once[T any](ctx context.Context, q db.Querier, g *Guard, tenantID uuid.UUID, scope string, key *Key, status int,
	out *T, create func() error) (replayed bool, err error) {
	if key == nil {
		return false, create()
	}
	stored, err := g.Begin(ctx, q, tenantID, scope, *key)
	if err != nil {
		return false, err
	}
	if stored != nil {
		if err := json.Unmarshal(stored.Body, out); err != nil {
			return false, fmt.Errorf("idempotency: decode the stored %s response: %w", scope, err)
		}
		return true, nil
	}
	if err := create(); err != nil {
		return false, err
	}
	body, err := json.Marshal(out)
	if err != nil {
		return false, fmt.Errorf("idempotency: encode the %s response: %w", scope, err)
	}
	return false, g.Complete(ctx, q, tenantID, scope, *key, status, body)
}
