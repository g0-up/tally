// Package support runs the store's support tickets and the consented, read-only support
// sessions through which a platform admin may look at the granted parts of a store's data.
package support

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// Ticket statuses.
const (
	TicketOpen            = "open"
	TicketWaitingCustomer = "waiting_customer"
	TicketInProgress      = "in_progress"
	TicketResolved        = "resolved"
	TicketClosed          = "closed"
)

// Ticket priorities.
const (
	PriorityLow    = "low"
	PriorityNormal = "normal"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

// Message authors.
const (
	AuthorOwner = "owner"
	AuthorAdmin = "platform_admin"
)

// AttachmentOwner is the attachments.owner_type of files attached to a support ticket.
const AttachmentOwner = "support_ticket"

// Support session durations.
const (
	DefaultSessionHours = 2
	MaxSessionHours     = 8
)

var (
	ticketStatuses   = []string{TicketOpen, TicketWaitingCustomer, TicketInProgress, TicketResolved, TicketClosed}
	ticketPriorities = []string{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}
)

// ViewModules maps each module an owner may grant to a support session to the first path
// segments of the tenant GET routes it opens. Files (attachments, exports, prints), the
// notification inbox and the AI Q&A are never part of a support view.
var ViewModules = map[string][]string{
	"catalog": {"products", "skus", "categories", "colors", "units", "tax-rates", "attributes", "attribute-sets",
		"mix-groups", "search", "price-lists", "price-changes", "promotions"},
	"partners":    {"parties", "customers", "customer-groups", "suppliers"},
	"inventory":   {"stock", "stock-card", "lots", "reservations", "stock-adjustments", "stock-counts", "cost-recalculations"},
	"procurement": {"procurement", "purchase-orders", "receipts", "supplier-documents", "purchase-returns", "purchase-price-adjustments"},
	"sales":       {"pos", "quotes", "invoices", "stock-outs", "sales-documents", "sales-returns", "exchanges"},
	"debt_cash":   {"debts", "payments", "statements", "funds", "payment-methods"},
	"kits":        {"kits", "assembly-orders", "disassemblies"},
	"campaign":    {"campaigns"},
	"crm":         {"care-tasks", "loyalty"},
	"reports":     {"reports", "saved-reports"},
	"settings":    {"store", "tenant", "reasons", "period-locks", "audit-events"},
}

// ModuleOfSegment reports whether segment (the first path segment under /api/v1) belongs to the
// view module.
func ModuleOfSegment(module, segment string) bool {
	for _, s := range ViewModules[module] {
		if s == segment {
			return true
		}
	}
	return false
}

// Ticket is a support ticket of a store.
type Ticket struct {
	ID              uuid.UUID  `db:"id"`
	TenantID        uuid.UUID  `db:"tenant_id"`
	Number          string     `db:"number"`
	Subject         string     `db:"subject"`
	Status          string     `db:"status"`
	Priority        string     `db:"priority"`
	SLADueAt        *time.Time `db:"sla_due_at"`
	AssignedAdminID *uuid.UUID `db:"assigned_admin_id"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
}

// Message is one message of a ticket.
type Message struct {
	ID            uuid.UUID              `db:"id"`
	TicketID      uuid.UUID              `db:"ticket_id"`
	AuthorType    string                 `db:"author_type"`
	AuthorID      uuid.UUID              `db:"author_id"`
	Body          string                 `db:"body"`
	AttachmentIDs db.JSONList[uuid.UUID] `db:"attachment_ids"`
	CreatedAt     time.Time              `db:"created_at"`
}

// Session is a support session the owner granted to the admin assigned to a ticket.
type Session struct {
	ID             uuid.UUID           `db:"id"`
	TenantID       uuid.UUID           `db:"tenant_id"`
	AdminID        uuid.UUID           `db:"admin_id"`
	TicketID       *uuid.UUID          `db:"ticket_id"`
	Scope          string              `db:"scope"`
	Modules        db.JSONList[string] `db:"modules"`
	ConsentTokenID uuid.UUID           `db:"consent_token_id"`
	GrantedAt      time.Time           `db:"granted_at"`
	ExpiresAt      time.Time           `db:"expires_at"`
	RevokedAt      *time.Time          `db:"revoked_at"`
}

// AdminSession is a support session of the signed-in admin with the slug and name of its store.
type AdminSession struct {
	Session
	TenantSlug string `db:"tenant_slug"`
	TenantName string `db:"tenant_name"`
}

// State is "revoked", "expired" or "active" at now.
func (s Session) State(now time.Time) string {
	switch {
	case s.RevokedAt != nil:
		return "revoked"
	case !now.Before(s.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}

// Grants reports whether the session opened module.
func (s Session) Grants(module string) bool {
	for _, m := range s.Modules {
		if m == module {
			return true
		}
	}
	return false
}

const ticketColumns = `id, tenant_id, number, subject, status, priority, sla_due_at, assigned_admin_id, created_at, updated_at`

const messageColumns = `id, ticket_id, author_type, author_id, body, to_jsonb(attachment_ids) AS attachment_ids, created_at`

const sessionColumns = `id, tenant_id, admin_id, ticket_id, scope, to_jsonb(modules) AS modules, consent_token_id, granted_at,
	expires_at, revoked_at`

// TicketOfTenant reports whether the support ticket id belongs to the tenant of q. Support
// tickets are a platform table without row security, so the tenant is matched explicitly.
func TicketOfTenant(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRowxContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM support_tickets
		  WHERE id = $1 AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)`, id).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("support: read ticket owner: %w", err)
	}
	return ok, nil
}
