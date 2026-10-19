package cash

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

const (
	maxNameLen      = 200
	maxBankFieldLen = 100
)

// maxOpeningBalance keeps an opening balance inside numeric(19,4).
var maxOpeningBalance = money.MustMoney("999999999999999")

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Onboarding is told when an opening balance is entered; nil skips it.
	Onboarding Onboarding
}

// Service implements the fund use cases: list funds and methods, edit a fund and its opening
// balance, and the balance and cash-flow queries.
type Service struct {
	db         *db.DB
	clock      clock.Clock
	audit      *audit.Recorder
	onboarding Onboarding
}

// NewService returns the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit, onboarding: d.Onboarding}
}

// Scope is the tenant a request acts on; ReadOnly runs reads under the read-only role (support).
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// Funds lists the funds with their current balance (cash first).
func (s *Service) Funds(ctx context.Context, scope Scope) ([]FundAccount, error) {
	var out []FundAccount
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = listFunds(ctx, q)
		return err
	})
	return out, err
}

// Fund returns one fund.
func (s *Service) Fund(ctx context.Context, scope Scope, id uuid.UUID) (FundAccount, error) {
	var out FundAccount
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = getFund(ctx, q, id, false)
		return err
	})
	return out, err
}

// Methods lists the payment methods with the kind of fund each pays into.
func (s *Service) Methods(ctx context.Context, scope Scope) ([]PaymentMethod, error) {
	var out []PaymentMethod
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = listMethods(ctx, q)
		return err
	})
	return out, err
}

// FundInput edits a fund. Nil or unset fields stay unchanged; a bank field set to null or blank
// is cleared.
type FundInput struct {
	Name           *string
	BankName       Opt[string]
	AccountNo      Opt[string]
	OpeningBalance *money.Money
	OpeningDate    Opt[time.Time]
}

// Opt is an optional PATCH field: Set reports that the field was sent, Value nil clears it.
type Opt[T any] struct {
	Set   bool
	Value *T
}

// UpdateFund edits a fund under its version (If-Match). The opening balance and its date may only
// change while the fund has no cash entry yet: afterwards the balance is history, corrected by
// documents. Entering an opening balance completes the opening_cash onboarding step.
func (s *Service) UpdateFund(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in FundInput) (FundAccount, error) {
	fields := map[string]string{}
	var name string
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		switch {
		case name == "":
			fields["name"] = "required"
		case utf8.RuneCountInString(name) > maxNameLen:
			fields["name"] = "too_long"
		}
	}
	bankName, bankErr := optText(in.BankName)
	if bankErr != "" {
		fields["bank_name"] = bankErr
	}
	accountNo, accErr := optText(in.AccountNo)
	if accErr != "" {
		fields["account_no"] = accErr
	}
	if in.OpeningBalance != nil && (in.OpeningBalance.IsNegative() || in.OpeningBalance.Cmp(maxOpeningBalance) > 0) {
		fields["opening_balance"] = "out_of_range"
	}
	if len(fields) > 0 {
		return FundAccount{}, errInvalid(fields)
	}
	var out FundAccount
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := getFund(ctx, q, id, true)
		if err != nil {
			return err
		}
		sets := []db.Set{}
		if in.Name != nil {
			sets = append(sets, db.Set{Column: "name", Value: name})
		}
		if in.BankName.Set || in.AccountNo.Set {
			if before.Kind != FundBank {
				f := map[string]string{}
				if in.BankName.Set {
					f["bank_name"] = "not_applicable"
				}
				if in.AccountNo.Set {
					f["account_no"] = "not_applicable"
				}
				return errInvalid(f)
			}
			if in.BankName.Set {
				sets = append(sets, db.Set{Column: "bank_name", Value: bankName})
			}
			if in.AccountNo.Set {
				sets = append(sets, db.Set{Column: "account_no", Value: accountNo})
			}
		}
		openingChanged := in.OpeningBalance != nil || in.OpeningDate.Set
		if openingChanged {
			has, err := fundHasEntries(ctx, q, id)
			if err != nil {
				return err
			}
			if has {
				return errFundHasEntries()
			}
			opening := before.OpeningBalance
			if in.OpeningBalance != nil {
				opening = *in.OpeningBalance
			}
			sets = append(sets, db.Set{Column: "opening_balance", Value: opening}, db.Set{Column: "balance", Value: opening})
			if in.OpeningDate.Set {
				sets = append(sets, db.Set{Column: "opening_date", Value: in.OpeningDate.Value})
			}
		}
		if len(sets) == 0 {
			if before.Version != version {
				return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
			}
			out = before
			return nil
		}
		now := s.clock.Now()
		if _, err := db.UpdateWithVersion(ctx, q, "fund_accounts", id, version, now, sets...); err != nil {
			return err
		}
		if out, err = getFund(ctx, q, id, false); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{
			Action: "fund_account.update", EntityType: "fund_account", EntityID: id, Before: before, After: out,
		}); err != nil {
			return err
		}
		if openingChanged && s.onboarding != nil {
			if err := s.onboarding.CompleteStep(ctx, q, actor.TenantID, StepOpeningCash, actor.ID, now); err != nil {
				return fmt.Errorf("cash: complete onboarding: %w", err)
			}
		}
		return nil
	})
	return out, err
}

// optText normalises an optional text field: blank clears it.
func optText(o Opt[string]) (*string, string) {
	if !o.Set || o.Value == nil {
		return nil, ""
	}
	v := strings.TrimSpace(*o.Value)
	if v == "" {
		return nil, ""
	}
	if utf8.RuneCountInString(v) > maxBankFieldLen {
		return nil, "too_long"
	}
	return &v, ""
}
