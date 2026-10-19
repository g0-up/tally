package inventory

import (
	"slices"

	"github.com/google/uuid"
)

// LotPicker chooses the lots an issue takes from. Candidates receives the lots of one SKU in
// FEFO order (expiry, received date, lot id) and returns those the issue may take, in the order
// it takes them; the ledger applies the quarantine and expiry rules on top.
type LotPicker interface {
	Candidates(lots []*LotStock) []*LotStock
}

// FefoLot takes the first-expiring lots of any color: the picker of SKUs without color stock.
type FefoLot struct{}

// Candidates implements LotPicker.
func (FefoLot) Candidates(lots []*LotStock) []*LotStock { return lots }

// ExactColorLot takes lots of exactly one color, first-expiring first; ColorID nil means the
// stock without a color ("Chưa gán màu"). Another color is never substituted.
type ExactColorLot struct {
	ColorID *uuid.UUID
}

// Candidates implements LotPicker.
func (p ExactColorLot) Candidates(lots []*LotStock) []*LotStock {
	out := make([]*LotStock, 0, len(lots))
	for _, l := range lots {
		if sameColor(l.ColorID, p.ColorID) {
			out = append(out, l)
		}
	}
	return out
}

// ExplicitLot takes one named lot: returns to a supplier, disassembly and screens where the user
// chose the lot.
type ExplicitLot struct {
	LotID uuid.UUID
}

// Candidates implements LotPicker.
func (p ExplicitLot) Candidates(lots []*LotStock) []*LotStock {
	i := slices.IndexFunc(lots, func(l *LotStock) bool { return l.ID == p.LotID })
	if i < 0 {
		return nil
	}
	return lots[i : i+1]
}

// sameColor compares nullable color ids (nil equals nil).
func sameColor(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// fefoCompare orders lots first-expiring first: expiry (no expiry last), then received date,
// then id — the order of readLotStocks.
func fefoCompare(a, b *LotStock) int {
	switch {
	case a.ExpiryDate == nil && b.ExpiryDate != nil:
		return 1
	case a.ExpiryDate != nil && b.ExpiryDate == nil:
		return -1
	case a.ExpiryDate != nil && !a.ExpiryDate.Equal(*b.ExpiryDate):
		return a.ExpiryDate.Compare(*b.ExpiryDate)
	}
	if c := a.ReceivedOn.Compare(b.ReceivedOn); c != 0 {
		return c
	}
	return compareUUID(a.ID, b.ID)
}
