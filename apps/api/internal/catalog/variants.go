package catalog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MaxVariantAxes keeps the SKU matrix usable.
const MaxVariantAxes = 3

// MaxVariantsPerProduct bounds one generation (5 × 10 × 10 is already more than a shop sells).
const MaxVariantsPerProduct = 500

// variantLabelSeparator joins option labels of a variant: "Hộp 500g · Dâu".
const variantLabelSeparator = " · "

// displayNameSeparator joins the product name and the variant label: "Sữa TH 180ml – Dâu".
const displayNameSeparator = " – "

// AxisOptions are the options of one variant axis taking part in a matrix, in axis order.
type AxisOptions struct {
	Attribute Attribute
	Options   []AttributeOption
}

// SkuDraft is one cell of a variant matrix.
type SkuDraft struct {
	// VariantKey is the option ids ordered by axis position joined by '|' ("" without axes).
	VariantKey   string
	VariantLabel string
	DisplayName  string
	OptionIDs    []uuid.UUID
	// AttributeIDs are the axis attributes of OptionIDs, in the same order.
	AttributeIDs []uuid.UUID
}

// VariantKeyOf joins option ids in axis order.
func VariantKeyOf(optionIDs []uuid.UUID) string {
	parts := make([]string, len(optionIDs))
	for i, id := range optionIDs {
		parts[i] = id.String()
	}
	return strings.Join(parts, "|")
}

// SKUNamer builds the display name of a SKU from its product name and variant label.
func SKUNamer(productName, variantLabel string) string {
	if variantLabel == "" {
		return productName
	}
	return productName + displayNameSeparator + variantLabel
}

// VariantMatrixFactory spans the SKU matrix of a product.
type VariantMatrixFactory struct{}

// Build returns the cartesian product of the axes' options, the first axis varying slowest. A
// product without axes has exactly one variant with an empty key. Every axis needs at least one
// option.
func (VariantMatrixFactory) Build(productName string, axes []AxisOptions) ([]SkuDraft, error) {
	if len(axes) > MaxVariantAxes {
		return nil, errInvalid(map[string]string{"axes": "too_many"})
	}
	total := 1
	for i, ax := range axes {
		if len(ax.Options) == 0 {
			return nil, errInvalid(map[string]string{fmt.Sprintf("axes[%d].option_ids", i): "required"})
		}
		total *= len(ax.Options)
		if total > MaxVariantsPerProduct {
			return nil, errInvalid(map[string]string{"axes": "too_many_variants"})
		}
	}
	drafts := make([]SkuDraft, 0, total)
	idx := make([]int, len(axes))
	for {
		d := SkuDraft{OptionIDs: make([]uuid.UUID, len(axes)), AttributeIDs: make([]uuid.UUID, len(axes))}
		labels := make([]string, len(axes))
		for i, ax := range axes {
			o := ax.Options[idx[i]]
			d.OptionIDs[i] = o.ID
			d.AttributeIDs[i] = ax.Attribute.ID
			labels[i] = o.Label
		}
		d.VariantKey = VariantKeyOf(d.OptionIDs)
		d.VariantLabel = strings.Join(labels, variantLabelSeparator)
		d.DisplayName = SKUNamer(productName, d.VariantLabel)
		drafts = append(drafts, d)
		// odometer increment, last axis fastest
		i := len(axes) - 1
		for ; i >= 0; i-- {
			idx[i]++
			if idx[i] < len(axes[i].Options) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			return drafts, nil
		}
	}
}

// skuCodeFormat is what a SKU code may contain (also used for generated codes).
var skuCodeFormat = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$`)

// SkuCodeAllocator hands out "<prefix>-<n>" codes, skipping codes already taken.
type SkuCodeAllocator struct {
	prefix string
	taken  map[string]bool
	next   int
}

// NewSkuCodeAllocator numbers from 1, skipping the codes in taken, so the same catalog state
// always yields the same codes (a preview equals the generation that follows it).
func NewSkuCodeAllocator(prefix string, taken []string) *SkuCodeAllocator {
	a := &SkuCodeAllocator{prefix: prefix, taken: map[string]bool{}, next: 1}
	for _, c := range taken {
		a.taken[strings.ToUpper(c)] = true
	}
	return a
}

// Reserve marks code as used (an explicit code given by the user).
func (a *SkuCodeAllocator) Reserve(code string) { a.taken[strings.ToUpper(code)] = true }

// Taken reports whether code is already used.
func (a *SkuCodeAllocator) Taken(code string) bool { return a.taken[strings.ToUpper(code)] }

// Next returns the next free code.
func (a *SkuCodeAllocator) Next() string {
	for {
		code := a.prefix + "-" + strconv.Itoa(a.next)
		a.next++
		if !a.taken[strings.ToUpper(code)] {
			a.taken[strings.ToUpper(code)] = true
			return code
		}
	}
}
