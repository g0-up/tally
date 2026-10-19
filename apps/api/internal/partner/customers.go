package partner

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// CustomerInput is the customer form. PartyID (create only) gives an existing party — a
// supplier — a customer profile instead of creating a new party; the party fields must then be
// left out. Status is set on update only; a new customer is active.
type CustomerInput struct {
	PartyInput
	PartyID          *uuid.UUID
	CustomerGroupID  *uuid.UUID
	CreditLimit      Opt[money.Money]
	PaymentTermsDays *int
	Status           *string
}

// CustomerResult is a saved customer with the parties it may duplicate.
type CustomerResult struct {
	Customer Customer
	Warnings []DuplicateWarning
}

// Customers lists customers by name.
func (s *Service) Customers(ctx context.Context, scope Scope, f CustomerFilter) ([]Customer, error) {
	if f.Status != "" && !validStatus(f.Status) {
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	patterns, err := searchPatterns(f.Q)
	if err != nil {
		return nil, err
	}
	var out []Customer
	err = s.read(ctx, scope, func(q db.Querier) error {
		out, err = listCustomers(ctx, q, f, patterns)
		return err
	})
	return out, err
}

// Customer reads one customer.
func (s *Service) Customer(ctx context.Context, scope Scope, id uuid.UUID) (Customer, error) {
	var out Customer
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = readCustomer(ctx, q, id)
		return err
	})
	return out, err
}

// checkGroup resolves the group of a customer form: the given one, which must exist and be
// active, or the built-in retail group.
func checkGroup(ctx context.Context, q db.Querier, id *uuid.UUID) (uuid.UUID, error) {
	if id == nil {
		return defaultGroupID(ctx, q)
	}
	g, err := readGroup(ctx, q, *id, false)
	if err != nil {
		if isNotFound(err) {
			return uuid.Nil, errInvalid(map[string]string{"customer_group_id": "not_found"})
		}
		return uuid.Nil, err
	}
	if !g.Active {
		return uuid.Nil, errGroupInactive()
	}
	return g.ID, nil
}

// ScopeCustomerCreate is the idempotency scope of CreateCustomer.
const ScopeCustomerCreate = "customer.create"

// CreateCustomer adds a customer: a new party, or a customer profile for an existing supplier. A
// key replays the first result of the request it identifies.
func (s *Service) CreateCustomer(ctx context.Context, actor audit.Actor, key *idempotency.Key, in CustomerInput) (CustomerResult, bool, error) {
	f := fieldErrors{}
	var p Party
	if in.PartyID != nil {
		for _, name := range in.given() {
			f[name] = "not_allowed_with_party_id"
		}
	} else {
		in.apply(&p, f, true)
	}
	if in.Status != nil {
		f["status"] = "not_allowed"
	}
	f.creditLimit("credit_limit", in.CreditLimit.Value)
	terms := 0
	if in.PaymentTermsDays != nil {
		terms = *in.PaymentTermsDays
		f.terms("payment_terms_days", terms)
	}
	if err := f.err(); err != nil {
		return CustomerResult{}, false, err
	}
	var out CustomerResult
	var replayed bool
	err := s.write(ctx, actor, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeCustomerCreate, key, http.StatusCreated, &out, func() error {
			groupID, err := checkGroup(ctx, q, in.CustomerGroupID)
			if err != nil {
				return err
			}
			now := s.clock.Now()
			if in.PartyID != nil {
				if p, err = readParty(ctx, q, *in.PartyID, true); err != nil {
					if isNotFound(err) {
						return errInvalid(map[string]string{"party_id": "not_found"})
					}
					return err
				}
				if _, err := readCustomer(ctx, q, p.ID); err == nil {
					return errAlreadyCustomer()
				} else if !isNotFound(err) {
					return err
				}
				// The supplier now reads is_customer = true: move the shared version.
				if _, err := updateParty(ctx, q, p, p.Version, now); err != nil {
					return err
				}
			} else {
				p.ID, p.CreatedAt, p.UpdatedAt = ids.New(), now, now
				p.SearchNorm = partySearchNorm(p, nil)
				if err := insertParty(ctx, q, actor.TenantID, p); err != nil {
					return err
				}
				if out.Warnings, err = s.dedupe.Check(ctx, q, p.ID, dedupeKeysOf(p)); err != nil {
					return err
				}
			}
			if _, err := q.ExecContext(ctx, `
				INSERT INTO customer_profiles (tenant_id, party_id, customer_group_id, credit_limit, payment_terms_days, status,
				                               created_at, updated_at, version)
				VALUES ($1, $2, $3, $4, $5, 'active', $6, $6, 1)`,
				actor.TenantID, p.ID, groupID, in.CreditLimit.Value, terms, now); err != nil {
				return fmt.Errorf("partner: insert customer profile: %w", err)
			}
			if out.Customer, err = readCustomer(ctx, q, p.ID); err != nil {
				return err
			}
			return s.record(ctx, q, actor, "customer.create", "customer", p.ID, nil, out.Customer)
		})
		return err
	})
	return out, replayed, err
}

