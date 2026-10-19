package posting

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// Lock kinds in the global lock order. doc_sequences comes first and is taken by the document
// number step itself.
const (
	LockSkuStock     = "sku_stock"
	LockLots         = "lots"
	LockLotBalances  = "lot_balances"
	LockReservations = "reservations"
	LockObligations  = "obligations"
	LockPayments     = "payments"
	LockFundAccounts = "fund_accounts"
)

// lockOrder is the only order rows are ever locked in, so two postings can never wait on each
// other in a cycle.
var lockOrder = []string{LockSkuStock, LockLots, LockLotBalances, LockReservations, LockObligations, LockPayments, LockFundAccounts}

func lockRank(kind string) int {
	for i, k := range lockOrder {
		if k == kind {
			return i
		}
	}
	return -1
}

// LockSet collects row ids to lock, by kind.
type LockSet struct {
	keys map[string][]uuid.UUID
}

// Add appends ids of kind. Unknown kinds are rejected when the set is acquired.
func (s *LockSet) Add(kind string, ids ...uuid.UUID) {
	if s.keys == nil {
		s.keys = map[string][]uuid.UUID{}
	}
	s.keys[kind] = append(s.keys[kind], ids...)
}

// LockStep is one ordered lock request.
type LockStep struct {
	Kind string
	IDs  []uuid.UUID
}

// Ordered returns the steps in the global order with ids ascending (byte order, the same order
// PostgreSQL sorts uuid) and duplicates removed.
func (s LockSet) Ordered() ([]LockStep, error) {
	steps := make([]LockStep, 0, len(s.keys))
	for kind, ids := range s.keys {
		if lockRank(kind) < 0 {
			return nil, fmt.Errorf("posting: unknown lock kind %q", kind)
		}
		if len(ids) == 0 {
			continue
		}
		sorted := append([]uuid.UUID(nil), ids...)
		sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i][:], sorted[j][:]) < 0 })
		uniq := sorted[:1]
		for _, id := range sorted[1:] {
			if id != uniq[len(uniq)-1] {
				uniq = append(uniq, id)
			}
		}
		steps = append(steps, LockStep{Kind: kind, IDs: uniq})
	}
	sort.Slice(steps, func(i, j int) bool { return lockRank(steps[i].Kind) < lockRank(steps[j].Kind) })
	return steps, nil
}

// LockFunc locks the rows of one kind, e.g.
// `SELECT 1 FROM sku_stock WHERE sku_id = ANY($1) ORDER BY sku_id FOR UPDATE`. ids arrive sorted.
type LockFunc func(ctx context.Context, q db.Querier, ids []uuid.UUID) error

// Locker holds the lock callbacks registered by domain packages.
type Locker struct {
	fns map[string]LockFunc
}

// NewLocker returns an empty locker.
func NewLocker() *Locker { return &Locker{fns: map[string]LockFunc{}} }

// Register installs fn for kind. It panics on unknown or duplicate kinds (wiring bugs).
func (l *Locker) Register(kind string, fn LockFunc) {
	if lockRank(kind) < 0 {
		panic(fmt.Sprintf("posting: unknown lock kind %q", kind))
	}
	if _, dup := l.fns[kind]; dup {
		panic(fmt.Sprintf("posting: locker for %q registered twice", kind))
	}
	l.fns[kind] = fn
}

// Acquire locks every row of set in the global order.
func (l *Locker) Acquire(ctx context.Context, q db.Querier, set LockSet) error {
	steps, err := set.Ordered()
	if err != nil {
		return err
	}
	for _, step := range steps {
		fn, ok := l.fns[step.Kind]
		if !ok {
			return fmt.Errorf("posting: no locker registered for %q", step.Kind)
		}
		if err := fn(ctx, q, step.IDs); err != nil {
			return fmt.Errorf("posting: lock %s: %w", step.Kind, err)
		}
	}
	return nil
}
