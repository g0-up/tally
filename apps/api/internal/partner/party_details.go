package partner

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// ContactInput is the contact form; on update a nil pointer or an unset Opt keeps the stored
// value. Making a contact primary demotes the previous primary contact of the party.
type ContactInput struct {
	Name      *string
	Phone     Opt[string]
	Email     Opt[string]
	RoleTitle Opt[string]
	IsPrimary *bool
}

// AddressInput is the address form; on update nil keeps the stored value. Making an address the
// default demotes the previous default of the party.
type AddressInput struct {
	Label       *string
	AddressLine *string
	IsDefault   *bool
}

// Contacts lists the contacts of a party, the primary one first.
func (s *Service) Contacts(ctx context.Context, scope Scope, partyID uuid.UUID) ([]Contact, error) {
	var out []Contact
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := readParty(ctx, q, partyID, false); err != nil {
			return err
		}
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+contactColumns+` FROM contacts WHERE party_id = $1 ORDER BY is_primary DESC, created_at, id`, partyID); err != nil {
			return fmt.Errorf("partner: list contacts: %w", err)
		}
		return nil
	})
	return out, err
}

func (in ContactInput) apply(c *Contact, f fieldErrors, creating bool) {
	if in.Name != nil || creating {
		name := ""
		if in.Name != nil {
			name = *in.Name
		}
		f.text("name", &name, true, maxNameLen)
		c.Name = name
	}
	if in.Phone.Set {
		c.Phone, _ = f.phone("phone", in.Phone.Value)
	}
	if in.Email.Set {
		c.Email = f.email("email", in.Email.Value)
	}
	if in.RoleTitle.Set {
		c.RoleTitle = f.optional("role_title", in.RoleTitle.Value, maxLabelLen)
	}
	if in.IsPrimary != nil {
		c.IsPrimary = *in.IsPrimary
	}
}

// CreateContact adds a contact to a party.
func (s *Service) CreateContact(ctx context.Context, actor audit.Actor, partyID uuid.UUID, in ContactInput) (Contact, error) {
	var c Contact
	f := fieldErrors{}
	in.apply(&c, f, true)
	if err := f.err(); err != nil {
		return Contact{}, err
	}
	var out Contact
	err := s.write(ctx, actor, func(q db.Querier) error {
		// The party row lock serialises the primary-contact switch.
		if _, err := readParty(ctx, q, partyID, true); err != nil {
			return err
		}
		if c.IsPrimary {
			if err := demote(ctx, q, demotePrimaryContact, partyID, uuid.Nil); err != nil {
				return err
			}
		}
		now := s.clock.Now()
		c.ID, c.PartyID = ids.New(), partyID
		if _, err := q.ExecContext(ctx, `
			INSERT INTO contacts (id, tenant_id, party_id, name, phone, email, role_title, is_primary, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			c.ID, actor.TenantID, partyID, c.Name, c.Phone, c.Email, c.RoleTitle, c.IsPrimary, now); err != nil {
			return fmt.Errorf("partner: insert contact: %w", err)
		}
		var err error
		if out, err = readContact(ctx, q, partyID, c.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "contact.create", "contact", c.ID, nil, out)
	})
	return out, err
}

// UpdateContact changes a contact of a party.
func (s *Service) UpdateContact(ctx context.Context, actor audit.Actor, partyID, id uuid.UUID, in ContactInput) (Contact, error) {
	var out Contact
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := readParty(ctx, q, partyID, true); err != nil {
			return err
		}
		before, err := readContact(ctx, q, partyID, id)
		if err != nil {
			return err
		}
		c := before
		f := fieldErrors{}
		in.apply(&c, f, false)
		if err := f.err(); err != nil {
			return err
		}
		if c.IsPrimary && !before.IsPrimary {
			if err := demote(ctx, q, demotePrimaryContact, partyID, id); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE contacts SET name = $2, phone = $3, email = $4, role_title = $5, is_primary = $6, updated_at = $7
			WHERE id = $1`, id, c.Name, c.Phone, c.Email, c.RoleTitle, c.IsPrimary, s.clock.Now()); err != nil {
			return fmt.Errorf("partner: update contact: %w", err)
		}
		if out, err = readContact(ctx, q, partyID, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "contact.update", "contact", id, before, out)
	})
	return out, err
}

