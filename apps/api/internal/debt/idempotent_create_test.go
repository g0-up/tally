package debt

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A statement or collection note created with an Idempotency-Key is stored as JSON and replayed
// decoded: the replay must answer the same body, and the statement the same ETag, as the first
// response.
func TestCreatedDebtDocumentsReplayUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Statement](t), func(s Statement) any {
		return []any{toStatementDTO(s), s.Version}
	})
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[CollectionNote](t), toCollectionNoteDTO)
}
