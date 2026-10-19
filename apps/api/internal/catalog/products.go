package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

const (
	productColumns = `id, type, name, name_key, category_id, attribute_set_id, attributes, description, status, created_at, updated_at, version`
	imageColumns   = `id, product_id, sku_id, attachment_id, sort_order, created_at`
	// maxImagesPerProduct bounds the gallery of a product.
	maxImagesPerProduct = 20
	// productAttachmentOwner is the attachments.owner_type of product images.
	productAttachmentOwner = "product"
)

func validProductType(t string) bool {
	switch t {
	case ProductGoods, ProductMaterial, ProductKit, ProductCombo:
		return true
	}
	return false
}

func validProductStatus(s string) bool {
	return s == StatusDraft || s == StatusActive || s == StatusInactive
}

// ProductFilter narrows ListProducts. Attributes is a JSON object of filterable attribute values
// matched by containment ({"brand": "<option id>"}).
type ProductFilter struct {
	Q          string
	CategoryID *uuid.UUID
	Status     string
	Type       string
	Attributes json.RawMessage
	// AfterKey and AfterID are the keyset position (name key and id of the last row seen).
	AfterKey *string
	AfterID  uuid.UUID
	Limit    int
}

// ProductListItem is a product row of the catalog list.
type ProductListItem struct {
	Product
	SkuCount int `db:"sku_count"`
}

// Products lists products by name.
func (s *Service) Products(ctx context.Context, scope Scope, f ProductFilter) ([]ProductListItem, error) {
	fields := fieldErrors{}
	if f.Status != "" && !validProductStatus(f.Status) {
		fields["status"] = "invalid"
	}
	if f.Type != "" && !validProductType(f.Type) {
		fields["type"] = "invalid"
	}
	if len(f.Q) > maxSearchTermLen*4 {
		fields["q"] = "too_long"
	}
	filter := "{}"
	if len(f.Attributes) > 0 {
		if len(f.Attributes) > MaxAttributesBytes {
			fields["attributes"] = "too_large"
		} else if obj, err := decodeObject(f.Attributes); err != nil {
			fields["attributes"] = "not_an_object"
		} else if len(obj) > 0 {
			filter = string(f.Attributes)
		}
	}
	if err := fields.err(); err != nil {
		return nil, err
	}
	words := strings.Fields(textnorm.SearchNorm(f.Q))
	patterns := make([]string, len(words))
	for i, w := range words {
		patterns[i] = "%" + w + "%"
	}
	var out []ProductListItem
	err := s.read(ctx, scope, func(q db.Querier) error {
		if filter != "{}" {
			if err := checkFilterableKeys(ctx, q, f.Attributes); err != nil {
				return err
			}
		}
		afterKey := ""
		if f.AfterKey != nil {
			afterKey = *f.AfterKey
		}
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+prefixed("p", productColumns)+`,
			       (SELECT count(*) FROM skus s WHERE s.product_id = p.id) AS sku_count
			FROM products p
			WHERE ($1::uuid IS NULL OR p.category_id = $1)
			  AND ($2 = '' OR p.status = $2)
			  AND ($3 = '' OR p.type = $3)
			  AND p.attributes @> $4::jsonb
			  AND (cardinality($5::text[]) = 0 OR p.name_key LIKE ALL ($5::text[])
			       OR p.id IN (SELECT s.product_id FROM skus s WHERE s.search_norm LIKE ALL ($5::text[])))
			  AND (NOT $6 OR (p.name_key, p.id) > ($7, $8::uuid))
			ORDER BY p.name_key, p.id
			LIMIT $9`,
			f.CategoryID, f.Status, f.Type, filter, patterns, f.AfterKey != nil, afterKey, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("catalog: list products: %w", err)
		}
		return nil
	})
	return out, err
}

