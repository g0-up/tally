package support

import (
	"time"

	"github.com/google/uuid"
)

// TicketDTO is a support ticket.
type TicketDTO struct {
	ID              uuid.UUID  `json:"id"`
	Number          string     `json:"number"`
	Subject         string     `json:"subject"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	SLADueAt        *time.Time `json:"sla_due_at"`
	AssignedAdminID *uuid.UUID `json:"assigned_admin_id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toTicketDTO(t Ticket) TicketDTO {
	return TicketDTO{
		ID: t.ID, Number: t.Number, Subject: t.Subject, Status: t.Status, Priority: t.Priority, SLADueAt: utcPtr(t.SLADueAt),
		AssignedAdminID: t.AssignedAdminID, CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	}
}

// MessageDTO is a ticket message.
type MessageDTO struct {
	ID            uuid.UUID   `json:"id"`
	AuthorType    string      `json:"author_type"`
	AuthorID      uuid.UUID   `json:"author_id"`
	Body          string      `json:"body"`
	AttachmentIDs []uuid.UUID `json:"attachment_ids"`
	CreatedAt     time.Time   `json:"created_at"`
}

func toMessageDTO(m Message) MessageDTO {
	return MessageDTO{
		ID: m.ID, AuthorType: m.AuthorType, AuthorID: m.AuthorID, Body: m.Body,
		AttachmentIDs: append([]uuid.UUID{}, m.AttachmentIDs...), CreatedAt: m.CreatedAt.UTC(),
	}
}

type ticketViewDTO struct {
	TicketDTO
	Messages []MessageDTO `json:"messages"`
}

func toTicketViewDTO(v TicketView) ticketViewDTO {
	out := ticketViewDTO{TicketDTO: toTicketDTO(v.Ticket), Messages: make([]MessageDTO, len(v.Messages))}
	for i, m := range v.Messages {
		out.Messages[i] = toMessageDTO(m)
	}
	return out
}

type adminTicketDTO struct {
	TicketDTO
	TenantID      uuid.UUID  `json:"tenant_id"`
	TenantSlug    string     `json:"tenant_slug"`
	TenantName    string     `json:"tenant_name"`
	LastMessageAt *time.Time `json:"last_message_at"`
}

type adminTicketViewDTO struct {
	ticketViewDTO
	TenantID uuid.UUID `json:"tenant_id"`
}

// SessionDTO is a support session with its state now.
type SessionDTO struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	AdminID   uuid.UUID  `json:"admin_id"`
	TicketID  *uuid.UUID `json:"ticket_id"`
	Scope     string     `json:"scope"`
	Modules   []string   `json:"modules"`
	State     string     `json:"state"`
	GrantedAt time.Time  `json:"granted_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// AdminSessionDTO is a support session in the admin list, with the store it opens.
type AdminSessionDTO struct {
	SessionDTO
	TenantSlug string `json:"tenant_slug"`
	TenantName string `json:"tenant_name"`
}

func toAdminSessionDTO(s AdminSession, now time.Time) AdminSessionDTO {
	return AdminSessionDTO{SessionDTO: toSessionDTO(s.Session, now), TenantSlug: s.TenantSlug, TenantName: s.TenantName}
}

func toSessionDTO(s Session, now time.Time) SessionDTO {
	return SessionDTO{
		ID: s.ID, TenantID: s.TenantID, AdminID: s.AdminID, TicketID: s.TicketID, Scope: s.Scope,
		Modules: append([]string{}, s.Modules...), State: s.State(now), GrantedAt: s.GrantedAt.UTC(),
		ExpiresAt: s.ExpiresAt.UTC(), RevokedAt: utcPtr(s.RevokedAt),
	}
}

type createTicketRequest struct {
	Subject            string `json:"subject" validate:"required,max=200"`
	Body               string `json:"body" validate:"required,max=10000"`
	Priority           string `json:"priority" validate:"omitempty,oneof=low normal high urgent"`
	AcknowledgeWarning bool   `json:"acknowledge_warning"`
}

type messageRequest struct {
	Body               string      `json:"body" validate:"required,max=10000"`
	AttachmentIDs      []uuid.UUID `json:"attachment_ids" validate:"max=10"`
	AcknowledgeWarning bool        `json:"acknowledge_warning"`
}

type grantSessionRequest struct {
	TicketID uuid.UUID `json:"ticket_id" validate:"required"`
	Modules  []string  `json:"modules" validate:"required,min=1,max=20"`
	Hours    int       `json:"hours" validate:"omitempty,min=1,max=8"`
}

type patchTicketRequest struct {
	Status          *string    `json:"status" validate:"omitempty,oneof=open waiting_customer in_progress resolved closed"`
	Priority        *string    `json:"priority" validate:"omitempty,oneof=low normal high urgent"`
	AssignedAdminID *uuid.UUID `json:"assigned_admin_id"`
	Unassign        bool       `json:"unassign"`
}

type adminMessageRequest struct {
	Body string `json:"body" validate:"required,max=10000"`
}
