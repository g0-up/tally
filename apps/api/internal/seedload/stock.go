package seedload

import "fmt"

// Stock is what the generator knows of the sellable stock of each goods SKU, per color slot, in
// whole base units. It only ever sells what it holds here, so no posting runs out of stock; which
// lot the ledger takes the units from does not matter to it.
type Stock struct {
	qty [][]int64
	// kits is the assembled stock of each kit of the plan.
	kits []int64
}

// NewStock is the empty stock of a catalog.
func NewStock(c CatalogPlan) *Stock {
	s := &Stock{qty: make([][]int64, len(c.Goods)), kits: make([]int64, len(c.Kits))}
	for i, g := range c.Goods {
		s.qty[i] = make([]int64, g.Slots())
	}
	return s
}

// Avail is the stock of goods g in slot.
func (s *Stock) Avail(g, slot int) int64 { return s.qty[g][slot] }

// Total is the stock of goods g over every slot.
func (s *Stock) Total(g int) int64 {
	var n int64
	for _, q := range s.qty[g] {
		n += q
	}
	return n
}

// Add puts q units of goods g into slot.
func (s *Stock) Add(g, slot int, q int64) { s.qty[g][slot] += q }

// Take removes q units of goods g from slot; it fails rather than going negative.
func (s *Stock) Take(g, slot int, q int64) error {
	if q <= 0 || s.qty[g][slot] < q {
		return fmt.Errorf("seedload: cannot take %d of goods %d slot %d holding %d", q, g, slot, s.qty[g][slot])
	}
	s.qty[g][slot] -= q
	return nil
}

// StockedSlot draws a slot of goods g that holds stock; false when none does.
func (s *Stock) StockedSlot(r *RNG, g int) (int, bool) {
	slots := s.qty[g]
	start := r.Intn(len(slots))
	for i := range slots {
		at := (start + i) % len(slots)
		if slots[at] > 0 {
			return at, true
		}
	}
	return 0, false
}

// AddKits records n assembled units of kit k.
func (s *Stock) AddKits(k int, n int64) { s.kits[k] += n }

// Kits is the assembled stock of kit k.
func (s *Stock) Kits(k int) int64 { return s.kits[k] }
