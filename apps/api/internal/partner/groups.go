package partner

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// GroupInput is the customer group form; on update nil keeps the stored value.
type GroupInput struct {
	Name          *string
	BasePriceKind *string
	Active        *bool
}

// CustomerGroups lists the customer groups by name; activeOnly hides deactivated ones.
func (s *Service) CustomerGroups(ctx context.Context, scope Scope, activeOnly bool) ([]CustomerGroup, error) {
	var out []CustomerGroup
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+groupColumns+` FROM customer_groups WHERE (NOT $1 OR active) ORDER BY name_key, id`, activeOnly); err != nil {
			return fmt.Errorf("partner: list customer groups: %w", err)
		}
		return nil
	})
	return out, err
}

func (in GroupInput) apply(g *CustomerGroup, f fieldErrors, creating bool) {
	if in.Name != nil || creating {
		name := ""
		if in.Name != nil {
			name = *in.Name
		}
		f.text("name", &name, true, maxLabelLen)
		g.Name, g.NameKey = name, textnorm.Key(name)
	}
	if in.BasePriceKind != nil || creating {
		kind := ""
		if in.BasePriceKind != nil {
			kind = *in.BasePriceKind
		}
		if kind != PriceRetail && kind != PriceWholesale {
			f["base_price_kind"] = "invalid"
		}
		g.BasePriceKind = kind
	}
	if in.Active != nil {
		g.Active = *in.Active
	}
}

// CreateCustomerGroup adds a customer group.
func (s *Service) CreateCustomerGroup(ctx context.Context, actor audit.Actor, in GroupInput) (CustomerGroup, error) {
	g := CustomerGroup{Active: true}
	f := fieldErrors{}
	in.apply(&g, f, true)
	if err := f.err(); err != nil {
		return CustomerGroup{}, err
	}
	var out CustomerGroup
	err := s.write(ctx, actor, func(q db.Querier) error {
		now := s.clock.Now()
		g.ID, g.CreatedAt, g.UpdatedAt = ids.New(), now, now
		if _, err := q.ExecContext(ctx, `
			INSERT INTO customer_groups (id, tenant_id, name, name_key, base_price_kind, is_system, active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, false, $6, $7, $7)`,
			g.ID, actor.TenantID, g.Name, g.NameKey, g.BasePriceKind, g.Active, now); err != nil {
			if db.IsUniqueViolation(err, "ux_customer_groups__name_key") {
				return errGroupNameTaken()
			}
			return fmt.Errorf("partner: insert customer group: %w", err)
		}
		var err error
		if out, err = readGroup(ctx, q, g.ID, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "customer_group.create", "customer_group", g.ID, nil, out)
	})
	return out, err
}

// UpdateCustomerGroup renames, reprices or (de)activates a group. The built-in groups keep their
// base price kind and stay active.
func (s *Service) UpdateCustomerGroup(ctx context.Context, actor audit.Actor, id uuid.UUID, in GroupInput) (CustomerGroup, error) {
	var out CustomerGroup
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readGroup(ctx, q, id, true)
		if err != nil {
			return err
		}
		g := before
		f := fieldErrors{}
		in.apply(&g, f, false)
		if err := f.err(); err != nil {
			return err
		}
		if before.IsSystem && (g.BasePriceKind != before.BasePriceKind || !g.Active) {
			return errSystemGroup()
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE customer_groups SET name = $2, name_key = $3, base_price_kind = $4, active = $5, updated_at = $6
			WHERE id = $1`, id, g.Name, g.NameKey, g.BasePriceKind, g.Active, s.clock.Now()); err != nil {
			if db.IsUniqueViolation(err, "ux_customer_groups__name_key") {
				return errGroupNameTaken()
			}
			return fmt.Errorf("partner: update customer group: %w", err)
		}
		if out, err = readGroup(ctx, q, id, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "customer_group.update", "customer_group", id, before, out)
	})
	return out, err
}

// DeleteCustomerGroup removes a group no customer, price list or promotion uses. Built-in groups
// cannot be deleted.
func (s *Service) DeleteCustomerGroup(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		before, err := readGroup(ctx, q, id, true)
		if err != nil {
			return err
		}
		if before.IsSystem {
			return errSystemGroup()
		}
		inUse, err := s.usage.CustomerGroupInUse(ctx, q, id)
		if err != nil {
			return err
		}
		if inUse {
			return errGroupInUse()
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM customer_groups WHERE id = $1`, id); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errGroupInUse()
			}
			return fmt.Errorf("partner: delete customer group: %w", err)
		}
		return s.record(ctx, q, actor, "customer_group.delete", "customer_group", id, before, nil)
	})
}
