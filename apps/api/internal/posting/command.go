// Package posting runs every ledger-affecting command (receipts, stock-outs, payments …) through
// one fixed transaction template: replay of an already completed request → entitlement → period
// lock → idempotency claim → document number → row locks in the global order → Apply → audit →
// outbox → stored response → commit. Documents implement PostingCommand; they never open
// transactions or wire kernel services themselves.
package posting

import (
	"context"
	"encoding/json"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
)

// Actor is who posts, in which tenant, through which request.
type Actor = audit.Actor

// PostingCommand is one document action. The name is the design's ubiquitous term that every
// domain refers to, so the package-name stutter is intended.
type PostingCommand interface { //nolint:revive // see above
	// Scope is the idempotency scope, e.g. "receipt.post".
	Scope() string
	// BusinessDate is the accounting date checked against the period lock.
	BusinessDate() time.Time
	// DocPrefix is the document number prefix ("PN", "BH" …) or "" when no number is issued.
	DocPrefix() string
	// LockKeys lists the rows to lock before Apply; the coordinator orders them.
	LockKeys() LockSet
	// Apply performs the command inside the posting transaction.
	Apply(ctx context.Context, tx Tx) (Result, error)
}

// ExtraDocs is implemented by a PostingCommand whose Apply writes further numbered documents
// besides its own: a stock out that takes the customer's money writes a PT next to its BH, an
// exchange pays back with a PC and takes with a PT. Issuing a number holds the doc_sequences row of
// its prefix until commit, and doc_sequences comes first in the global lock order, so every number
// a posting writes must be issued before its row locks — never from inside Apply.
//
// The coordinator issues the DocPrefix number and one number per ExtraDocPrefixes entry in its
// document number step, prefixes in ascending order (so two postings never wait on each other's
// sequences), and Apply takes each with TakeDocNo; debt.PaymentRecorder takes the PT/PC of the
// payment it records this way (debt.PaymentInput.DocPrefix gives it). List a prefix once per
// document Apply writes, e.g. {"PT", "PT"} for two receipts. A posting that leaves a reserved
// number untaken fails: the number cannot be handed back, and the sequence stays gap-free.
type ExtraDocs interface {
	ExtraDocPrefixes() []string
}

// Result is what Apply returns: the HTTP status (default 201) and the response body, which is
// marshalled to JSON and stored for idempotent replays.
type Result struct {
	Status int
	Body   any
}

// Response is the outcome of Post.
type Response struct {
	Status int
	Body   json.RawMessage
	// Replayed is true when the response comes from an earlier request with the same key.
	Replayed bool
	// DocNo is the number issued by this request ("" for replays and commands without a prefix).
	DocNo string
}
