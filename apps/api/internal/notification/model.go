// Package notification is the in-app notification centre. Producers registered in a
// ProducerRegistry describe the conditions that deserve the owner's attention; the daily Sweeper
// and the outbox consumers turn them into one notification per dedupe key, and resolve the
// notification when its condition clears.
package notification

import (
	"time"

	"github.com/google/uuid"
)

// Kinds of notification produced.
const (
	KindReceivableOverdue  = "receivable_overdue"
	KindPayableDueSoon     = "payable_due_soon"
	KindLotExpiring        = "lot_expiring"
	KindLotExpired         = "lot_expired"
	KindBelowMinStock      = "below_min_stock"
	KindSurplusStock       = "surplus_stock"
	KindPOSSyncException   = "pos_sync_exception"
	KindStatementDisputed  = "statement_disputed"
	KindSubscriptionStatus = "subscription_status"
	KindExportReady        = "export_ready"
)

// Severities, from least to most urgent.
const (
	SeverityInfo = "info"
	SeverityWarn = "warn"
	SeverityBad  = "bad"
)

// Who resolved a notification.
const (
	ResolvedByUser   = "user"
	ResolvedBySystem = "system"
)

// Inbox states a list can filter on.
const (
	// StateActive is every unresolved notification that is not snoozed (the default).
	StateActive = "active"
	// StateUnread is the active notifications not read yet.
	StateUnread   = "unread"
	StateSnoozed  = "snoozed"
	StateResolved = "resolved"
	StateAll      = "all"
)

func validState(s string) bool {
	switch s {
	case StateActive, StateUnread, StateSnoozed, StateResolved, StateAll:
		return true
	}
	return false
}

// Notice is a condition a producer raises: one notification per DedupeKey.
type Notice struct {
	Kind       string
	DedupeKey  string
	Severity   string
	Title      string
	Body       *string
	EntityType *string
	EntityID   *uuid.UUID
	// ColorID is set on lot notices: the same SKU in two colors is two different lots.
	ColorID *uuid.UUID
	// ActionPath is the web route that opens the subject; nil when there is nothing to open.
	ActionPath *string
	// DocNo is the number of the document the notice is about, when it is about one.
	DocNo *string
}

// Notification is a row of the notification centre.
type Notification struct {
	ID           uuid.UUID  `db:"id"`
	Kind         string     `db:"kind"`
	DedupeKey    string     `db:"dedupe_key"`
	Severity     string     `db:"severity"`
	Title        string     `db:"title"`
	Body         *string    `db:"body"`
	EntityType   *string    `db:"entity_type"`
	EntityID     *uuid.UUID `db:"entity_id"`
	ColorID      *uuid.UUID `db:"color_id"`
	ActionPath   *string    `db:"action_path"`
	DocNo        *string    `db:"doc_no"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
	ReadAt       *time.Time `db:"read_at"`
	ResolvedAt   *time.Time `db:"resolved_at"`
	ResolvedBy   *string    `db:"resolved_by"`
	SnoozedUntil *time.Time `db:"snoozed_until"`
}

const notificationColumns = `id, kind, dedupe_key, severity, title, body, entity_type, entity_id, color_id, action_path, doc_no,
	created_at, updated_at, read_at, resolved_at, resolved_by, snoozed_until`

// key builds the dedupe key of a kind and subject. Keys carry no date: a condition that lasts
// several days stays one notification.
func key(kind string, subject uuid.UUID) string { return kind + ":" + subject.String() }

func ptr[T any](v T) *T { return &v }
