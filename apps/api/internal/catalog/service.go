package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Text limits of catalog fields, in characters.
const (
	maxNameLen        = 200
	maxLabelLen       = 100
	maxDescriptionLen = 2000
	maxSpecTextLen    = 200
	// MaxCategoryDepth is how many levels the category tree may have.
	MaxCategoryDepth = 4
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Usage answers the edit rules; nil uses SQLUsageReader.
	Usage UsageReader
	// Search runs the picker searches; nil uses SQLSearchRepository.
	Search SearchRepository
	// Stock returns on-hand quantities per color; nil until the inventory module wires it.
	Stock StockByColorReader
	// Prices stores sale prices; nil until the pricing module wires it.
	Prices PriceWriter
	// Hints returns the search price hints; nil until the pricing module wires it.
	Hints PriceHintReader
	// Types validates attribute values; nil uses DefaultAttributeTypes.
	Types AttributeTypeRegistry
	// Limits enforces the plan's SKU limit; nil leaves the catalog unbounded.
	Limits LimitChecker
}

// LimitChecker answers 409 plan_limit_reached when used exceeds the tenant's plan limit key; q
// is the caller's tenant transaction.
type LimitChecker interface {
	Within(ctx context.Context, q db.Querier, tenantID uuid.UUID, key string, used int64) error
}

// LimitSKUs is the plan limit on the number of SKUs.
const LimitSKUs = "skus"

// Service implements the catalog use cases. Every method runs in its own tenant transaction;
// the tenant id always comes from the authenticated session.
type Service struct {
	db         *db.DB
	clock      clock.Clock
	audit      *audit.Recorder
	usage      UsageReader
	search     SearchRepository
	stock      StockByColorReader
	prices     PriceWriter
	hints      PriceHintReader
	limits     LimitChecker
	validator  AttributeValidator
	indexer    SearchIndexer
	matrix     VariantMatrixFactory
	mixPolicy  MixGroupPolicy
	setSpec    AttributeSetChangeSpec
	skuSpec    SKUChangeSpec
	unitSpec   UnitConversionSpec
	attrSpec   AttributeChangeSpec
	optionSpec OptionDeleteSpec
	axisSpec   VariantAxisChangeSpec
	mixSpec    MixGroupChangeSpec
	// tx, when set, is a transaction the caller already holds (the tenant provisioning
	// transaction of the seeders); every use case then runs in it instead of opening its own.
	tx db.Querier
	// recipeStatus, when set, lets a use case change the status of a kit or combo product: only
	// the kit's recipe lifecycle (publish, archive) may.
	recipeStatus bool
}

// within returns a copy of the service whose use cases run in the caller's tenant transaction q.
func (s *Service) within(q db.Querier) *Service {
	c := *s
	c.tx = q
	return &c
}

// NewService returns the service.
func NewService(d Deps) *Service {
	usage := d.Usage
	if usage == nil {
		usage = SQLUsageReader{}
	}
	search := d.Search
	if search == nil {
		search = SQLSearchRepository{}
	}
	types := d.Types
	if types == nil {
		types = DefaultAttributeTypes()
	}
	return &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, usage: usage, search: search, stock: d.Stock, prices: d.Prices,
		hints:      d.Hints,
		limits:     d.Limits,
		validator:  AttributeValidator{Types: types},
		skuSpec:    SKUChangeSpec{Usage: usage, OnHand: usage.SkuOnHand},
		unitSpec:   UnitConversionSpec{Usage: usage},
		attrSpec:   AttributeChangeSpec{Usage: usage},
		optionSpec: OptionDeleteSpec{Usage: usage},
		axisSpec:   VariantAxisChangeSpec{Usage: usage},
		mixSpec:    MixGroupChangeSpec{Usage: usage},
	}
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// read runs fn in a tenant transaction suited to scope.
func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// write runs fn in a tenant transaction of the actor's tenant.
func (s *Service) write(ctx context.Context, actor audit.Actor, fn func(q db.Querier) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	return s.db.WithTenantTx(ctx, actor.TenantID, fn)
}

// record appends an audit event.
func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, action, entity string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: id, Before: before, After: after})
}

// nameKey is the uniqueness key of a catalog name.
func nameKey(s string) string { return textnorm.Key(s) }

// fieldErrors collects field-level validation failures.
type fieldErrors map[string]string

func (f fieldErrors) err() error {
	if len(f) == 0 {
		return nil
	}
	return errInvalid(f)
}

// text trims v and checks it: required, at most max characters, no control characters.
func (f fieldErrors) text(field string, v *string, required bool, maxLen int) {
	*v = strings.TrimSpace(*v)
	switch {
	case *v == "":
		if required {
			f[field] = "required"
		}
	case utf8.RuneCountInString(*v) > maxLen:
		f[field] = "too_long"
	case !utf8.ValidString(*v) || strings.ContainsFunc(*v, isControl):
		f[field] = "invalid_text"
	}
}

// optionalText trims an optional text and turns "" into nil.
func (f fieldErrors) optionalText(field string, v *string, maxLen int) *string {
	if v == nil {
		return nil
	}
	s := *v
	f.text(field, &s, false, maxLen)
	if s == "" {
		return nil
	}
	return &s
}

// multilineText is like text but accepts line breaks (descriptions).
func (f fieldErrors) multilineText(field string, v *string, maxLen int) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	switch {
	case s == "":
		return nil
	case utf8.RuneCountInString(s) > maxLen:
		f[field] = "too_long"
	case !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return isControl(r) && r != '\n' && r != '\r' && r != '\t' }):
		f[field] = "invalid_text"
	}
	return &s
}

// Opt is an optional field of a partial update that may also be cleared: Set reports whether
// the field was given, Value is nil when it was given as null.
type Opt[T any] struct {
	Set   bool
	Value *T
}

// Some returns a set Opt holding v.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: &v} }

// UnmarshalJSON marks the field as given; JSON null clears it.
func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(bytes.TrimSpace(data)) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// lockProduct reads a product row FOR UPDATE, serialising changes to its SKUs and axes.
func lockProduct(ctx context.Context, q db.Querier, id uuid.UUID) (Product, error) {
	var p Product
	err := q.QueryRowxContext(ctx, `SELECT `+productColumns+` FROM products WHERE id = $1 FOR UPDATE`, id).StructScan(&p)
	if db.IsNoRows(err) {
		return Product{}, errNotFound()
	}
	if err != nil {
		return Product{}, fmt.Errorf("catalog: lock product: %w", err)
	}
	return p, nil
}
