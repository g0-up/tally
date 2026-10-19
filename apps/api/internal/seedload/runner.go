package seedload

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Deps are the services the generator posts through; it never writes a table itself.
type Deps struct {
	DB      *db.DB
	Domains app.Domains
	Org     *org.Service
}

// Tenant is a freshly created tenant to fill and the owner the documents are posted as.
type Tenant struct {
	// Index is the 1-based number of the tenant in the run; it seeds the tenant's RNG.
	Index   int
	ID      uuid.UUID
	OwnerID uuid.UUID
	Slug    string
}

// expiryBase is the first expiry date of lot-tracked goods; each later lot of a SKU expires a
// week after the one before, so lots never share an expiry date and FEFO order is fixed.
var expiryBase = time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)

// runner fills one tenant. It is not safe for concurrent use: a tenant posts sequentially.
type runner struct {
	deps  Deps
	opts  Options
	t     Tenant
	actor audit.Actor
	rng   *RNG

	plan       CatalogPlan
	stock      *Stock
	colors     []catalog.Color
	goodsSku   []uuid.UUID
	kitProduct []uuid.UUID
	suppliers  []uuid.UUID
	customers  []uuid.UUID
	// receivable is what each customer still owes on invoices, in whole VND.
	receivable []int64
	// lotSeq numbers the lots of each lot-tracked goods.
	lotSeq []int

	cashMethod     uuid.UUID
	salesReason    uuid.UUID
	purchaseReason uuid.UUID

	docs map[DocKind]int
	// keys numbers the idempotency keys of each request kind.
	keys map[string]int
}

// Run creates the catalog and partners of the tenant, then posts rounds of documents until the
// tenant holds at least opts.Moves stock moves, and reports what it posted. progress, when not
// nil, is called after every round with the summary so far.
//
// Determinism: the RNG of the tenant is seeded from (opts.Seed, t.Index) and every choice is
// drawn from it in a fixed order, with the stock the generator keeps in memory deciding what can
// be sold; so the same options give the same documents, SKU codes, lot numbers, per-lot stock and
// document counts. Row ids and timestamps come from the server and differ between runs.
func Run(ctx context.Context, deps Deps, opts Options, t Tenant, progress func(Summary)) (Summary, error) {
	if err := opts.Validate(); err != nil {
		return Summary{}, err
	}
	if deps.DB == nil || deps.Org == nil {
		return Summary{}, errors.New("seedload: database and org service are required")
	}
	start := time.Now()
	r := &runner{
		deps: deps, opts: opts, t: t,
		actor: audit.Actor{TenantID: t.ID, Type: audit.ActorOwner, ID: t.OwnerID, Source: audit.SourceCLI},
		rng:   NewRNG(opts.Seed, t.Index),
		docs:  map[DocKind]int{},
		keys:  map[string]int{},
	}
	if err := r.actor.Validate(); err != nil {
		return Summary{}, fmt.Errorf("seedload: actor: %w", err)
	}
	if err := r.setup(ctx); err != nil {
		return Summary{}, err
	}
	sum := Summary{Slug: t.Slug, Skus: r.plan.Skus(), Docs: r.docs}
	var last int64 = -1
	for round := 0; ; round++ {
		if err := ctx.Err(); err != nil {
			return sum, fmt.Errorf("seedload: %s: %w", t.Slug, err)
		}
		if err := r.round(ctx, round); err != nil {
			return sum, fmt.Errorf("seedload: %s round %d: %w", t.Slug, round, err)
		}
		moves, err := r.moves(ctx)
		if err != nil {
			return sum, err
		}
		sum.Rounds, sum.Moves, sum.Elapsed = round+1, moves, time.Since(start)
		if progress != nil {
			progress(sum)
		}
		if moves >= int64(opts.Moves) {
			return sum, nil
		}
		if moves <= last {
			return sum, fmt.Errorf("seedload: %s round %d posted no stock moves", t.Slug, round)
		}
		last = moves
	}
}

