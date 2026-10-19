// Package seedload generates realistic, deterministic load data for `tally seed load`: a catalog
// with lot-tracked, colored and plain goods plus kits and combos, suppliers and customers, then
// rounds of receipts, POS sales, invoices with their stock outs, returns, debt payments, stock
// counts and kit assembly, all posted through the domain services until each tenant holds the
// requested number of stock moves.
//
// The same seed gives the same documents, the same stock per (SKU code, color, lot) and the same
// document counts. Row ids, timestamps and the verification tokens differ from run to run.
package seedload

import (
	"errors"
	"fmt"
	"strconv"
)

// Limits of the options. MaxSkus is the plan limit of SKUs per tenant.
const (
	MinTenants = 1
	MaxTenants = 100
	MinSkus    = 20
	MaxSkus    = 20000
	MinMoves   = 1
	MaxMoves   = 100_000_000
)

// Defaults of the command flags.
const (
	DefaultTenants    = 1
	DefaultSkus       = 20000
	DefaultMoves      = 1_000_000
	DefaultSeed       = 42
	DefaultSlugPrefix = "load"
)

// Concurrency is how many tenants post at the same time; each tenant posts sequentially.
const Concurrency = 4

// Options are the parameters of one load run.
type Options struct {
	// Tenants is how many tenants are created, numbered 1..Tenants.
	Tenants int
	// Skus is the number of SKUs per tenant, kits and combos included.
	Skus int
	// Moves is the number of stock moves each tenant reaches at least.
	Moves int
	// Seed makes the run reproducible.
	Seed int64
	// SlugPrefix starts every tenant slug: "<prefix>-<seed>-<n>".
	SlugPrefix string
}

// Validate checks the options against their limits.
func (o Options) Validate() error {
	var errs []error
	if o.Tenants < MinTenants || o.Tenants > MaxTenants {
		errs = append(errs, fmt.Errorf("--tenants must be between %d and %d, got %d", MinTenants, MaxTenants, o.Tenants))
	}
	if o.Skus < MinSkus || o.Skus > MaxSkus {
		errs = append(errs, fmt.Errorf("--skus must be between %d and %d (the plan limit), got %d", MinSkus, MaxSkus, o.Skus))
	}
	if o.Moves < MinMoves || o.Moves > MaxMoves {
		errs = append(errs, fmt.Errorf("--moves must be between %d and %d, got %d", MinMoves, MaxMoves, o.Moves))
	}
	if o.SlugPrefix == "" {
		errs = append(errs, errors.New("--slug-prefix must not be empty"))
	}
	return errors.Join(errs...)
}

// Slug is the address of tenant n (1-based) of a run.
func (o Options) Slug(n int) string {
	return o.SlugPrefix + "-" + strconv.FormatInt(o.Seed, 10) + "-" + strconv.Itoa(n)
}

// OwnerEmail is the deterministic e-mail address of the owner of the tenant at slug.
func OwnerEmail(slug string) string { return "owner@" + slug + ".load.tally.test" }
