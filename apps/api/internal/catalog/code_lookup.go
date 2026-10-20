package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// What a scanned code matched.
const (
	MatchedByBarcode = "barcode"
	MatchedBySkuCode = "sku_code"
)

// CodeMatch is the (SKU, unit) a scanned code stands for: the unit of the barcode, or the base unit
// when the code is a SKU code. Factor converts one of that unit to base units.
type CodeMatch struct {
	SkuID     uuid.UUID    `db:"sku_id"`
	UnitID    uuid.UUID    `db:"unit_id"`
	UnitName  string       `db:"unit_name"`
	Factor    money.Factor `db:"factor"`
	MatchedBy string       `db:"matched_by"`
}

// SkuCodeMatch is a CodeMatch with the SKU as the product lists show it.
type SkuCodeMatch struct {
	CodeMatch
	Sku SkuListItem
}

// LookupCode resolves a scanned or typed code to an active SKU of an active product. A barcode
// matches under any of its CodeVariants (the code as typed first, then the variant order) on an
// active unit of the SKU; failing that, the code is tried as a SKU code as typed, then upper-cased.
// No match answers 404 code_not_found; a code that cannot be a barcode answers 422.
func (s *Service) LookupCode(ctx context.Context, scope Scope, code string) (CodeMatch, error) {
	code = strings.TrimSpace(code)
	if err := ValidateBarcode(code); err != nil {
		return CodeMatch{}, err
	}
	var out CodeMatch
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = lookupCode(ctx, q, code)
		return err
	})
	return out, err
}

// LookupCodeIn is LookupCode inside the caller's tenant transaction q.
func (s *Service) LookupCodeIn(ctx context.Context, q db.Querier, code string) (CodeMatch, error) {
	return s.within(q).LookupCode(ctx, Scope{}, code)
}

// LookupSkuCode is LookupCode with the matched SKU read in the same transaction.
func (s *Service) LookupSkuCode(ctx context.Context, scope Scope, code string) (SkuCodeMatch, error) {
	code = strings.TrimSpace(code)
	if err := ValidateBarcode(code); err != nil {
		return SkuCodeMatch{}, err
	}
	var out SkuCodeMatch
	err := s.read(ctx, scope, func(q db.Querier) error {
		m, err := lookupCode(ctx, q, code)
		if err != nil {
			return err
		}
		items := []SkuListItem{}
		if err := sqlx.SelectContext(ctx, q, &items, skuListSelect+` WHERE s.id = $1`, m.SkuID); err != nil {
			return fmt.Errorf("catalog: read matched sku: %w", err)
		}
		if len(items) == 0 {
			return errCodeNotFound()
		}
		if err := s.fillSkuList(ctx, q, items); err != nil {
			return err
		}
		out = SkuCodeMatch{CodeMatch: m, Sku: items[0]}
		return nil
	})
	return out, err
}

func lookupCode(ctx context.Context, q db.Querier, code string) (CodeMatch, error) {
	var found []CodeMatch
	if err := sqlx.SelectContext(ctx, q, &found, `
		SELECT b.sku_id, b.unit_id, u.name AS unit_name, su.factor, 'barcode' AS matched_by
		FROM sku_barcodes b
		JOIN skus s ON s.id = b.sku_id
		JOIN products p ON p.id = s.product_id
		JOIN sku_units su ON su.sku_id = b.sku_id AND su.unit_id = b.unit_id
		JOIN units u ON u.id = b.unit_id
		WHERE b.code = ANY($1::text[]) AND s.status = 'active' AND p.status = 'active' AND su.active
		ORDER BY array_position($1::text[], b.code)
		LIMIT 1`, CodeVariants(code)); err != nil {
		return CodeMatch{}, fmt.Errorf("catalog: look up barcode: %w", err)
	}
	if len(found) > 0 {
		return found[0], nil
	}
	if err := sqlx.SelectContext(ctx, q, &found, `
		SELECT s.id AS sku_id, s.base_unit_id AS unit_id, u.name AS unit_name, su.factor, 'sku_code' AS matched_by
		FROM skus s
		JOIN products p ON p.id = s.product_id
		JOIN sku_units su ON su.sku_id = s.id AND su.unit_id = s.base_unit_id
		JOIN units u ON u.id = s.base_unit_id
		WHERE s.code IN ($1, upper($1)) AND s.status = 'active' AND p.status = 'active'
		ORDER BY s.code = $1 DESC
		LIMIT 1`, code); err != nil {
		return CodeMatch{}, fmt.Errorf("catalog: look up sku code: %w", err)
	}
	if len(found) == 0 {
		return CodeMatch{}, errCodeNotFound()
	}
	return found[0], nil
}
