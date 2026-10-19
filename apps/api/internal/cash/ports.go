package cash

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// StepOpeningCash is the onboarding step completed when the owner enters the opening balance of a fund.
const StepOpeningCash = "opening_cash"

// Onboarding marks a step of the tenant's onboarding checklist done (implemented by the
// organisation domain, adapted in the composition root).
type Onboarding interface {
	CompleteStep(ctx context.Context, q db.Querier, tenantID uuid.UUID, step string, by uuid.UUID, at time.Time) error
}
