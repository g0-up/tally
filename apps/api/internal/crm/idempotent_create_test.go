package crm

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A consent or re-order draft recorded with an Idempotency-Key is stored as JSON and replayed
// decoded: the replay must answer the same body as the first response.
func TestRecordedCareResultsReplayUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Consent](t), toConsentDTO)
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Reorder](t), toReorderDTO)
}
