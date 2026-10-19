package support

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Error codes of the support API.
const (
	CodePossiblePassword = "possible_password"
	CodeTicketClosed     = "ticket_closed"
	CodeNoAssignedAdmin  = "ticket_not_assigned"
	CodeSessionInactive  = "support_session_inactive"
)

// Consent is the owner-account side of a support session, implemented by identity: the consent
// token is written in the account transaction and within runs in it.
type Consent interface {
	GrantSupportConsent(ctx context.Context, p middleware.Principal, expires time.Time,
		within func(q db.Querier, tokenID uuid.UUID) error) (uuid.UUID, error)
	RevokeSupportConsent(ctx context.Context, p middleware.Principal, tokenID uuid.UUID,
		within func(q db.Querier) error) error
}

// SLA answers the support response time of the tenant's plan, implemented by saas.
type SLA interface {
	SupportSLAHours(ctx context.Context, q db.Querier, tenantID uuid.UUID) (int, error)
}

// Deps are the collaborators of the support service.
type Deps struct {
	DB      *db.DB
	Clock   clock.Clock
	Audit   *audit.Recorder
	Consent Consent
	SLA     SLA
}

// Service runs support tickets and support sessions.
type Service struct {
	db      *db.DB
	clock   clock.Clock
	audit   *audit.Recorder
	consent Consent
	sla     SLA
}

// NewService returns the service; Consent is required for support sessions.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit, consent: d.Consent, sla: d.SLA}
}

// passwordPhrases are phrases that suggest a message carries a password or a one-time code.
var passwordPhrases = regexp.MustCompile(`(?i)(mật\s*khẩu|mat\s*khau|password|passwd|mã\s*otp|ma\s*otp|\botp\b|` +
	`\b(mk|pass|pw|pwd)\s*[:=])`)

// LooksLikePassword reports whether text seems to contain a password or a one-time code. Support
// never asks for either; the owner is warned before sending.
func LooksLikePassword(text string) bool { return passwordPhrases.MatchString(text) }

