package partner

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// SupplierInput is the supplier form. PartyID (create only) gives an existing party — a
// customer — a supplier profile instead of creating a new party; the party fields must then be
// left out. The trading status changes through SetSupplierStatus only.
type SupplierInput struct {
	PartyInput
	PartyID           *uuid.UUID
	PaymentTermsDays  *int
	SuppliedGoodsNote Opt[string]
}

// SupplierResult is a saved supplier with the parties it may duplicate.
type SupplierResult struct {
	Supplier Supplier
	Warnings []DuplicateWarning
}

// Suppliers lists suppliers by name; the filter text also matches the supplied goods.
func (s *Service) Suppliers(ctx context.Context, scope Scope, f SupplierFilter) ([]Supplier, error) {
	if f.Status != "" && !validStatus(f.Status) {
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	patterns, err := searchPatterns(f.Q)
	if err != nil {
		return nil, err
	}
	var out []Supplier
	err = s.read(ctx, scope, func(q db.Querier) error {
		out, err = listSuppliers(ctx, q, f, patterns)
		return err
	})
	return out, err
}

// Supplier reads one supplier, whatever its trading status: the documents of a supplier that
// stopped trading stay readable.
func (s *Service) Supplier(ctx context.Context, scope Scope, id uuid.UUID) (Supplier, error) {
	var out Supplier
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = readSupplier(ctx, q, id)
		return err
	})
	return out, err
}

// CreateSupplier adds a supplier: a new party, or a supplier profile for an existing customer.
func (s *Service) CreateSupplier(ctx context.Context, actor audit.Actor, in SupplierInput) (SupplierResult, error) {
	f := fieldErrors{}
	var p Party
	if in.PartyID != nil {
		for _, name := range in.given() {
			f[name] = "not_allowed_with_party_id"
		}
	} else {
		in.apply(&p, f, true)
	}
	terms := 0
	if in.PaymentTermsDays != nil {
		terms = *in.PaymentTermsDays
		f.terms("payment_terms_days", terms)
	}
	note := f.multiline("supplied_goods_note", in.SuppliedGoodsNote.Value, maxNoteLen)
	if err := f.err(); err != nil {
		return SupplierResult{}, err
	}
	var out SupplierResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		now := s.clock.Now()
		var err error
		if in.PartyID != nil {
			if p, err = readParty(ctx, q, *in.PartyID, true); err != nil {
				if isNotFound(err) {
					return errInvalid(map[string]string{"party_id": "not_found"})
				}
				return err
			}
			if _, err := readSupplier(ctx, q, p.ID); err == nil {
				return errAlreadySupplier()
			} else if !isNotFound(err) {
				return err
			}
			// The supplied goods join the search text; the customer now reads is_supplier = true.
			p.SearchNorm = partySearchNorm(p, note)
			if _, err := updateParty(ctx, q, p, p.Version, now); err != nil {
				return err
			}
		} else {
			p.ID, p.CreatedAt, p.UpdatedAt = ids.New(), now, now
			p.SearchNorm = partySearchNorm(p, note)
			if err := insertParty(ctx, q, actor.TenantID, p); err != nil {
				return err
			}
			if out.Warnings, err = s.dedupe.Check(ctx, q, p.ID, dedupeKeysOf(p)); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO supplier_profiles (tenant_id, party_id, payment_terms_days, supplied_goods_note, status,
			                               created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, 'active', $5, $5, 1)`,
			actor.TenantID, p.ID, terms, note, now); err != nil {
			return fmt.Errorf("partner: insert supplier profile: %w", err)
		}
		if out.Supplier, err = readSupplier(ctx, q, p.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "supplier.create", "supplier", p.ID, nil, out.Supplier)
	})
	return out, err
}

// UpdateSupplier changes a supplier under the optimistic version of its party.
func (s *Service) UpdateSupplier(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in SupplierInput) (SupplierResult, error) {
	f := fieldErrors{}
	if in.PartyID != nil {
		f["party_id"] = "not_allowed"
	}
	if in.PaymentTermsDays != nil {
		f.terms("payment_terms_days", *in.PaymentTermsDays)
	}
	note := f.multiline("supplied_goods_note", in.SuppliedGoodsNote.Value, maxNoteLen)
	if err := f.err(); err != nil {
		return SupplierResult{}, err
	}
	var out SupplierResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := readParty(ctx, q, id, true); err != nil {
			return err
		}
		before, err := readSupplier(ctx, q, id)
		if err != nil {
			return err
		}
		p := before.Party
		keysChanged := in.apply(&p, f, false)
		if err := f.err(); err != nil {
			return err
		}
		prof := before.Profile
		if in.PaymentTermsDays != nil {
			prof.PaymentTermsDays = *in.PaymentTermsDays
		}
		if in.SuppliedGoodsNote.Set {
			prof.SuppliedGoodsNote = note
		}
		p.SearchNorm = partySearchNorm(p, prof.SuppliedGoodsNote)
		now := s.clock.Now()
		if _, err := updateParty(ctx, q, p, expected, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE supplier_profiles
			SET payment_terms_days = $2, supplied_goods_note = $3, updated_at = $4, version = version + 1
			WHERE party_id = $1`,
			id, prof.PaymentTermsDays, prof.SuppliedGoodsNote, now); err != nil {
			return fmt.Errorf("partner: update supplier profile: %w", err)
		}
		if keysChanged {
			if out.Warnings, err = s.dedupe.Check(ctx, q, id, dedupeKeysOf(p)); err != nil {
				return err
			}
		}
		if out.Supplier, err = readSupplier(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "supplier.update", "supplier", id, before, out.Supplier)
	})
	return out, err
}

