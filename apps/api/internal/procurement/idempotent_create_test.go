package procurement

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A create run with an Idempotency-Key stores its result as JSON and replays it decoded: the
// replay must answer the same body and the same ETag as the first response.
func TestCreatedDocumentsReplayUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[OrderDetail](t), func(o OrderDetail) any {
		return []any{toOrderDetailDTO(o), o.Version}
	})
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[DocumentDetail](t), func(d DocumentDetail) any {
		return []any{toDocumentDTO(d), d.Version}
	})
}
