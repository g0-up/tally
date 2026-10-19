package partner

import (
	"context"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// UsageReader answers whether a partner is referenced by records of other modules, which decides
// between delete and deactivate. Until the procurement, sales, debt, pricing and CRM modules
// provide their own readers, SQLUsageReader answers with read-only queries on their tables.
type UsageReader interface {
	// SupplierInUse reports whether a purchase document, a payable or an outgoing payment
	// references the party as a supplier.
	SupplierInUse(ctx context.Context, q db.Querier, partyID uuid.UUID) (bool, error)
	// SupplierHasOpenOrders reports whether a confirmed purchase order of the party still waits
	// for goods, which blocks stopping the supplier.
	SupplierHasOpenOrders(ctx context.Context, q db.Querier, partyID uuid.UUID) (bool, error)
	// CustomerInUse reports whether a sales document, a receivable, an incoming payment, a price
	// list or a CRM record references the party as a customer.
	CustomerInUse(ctx context.Context, q db.Querier, partyID uuid.UUID) (bool, error)
	// CustomerGroupInUse reports whether a customer, a price list or a promotion references the
	// group.
	CustomerGroupInUse(ctx context.Context, q db.Querier, groupID uuid.UUID) (bool, error)
}
