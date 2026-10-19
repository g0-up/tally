package sales

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A create run with an Idempotency-Key stores its result as JSON and replays it decoded: the
// replay must answer the same body and the same ETag as the first response.
func TestCreatedDocumentsReplayUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Quote](t), func(q Quote) any { return []any{q, quoteVersion(q)} })
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Invoice](t), func(i Invoice) any { return []any{i, invoiceVersion(i)} })
}