// prefixed qualifies a column list with a table alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// checkFilterableKeys accepts only filterable attribute codes as filter keys.
func checkFilterableKeys(ctx context.Context, q db.Querier, raw json.RawMessage) error {
	obj, err := decodeObject(raw)
	if err != nil {
		return errInvalid(map[string]string{"attributes": "not_an_object"})
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	var filterable []string
	if err := sqlx.SelectContext(ctx, q, &filterable, `SELECT code FROM attributes WHERE code = ANY($1) AND is_filterable`, keys); err != nil {
		return fmt.Errorf("catalog: read filterable attributes: %w", err)
	}
	fields := map[string]string{}
	for _, k := range keys {
		found := false
		for _, c := range filterable {
			found = found || c == k
		}
		if !found {
			fields["attributes."+k] = "not_filterable"
		}
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// ProductDetail is a product with its axes, SKUs and images.
type ProductDetail struct {
	Product Product
	Axes    []AxisDetail
	Skus    []SkuDetail
	Images  []ProductImage
}

// AxisDetail is a variant axis with its attribute and options.
type AxisDetail struct {
	Position  int
	Attribute Attribute
}

// SkuDetail is a SKU with its units, barcodes and variant options.
type SkuDetail struct {
	Sku       Sku
	Units     []SkuUnit
	Barcodes  []Barcode
	OptionIDs []uuid.UUID
}

func readProduct(ctx context.Context, q db.Querier, id uuid.UUID) (Product, error) {
	var p Product
	err := q.QueryRowxContext(ctx, `SELECT `+productColumns+` FROM products WHERE id = $1`, id).StructScan(&p)
	if db.IsNoRows(err) {
		return Product{}, errNotFound()
	}
	if err != nil {
		return Product{}, fmt.Errorf("catalog: read product: %w", err)
	}
	return p, nil
}

// Product returns a product with its axes, SKUs, units, barcodes and images.
func (s *Service) Product(ctx context.Context, scope Scope, id uuid.UUID) (ProductDetail, error) {
	var d ProductDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		d, err = readProductDetail(ctx, q, id)
		return err
	})
	return d, err
}

func readProductDetail(ctx context.Context, q db.Querier, id uuid.UUID) (ProductDetail, error) {
	p, err := readProduct(ctx, q, id)
	if err != nil {
		return ProductDetail{}, err
	}
	d := ProductDetail{Product: p}
	if d.Axes, err = readAxes(ctx, q, id); err != nil {
		return ProductDetail{}, err
	}
	var skuIDs []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &skuIDs, `SELECT id FROM skus WHERE product_id = $1 ORDER BY code`, id); err != nil {
		return ProductDetail{}, fmt.Errorf("catalog: list product skus: %w", err)
	}
	if d.Skus, err = readSkuDetails(ctx, q, skuIDs); err != nil {
		return ProductDetail{}, err
	}
	if err := sqlx.SelectContext(ctx, q, &d.Images, `SELECT `+imageColumns+` FROM product_images WHERE product_id = $1
		ORDER BY sort_order, created_at`, id); err != nil {
		return ProductDetail{}, fmt.Errorf("catalog: list product images: %w", err)
	}
	return d, nil
}

// ProductInput creates a product. Sku, when given, creates its single SKU (a product with
// variants gets its SKUs from the variant generator instead).
type ProductInput struct {
	Type           string
	Name           string
	CategoryID     *uuid.UUID
	AttributeSetID Opt[uuid.UUID]
	Attributes     json.RawMessage
	Description    *string
	Status         string
	Sku            *SkuInput
	// SkuExtras adds a purchase unit and base sale prices to Sku; ignored without Sku.
	SkuExtras SkuExtras
}

// SkuExtras completes a product's first SKU: an optional purchase unit holding Factor base
// units, and retail/wholesale prices per base unit.
type SkuExtras struct {
	PurchaseUnitID *uuid.UUID
	Factor         *string
	RetailPrice    *money.Money
	WholesalePrice *money.Money
}

