package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// codePrefixFormat is what a generated SKU code prefix may contain ("<prefix>-<n>" stays a valid code).
var codePrefixFormat = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,29}$`)

// VariantAxes returns the variant axes of a product.
func (s *Service) VariantAxes(ctx context.Context, scope Scope, productID uuid.UUID) ([]AxisDetail, error) {
	var out []AxisDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := readProduct(ctx, q, productID); err != nil {
			return err
		}
		var err error
		out, err = readAxes(ctx, q, productID)
		return err
	})
	return out, err
}

// ReplaceVariantAxes sets the variant axes of a product (attribute ids in position order). Once a
// SKU of the product has stock movements the axes are locked. Existing SKUs keep their variant
// keys; generating again adds the SKUs of the new combinations.
func (s *Service) ReplaceVariantAxes(ctx context.Context, actor audit.Actor, productID uuid.UUID, attributeIDs []uuid.UUID) ([]AxisDetail, error) {
	var out []AxisDetail
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockProduct(ctx, q, productID); err != nil {
			return err
		}
		var current []VariantAxis
		if err := sqlx.SelectContext(ctx, q, &current, `SELECT attribute_id, position FROM product_variant_axes WHERE product_id = $1`,
			productID); err != nil {
			return fmt.Errorf("catalog: read variant axes: %w", err)
		}
		var skuIDs []uuid.UUID
		if err := sqlx.SelectContext(ctx, q, &skuIDs, `SELECT id FROM skus WHERE product_id = $1`, productID); err != nil {
			return fmt.Errorf("catalog: list product skus: %w", err)
		}
		attrs := map[uuid.UUID]Attribute{}
		if len(attributeIDs) > 0 {
			list, err := listAttributes(ctx, q, "", attributeIDs)
			if err != nil {
				return err
			}
			for _, a := range list {
				attrs[a.ID] = a
			}
		}
		if err := s.axisSpec.Check(ctx, q, skuIDs, current, attributeIDs, attrs); err != nil {
			return err
		}
		if !sameAxes(current, attributeIDs) {
			if _, err := q.ExecContext(ctx, `DELETE FROM product_variant_axes WHERE product_id = $1`, productID); err != nil {
				return fmt.Errorf("catalog: clear variant axes: %w", err)
			}
			for i, id := range attributeIDs {
				if _, err := q.ExecContext(ctx, `INSERT INTO product_variant_axes (tenant_id, product_id, attribute_id, position)
					VALUES ($1, $2, $3, $4)`, actor.TenantID, productID, id, i+1); err != nil {
					return fmt.Errorf("catalog: insert variant axis: %w", err)
				}
			}
		}
		var err error
		if out, err = readAxes(ctx, q, productID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "product.axes_replace", "product", productID, current, attributeIDs)
	})
	return out, err
}

// VariantRequest selects the options of the product's axes to span (all active options of an axis
// when none of its options is listed) and how new SKUs are coded: an explicit code per variant key,
// else "<CodePrefix>-<n>". Defaults are the fields of new SKUs (Code is ignored).
type VariantRequest struct {
	OptionIDs  []uuid.UUID
	CodePrefix string
	Codes      map[string]string
	Defaults   *SkuInput
}

// VariantRow is one cell of the matrix: an existing SKU or the SKU generation would create.
type VariantRow struct {
	VariantKey   string
	VariantLabel string
	DisplayName  string
	OptionIDs    []uuid.UUID
	Code         string
	// SkuID is set when the variant already exists.
	SkuID *uuid.UUID
}

// VariantResult is the outcome of a generation.
type VariantResult struct {
	Created  []SkuDetail
	Existing int
}

// planVariants spans the matrix and assigns codes; preview and generation share it so the preview
// equals the generation that follows on the same catalog state.
func (s *Service) planVariants(ctx context.Context, q db.Querier, p Product, req VariantRequest) ([]VariantRow, []SkuDraft, error) {
	axes, err := readAxes(ctx, q, p.ID)
	if err != nil {
		return nil, nil, err
	}
	selected := map[uuid.UUID]bool{}
	for _, id := range req.OptionIDs {
		selected[id] = true
	}
	matched := map[uuid.UUID]bool{}
	spans := make([]AxisOptions, len(axes))
	for i, ax := range axes {
		spans[i].Attribute = ax.Attribute
		anySelected := false
		for _, o := range ax.Attribute.Options {
			anySelected = anySelected || selected[o.ID]
		}
		for _, o := range ax.Attribute.Options {
			switch {
			case selected[o.ID]:
				matched[o.ID] = true
				spans[i].Options = append(spans[i].Options, o)
			case !anySelected && o.Active:
				spans[i].Options = append(spans[i].Options, o)
			}
		}
	}
	fields := map[string]string{}
	for i, id := range req.OptionIDs {
		if !matched[id] {
			fields[fmt.Sprintf("option_ids[%d]", i)] = "not_an_axis_option"
		}
	}
	if len(fields) > 0 {
		return nil, nil, errInvalid(fields)
	}
	drafts, err := s.matrix.Build(p.Name, spans)
	if err != nil {
		return nil, nil, err
	}
	var existing []struct {
		ID         uuid.UUID `db:"id"`
		Code       string    `db:"code"`
		VariantKey string    `db:"variant_key"`
	}
	if err := sqlx.SelectContext(ctx, q, &existing, `SELECT id, code, variant_key FROM skus WHERE product_id = $1`, p.ID); err != nil {
		return nil, nil, fmt.Errorf("catalog: read product skus: %w", err)
	}
	byKey := map[string]int{}
	for i, e := range existing {
		byKey[e.VariantKey] = i
	}
	prefix := strings.TrimSpace(req.CodePrefix)
	var taken []string
	if prefix != "" {
		if !codePrefixFormat.MatchString(prefix) {
			return nil, nil, errInvalid(map[string]string{"code_prefix": "invalid_format"})
		}
		if err := sqlx.SelectContext(ctx, q, &taken, `SELECT code FROM skus WHERE upper(code) LIKE upper($1) || '-%'`, prefix); err != nil {
			return nil, nil, fmt.Errorf("catalog: read taken sku codes: %w", err)
		}
	}
	alloc := NewSkuCodeAllocator(prefix, taken)
	explicit := map[string]string{}
	for key, code := range req.Codes {
		code = strings.TrimSpace(code)
		if !skuCodeFormat.MatchString(code) {
			fields["codes."+key] = "invalid_format"
			continue
		}
		explicit[key] = code
		alloc.Reserve(code)
	}
	if len(fields) > 0 {
		return nil, nil, errInvalid(fields)
	}
	rows := make([]VariantRow, len(drafts))
	var fresh []SkuDraft
	for i, d := range drafts {
		rows[i] = VariantRow{VariantKey: d.VariantKey, VariantLabel: d.VariantLabel, DisplayName: d.DisplayName, OptionIDs: d.OptionIDs}
		if j, ok := byKey[d.VariantKey]; ok {
			id := existing[j].ID
			rows[i].Code, rows[i].SkuID = existing[j].Code, &id
			continue
		}
		switch code, ok := explicit[d.VariantKey]; {
		case ok:
			rows[i].Code = code
		case prefix == "":
			return nil, nil, errInvalid(map[string]string{"code_prefix": "required"})
		default:
			rows[i].Code = alloc.Next()
		}
		fresh = append(fresh, d)
	}
	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.SkuID == nil {
			codes = append(codes, strings.ToUpper(r.Code))
		}
	}
	if len(codes) > 0 {
		var clash []string
		if err := sqlx.SelectContext(ctx, q, &clash, `SELECT code FROM skus WHERE upper(code) = ANY($1) ORDER BY code`, codes); err != nil {
			return nil, nil, fmt.Errorf("catalog: check sku codes: %w", err)
		}
		if len(clash) > 0 {
			return nil, nil, errSkuCodeTaken()
		}
		seen := map[string]bool{}
		for _, c := range codes {
			if seen[strings.ToUpper(c)] {
				return nil, nil, errSkuCodeTaken()
			}
			seen[strings.ToUpper(c)] = true
		}
	}
	return rows, fresh, nil
}

// PreviewVariants returns the matrix generation would produce, without writing.
func (s *Service) PreviewVariants(ctx context.Context, scope Scope, productID uuid.UUID, req VariantRequest) ([]VariantRow, error) {
	var rows []VariantRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		p, err := readProduct(ctx, q, productID)
		if err != nil {
			return err
		}
		rows, _, err = s.planVariants(ctx, q, p, req)
		return err
	})
	return rows, err
}

// GenerateVariants creates the SKUs of the matrix that do not exist yet, in one transaction.
// Generating again with the same request creates nothing.
func (s *Service) GenerateVariants(ctx context.Context, actor audit.Actor, productID uuid.UUID, req VariantRequest) (VariantResult, error) {
	var res VariantResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		p, err := lockProduct(ctx, q, productID)
		if err != nil {
			return err
		}
		rows, fresh, err := s.planVariants(ctx, q, p, req)
		if err != nil {
			return err
		}
		res.Existing = len(rows) - len(fresh)
		if len(fresh) == 0 {
			res.Created = []SkuDetail{}
			return nil
		}
		if req.Defaults == nil {
			return errInvalid(map[string]string{"defaults": "required"})
		}
		defaults := *req.Defaults
		f := fieldErrors{}
		defaults.normaliseFields(f, "defaults.")
		if defaults.Barcode != nil {
			f["defaults.barcode"] = "not_allowed"
		}
		if err := f.err(); err != nil {
			return err
		}
		codeOf := map[string]string{}
		for _, r := range rows {
			codeOf[r.VariantKey] = r.Code
		}
		created := make([]uuid.UUID, 0, len(fresh))
		for _, d := range fresh {
			in := defaults
			in.Code = codeOf[d.VariantKey]
			sku, err := s.insertSku(ctx, q, actor.TenantID, p, d, in, "defaults.")
			if err != nil {
				return err
			}
			created = append(created, sku.ID)
		}
		if res.Created, err = readSkuDetails(ctx, q, created); err != nil {
			return err
		}
		codes := make([]string, len(res.Created))
		for i, d := range res.Created {
			codes[i] = d.Sku.Code
		}
		return s.record(ctx, q, actor, "product.variants_generate", "product", productID, nil, map[string]any{"created": codes})
	})
	return res, err
}