// setup reads the store defaults the documents need and creates the catalog and partners.
func (r *runner) setup(ctx context.Context) error {
	scope := catalog.Scope{TenantID: r.t.ID}
	colors, err := r.deps.Domains.Catalog.Colors(ctx, scope)
	if err != nil {
		return fmt.Errorf("seedload: colors: %w", err)
	}
	slices.SortFunc(colors, func(a, b catalog.Color) int { return cmp.Compare(a.Code, b.Code) })
	r.colors = colors
	units, err := r.deps.Domains.Catalog.Units(ctx, scope, true)
	if err != nil {
		return fmt.Errorf("seedload: units: %w", err)
	}
	if len(units) == 0 {
		return errors.New("seedload: the store has no units")
	}
	slices.SortFunc(units, func(a, b catalog.Unit) int { return cmp.Compare(a.Name, b.Name) })
	unitID := func(name string) uuid.UUID {
		for _, u := range units {
			if u.Name == name {
				return u.ID
			}
		}
		return units[0].ID
	}
	if err := r.lookups(ctx); err != nil {
		return err
	}
	if r.plan, err = PlanCatalog(r.rng, r.opts.Skus, len(colors)); err != nil {
		return err
	}
	r.stock = NewStock(r.plan)
	r.lotSeq = make([]int, len(r.plan.Goods))
	if err := r.createGoods(ctx, unitID); err != nil {
		return err
	}
	if err := r.createKits(ctx, unitID); err != nil {
		return err
	}
	return r.createParties(ctx)
}

// lookups finds the cash payment method and the return reasons, by code and name so the choice
// does not depend on row ids.
func (r *runner) lookups(ctx context.Context) error {
	methods, err := r.deps.Domains.Cash.Methods(ctx, cash.Scope{TenantID: r.t.ID})
	if err != nil {
		return fmt.Errorf("seedload: payment methods: %w", err)
	}
	for _, m := range methods {
		if m.Code == cash.MethodCash {
			r.cashMethod = m.ID
		}
	}
	if r.cashMethod == uuid.Nil {
		return errors.New("seedload: the store has no cash payment method")
	}
	if r.salesReason, err = r.reason(ctx, "sales_return"); err != nil {
		return err
	}
	r.purchaseReason, err = r.reason(ctx, "purchase_return")
	return err
}

func (r *runner) reason(ctx context.Context, kind string) (uuid.UUID, error) {
	reasons, err := r.deps.Org.Reasons(ctx, org.Scope{TenantID: r.t.ID}, org.ReasonFilter{Kind: kind, ActiveOnly: true})
	if err != nil {
		return uuid.Nil, fmt.Errorf("seedload: %s reasons: %w", kind, err)
	}
	if len(reasons) == 0 {
		return uuid.Nil, fmt.Errorf("seedload: the store has no active %s reason", kind)
	}
	return slices.MinFunc(reasons, func(a, b org.Reason) int { return cmp.Compare(a.Name, b.Name) }).ID, nil
}

func (r *runner) createGoods(ctx context.Context, unitID func(string) uuid.UUID) error {
	r.goodsSku = make([]uuid.UUID, len(r.plan.Goods))
	for i, g := range r.plan.Goods {
		cost, retail, wholesale := vnd(g.Cost), vnd(g.Retail), vnd(g.Wholesale)
		lot := g.Kind == GoodsLot
		detail, err := r.deps.Domains.Catalog.CreateProduct(ctx, r.actor, catalog.ProductInput{
			Name: g.Name,
			Sku: &catalog.SkuInput{
				Code: g.Code, BaseUnitID: unitID(g.Unit), TrackLot: lot, TrackExpiry: lot, RefCost: &cost,
			},
			SkuExtras: catalog.SkuExtras{RetailPrice: &retail, WholesalePrice: &wholesale},
		})
		if err != nil {
			return fmt.Errorf("seedload: create product %s: %w", g.Code, err)
		}
		if len(detail.Skus) != 1 {
			return fmt.Errorf("seedload: product %s has %d skus, want 1", g.Code, len(detail.Skus))
		}
		r.goodsSku[i] = detail.Skus[0].Sku.ID
	}
	return nil
}

