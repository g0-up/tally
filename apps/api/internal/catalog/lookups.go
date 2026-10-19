package catalog

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Colors, units and tax rates: the small lookups every product and document line refers to.

const (
	colorColumns   = `id, code, name, hex, sort_order, is_system, active`
	unitColumns    = `id, name, active, created_at`
	taxRateColumns = `id, name, rate_percent, active, created_at`
)

var maxTaxRate = money.MustPercent("100")

// Colors returns the fixed colors in display order. They are seeded and not editable.
func (s *Service) Colors(ctx context.Context, scope Scope) ([]Color, error) {
	var out []Color
	err := s.read(ctx, scope, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+colorColumns+` FROM colors ORDER BY sort_order, code`)
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: list colors: %w", err)
	}
	return out, nil
}

// Units returns the units of measure by name; activeOnly hides deactivated ones.
func (s *Service) Units(ctx context.Context, scope Scope, activeOnly bool) ([]Unit, error) {
	var out []Unit
	err := s.read(ctx, scope, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+unitColumns+` FROM units WHERE active OR NOT $1 ORDER BY name_key`, activeOnly)
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: list units: %w", err)
	}
	return out, nil
}

// UnitInput creates or changes a unit; nil fields are kept on update.
type UnitInput struct {
	Name   *string
	Active *bool
}

func readUnit(ctx context.Context, q db.Querier, id uuid.UUID) (Unit, error) {
	var u Unit
	err := q.QueryRowxContext(ctx, `SELECT `+unitColumns+` FROM units WHERE id = $1`, id).StructScan(&u)
	if db.IsNoRows(err) {
		return Unit{}, errNotFound()
	}
	if err != nil {
		return Unit{}, fmt.Errorf("catalog: read unit: %w", err)
	}
	return u, nil
}

// CreateUnit adds a unit; names are unique by name key ("Hộp" and "hop" clash).
func (s *Service) CreateUnit(ctx context.Context, actor audit.Actor, in UnitInput) (Unit, error) {
	f := fieldErrors{}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	f.text("name", &name, true, maxLabelLen)
	if err := f.err(); err != nil {
		return Unit{}, err
	}
	u := Unit{ID: ids.New(), Name: name, Active: in.Active == nil || *in.Active, CreatedAt: s.clock.Now()}
	err := s.write(ctx, actor, func(q db.Querier) error {
		if err := insertUnit(ctx, q, actor.TenantID, u); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "unit.create", "unit", u.ID, nil, u)
	})
	return u, err
}

func insertUnit(ctx context.Context, q db.Querier, tenantID uuid.UUID, u Unit) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO units (id, tenant_id, name, name_key, active, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, tenantID, u.Name, nameKey(u.Name), u.Active, u.CreatedAt)
	if db.IsUniqueViolation(err, "ux_units__name_key") {
		return errNameTaken()
	}
	if err != nil {
		return fmt.Errorf("catalog: insert unit: %w", err)
	}
	return nil
}

// UpdateUnit renames or (de)activates a unit. Renaming is safe: document lines snapshot the name.
func (s *Service) UpdateUnit(ctx context.Context, actor audit.Actor, id uuid.UUID, in UnitInput) (Unit, error) {
	f := fieldErrors{}
	if in.Name != nil {
		f.text("name", in.Name, true, maxLabelLen)
	}
	if err := f.err(); err != nil {
		return Unit{}, err
	}
	var out Unit
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readUnit(ctx, q, id)
		if err != nil {
			return err
		}
		out = before
		if in.Name != nil {
			out.Name = *in.Name
		}
		if in.Active != nil {
			out.Active = *in.Active
		}
		_, err = q.ExecContext(ctx, `UPDATE units SET name = $2, name_key = $3, active = $4 WHERE id = $1`,
			id, out.Name, nameKey(out.Name), out.Active)
		if db.IsUniqueViolation(err, "ux_units__name_key") {
			return errNameTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: update unit: %w", err)
		}
		return s.record(ctx, q, actor, "unit.update", "unit", id, before, out)
	})
	return out, err
}

