package pricing

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A sale price set with an Idempotency-Key stores its changes as JSON and replays them decoded:
// the replay must answer the same body as the first response.
func TestSalePriceChangesReplayUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[[]PriceChange](t), func(out []PriceChange) any {
		return listOf(mapSlice(out, toPriceChangeDTO))
	})
}
