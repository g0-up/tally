package cash

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// SeedContext is what the seeder needs from the tenant provisioning (mapped from the
// organisation's seed context by the composition root).
type SeedContext struct {
	TenantID uuid.UUID
	Now      time.Time
}

// Seeder creates the two funds and two payment methods of a new tenant.
type Seeder struct{}

// NewSeeder returns the fund seeder.
func NewSeeder() Seeder { return Seeder{} }

// Default fund and method names, shown in the owner's language.
const (
	cashFundName       = "Tiền mặt tại cửa hàng"
	bankFundName       = "Tài khoản ngân hàng"
	cashMethodName     = "Tiền mặt"
	transferMethodName = "Chuyển khoản"
)

// SeedFunds creates the cash and bank funds and the cash and bank-transfer methods. A bank
// transfer requires verification: it waits in the verification queue until the owner sees the
// money arrive. Re-running it changes nothing.
func (Seeder) SeedFunds(ctx context.Context, q db.Querier, sc SeedContext) error {
	funds := []struct{ kind, name string }{{FundCash, cashFundName}, {FundBank, bankFundName}}
	for _, f := range funds {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO fund_accounts (id, tenant_id, kind, name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5)
			ON CONFLICT ON CONSTRAINT ux_fund_accounts__kind DO NOTHING`, ids.New(), sc.TenantID, f.kind, f.name, sc.Now); err != nil {
			return fmt.Errorf("cash: seed fund %s: %w", f.kind, err)
		}
	}
	methods := []struct {
		code, name, fund string
		verify           bool
	}{{MethodCash, cashMethodName, FundCash, false}, {MethodBankTransfer, transferMethodName, FundBank, true}}
	for _, m := range methods {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO payment_methods (id, tenant_id, code, name, fund_account_id, requires_verification, created_at)
			SELECT $1, $2, $3, $4, f.id, $6, $7 FROM fund_accounts f WHERE f.kind = $5
			ON CONFLICT ON CONSTRAINT ux_payment_methods__code DO NOTHING`,
			ids.New(), sc.TenantID, m.code, m.name, m.fund, m.verify, sc.Now); err != nil {
			return fmt.Errorf("cash: seed payment method %s: %w", m.code, err)
		}
	}
	return nil
}