func (r *runner) createKits(ctx context.Context, unitID func(string) uuid.UUID) error {
	r.kitProduct = make([]uuid.UUID, len(r.plan.Kits))
	for i, k := range r.plan.Kits {
		items := make([]kits.ItemInput, len(k.Items))
		for j, it := range k.Items {
			items[j] = kits.ItemInput{Kind: kits.KindGoods, SkuID: &r.goodsSku[it.Goods], Qty: money.NewQtyFromInt(it.Qty)}
		}
		typ := kits.TypeKit
		if k.Combo {
			typ = kits.TypeCombo
		}
		retail := vnd(k.Retail)
		in := kits.KitInput{
			Type: typ, Name: k.Name, SkuCode: k.Code, BaseUnitID: unitID(k.Unit), RetailPrice: &retail,
			Recipe: kits.RecipeInput{Items: items},
		}
		key, err := r.key("kit", http.MethodPost, "/api/v1/kits", in)
		if err != nil {
			return err
		}
		kit, _, err := r.deps.Domains.Kits.CreateKit(ctx, r.actor, &key, in)
		if err != nil {
			return fmt.Errorf("seedload: create kit %s: %w", k.Code, err)
		}
		pub, err := r.key("kit-publish", http.MethodPost, "/api/v1/kits/"+k.Code+"/publish", struct{ Version int }{kit.Latest.Version})
		if err != nil {
			return err
		}
		if _, _, err := r.deps.Domains.Kits.PublishKit(ctx, r.actor, &pub, kit.ProductID, kit.Latest.Version); err != nil {
			return fmt.Errorf("seedload: publish kit %s: %w", k.Code, err)
		}
		r.kitProduct[i] = kit.ProductID
	}
	return nil
}

func (r *runner) createParties(ctx context.Context) error {
	for _, p := range PlanParties(r.rng, SupplierCount(r.opts.Skus), false) {
		res, err := r.deps.Domains.Partner.CreateSupplier(ctx, r.actor, partner.SupplierInput{PartyInput: partyInput(p)})
		if err != nil {
			return fmt.Errorf("seedload: create supplier %q: %w", p.Name, err)
		}
		r.suppliers = append(r.suppliers, res.Supplier.ID)
	}
	for _, p := range PlanParties(r.rng, CustomerCount(r.opts.Skus), true) {
		in := partner.CustomerInput{PartyInput: partyInput(p)}
		key, err := r.key("customer", http.MethodPost, "/api/v1/customers", in)
		if err != nil {
			return err
		}
		res, _, err := r.deps.Domains.Partner.CreateCustomer(ctx, r.actor, &key, in)
		if err != nil {
			return fmt.Errorf("seedload: create customer %q: %w", p.Name, err)
		}
		r.customers = append(r.customers, res.Customer.ID)
	}
	r.receivable = make([]int64, len(r.customers))
	return nil
}

func partyInput(p PartyPlan) partner.PartyInput {
	kind := partner.KindOrganization
	if p.Person {
		kind = partner.KindPerson
	}
	name := p.Name
	return partner.PartyInput{Kind: &kind, Name: &name, Phone: partner.Some(p.Phone), Address: partner.Some(p.Address)}
}

// moves counts the stock moves of the tenant.
func (r *runner) moves(ctx context.Context) (int64, error) {
	var n int64
	err := r.deps.DB.WithReadOnlyTenantTx(ctx, r.t.ID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM stock_moves`).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("seedload: count stock moves: %w", err)
	}
	return n, nil
}

// key is the idempotency key of the next request of kind: its value is derived from the seed,
// the tenant and the request number, its hash from the request as the HTTP API would see it.
func (r *runner) key(kind, method, path string, body any) (idempotency.Key, error) {
	r.keys[kind]++
	raw, err := json.Marshal(body)
	if err != nil {
		return idempotency.Key{}, fmt.Errorf("seedload: encode %s request: %w", kind, err)
	}
	hash, err := idempotency.RequestHash(method, path, raw)
	if err != nil {
		return idempotency.Key{}, fmt.Errorf("seedload: hash %s request: %w", kind, err)
	}
	return idempotency.Key{
		Value:       fmt.Sprintf("seed-load:%d:%d:%s:%d", r.opts.Seed, r.t.Index, kind, r.keys[kind]),
		RequestHash: hash,
	}, nil
}

// colorID is the color of slot of goods g, nil for goods kept without color.
func (r *runner) colorID(g, slot int) *uuid.UUID {
	p := r.plan.Goods[g]
	if p.Kind != GoodsColored {
		return nil
	}
	id := r.colors[p.Colors[slot]].ID
	return &id
}

func vnd(v int64) money.Money { return money.NewMoneyFromInt(v) }
