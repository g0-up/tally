package seedload

import (
	"errors"
	"fmt"
)

// GoodsKind is how the stock of a goods SKU is kept.
type GoodsKind int

// The goods kinds of the catalog.
const (
	// GoodsPlain has one lot without number or expiry.
	GoodsPlain GoodsKind = iota
	// GoodsLot is bought in numbered lots with an expiry date and sold first-expiry-first-out.
	GoodsLot
	// GoodsColored is kept per color and always received and sold with one.
	GoodsColored
)

// minPlainGoods is how many plain goods open the catalog, so every kit finds its components.
const minPlainGoods = 4

// GoodsPlan is one goods SKU (a product with a single SKU) of the catalog.
type GoodsPlan struct {
	Code string
	Name string
	// Unit is the name of the base unit.
	Unit string
	Kind GoodsKind
	// Colors are indexes into the store colors sorted by code, for colored goods only.
	Colors []int
	// Retail, Wholesale and Cost are whole VND per base unit.
	Retail, Wholesale, Cost int64
}

// Slots is how many separate stocks the goods have: one per color, else one.
func (p GoodsPlan) Slots() int {
	if p.Kind == GoodsColored {
		return len(p.Colors)
	}
	return 1
}

// KitItem is a component of a kit: Qty base units of goods Goods.
type KitItem struct {
	Goods int
	Qty   int64
}

// KitPlan is a kit (assembled in advance and stocked) or a combo (expanded when sold).
type KitPlan struct {
	Code  string
	Name  string
	Unit  string
	Combo bool
	Items []KitItem
	// Retail is whole VND.
	Retail int64
}

// CatalogPlan is the catalog of one tenant: Goods plus Kits make --skus SKUs.
type CatalogPlan struct {
	Goods []GoodsPlan
	Kits  []KitPlan
}

// Skus is the number of SKUs of the plan.
func (c CatalogPlan) Skus() int { return len(c.Goods) + len(c.Kits) }

// PartyPlan is a supplier or customer.
type PartyPlan struct {
	Name    string
	Person  bool
	Phone   string
	Address string
}

// KitCount is how many kits and combos a catalog of skus SKUs holds.
func KitCount(skus int) int { return clamp(skus/1000, 2, 12) }

// SupplierCount and CustomerCount size the partners to the catalog.
func SupplierCount(skus int) int { return clamp(skus/400, 3, 40) }

// CustomerCount is how many customers a catalog of skus SKUs is sold to on invoice.
func CustomerCount(skus int) int { return clamp(skus/100, 5, 200) }

// PlanCatalog draws the catalog of skus SKUs; colorCount is how many colors the store has.
func PlanCatalog(g *RNG, skus, colorCount int) (CatalogPlan, error) {
	if skus < MinSkus || skus > MaxSkus {
		return CatalogPlan{}, fmt.Errorf("seedload: %d skus is outside %d..%d", skus, MinSkus, MaxSkus)
	}
	if colorCount < 2 {
		return CatalogPlan{}, errors.New("seedload: the store needs at least two colors")
	}
	kitCount := KitCount(skus)
	goods := make([]GoodsPlan, skus-kitCount)
	var plain []int
	// Product names are unique in a store: a repeated name gets the number of its repeat.
	names := make(map[string]int, len(goods))
	for i := range goods {
		noun := pick(g, productNouns)
		for i < minPlainGoods && (noun.lot || noun.colored) {
			noun = pick(g, productNouns)
		}
		retail := roundTo(int64(noun.price)*int64(g.Between(80, 130))*10, 1000)
		name := noun.name + " " + pick(g, brandNames) + " " + pick(g, noun.sizes)
		names[name]++
		if n := names[name]; n > 1 {
			name = fmt.Sprintf("%s - mẫu %d", name, n)
		}
		p := GoodsPlan{
			Code:      fmt.Sprintf("SP%05d", i+1),
			Name:      name,
			Unit:      noun.unit,
			Retail:    retail,
			Wholesale: roundTo(retail*90/100, 1000),
			Cost:      roundTo(retail*int64(g.Between(55, 80))/100, 100),
		}
		switch {
		case noun.lot:
			p.Kind = GoodsLot
		case noun.colored:
			p.Kind = GoodsColored
			p.Colors = g.Sample(colorCount, g.Between(2, min(4, colorCount)))
		default:
			plain = append(plain, i)
		}
		goods[i] = p
	}
	kits := make([]KitPlan, kitCount)
	for k := range kits {
		combo := k == 0
		items := g.Sample(len(plain), g.Between(2, min(4, len(plain))))
		kp := KitPlan{Combo: combo, Unit: "hộp"}
		var sum int64
		for _, at := range items {
			item := KitItem{Goods: plain[at], Qty: int64(g.Between(1, 3))}
			kp.Items = append(kp.Items, item)
			sum += goods[item.Goods].Retail * item.Qty
		}
		kp.Retail = max(roundTo(sum*95/100, 1000), 1000)
		if combo {
			kp.Code = fmt.Sprintf("COMBO%03d", k+1)
		} else {
			kp.Code = fmt.Sprintf("KIT%03d", k+1)
		}
		kp.Name = fmt.Sprintf("%s %s số %d", pick(g, kitThemes), pick(g, brandNames), k+1)
		kits[k] = kp
	}
	return CatalogPlan{Goods: goods, Kits: kits}, nil
}

// PlanParties draws n suppliers (organizations) or customers (mostly people).
func PlanParties(g *RNG, n int, customers bool) []PartyPlan {
	out := make([]PartyPlan, n)
	for i := range out {
		p := PartyPlan{Person: customers && g.Chance(70)}
		if p.Person {
			p.Name = personName(g)
		} else {
			p.Name = companyName(g)
		}
		p.Phone = phone(g)
		p.Address = address(g)
		out[i] = p
	}
	return out
}

// roundTo rounds v down to a multiple of step, never below step.
func roundTo(v, step int64) int64 { return max(v/step*step, step) }

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
