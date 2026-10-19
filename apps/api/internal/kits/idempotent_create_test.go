package kits

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A create run with an Idempotency-Key stores its result as JSON and replays it decoded: the
// replay must answer the same body and the same ETag as the first response.
func TestCreatedDocumentsReplayUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Kit](t), func(k Kit) any { return []any{toKitDTO(k), k.Latest.Version} })
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[OrderDetail](t), func(o OrderDetail) any { return []any{orderDTO(o), o.Version} })
}