func errPossiblePassword() error {
	return apperr.NewValidation(CodePossiblePassword,
		"The message seems to contain a password or a one-time code. Support never asks for them; remove it or confirm to send",
		map[string]string{"body": CodePossiblePassword})
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errTicketNotFound() error { return apperr.NewNotFound("not_found", "Support ticket not found") }

func errTicketClosed() error {
	return apperr.NewConflict(CodeTicketClosed, "The support ticket is closed; open a new one")
}

// NewTicket is an owner's new ticket with its first message.
type NewTicket struct {
	Subject            string
	Body               string
	Priority           string
	AcknowledgeWarning bool
}

// NewMessage is a message added to a ticket.
type NewMessage struct {
	Body               string
	AttachmentIDs      []uuid.UUID
	AcknowledgeWarning bool
}

func validBody(body string) bool {
	b := strings.TrimSpace(body)
	return b != "" && utf8.RuneCountInString(b) <= 10000
}

func (m NewMessage) validate() error {
	fields := map[string]string{}
	if !validBody(m.Body) {
		fields["body"] = "invalid"
	}
	if len(m.AttachmentIDs) > 10 {
		fields["attachment_ids"] = "too_many"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	if !m.AcknowledgeWarning && LooksLikePassword(m.Body) {
		return errPossiblePassword()
	}
	return nil
}

// TicketView is a ticket with its messages.
type TicketView struct {
	Ticket   Ticket
	Messages []Message
}

func requireOwner(actor audit.Actor) error {
	if actor.Type != audit.ActorOwner {
		return apperr.NewForbidden("forbidden", "Only the store owner can do this")
	}
	return nil
}

// insertTicket numbers and inserts t: "HT-{yyyymmdd}-{6 Crockford characters of the id}", with a
// new id on the rare number collision.
func insertTicket(ctx context.Context, q db.Querier, t *Ticket, day time.Time) error {
	for range 5 {
		t.ID = ids.New()
		t.Number = "HT-" + day.Format("20060102") + "-" + ids.Crockford(t.ID, 6)
		res, err := q.ExecContext(ctx, `
			INSERT INTO support_tickets (id, tenant_id, number, subject, status, priority, sla_due_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT ON CONSTRAINT ux_support_tickets__number DO NOTHING`,
			t.ID, t.TenantID, t.Number, t.Subject, t.Status, t.Priority, t.SLADueAt, t.CreatedAt)
		if err != nil {
			return fmt.Errorf("support: insert ticket: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("support: insert ticket: %w", err)
		} else if n == 1 {
			return nil
		}
	}
	return errors.New("support: insert ticket: no free ticket number")
}

func insertMessage(ctx context.Context, q db.Querier, m Message) error {
	attachments := make([]string, len(m.AttachmentIDs))
	for i, id := range m.AttachmentIDs {
		attachments[i] = id.String()
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO support_ticket_messages (id, ticket_id, author_type, author_id, body, attachment_ids, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::uuid[], $7)`,
		m.ID, m.TicketID, m.AuthorType, m.AuthorID, m.Body, attachments, m.CreatedAt); err != nil {
		return fmt.Errorf("support: insert message: %w", err)
	}
	return nil
}

// readTicket returns the ticket id; tenantID uuid.Nil reads any store's ticket (admin).
func readTicket(ctx context.Context, q db.Querier, tenantID, id uuid.UUID, forUpdate bool) (Ticket, error) {
	query := `SELECT ` + ticketColumns + ` FROM support_tickets WHERE id = $1 AND ($2 = '00000000-0000-0000-0000-000000000000'::uuid OR tenant_id = $2)`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var t Ticket
	err := sqlx.GetContext(ctx, q, &t, query, id, tenantID)
	if db.IsNoRows(err) {
		return Ticket{}, errTicketNotFound()
	}
	if err != nil {
		return Ticket{}, fmt.Errorf("support: read ticket: %w", err)
	}
	return t, nil
}

func ticketMessages(ctx context.Context, q db.Querier, ticketID uuid.UUID) ([]Message, error) {
	out := []Message{}
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT `+messageColumns+` FROM support_ticket_messages WHERE ticket_id = $1 ORDER BY created_at, id`, ticketID); err != nil {
		return nil, fmt.Errorf("support: list messages: %w", err)
	}
	return out, nil
}

// checkAttachments requires every id to be a file of the ticket in the tenant of q.
func checkAttachments(ctx context.Context, q db.Querier, ticketID uuid.UUID, attachmentIDs []uuid.UUID) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	list := make([]string, len(attachmentIDs))
	for i, id := range attachmentIDs {
		list[i] = id.String()
	}
	var n int
	if err := q.QueryRowxContext(ctx, `
		SELECT count(*) FROM attachments
		WHERE id = ANY($1::uuid[]) AND owner_type = $2 AND owner_id = $3`, list, AttachmentOwner, ticketID).Scan(&n); err != nil {
		return fmt.Errorf("support: check attachments: %w", err)
	}
	if n != len(uniqueIDs(attachmentIDs)) {
		return errInvalid(map[string]string{"attachment_ids": "not_found"})
	}
	return nil
}

func uniqueIDs(in []uuid.UUID) []uuid.UUID {
	out := []uuid.UUID{}
	for _, id := range in {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func ticketSnapshot(t Ticket) map[string]any {
	return map[string]any{"number": t.Number, "status": t.Status, "priority": t.Priority, "assigned_admin_id": t.AssignedAdminID}
}

// CreateTicket opens a ticket with its first message; the SLA comes from the store's plan.
func (s *Service) CreateTicket(ctx context.Context, actor audit.Actor, in NewTicket) (TicketView, error) {
	if err := requireOwner(actor); err != nil {
		return TicketView{}, err
	}
	subject := strings.TrimSpace(in.Subject)
	fields := map[string]string{}
	if subject == "" || utf8.RuneCountInString(subject) > 200 {
		fields["subject"] = "invalid"
	}
	if in.Priority == "" {
		in.Priority = PriorityNormal
	}
	if !slices.Contains(ticketPriorities, in.Priority) {
		fields["priority"] = "invalid"
	}
	if len(fields) > 0 {
		return TicketView{}, errInvalid(fields)
	}
	msg := NewMessage{Body: in.Body, AcknowledgeWarning: in.AcknowledgeWarning}
	if err := msg.validate(); err != nil {
		return TicketView{}, err
	}
	if !in.AcknowledgeWarning && LooksLikePassword(subject) {
		return TicketView{}, errPossiblePassword()
	}
	now := s.clock.Now()
	var out TicketView
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		t := Ticket{TenantID: actor.TenantID, Subject: subject, Status: TicketOpen, Priority: in.Priority, CreatedAt: now, UpdatedAt: now}
		if s.sla != nil {
			hours, err := s.sla.SupportSLAHours(ctx, q, actor.TenantID)
			if err != nil {
				return err
			}
			due := now.Add(time.Duration(hours) * time.Hour)
			t.SLADueAt = &due
		}
		if err := insertTicket(ctx, q, &t, now); err != nil {
			return err
		}
		m := Message{ID: ids.New(), TicketID: t.ID, AuthorType: AuthorOwner, AuthorID: actor.ID,
			Body: strings.TrimSpace(in.Body), AttachmentIDs: db.JSONList[uuid.UUID]{}, CreatedAt: now}
		if err := insertMessage(ctx, q, m); err != nil {
			return err
		}
		out = TicketView{Ticket: t, Messages: []Message{m}}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "support_ticket.create", EntityType: "support_ticket", EntityID: t.ID, DocNo: t.Number,
			After: ticketSnapshot(t),
		})
	})
	return out, err
}

