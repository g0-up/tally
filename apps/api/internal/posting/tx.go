package posting

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Tx is what a command sees inside the posting transaction: the tenant querier plus the request
// scoped kernel helpers. Audit and Publish are buffered and written right after Apply succeeds.
type Tx interface {
	db.Querier
	// Now is the posting timestamp (one value for the whole command).
	Now() time.Time
	// Actor is who posts.
	Actor() Actor
	// TenantID is the tenant of the transaction.
	TenantID() uuid.UUID
	// BusinessDate is the command's accounting date.
	BusinessDate() time.Time
	// DocNo is the document number issued for this command ("" without a prefix).
	DocNo() string
	// Audit queues an audit event (DocNo defaults to the issued number).
	Audit(evt audit.Event)
	// Publish queues an outbox event.
	Publish(evt outbox.Event)
}

type postingTx struct {
	db.Querier
	now          time.Time
	actor        Actor
	businessDate time.Time
	docNo        string
	// extraDocNos are the numbers issued for ExtraDocPrefixes and not taken yet, by prefix.
	extraDocNos map[string][]string
	audits      []audit.Event
	events      []outbox.Event
}

var _ Tx = (*postingTx)(nil)

func (t *postingTx) Now() time.Time          { return t.now }
func (t *postingTx) Actor() Actor            { return t.actor }
func (t *postingTx) TenantID() uuid.UUID     { return t.actor.TenantID }
func (t *postingTx) BusinessDate() time.Time { return t.businessDate }
func (t *postingTx) DocNo() string           { return t.docNo }

func (t *postingTx) Audit(evt audit.Event) {
	if evt.DocNo == "" {
		evt.DocNo = t.docNo
	}
	t.audits = append(t.audits, evt)
}

func (t *postingTx) Publish(evt outbox.Event) { t.events = append(t.events, evt) }

func (t *postingTx) takeDocNo(prefix string) (string, bool) {
	nos := t.extraDocNos[prefix]
	if len(nos) == 0 {
		return "", false
	}
	t.extraDocNos[prefix] = nos[1:]
	return nos[0], true
}

// untakenDocNos lists the reserved numbers Apply did not take.
func (t *postingTx) untakenDocNos() []string {
	var out []string
	for _, nos := range t.extraDocNos {
		out = append(out, nos...)
	}
	sort.Strings(out)
	return out
}

// TakeDocNo returns the next number the coordinator issued for prefix from the command's
// ExtraDocPrefixes (see ExtraDocs), in issue order. It fails when none is left: the command lists
// fewer documents of that prefix than Apply writes.
func TakeDocNo(tx Tx, prefix string) (string, error) {
	t, ok := tx.(interface{ takeDocNo(string) (string, bool) })
	if ok {
		if no, ok := t.takeDocNo(prefix); ok {
			return no, nil
		}
	}
	return "", fmt.Errorf("posting: no %s number left for this posting: list it in the command's ExtraDocPrefixes", prefix)
}
