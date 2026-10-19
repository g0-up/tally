package saas

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency/idempotencytest"
)

// A plan change run with an Idempotency-Key stores the issued invoice as JSON and replays it
// decoded: the replay must answer the same body as the first response.
func TestIssuedInvoiceReplaysUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[Invoice](t), ToInvoiceDTO)
}

// A data export request run with an Idempotency-Key stores the queued job and replays it decoded.
func TestQueuedDataExportReplaysUnchanged(t *testing.T) {
	idempotencytest.AssertRoundTrip(t, idempotencytest.Filled[DataExport](t), func(d DataExport) DataExport { return d })
}
