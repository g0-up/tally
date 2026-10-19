package printing

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// Provider reads one document of a type as print data. Version is the sent version asked for;
// nil prints the document as it stands (for a versioned type, its latest sent version).
// Providers set the document's own fields and leave the label, type, store header, party
// details and color names to the service.
type Provider func(ctx context.Context, scope Scope, id uuid.UUID, version *int) (Doc, error)

type entry struct {
	provider  Provider
	versioned bool
}

// Registry maps document types to their providers.
type Registry struct {
	entries map[string]entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{entries: map[string]entry{}} }

// Register adds the provider of docType; versioned types accept a version to print. Registering a
// type twice is a wiring error.
func (r *Registry) Register(docType string, p Provider, versioned bool) {
	if docType == "" || p == nil {
		panic("printing: Register needs a document type and a provider")
	}
	if _, dup := r.entries[docType]; dup {
		panic("printing: document type registered twice: " + docType)
	}
	r.entries[docType] = entry{provider: p, versioned: versioned}
}

// DocTypes lists the registered document types in order.
func (r *Registry) DocTypes() []string {
	out := make([]string, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Deps are the collaborators of the service.
type Deps struct {
	DB       *db.DB
	Clock    clock.Clock
	Registry *Registry
}

// Service prints documents through the registry.
type Service struct {
	db       *db.DB
	clock    clock.Clock
	registry *Registry
}

// NewService returns the print service over the providers of d.Registry.
func NewService(d Deps) *Service {
	if d.Registry == nil {
		d.Registry = NewRegistry()
	}
	return &Service{db: d.DB, clock: d.Clock, registry: d.Registry}
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

// Print returns the print data of a document of the tenant.
func (s *Service) Print(ctx context.Context, scope Scope, docType string, id uuid.UUID, version *int) (Doc, error) {
	e, ok := s.registry.entries[docType]
	if !ok {
		return Doc{}, errInvalid(map[string]string{"doc_type": "unknown"})
	}
	if version != nil && !e.versioned {
		return Doc{}, errInvalid(map[string]string{"version": "unsupported"})
	}
	if version != nil && *version < 1 {
		return Doc{}, errInvalid(map[string]string{"version": "invalid"})
	}
	out, err := e.provider(ctx, scope, id, version)
	if err != nil {
		return Doc{}, err
	}
	out.Label, out.DocType, out.PrintedAt = Label, docType, s.clock.Now()
	if err := s.complete(ctx, scope, &out); err != nil {
		return Doc{}, err
	}
	return out, nil
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// complete adds what every printed copy shows besides the document: the store header, the party
// details, the color names, and empty lists for absent ones.
func (s *Service) complete(ctx context.Context, scope Scope, d *Doc) error {
	if d.Groups == nil {
		d.Groups = []Group{}
	}
	if d.Amounts == nil {
		d.Amounts = []Amount{}
	}
	if d.Related == nil {
		d.Related = []Related{}
	}
	return s.read(ctx, scope, func(q db.Querier) error {
		if d.Tenant == nil {
			t, err := readTenant(ctx, q, scope.TenantID)
			if err != nil {
				return err
			}
			d.Tenant = t
		}
		for _, p := range []*Party{d.Customer, d.Supplier} {
			if p != nil && p.Name == "" {
				if err := readParty(ctx, q, p); err != nil {
					return err
				}
			}
		}
		return nameColors(ctx, q, d)
	})
}

// readTenant is the store header; nil under a role that cannot read the tenant (support).
func readTenant(ctx context.Context, q db.Querier, tenantID uuid.UUID) (*Tenant, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return nil, fmt.Errorf("printing: read tenant privileges: %w", err)
	}
	if !readsTenant {
		return nil, nil
	}
	var t Tenant
	if err := sqlx.GetContext(ctx, q, &t, `
		SELECT display_name, legal_name, tax_code, address, phone FROM tenants WHERE id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("printing: read tenant header: %w", err)
	}
	return &t, nil
}

func readParty(ctx context.Context, q db.Querier, p *Party) error {
	err := sqlx.GetContext(ctx, q, p, `SELECT id, name, phone, tax_code, address FROM parties WHERE id = $1`, p.ID)
	if db.IsNoRows(err) {
		return apperr.NewNotFound("not_found", "Party not found")
	}
	if err != nil {
		return fmt.Errorf("printing: read party: %w", err)
	}
	return nil
}

// eachLine calls fn on every printed line.
func (d *Doc) eachLine(fn func(*Line)) {
	for i := range d.Groups {
		for k := range d.Groups[i].Lines {
			fn(&d.Groups[i].Lines[k])
		}
	}
	for i := range d.Returned {
		fn(&d.Returned[i])
	}
}

// nameColors fills in the color names of the lines that name none.
func nameColors(ctx context.Context, q db.Querier, d *Doc) error {
	var colorIDs []uuid.UUID
	d.eachLine(func(l *Line) {
		if l.Color == nil && l.ColorID != nil {
			colorIDs = append(colorIDs, *l.ColorID)
		}
	})
	if len(colorIDs) == 0 {
		return nil
	}
	slices.SortFunc(colorIDs, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	var rows []struct {
		ID   uuid.UUID `db:"id"`
		Name string    `db:"name"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, name FROM colors WHERE id = ANY($1)`, slices.Compact(colorIDs)); err != nil {
		return fmt.Errorf("printing: read color names: %w", err)
	}
	names := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	d.eachLine(func(l *Line) {
		if l.Color == nil && l.ColorID != nil {
			if n, ok := names[*l.ColorID]; ok {
				l.Color = &n
			}
		}
	})
	return nil
}