// CreateProduct adds a product. Without an explicit attribute set it takes its category's set.
func (s *Service) CreateProduct(ctx context.Context, actor audit.Actor, in ProductInput) (ProductDetail, error) {
	f := fieldErrors{}
	if in.Type == "" {
		in.Type = ProductGoods
	}
	if !validProductType(in.Type) {
		f["type"] = "invalid"
	}
	// A kit or combo is sold by its recipe: it starts as a draft and publishing the recipe
	// activates it.
	byRecipe := in.Type == ProductKit || in.Type == ProductCombo
	switch {
	case in.Status == "" && byRecipe:
		in.Status = StatusDraft
	case in.Status == "":
		in.Status = StatusActive
	}
	switch {
	case !validProductStatus(in.Status):
		f["status"] = "invalid"
	case byRecipe && in.Status != StatusDraft:
		f["status"] = "set_by_recipe"
	}
	f.text("name", &in.Name, true, maxNameLen)
	in.Description = f.multilineText("description", in.Description, maxDescriptionLen)
	if in.Sku != nil {
		in.Sku.normalise(f, "sku.")
		if f["name"] == "required" || f["sku.code"] == "required" {
			return ProductDetail{}, errCodeAndNameRequired(f)
		}
	}
	x := in.SkuExtras
	if x.RetailPrice != nil && x.RetailPrice.IsNegative() {
		f["sku.retail_price"] = "negative"
	}
	if x.WholesalePrice != nil && x.WholesalePrice.IsNegative() {
		f["sku.wholesale_price"] = "negative"
	}
	if err := f.err(); err != nil {
		return ProductDetail{}, err
	}
	purchaseUnit, factor := uuid.Nil, money.FactorOne
	if in.Sku != nil {
		var err error
		if purchaseUnit, factor, err = purchaseUnitOf(in.Sku.BaseUnitID, x.PurchaseUnitID, x.Factor); err != nil {
			return ProductDetail{}, err
		}
	}
	now := s.clock.Now()
	p := Product{ID: ids.New(), Type: in.Type, Name: in.Name, NameKey: nameKey(in.Name), CategoryID: in.CategoryID,
		AttributeSetID: in.AttributeSetID.Value, Description: in.Description, Status: in.Status,
		CreatedAt: now, UpdatedAt: now, Version: 1}
	var d ProductDetail
	err := s.write(ctx, actor, func(q db.Querier) error {
		if p.CategoryID != nil {
			c, err := readCategory(ctx, q, *p.CategoryID)
			if apperrNotFound(err) {
				return errInvalid(map[string]string{"category_id": "unknown_category"})
			}
			if err != nil {
				return err
			}
			if !in.AttributeSetID.Set {
				p.AttributeSetID = c.AttributeSetID
			}
		}
		if err := checkAttributeSet(ctx, q, p.AttributeSetID, "attribute_set_id"); err != nil {
			return err
		}
		set, err := setAttributes(ctx, q, p.AttributeSetID)
		if err != nil {
			return err
		}
		if p.Attributes, err = s.validator.Validate(in.Attributes, set, nil); err != nil {
			return err
		}
		if err := insertProduct(ctx, q, actor.TenantID, p); err != nil {
			return err
		}
		if in.Sku != nil {
			sku, err := s.insertSku(ctx, q, actor.TenantID, p, SkuDraft{DisplayName: p.Name}, *in.Sku, "sku.")
			if err != nil {
				return err
			}
			if purchaseUnit != uuid.Nil {
				if err := addPurchaseUnit(ctx, q, actor.TenantID, sku.ID, purchaseUnit, factor, now); err != nil {
					return err
				}
			}
			if _, err := s.setBasePrices(ctx, q, actor, sku.ID, in.Sku.BaseUnitID, x.RetailPrice, x.WholesalePrice, ""); err != nil {
				return err
			}
		}
		if d, err = readProductDetail(ctx, q, p.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "product.create", "product", p.ID, nil, d)
	})
	return d, err
}