// UpdateCustomer changes a customer under the optimistic version of its party.
func (s *Service) UpdateCustomer(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in CustomerInput) (CustomerResult, error) {
	f := fieldErrors{}
	if in.PartyID != nil {
		f["party_id"] = "not_allowed"
	}
	if in.Status != nil && !validStatus(*in.Status) {
		f["status"] = "invalid"
	}
	f.creditLimit("credit_limit", in.CreditLimit.Value)
	if in.PaymentTermsDays != nil {
		f.terms("payment_terms_days", *in.PaymentTermsDays)
	}
	if in.CustomerGroupID != nil && *in.CustomerGroupID == uuid.Nil {
		f["customer_group_id"] = "required"
	}
	if err := f.err(); err != nil {
		return CustomerResult{}, err
	}
	var out CustomerResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := readParty(ctx, q, id, true); err != nil {
			return err
		}
		before, err := readCustomer(ctx, q, id)
		if err != nil {
			return err
		}
		p := before.Party
		keysChanged := in.apply(&p, f, false)
		if err := f.err(); err != nil {
			return err
		}
		prof := before.Profile
		if in.CustomerGroupID != nil && *in.CustomerGroupID != prof.CustomerGroupID {
			if prof.CustomerGroupID, err = checkGroup(ctx, q, in.CustomerGroupID); err != nil {
				return err
			}
		}
		if in.CreditLimit.Set {
			prof.CreditLimit = in.CreditLimit.Value
		}
		if in.PaymentTermsDays != nil {
			prof.PaymentTermsDays = *in.PaymentTermsDays
		}
		if in.Status != nil {
			prof.Status = *in.Status
		}
		note, err := suppliedGoodsNote(ctx, q, id)
		if err != nil {
			return err
		}
		p.SearchNorm = partySearchNorm(p, note)
		now := s.clock.Now()
		if _, err := updateParty(ctx, q, p, expected, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE customer_profiles
			SET customer_group_id = $2, credit_limit = $3, payment_terms_days = $4, status = $5,
			    updated_at = $6, version = version + 1
			WHERE party_id = $1`,
			id, prof.CustomerGroupID, prof.CreditLimit, prof.PaymentTermsDays, prof.Status, now); err != nil {
			return fmt.Errorf("partner: update customer profile: %w", err)
		}
		if keysChanged {
			if out.Warnings, err = s.dedupe.Check(ctx, q, id, dedupeKeysOf(p)); err != nil {
				return err
			}
		}
		if out.Customer, err = readCustomer(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "customer.update", "customer", id, before, out.Customer)
	})
	return out, err
}

// DeleteCustomer removes a customer nothing references; the party goes too unless it is also a
// supplier. A customer in use can only be marked inactive.
func (s *Service) DeleteCustomer(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		p, err := readParty(ctx, q, id, true)
		if err != nil {
			return err
		}
		before, err := readCustomer(ctx, q, id)
		if err != nil {
			return err
		}
		inUse, err := s.usage.CustomerInUse(ctx, q, id)
		if err != nil {
			return err
		}
		if inUse {
			return errCustomerInUse()
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM customer_profiles WHERE party_id = $1`, id); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errCustomerInUse()
			}
			return fmt.Errorf("partner: delete customer profile: %w", err)
		}
		if before.IsSupplier {
			// The supplier now reads is_customer = false: move the shared version.
			if _, err := updateParty(ctx, q, p, p.Version, s.clock.Now()); err != nil {
				return err
			}
		} else if err := deletePartyIfOrphan(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "customer.delete", "customer", id, before, nil)
	})
}
