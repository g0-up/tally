package seedload

import (
	"math/rand/v2"
)

// RNG is the random source of one tenant. Every draw goes through it in a fixed order, so the same
// seed and tenant index give the same sequence of documents.
type RNG struct {
	r *rand.Rand
}

// NewRNG seeds the source of tenant index (1-based) of a run with seed.
func NewRNG(seed int64, tenantIndex int) *RNG {
	//nolint:gosec // deterministic load data, not a secret; the sign bits only mix the stream
	return &RNG{r: rand.New(rand.NewPCG(uint64(seed), uint64(tenantIndex)))}
}

// Intn returns a number in [0, n); n must be positive.
func (g *RNG) Intn(n int) int { return g.r.IntN(n) }

// Between returns a number in [lo, hi]; hi below lo returns lo.
func (g *RNG) Between(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + g.r.IntN(hi-lo+1)
}

// Chance reports true with probability percent/100.
func (g *RNG) Chance(percent int) bool { return g.r.IntN(100) < percent }

// Sample returns k distinct numbers of [0, n) in draw order (all of them, shuffled, when k >= n).
func (g *RNG) Sample(n, k int) []int {
	if k <= 0 || n <= 0 {
		return nil
	}
	if k >= n {
		return g.r.Perm(n)
	}
	if k*4 > n {
		return g.r.Perm(n)[:k]
	}
	seen := make(map[int]struct{}, k)
	out := make([]int, 0, k)
	for len(out) < k {
		v := g.r.IntN(n)
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// pick returns one element of xs, which must not be empty.
func pick[T any](g *RNG, xs []T) T { return xs[g.Intn(len(xs))] }
