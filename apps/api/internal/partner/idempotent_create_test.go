package partner

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A customer created with an Idempotency-Key is stored as JSON and replayed decoded: the replay
// must answer the same body, duplicate warnings included, and the same ETag as the first response.
func TestCreatedCustomerReplaysUnchanged(t *testing.T) {
	res := idempotencytest.Filled[CustomerResult](t)
	if len(res.Warnings) == 0 {
		t.Fatal("the sample result carries no duplicate warnings")
	}
	idempotencytest.AssertRoundTrip(t, res, func(r CustomerResult) any {
		return []any{toCustomerResultDTO(r), r.Customer.Version}
	})
}