// DeleteContact removes a contact of a party.
func (s *Service) DeleteContact(ctx context.Context, actor audit.Actor, partyID, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		before, err := readContact(ctx, q, partyID, id)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM contacts WHERE id = $1`, id); err != nil {
			return fmt.Errorf("partner: delete contact: %w", err)
		}
		return s.record(ctx, q, actor, "contact.delete", "contact", id, before, nil)
	})
}

// Addresses lists the addresses of a party, the default one first.
func (s *Service) Addresses(ctx context.Context, scope Scope, partyID uuid.UUID) ([]Address, error) {
	var out []Address
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := readParty(ctx, q, partyID, false); err != nil {
			return err
		}
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+addressColumns+` FROM party_addresses WHERE party_id = $1 ORDER BY is_default DESC, created_at, id`, partyID); err != nil {
			return fmt.Errorf("partner: list addresses: %w", err)
		}
		return nil
	})
	return out, err
}

func (in AddressInput) apply(a *Address, f fieldErrors, creating bool) {
	if in.Label != nil || creating {
		label := ""
		if in.Label != nil {
			label = *in.Label
		}
		f.text("label", &label, true, maxLabelLen)
		a.Label = label
	}
	if in.AddressLine != nil || creating {
		line := ""
		if in.AddressLine != nil {
			line = *in.AddressLine
		}
		f.text("address_line", &line, true, maxAddressLen)
		a.AddressLine = line
	}
	if in.IsDefault != nil {
		a.IsDefault = *in.IsDefault
	}
}

// CreateAddress adds an address to a party.
func (s *Service) CreateAddress(ctx context.Context, actor audit.Actor, partyID uuid.UUID, in AddressInput) (Address, error) {
	var a Address
	f := fieldErrors{}
	in.apply(&a, f, true)
	if err := f.err(); err != nil {
		return Address{}, err
	}
	var out Address
	err := s.write(ctx, actor, func(q db.Querier) error {
		// The party row lock serialises the default-address switch.
		if _, err := readParty(ctx, q, partyID, true); err != nil {
			return err
		}
		if a.IsDefault {
			if err := demote(ctx, q, demoteDefaultAddress, partyID, uuid.Nil); err != nil {
				return err
			}
		}
		a.ID, a.PartyID = ids.New(), partyID
		if _, err := q.ExecContext(ctx, `
			INSERT INTO party_addresses (id, tenant_id, party_id, label, address_line, is_default, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			a.ID, actor.TenantID, partyID, a.Label, a.AddressLine, a.IsDefault, s.clock.Now()); err != nil {
			return fmt.Errorf("partner: insert address: %w", err)
		}
		var err error
		if out, err = readAddress(ctx, q, partyID, a.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "party_address.create", "party_address", a.ID, nil, out)
	})
	return out, err
}

// UpdateAddress changes an address of a party.
func (s *Service) UpdateAddress(ctx context.Context, actor audit.Actor, partyID, id uuid.UUID, in AddressInput) (Address, error) {
	var out Address
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := readParty(ctx, q, partyID, true); err != nil {
			return err
		}
		before, err := readAddress(ctx, q, partyID, id)
		if err != nil {
			return err
		}
		a := before
		f := fieldErrors{}
		in.apply(&a, f, false)
		if err := f.err(); err != nil {
			return err
		}
		if a.IsDefault && !before.IsDefault {
			if err := demote(ctx, q, demoteDefaultAddress, partyID, id); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE party_addresses SET label = $2, address_line = $3, is_default = $4 WHERE id = $1`,
			id, a.Label, a.AddressLine, a.IsDefault); err != nil {
			return fmt.Errorf("partner: update address: %w", err)
		}
		if out, err = readAddress(ctx, q, partyID, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "party_address.update", "party_address", id, before, out)
	})
	return out, err
}

// DeleteAddress removes an address of a party.
func (s *Service) DeleteAddress(ctx context.Context, actor audit.Actor, partyID, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		before, err := readAddress(ctx, q, partyID, id)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM party_addresses WHERE id = $1`, id); err != nil {
			return fmt.Errorf("partner: delete address: %w", err)
		}
		return s.record(ctx, q, actor, "party_address.delete", "party_address", id, before, nil)
	})
}

// Demotion queries clear the flag of a party's other rows so a new row can take it; the partial
// unique indexes allow one primary contact and one default address per party.
const (
	demotePrimaryContact = `UPDATE contacts SET is_primary = false WHERE party_id = $1 AND id <> $2 AND is_primary`
	demoteDefaultAddress = `UPDATE party_addresses SET is_default = false WHERE party_id = $1 AND id <> $2 AND is_default`
)

func demote(ctx context.Context, q db.Querier, query string, partyID, except uuid.UUID) error {
	if _, err := q.ExecContext(ctx, query, partyID, except); err != nil {
		return fmt.Errorf("partner: demote: %w", err)
	}
	return nil
}