// TaxRates returns the tax rates by name; activeOnly hides deactivated ones.
func (s *Service) TaxRates(ctx context.Context, scope Scope, activeOnly bool) ([]TaxRate, error) {
	var out []TaxRate
	err := s.read(ctx, scope, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+taxRateColumns+` FROM tax_rates WHERE active OR NOT $1 ORDER BY rate_percent, name_key`, activeOnly)
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: list tax rates: %w", err)
	}
	return out, nil
}

// TaxRateInput creates or changes a tax rate; nil fields are kept on update. Changing a rate
// never changes posted documents: every line snapshots its rate.
type TaxRateInput struct {
	Name        *string
	RatePercent *money.Percent
	Active      *bool
}

func (in *TaxRateInput) validate(create bool) error {
	f := fieldErrors{}
	if in.Name != nil || create {
		if in.Name == nil {
			in.Name = new(string)
		}
		f.text("name", in.Name, true, maxLabelLen)
	}
	switch {
	case in.RatePercent == nil && create:
		f["rate_percent"] = "required"
	case in.RatePercent != nil && (in.RatePercent.Decimal().IsNegative() || in.RatePercent.Cmp(maxTaxRate) > 0):
		f["rate_percent"] = "out_of_range"
	}
	return f.err()
}

func readTaxRate(ctx context.Context, q db.Querier, id uuid.UUID) (TaxRate, error) {
	var t TaxRate
	err := q.QueryRowxContext(ctx, `SELECT `+taxRateColumns+` FROM tax_rates WHERE id = $1`, id).StructScan(&t)
	if db.IsNoRows(err) {
		return TaxRate{}, errNotFound()
	}
	if err != nil {
		return TaxRate{}, fmt.Errorf("catalog: read tax rate: %w", err)
	}
	return t, nil
}

// CreateTaxRate adds a tax rate.
func (s *Service) CreateTaxRate(ctx context.Context, actor audit.Actor, in TaxRateInput) (TaxRate, error) {
	if err := in.validate(true); err != nil {
		return TaxRate{}, err
	}
	t := TaxRate{ID: ids.New(), Name: *in.Name, RatePercent: *in.RatePercent, Active: in.Active == nil || *in.Active, CreatedAt: s.clock.Now()}
	err := s.write(ctx, actor, func(q db.Querier) error {
		if err := insertTaxRate(ctx, q, actor.TenantID, t); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "tax_rate.create", "tax_rate", t.ID, nil, t)
	})
	return t, err
}

func insertTaxRate(ctx context.Context, q db.Querier, tenantID uuid.UUID, t TaxRate) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO tax_rates (id, tenant_id, name, name_key, rate_percent, active, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		t.ID, tenantID, t.Name, nameKey(t.Name), t.RatePercent, t.Active, t.CreatedAt)
	if db.IsUniqueViolation(err, "ux_tax_rates__name_key") {
		return errNameTaken()
	}
	if err != nil {
		return fmt.Errorf("catalog: insert tax rate: %w", err)
	}
	return nil
}

// UpdateTaxRate changes a tax rate.
func (s *Service) UpdateTaxRate(ctx context.Context, actor audit.Actor, id uuid.UUID, in TaxRateInput) (TaxRate, error) {
	if err := in.validate(false); err != nil {
		return TaxRate{}, err
	}
	var out TaxRate
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readTaxRate(ctx, q, id)
		if err != nil {
			return err
		}
		out = before
		if in.Name != nil {
			out.Name = *in.Name
		}
		if in.RatePercent != nil {
			out.RatePercent = *in.RatePercent
		}
		if in.Active != nil {
			out.Active = *in.Active
		}
		_, err = q.ExecContext(ctx, `UPDATE tax_rates SET name = $2, name_key = $3, rate_percent = $4, active = $5 WHERE id = $1`,
			id, out.Name, nameKey(out.Name), out.RatePercent, out.Active)
		if db.IsUniqueViolation(err, "ux_tax_rates__name_key") {
			return errNameTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: update tax rate: %w", err)
		}
		return s.record(ctx, q, actor, "tax_rate.update", "tax_rate", id, before, out)
	})
	return out, err
}
