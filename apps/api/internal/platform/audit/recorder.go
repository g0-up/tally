// Package audit appends audit_events rows inside the caller's transaction. before/after
// snapshots are redacted: any key that names a credential (password, token, secret, otp …) is
// replaced, at any depth, before the row is written.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
)

// ActorType says who acted.
type ActorType string

// Actor types (audit_events.actor_type).
const (
	ActorOwner         ActorType = "owner"
	ActorSupport       ActorType = "support"
	ActorSystem        ActorType = "system"
	ActorPlatformAdmin ActorType = "platform_admin"
)

// Source says through which channel the action arrived.
type Source string

// Sources (audit_events.source).
const (
	SourceWeb            Source = "web"
	SourcePOS            Source = "pos"
	SourcePOSOfflineSync Source = "pos_offline_sync"
	SourceCLI            Source = "cli"
	SourceWorker         Source = "worker"
	SourceSupport        Source = "support"
)

// Actor identifies who performs a command, in which tenant and through which request.
type Actor struct {
	TenantID  uuid.UUID
	Type      ActorType
	ID        uuid.UUID // uuid.Nil for system actors
	Source    Source
	RequestID string
	IP        string // client address; ignored when not a valid IP
}

// Validate checks the actor before anything is written.
func (a Actor) Validate() error {
	if a.TenantID == uuid.Nil {
		return errors.New("audit actor: tenant id is empty")
	}
	switch a.Type {
	case ActorOwner, ActorSupport, ActorPlatformAdmin:
		if a.ID == uuid.Nil {
			return fmt.Errorf("audit actor: %s actor needs an id", a.Type)
		}
	case ActorSystem:
	default:
		return fmt.Errorf("audit actor: unknown type %q", a.Type)
	}
	switch a.Source {
	case SourceWeb, SourcePOS, SourcePOSOfflineSync, SourceCLI, SourceWorker, SourceSupport:
	default:
		return fmt.Errorf("audit actor: unknown source %q", a.Source)
	}
	return nil
}

// Event is one audited action, e.g. Action "stock_out.post" on EntityType "stock_out".
type Event struct {
	Action     string
	EntityType string
	EntityID   uuid.UUID // optional
	DocNo      string    // optional
	Before     any       // optional snapshot, marshalled to JSON and redacted
	After      any       // optional snapshot, marshalled to JSON and redacted
}

// Recorder writes audit events.
type Recorder struct {
	clock clock.Clock
}

// NewRecorder returns a recorder stamping events with clk.
func NewRecorder(clk clock.Clock) *Recorder { return &Recorder{clock: clk} }

// Record appends evt for actor using q (the caller's tenant transaction).
func (r *Recorder) Record(ctx context.Context, q db.Querier, actor Actor, evt Event) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	if evt.Action == "" || evt.EntityType == "" {
		return errors.New("audit event: action and entity type are required")
	}
	before, err := Redact(evt.Before)
	if err != nil {
		return fmt.Errorf("audit event %s: before: %w", evt.Action, err)
	}
	after, err := Redact(evt.After)
	if err != nil {
		return fmt.Errorf("audit event %s: after: %w", evt.Action, err)
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO audit_events
			(id, tenant_id, actor_type, actor_id, action, entity_type, entity_id, doc_no, before, after, source, request_id, ip, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		ids.New(), actor.TenantID, string(actor.Type), nullUUID(actor.ID), evt.Action, evt.EntityType,
		nullUUID(evt.EntityID), nullString(evt.DocNo), before, after, string(actor.Source),
		nullString(actor.RequestID), nullIP(actor.IP), r.clock.Now())
	if err != nil {
		return fmt.Errorf("record audit event %s: %w", evt.Action, err)
	}
	return nil
}

// Redact marshals v to JSON and replaces the values of credential-like keys at any depth. nil
// stays nil (SQL NULL).
func Redact(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return json.Marshal(redactValue(tree))
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if logging.IsSensitiveKey(k) {
				t[k] = logging.Redacted
				continue
			}
			t[k] = redactValue(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactValue(child)
		}
		return t
	default:
		return v
	}
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIP(s string) any {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return addr.Unmap().String()
}