// Tickets lists the store's own tickets, newest first.
func (s *Service) Tickets(ctx context.Context, tenantID uuid.UUID) ([]Ticket, error) {
	out := []Ticket{}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT `+ticketColumns+` FROM support_tickets WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT 500`, tenantID)
	})
	if err != nil {
		return nil, fmt.Errorf("support: list tickets: %w", err)
	}
	return out, nil
}

// Ticket returns one of the store's tickets with its messages.
func (s *Service) Ticket(ctx context.Context, tenantID, id uuid.UUID) (TicketView, error) {
	if tenantID == uuid.Nil {
		return TicketView{}, errTicketNotFound()
	}
	return s.ticketView(ctx, tenantID, id)
}

func (s *Service) ticketView(ctx context.Context, tenantID, id uuid.UUID) (TicketView, error) {
	var out TicketView
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		t, err := readTicket(ctx, q, tenantID, id, false)
		if err != nil {
			return err
		}
		msgs, err := ticketMessages(ctx, q, t.ID)
		out = TicketView{Ticket: t, Messages: msgs}
		return err
	})
	return out, err
}

// AddOwnerMessage adds the owner's message; a ticket waiting for the customer or resolved opens
// again, a closed one answers 409 ticket_closed.
func (s *Service) AddOwnerMessage(ctx context.Context, actor audit.Actor, ticketID uuid.UUID, in NewMessage) (Message, error) {
	if err := requireOwner(actor); err != nil {
		return Message{}, err
	}
	if err := in.validate(); err != nil {
		return Message{}, err
	}
	now := s.clock.Now()
	var out Message
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		t, err := readTicket(ctx, q, actor.TenantID, ticketID, true)
		if err != nil {
			return err
		}
		if t.Status == TicketClosed {
			return errTicketClosed()
		}
		if err := checkAttachments(ctx, q, t.ID, in.AttachmentIDs); err != nil {
			return err
		}
		out = Message{ID: ids.New(), TicketID: t.ID, AuthorType: AuthorOwner, AuthorID: actor.ID,
			Body: strings.TrimSpace(in.Body), AttachmentIDs: uniqueIDs(in.AttachmentIDs), CreatedAt: now}
		if err := insertMessage(ctx, q, out); err != nil {
			return err
		}
		status := t.Status
		if status == TicketWaitingCustomer || status == TicketResolved {
			status = TicketOpen
		}
		if _, err := q.ExecContext(ctx, `UPDATE support_tickets SET status = $2, updated_at = $3 WHERE id = $1`,
			t.ID, status, now); err != nil {
			return fmt.Errorf("support: update ticket: %w", err)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "support_ticket.message", EntityType: "support_ticket", EntityID: t.ID, DocNo: t.Number,
			After: map[string]any{"message_id": out.ID, "status": status, "attachments": len(out.AttachmentIDs)},
		})
	})
	return out, err
}

// TicketFilter narrows the admin ticket list.
type TicketFilter struct {
	Status   string
	TenantID *uuid.UUID
}

// AdminTicket is a ticket in the admin list with its store's slug and name.
type AdminTicket struct {
	Ticket
	TenantSlug  string     `db:"tenant_slug"`
	TenantName  string     `db:"tenant_name"`
	LastMessage *time.Time `db:"last_message_at"`
}

// AdminTicketCursor is the keyset cursor of an admin ticket row: its SLA due time (empty when it
// has none), its creation time and its id.
func AdminTicketCursor(t AdminTicket) httpx.Cursor {
	due := ""
	if t.SLADueAt != nil {
		due = t.SLADueAt.UTC().Format(time.RFC3339Nano)
	}
	return httpx.Cursor{SortKey: due + "|" + t.CreatedAt.UTC().Format(time.RFC3339Nano), ID: t.ID}
}

// ticketsAfter is the keyset condition of the admin ticket list after cur, in the order
// sla_due_at NULLS LAST, created_at, id, with its values bound from $4 on.
func ticketsAfter(cur *httpx.Cursor) (string, []any, error) {
	if cur == nil {
		return "true", nil, nil
	}
	invalid := apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
	rawDue, rawCreated, ok := strings.Cut(cur.SortKey, "|")
	if !ok {
		return "", nil, invalid
	}
	created, err := time.Parse(time.RFC3339Nano, rawCreated)
	if err != nil {
		return "", nil, invalid
	}
	if rawDue == "" {
		return "t.sla_due_at IS NULL AND (t.created_at, t.id) > ($4, $5)", []any{created, cur.ID}, nil
	}
	due, err := time.Parse(time.RFC3339Nano, rawDue)
	if err != nil {
		return "", nil, invalid
	}
	return `(t.sla_due_at > $4 OR t.sla_due_at IS NULL OR (t.sla_due_at = $4 AND (t.created_at, t.id) > ($5, $6)))`,
		[]any{due, created, cur.ID}, nil
}

// AdminTickets lists one page of the tickets of every store except replaced sandboxes, oldest SLA
// first and tickets without an SLA last; it reads page.FetchLimit rows.
func (s *Service) AdminTickets(ctx context.Context, f TicketFilter, page httpx.Page) ([]AdminTicket, error) {
	if f.Status != "" && !slices.Contains(ticketStatuses, f.Status) {
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	after, afterArgs, err := ticketsAfter(page.After)
	if err != nil {
		return nil, err
	}
	args := append([]any{f.Status, f.TenantID, page.FetchLimit()}, afterArgs...)
	out := []AdminTicket{}
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT t.id, t.tenant_id, t.number, t.subject, t.status, t.priority, t.sla_due_at, t.assigned_admin_id,
			  t.created_at, t.updated_at, tn.slug AS tenant_slug, tn.display_name AS tenant_name,
			  (SELECT max(m.created_at) FROM support_ticket_messages m WHERE m.ticket_id = t.id) AS last_message_at
			FROM support_tickets t JOIN tenants tn ON tn.id = t.tenant_id
			WHERE tn.status <> 'replaced' AND ($1 = '' OR t.status = $1) AND ($2::uuid IS NULL OR t.tenant_id = $2)
			  AND `+after+`
			ORDER BY t.sla_due_at NULLS LAST, t.created_at, t.id LIMIT $3`, args...)
	})
	if err != nil {
		return nil, fmt.Errorf("support: list admin tickets: %w", err)
	}
	return out, nil
}