func insertProduct(ctx context.Context, q db.Querier, tenantID uuid.UUID, p Product) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO products (id, tenant_id, type, name, name_key, category_id, attribute_set_id, attributes, description, status,
		  created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`,
		p.ID, tenantID, p.Type, p.Name, p.NameKey, p.CategoryID, p.AttributeSetID, p.Attributes, p.Description, p.Status, p.CreatedAt)
	if db.IsUniqueViolation(err, "ux_products__name_key") {
		return errProductNameTaken()
	}
	if err != nil {
		return fmt.Errorf("catalog: insert product: %w", err)
	}
	return nil
}

// ProductPatch changes a product; unset fields are kept. Attributes, when given, replaces the
// values of the set's attributes (values hidden by the set are kept).
type ProductPatch struct {
	Type           *string
	Name           *string
	CategoryID     Opt[uuid.UUID]
	AttributeSetID Opt[uuid.UUID]
	Attributes     json.RawMessage
	Description    Opt[string]
	Status         *string
}

// UpdateProduct changes a product guarded by its version. Renaming renames its SKUs; the type is
// fixed once the product has SKUs.
func (s *Service) UpdateProduct(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in ProductPatch) (ProductDetail, error) {
	f := fieldErrors{}
	if in.Type != nil && !validProductType(*in.Type) {
		f["type"] = "invalid"
	}
	if in.Status != nil && !validProductStatus(*in.Status) {
		f["status"] = "invalid"
	}
	if in.Name != nil {
		f.text("name", in.Name, true, maxNameLen)
	}
	if in.Description.Set {
		in.Description.Value = f.multilineText("description", in.Description.Value, maxDescriptionLen)
	}
	if err := f.err(); err != nil {
		return ProductDetail{}, err
	}
	var d ProductDetail
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := lockProduct(ctx, q, id)
		if err != nil {
			return err
		}
		p := before
		var skuIDs []uuid.UUID
		if err := sqlx.SelectContext(ctx, q, &skuIDs, `SELECT id FROM skus WHERE product_id = $1`, id); err != nil {
			return fmt.Errorf("catalog: list product skus: %w", err)
		}
		if in.Type != nil && *in.Type != p.Type {
			if len(skuIDs) > 0 {
				return errProductTypeLocked()
			}
			p.Type = *in.Type
		}
		if in.Name != nil {
			p.Name, p.NameKey = *in.Name, nameKey(*in.Name)
		}
		if in.CategoryID.Set {
			p.CategoryID = in.CategoryID.Value
			if p.CategoryID != nil {
				if _, err := readCategory(ctx, q, *p.CategoryID); apperrNotFound(err) {
					return errInvalid(map[string]string{"category_id": "unknown_category"})
				} else if err != nil {
					return err
				}
			}
		}
		if in.AttributeSetID.Set {
			p.AttributeSetID = in.AttributeSetID.Value
			if err := checkAttributeSet(ctx, q, p.AttributeSetID, "attribute_set_id"); err != nil {
				return err
			}
		}
		if in.Attributes != nil || in.AttributeSetID.Set {
			input := in.Attributes
			if input == nil {
				input = json.RawMessage(before.Attributes)
			}
			set, err := setAttributes(ctx, q, p.AttributeSetID)
			if err != nil {
				return err
			}
			if p.Attributes, err = s.validator.Validate(input, set, before.Attributes); err != nil {
				return err
			}
		}
		if in.Description.Set {
			p.Description = in.Description.Value
		}
		if in.Status != nil && *in.Status != p.Status {
			if (p.Type == ProductKit || p.Type == ProductCombo) && !s.recipeStatus {
				return errInvalid(map[string]string{"status": "set_by_recipe"})
			}
			p.Status = *in.Status
		}
		p.Version, err = db.UpdateWithVersion(ctx, q, "products", id, expected, s.clock.Now(),
			db.Set{Column: "type", Value: p.Type}, db.Set{Column: "name", Value: p.Name},
			db.Set{Column: "name_key", Value: p.NameKey}, db.Set{Column: "category_id", Value: p.CategoryID},
			db.Set{Column: "attribute_set_id", Value: p.AttributeSetID}, db.Set{Column: "attributes", Value: p.Attributes},
			db.Set{Column: "description", Value: p.Description}, db.Set{Column: "status", Value: p.Status})
		if db.IsUniqueViolation(err, "ux_products__name_key") {
			return errProductNameTaken()
		}
		if err != nil {
			return err
		}
		if p.Name != before.Name {
			err = s.refreshSkuNames(ctx, q, skuIDs)
		} else if !jsonEqual(json.RawMessage(p.Attributes), json.RawMessage(before.Attributes)) {
			err = s.indexer.Reindex(ctx, q, skuIDs)
		}
		if err != nil {
			return err
		}
		if d, err = readProductDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "product.update", "product", id, before, d.Product)
	})
	return d, err
}

// DeleteProduct removes a product none of whose SKUs is used, with its SKUs, units, barcodes,
// variant values, axes and image links (the attachments stay).
func (s *Service) DeleteProduct(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		p, err := lockProduct(ctx, q, id)
		if err != nil {
			return err
		}
		used, err := s.usage.ProductInUse(ctx, q, id)
		if err != nil {
			return err
		}
		if used {
			return errProductInUse()
		}
		for _, stmt := range []string{
			`DELETE FROM product_images WHERE product_id = $1`,
			`DELETE FROM sku_barcodes WHERE sku_id IN (SELECT id FROM skus WHERE product_id = $1)`,
			`DELETE FROM sku_units WHERE sku_id IN (SELECT id FROM skus WHERE product_id = $1)`,
			`DELETE FROM sku_variant_values WHERE sku_id IN (SELECT id FROM skus WHERE product_id = $1)`,
			`DELETE FROM skus WHERE product_id = $1`,
			`DELETE FROM product_variant_axes WHERE product_id = $1`,
			`UPDATE mix_groups SET source_product_id = NULL WHERE source_product_id = $1`,
			`DELETE FROM products WHERE id = $1`,
		} {
			if _, err := q.ExecContext(ctx, stmt, id); err != nil {
				if db.IsForeignKeyViolation(err, "") {
					return errProductInUse()
				}
				return fmt.Errorf("catalog: delete product: %w", err)
			}
		}
		return s.record(ctx, q, actor, "product.delete", "product", id, p, nil)
	})
}

// CreateProductIn runs CreateProduct in the caller's tenant transaction q: a kit or combo writes
// its product together with its recipe.
func (s *Service) CreateProductIn(ctx context.Context, q db.Querier, actor audit.Actor, in ProductInput) (ProductDetail, error) {
	return s.within(q).CreateProduct(ctx, actor, in)
}

// UpdateProductIn runs UpdateProduct in the caller's tenant transaction q.
func (s *Service) UpdateProductIn(ctx context.Context, q db.Querier, actor audit.Actor, id uuid.UUID, expected int, in ProductPatch) (ProductDetail, error) {
	return s.within(q).UpdateProduct(ctx, actor, id, expected, in)
}

// UpdateKitProductIn runs UpdateProduct in the caller's tenant transaction q for the kit that owns
// the product: it alone may change the status of a kit or combo, as it publishes or archives the
// kit's recipe.
func (s *Service) UpdateKitProductIn(ctx context.Context, q db.Querier, actor audit.Actor, id uuid.UUID, expected int, in ProductPatch) (ProductDetail, error) {
	c := s.within(q)
	c.recipeStatus = true
	return c.UpdateProduct(ctx, actor, id, expected, in)
}

// DeleteProductIn runs DeleteProduct in the caller's tenant transaction q.
func (s *Service) DeleteProductIn(ctx context.Context, q db.Querier, actor audit.Actor, id uuid.UUID) error {
	return s.within(q).DeleteProduct(ctx, actor, id)
}

// Images returns the images of a product.
func (s *Service) Images(ctx context.Context, scope Scope, productID uuid.UUID) ([]ProductImage, error) {
	var out []ProductImage
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := readProduct(ctx, q, productID); err != nil {
			return err
		}
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+imageColumns+` FROM product_images WHERE product_id = $1
			ORDER BY sort_order, created_at`, productID)
	})
	return out, err
}

// ImageInput links an uploaded attachment to a product.
type ImageInput struct {
	AttachmentID uuid.UUID
	SkuID        *uuid.UUID
	SortOrder    *int
}

// AddImage links a clean image attachment uploaded for the product (owner_type "product").
func (s *Service) AddImage(ctx context.Context, actor audit.Actor, productID uuid.UUID, in ImageInput) (ProductImage, error) {
	var img ProductImage
	err := s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockProduct(ctx, q, productID); err != nil {
			return err
		}
		var att struct {
			OwnerType   string    `db:"owner_type"`
			OwnerID     uuid.UUID `db:"owner_id"`
			ScanStatus  string    `db:"scan_status"`
			ContentType string    `db:"content_type"`
		}
		err := q.QueryRowxContext(ctx, `SELECT owner_type, owner_id, scan_status, content_type FROM attachments WHERE id = $1`,
			in.AttachmentID).StructScan(&att)
		if db.IsNoRows(err) {
			return errInvalid(map[string]string{"attachment_id": "unknown_attachment"})
		}
		if err != nil {
			return fmt.Errorf("catalog: read attachment: %w", err)
		}
		switch {
		case att.OwnerType != productAttachmentOwner || att.OwnerID != productID:
			return errInvalid(map[string]string{"attachment_id": "not_for_this_product"})
		case att.ScanStatus != "clean":
			return errInvalid(map[string]string{"attachment_id": "not_clean"})
		case !strings.HasPrefix(att.ContentType, "image/"):
			return errInvalid(map[string]string{"attachment_id": "not_an_image"})
		}
		if in.SkuID != nil {
			ok, err := exists(ctx, q, `SELECT 1 FROM skus WHERE id = $1 AND product_id = $2`, *in.SkuID, productID)
			if err != nil {
				return err
			}
			if !ok {
				return errInvalid(map[string]string{"sku_id": "not_of_this_product"})
			}
		}
		var count, dup int
		if err := q.QueryRowxContext(ctx, `SELECT count(*), count(*) FILTER (WHERE attachment_id = $2) FROM product_images
			WHERE product_id = $1`, productID, in.AttachmentID).Scan(&count, &dup); err != nil {
			return fmt.Errorf("catalog: count images: %w", err)
		}
		if dup > 0 {
			return errInvalid(map[string]string{"attachment_id": "already_linked"})
		}
		if count >= maxImagesPerProduct {
			return errInvalid(map[string]string{"attachment_id": "too_many_images"})
		}
		img = ProductImage{ID: ids.New(), ProductID: productID, SkuID: in.SkuID, AttachmentID: in.AttachmentID,
			SortOrder: (count + 1) * 10, CreatedAt: s.clock.Now()}
		if in.SortOrder != nil {
			img.SortOrder = *in.SortOrder
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO product_images (id, tenant_id, product_id, sku_id, attachment_id, sort_order, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			img.ID, actor.TenantID, img.ProductID, img.SkuID, img.AttachmentID, img.SortOrder, img.CreatedAt); err != nil {
			return fmt.Errorf("catalog: insert product image: %w", err)
		}
		return s.record(ctx, q, actor, "product.image_add", "product", productID, nil, img)
	})
	return img, err
}

// DeleteImage unlinks an image from a product (the attachment stays).
func (s *Service) DeleteImage(ctx context.Context, actor audit.Actor, productID, imageID uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockProduct(ctx, q, productID); err != nil {
			return err
		}
		var img ProductImage
		err := q.QueryRowxContext(ctx, `DELETE FROM product_images WHERE id = $1 AND product_id = $2 RETURNING `+imageColumns,
			imageID, productID).StructScan(&img)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("catalog: delete product image: %w", err)
		}
		return s.record(ctx, q, actor, "product.image_remove", "product", productID, img, nil)
	})
}