// SetSupplierStatus stops (inactive) or resumes (active) trading with a supplier. A stopped
// supplier is hidden from pickers; its documents and payables stay readable and settleable. A
// supplier cannot stop while a confirmed purchase order still waits for its goods.
func (s *Service) SetSupplierStatus(ctx context.Context, actor audit.Actor, id uuid.UUID, status string) (Supplier, error) {
	if !validStatus(status) {
		return Supplier{}, errInvalid(map[string]string{"status": "invalid"})
	}
	var out Supplier
	err := s.write(ctx, actor, func(q db.Querier) error {
		p, err := readParty(ctx, q, id, true)
		if err != nil {
			return err
		}
		before, err := readSupplier(ctx, q, id)
		if err != nil {
			return err
		}
		if before.Profile.Status == status {
			out = before
			return nil
		}
		if status == StatusInactive {
			open, err := s.usage.SupplierHasOpenOrders(ctx, q, id)
			if err != nil {
				return err
			}
			if open {
				return errSupplierHasOpenOrders()
			}
		}
		now := s.clock.Now()
		if _, err := updateParty(ctx, q, p, p.Version, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE supplier_profiles SET status = $2, updated_at = $3, version = version + 1 WHERE party_id = $1`,
			id, status, now); err != nil {
			return fmt.Errorf("partner: set supplier status: %w", err)
		}
		if out, err = readSupplier(ctx, q, id); err != nil {
			return err
		}
		action := "supplier.stop"
		if status == StatusActive {
			action = "supplier.reopen"
		}
		return s.record(ctx, q, actor, action, "supplier", id, before, out)
	})
	return out, err
}

// DeleteSupplier removes a supplier nothing references; the party goes too unless it is also a
// customer. A supplier with documents can only stop trading.
func (s *Service) DeleteSupplier(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		p, err := readParty(ctx, q, id, true)
		if err != nil {
			return err
		}
		before, err := readSupplier(ctx, q, id)
		if err != nil {
			return err
		}
		inUse, err := s.usage.SupplierInUse(ctx, q, id)
		if err != nil {
			return err
		}
		if inUse {
			return errSupplierInUse()
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM supplier_profiles WHERE party_id = $1`, id); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errSupplierInUse()
			}
			return fmt.Errorf("partner: delete supplier profile: %w", err)
		}
		if before.IsCustomer {
			// The supplied goods leave the search text; the customer now reads is_supplier = false.
			p.SearchNorm = partySearchNorm(p, nil)
			if _, err := updateParty(ctx, q, p, p.Version, s.clock.Now()); err != nil {
				return err
			}
		} else if err := deletePartyIfOrphan(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "supplier.delete", "supplier", id, before, nil)
	})
}