// AdminTicket returns any store's ticket with its messages.
func (s *Service) AdminTicket(ctx context.Context, id uuid.UUID) (TicketView, error) {
	return s.ticketView(ctx, uuid.Nil, id)
}

// TicketPatch is an admin's change of a ticket; nil fields stay.
type TicketPatch struct {
	Status          *string
	Priority        *string
	AssignedAdminID *uuid.UUID
	Unassign        bool
}

// adminActor is the audit actor of a platform admin acting on a store.
func adminActor(tenantID, adminID uuid.UUID, requestID, ip string) audit.Actor {
	return audit.Actor{TenantID: tenantID, Type: audit.ActorPlatformAdmin, ID: adminID, Source: audit.SourceWeb,
		RequestID: requestID, IP: ip}
}

// AdminRequest identifies the admin and the request acting on a ticket.
type AdminRequest struct {
	AdminID   uuid.UUID
	RequestID string
	IP        string
}

// withTicketTenant runs fn in the tenant transaction of the ticket's store (the admin's change
// and its audit row commit together), with the ticket row locked.
func (s *Service) withTicketTenant(ctx context.Context, ticketID uuid.UUID, fn func(q db.Querier, t Ticket) error) error {
	var tenantID uuid.UUID
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			SELECT t.tenant_id FROM support_tickets t JOIN tenants tn ON tn.id = t.tenant_id
			WHERE t.id = $1 AND tn.status <> 'replaced'`, ticketID).Scan(&tenantID)
		if db.IsNoRows(err) {
			return errTicketNotFound()
		}
		return err
	})
	if err != nil {
		return err
	}
	return s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		t, err := readTicket(ctx, q, tenantID, ticketID, true)
		if err != nil {
			return err
		}
		return fn(q, t)
	})
}

// PatchTicket changes a ticket's status, priority or assigned admin.
func (s *Service) PatchTicket(ctx context.Context, r AdminRequest, ticketID uuid.UUID, p TicketPatch) (Ticket, error) {
	fields := map[string]string{}
	if p.Status != nil && !slices.Contains(ticketStatuses, *p.Status) {
		fields["status"] = "invalid"
	}
	if p.Priority != nil && !slices.Contains(ticketPriorities, *p.Priority) {
		fields["priority"] = "invalid"
	}
	if len(fields) > 0 {
		return Ticket{}, errInvalid(fields)
	}
	var out Ticket
	err := s.withTicketTenant(ctx, ticketID, func(q db.Querier, t Ticket) error {
		before := ticketSnapshot(t)
		if p.Status != nil {
			t.Status = *p.Status
		}
		if p.Priority != nil {
			t.Priority = *p.Priority
		}
		if p.Unassign {
			t.AssignedAdminID = nil
		} else if p.AssignedAdminID != nil {
			var active bool
			err := q.QueryRowxContext(ctx, `SELECT status = 'active' FROM platform_admins WHERE id = $1`, *p.AssignedAdminID).Scan(&active)
			if db.IsNoRows(err) || (err == nil && !active) {
				return errInvalid(map[string]string{"assigned_admin_id": "not_found"})
			}
			if err != nil {
				return fmt.Errorf("support: read admin: %w", err)
			}
			t.AssignedAdminID = p.AssignedAdminID
		}
		t.UpdatedAt = s.clock.Now()
		if _, err := q.ExecContext(ctx, `
			UPDATE support_tickets SET status = $2, priority = $3, assigned_admin_id = $4, updated_at = $5 WHERE id = $1`,
			t.ID, t.Status, t.Priority, t.AssignedAdminID, t.UpdatedAt); err != nil {
			return fmt.Errorf("support: update ticket: %w", err)
		}
		out = t
		return s.audit.Record(ctx, q, adminActor(t.TenantID, r.AdminID, r.RequestID, r.IP), audit.Event{
			Action: "support_ticket.update", EntityType: "support_ticket", EntityID: t.ID, DocNo: t.Number,
			Before: before, After: ticketSnapshot(t),
		})
	})
	return out, err
}

// AddAdminMessage answers a ticket; it then waits for the customer. Support never asks for a
// password, so an admin message that looks like it does is refused outright.
func (s *Service) AddAdminMessage(ctx context.Context, r AdminRequest, ticketID uuid.UUID, body string) (Message, error) {
	if !validBody(body) {
		return Message{}, errInvalid(map[string]string{"body": "invalid"})
	}
	if LooksLikePassword(body) {
		return Message{}, errPossiblePassword()
	}
	now := s.clock.Now()
	var out Message
	err := s.withTicketTenant(ctx, ticketID, func(q db.Querier, t Ticket) error {
		if t.Status == TicketClosed {
			return errTicketClosed()
		}
		out = Message{ID: ids.New(), TicketID: t.ID, AuthorType: AuthorAdmin, AuthorID: r.AdminID,
			Body: strings.TrimSpace(body), AttachmentIDs: db.JSONList[uuid.UUID]{}, CreatedAt: now}
		if err := insertMessage(ctx, q, out); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE support_tickets SET status = $2, updated_at = $3 WHERE id = $1`,
			t.ID, TicketWaitingCustomer, now); err != nil {
			return fmt.Errorf("support: update ticket: %w", err)
		}
		return s.audit.Record(ctx, q, adminActor(t.TenantID, r.AdminID, r.RequestID, r.IP), audit.Event{
			Action: "support_ticket.reply", EntityType: "support_ticket", EntityID: t.ID, DocNo: t.Number,
			After: map[string]any{"message_id": out.ID, "status": TicketWaitingCustomer},
		})
	})
	return out, err
}
