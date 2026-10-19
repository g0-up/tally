package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Search kinds of GET /search.
const (
	SearchSku      = "sku"
	SearchCustomer = "customer"
	SearchSupplier = "supplier"
)

// Search limits.
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 50
	maxSearchTermLen   = 100
	// searchCandidates is how many rows the repository returns for ranking in Go.
	searchCandidates = 200
)

// SkuSearchText is what the search text of one SKU is built from.
type SkuSearchText struct {
	Code        string
	DisplayName string
	Barcodes    []string
	// FlavorLabels are the short labels the SKU carries in mix groups.
	FlavorLabels []string
	// AttributeTexts are the product's values of searchable attributes (option labels for selects).
	AttributeTexts []string
}

// SearchIndexer builds skus.search_norm: code, display name (product name and variant label),
// barcodes, flavor labels and searchable attribute values, without diacritics.
type SearchIndexer struct{}

// Text returns the normalised search text of a SKU.
func (SearchIndexer) Text(in SkuSearchText) string {
	parts := make([]string, 0, 2+len(in.Barcodes)+len(in.FlavorLabels)+len(in.AttributeTexts))
	parts = append(parts, in.Code, in.DisplayName)
	parts = append(parts, in.Barcodes...)
	parts = append(parts, in.FlavorLabels...)
	parts = append(parts, in.AttributeTexts...)
	return textnorm.SearchNorm(strings.Join(parts, " "))
}

type skuIndexRow struct {
	ID          uuid.UUID  `db:"id"`
	Code        string     `db:"code"`
	DisplayName string     `db:"display_name"`
	Attributes  JSONObject `db:"attributes"`
}

type skuText struct {
	SkuID uuid.UUID `db:"sku_id"`
	Text  string    `db:"text"`
}

// Reindex rebuilds the search text of the SKUs.
func (ix SearchIndexer) Reindex(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) error {
	if len(skuIDs) == 0 {
		return nil
	}
	var rows []skuIndexRow
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT s.id, s.code, s.display_name, p.attributes
		FROM skus s JOIN products p ON p.id = s.product_id WHERE s.id = ANY($1)`, skuIDs); err != nil {
		return fmt.Errorf("catalog: read skus to index: %w", err)
	}
	var barcodes, flavors []skuText
	if err := sqlx.SelectContext(ctx, q, &barcodes, `SELECT sku_id, code AS text FROM sku_barcodes WHERE sku_id = ANY($1) ORDER BY code`, skuIDs); err != nil {
		return fmt.Errorf("catalog: read barcodes to index: %w", err)
	}
	if err := sqlx.SelectContext(ctx, q, &flavors, `SELECT sku_id, flavor_label AS text FROM mix_group_items WHERE sku_id = ANY($1) ORDER BY flavor_label`, skuIDs); err != nil {
		return fmt.Errorf("catalog: read flavor labels to index: %w", err)
	}
	searchable, err := searchableAttributes(ctx, q)
	if err != nil {
		return err
	}
	group := func(texts []skuText) map[uuid.UUID][]string {
		m := map[uuid.UUID][]string{}
		for _, t := range texts {
			m[t.SkuID] = append(m[t.SkuID], t.Text)
		}
		return m
	}
	byBarcode, byFlavor := group(barcodes), group(flavors)
	idList := make([]uuid.UUID, len(rows))
	norms := make([]string, len(rows))
	for i, r := range rows {
		idList[i] = r.ID
		norms[i] = ix.Text(SkuSearchText{
			Code: r.Code, DisplayName: r.DisplayName, Barcodes: byBarcode[r.ID], FlavorLabels: byFlavor[r.ID],
			AttributeTexts: attributeTexts(r.Attributes, searchable),
		})
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE skus SET search_norm = v.norm FROM unnest($1::uuid[], $2::text[]) AS v(id, norm) WHERE skus.id = v.id`,
		idList, norms); err != nil {
		return fmt.Errorf("catalog: update search text: %w", err)
	}
	return nil
}

func searchableAttributes(ctx context.Context, q db.Querier) ([]Attribute, error) {
	var idList []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &idList, `SELECT id FROM attributes WHERE is_searchable AND status = 'active'`); err != nil {
		return nil, fmt.Errorf("catalog: read searchable attributes: %w", err)
	}
	if len(idList) == 0 {
		return nil, nil
	}
	return listAttributes(ctx, q, StatusActive, idList)
}

// attributeTexts returns the searchable words of a product's attribute values.
func attributeTexts(values JSONObject, searchable []Attribute) []string {
	if len(searchable) == 0 || len(values) == 0 {
		return nil
	}
	obj, err := decodeObject(values)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range searchable {
		raw, ok := obj[a.Code]
		if !ok {
			continue
		}
		label := func(id string) {
			for _, o := range a.Options {
				if o.ID.String() == id {
					out = append(out, o.Label)
				}
			}
		}
		switch a.DataType {
		case DataText:
			var s string
			if json.Unmarshal(raw, &s) == nil {
				out = append(out, s)
			}
		case DataNumber:
			var n json.Number
			if json.Unmarshal(raw, &n) == nil {
				text := n.String()
				if a.UnitLabel != nil {
					text += *a.UnitLabel
				}
				out = append(out, text)
			}
		case DataSelect:
			var s string
			if json.Unmarshal(raw, &s) == nil {
				label(s)
			}
		case DataMultiselect:
			var list []string
			if json.Unmarshal(raw, &list) == nil {
				for _, s := range list {
					label(s)
				}
			}
		case DataBoolean, DataDate:
			// not searchable as words
		}
	}
	return out
}

// refreshSkuNames rebuilds the variant label and display name of SKUs from their product name and
// the labels of their variant options (in axis order), then their search text. SKUs without
// variant values keep their variant label.
func (s *Service) refreshSkuNames(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) error {
	if len(skuIDs) == 0 {
		return nil
	}
	var rows []struct {
		ID           uuid.UUID `db:"id"`
		ProductName  string    `db:"product_name"`
		VariantLabel string    `db:"variant_label"`
		Labels       *string   `db:"labels"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT s.id, p.name AS product_name, s.variant_label,
		       (SELECT string_agg(o.label, $2 ORDER BY COALESCE(ax.position, 99), o.label)
		          FROM sku_variant_values v
		          JOIN attribute_options o ON o.id = v.option_id
		          LEFT JOIN product_variant_axes ax ON ax.product_id = s.product_id AND ax.attribute_id = v.attribute_id
		         WHERE v.sku_id = s.id) AS labels
		FROM skus s JOIN products p ON p.id = s.product_id WHERE s.id = ANY($1)`, skuIDs, variantLabelSeparator); err != nil {
		return fmt.Errorf("catalog: read sku names: %w", err)
	}
	idList := make([]uuid.UUID, len(rows))
	labels := make([]string, len(rows))
	names := make([]string, len(rows))
	for i, r := range rows {
		label := r.VariantLabel
		if r.Labels != nil {
			label = *r.Labels
		}
		idList[i], labels[i], names[i] = r.ID, label, SKUNamer(r.ProductName, label)
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE skus SET variant_label = v.label, display_name = v.name
		FROM unnest($1::uuid[], $2::text[], $3::text[]) AS v(id, label, name) WHERE skus.id = v.id`,
		idList, labels, names); err != nil {
		return fmt.Errorf("catalog: update sku names: %w", err)
	}
	return s.indexer.Reindex(ctx, q, idList)
}

// relabelVariants renames the SKUs whose variant uses the option.
func (s *Service) relabelVariants(ctx context.Context, q db.Querier, optionID uuid.UUID) error {
	var skuIDs []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &skuIDs, `SELECT sku_id FROM sku_variant_values WHERE option_id = $1`, optionID); err != nil {
		return fmt.Errorf("catalog: find variants of option: %w", err)
	}
	return s.refreshSkuNames(ctx, q, skuIDs)
}

// reindexProductsUsing refreshes the search text of every SKU whose product stores a value for
// the attribute.
func (s *Service) reindexProductsUsing(ctx context.Context, q db.Querier, a Attribute) error {
	var skuIDs []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &skuIDs, `
		SELECT s.id FROM skus s JOIN products p ON p.id = s.product_id WHERE p.attributes -> $1 IS NOT NULL`, a.Code); err != nil {
		return fmt.Errorf("catalog: find products using attribute: %w", err)
	}
	return s.indexer.Reindex(ctx, q, skuIDs)
}

// SearchQuery is a normalised picker search.
type SearchQuery struct {
	// Raw is the trimmed term as typed (exact code, barcode and tax code matches).
	Raw string
	// Patterns are the LIKE patterns of the normalised words ("%banh%", "%xanh%").
	Patterns []string
	// PhoneKey is the term as a phone key ("" when it has no digit).
	PhoneKey string
	// InStock keeps only SKUs with stock on hand.
	InStock bool
	// Limit is how many candidates to return.
	Limit int
}

// SearchCandidate is one row a SearchRepository returns; ranking happens in Go.
type SearchCandidate struct {
	ID         uuid.UUID `db:"id"`
	Code       string    `db:"code"`
	Label      string    `db:"label"`
	Sublabel   *string   `db:"sublabel"`
	Unit       *string   `db:"unit"`
	SearchNorm string    `db:"search_norm"`
	// Exact is set for an exact code, barcode, phone or tax code match.
	Exact bool `db:"exact"`
}

// SearchRepository finds picker candidates. The LIKE implementation can be replaced by a token
// table without touching callers.
type SearchRepository interface {
	SearchSkus(ctx context.Context, q db.Querier, sq SearchQuery) ([]SearchCandidate, error)
	// SearchParties finds parties with an active customer (or supplier) profile.
	SearchParties(ctx context.Context, q db.Querier, kind string, sq SearchQuery) ([]SearchCandidate, error)
}

// SQLSearchRepository searches search_norm with LIKE.
type SQLSearchRepository struct{}

// SearchSkus implements SearchRepository: active SKUs of active products.
func (SQLSearchRepository) SearchSkus(ctx context.Context, q db.Querier, sq SearchQuery) ([]SearchCandidate, error) {
	var out []SearchCandidate
	err := sqlx.SelectContext(ctx, q, &out, `
		SELECT s.id, s.code, s.display_name AS label, s.spec_text AS sublabel, u.name AS unit, s.search_norm,
		       (s.code = $1 OR s.code = upper($1) OR s.id IN (SELECT b.sku_id FROM sku_barcodes b WHERE b.code = $1)) AS exact
		FROM skus s
		JOIN products p ON p.id = s.product_id
		JOIN units u ON u.id = s.base_unit_id
		WHERE s.status = 'active' AND p.status = 'active'
		  AND (s.code = $1 OR s.code = upper($1) OR s.id IN (SELECT b.sku_id FROM sku_barcodes b WHERE b.code = $1)
		       OR s.search_norm LIKE ALL ($2::text[]))
		  AND (NOT $3 OR EXISTS (SELECT 1 FROM sku_stock st WHERE st.sku_id = s.id AND st.on_hand > 0))
		ORDER BY exact DESC, length(s.search_norm), s.id
		LIMIT $4`, sq.Raw, sq.Patterns, sq.InStock, sq.Limit)
	if err != nil {
		return nil, fmt.Errorf("catalog: search skus: %w", err)
	}
	return out, nil
}

// SearchParties implements SearchRepository.
func (SQLSearchRepository) SearchParties(ctx context.Context, q db.Querier, kind string, sq SearchQuery) ([]SearchCandidate, error) {
	profile := "customer_profiles"
	if kind == SearchSupplier {
		profile = "supplier_profiles"
	}
	var out []SearchCandidate
	err := sqlx.SelectContext(ctx, q, &out, `
		SELECT p.id, COALESCE(p.tax_code, '') AS code, p.name AS label, COALESCE(p.phone, p.address) AS sublabel,
		       NULL::text AS unit, p.search_norm,
		       COALESCE(($2 <> '' AND p.phone_key = $2) OR p.tax_code = $1, false) AS exact
		FROM parties p
		JOIN `+profile+` r ON r.party_id = p.id AND r.status = 'active'
		WHERE ($2 <> '' AND p.phone_key = $2) OR p.tax_code = $1 OR p.search_norm LIKE ALL ($3::text[])
		ORDER BY exact DESC, length(p.search_norm), p.id
		LIMIT $4`, sq.Raw, sq.PhoneKey, sq.Patterns, sq.Limit)
	if err != nil {
		return nil, fmt.Errorf("catalog: search parties: %w", err)
	}
	return out, nil
}

// SearchRequest is GET /search.
type SearchRequest struct {
	Kind    string
	Q       string
	Limit   int
	InStock bool
}

// SearchItem is one picker result.
type SearchItem struct {
	Kind     string
	ID       uuid.UUID
	Code     string
	Label    string
	Sublabel *string
	Unit     *string
	// PriceHint is the SKU's retail base-list price of one base unit, filled by the pricing
	// module; nil when the SKU has none or pricing is not wired.
	PriceHint *money.Money
	// OnHandByColor is filled for SKUs by the inventory module; empty until it is wired.
	OnHandByColor []ColorQty
}

// Search finds SKUs, customers or suppliers for the pickers: an exact code, barcode, phone or tax
// code match first, then words matched anywhere (accent-insensitive), prefix matches first and
// shorter texts before longer ones.
func (s *Service) Search(ctx context.Context, scope Scope, req SearchRequest) ([]SearchItem, error) {
	f := fieldErrors{}
	if req.Kind != SearchSku && req.Kind != SearchCustomer && req.Kind != SearchSupplier {
		f["type"] = "invalid"
	}
	switch {
	case req.Limit == 0:
		req.Limit = DefaultSearchLimit
	case req.Limit < 0 || req.Limit > MaxSearchLimit:
		f["limit"] = "out_of_range"
	}
	raw := strings.TrimSpace(req.Q)
	if utf8.RuneCountInString(raw) > maxSearchTermLen {
		f["q"] = "too_long"
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	norm := textnorm.SearchNorm(raw)
	words := strings.Fields(norm)
	sq := SearchQuery{Raw: raw, Patterns: make([]string, len(words)), PhoneKey: textnorm.PhoneKey(raw),
		InStock: req.InStock, Limit: searchCandidates}
	for i, w := range words {
		sq.Patterns[i] = "%" + w + "%"
	}
	var items []SearchItem
	err := s.read(ctx, scope, func(q db.Querier) error {
		var cands []SearchCandidate
		var err error
		if req.Kind == SearchSku {
			cands, err = s.search.SearchSkus(ctx, q, sq)
		} else {
			cands, err = s.search.SearchParties(ctx, q, req.Kind, sq)
		}
		if err != nil {
			return err
		}
		cands = RankCandidates(cands, norm)
		if len(cands) > req.Limit {
			cands = cands[:req.Limit]
		}
		items = make([]SearchItem, len(cands))
		skuIDs := make([]uuid.UUID, len(cands))
		for i, c := range cands {
			items[i] = SearchItem{Kind: req.Kind, ID: c.ID, Code: c.Code, Label: c.Label, Sublabel: c.Sublabel,
				Unit: c.Unit, OnHandByColor: []ColorQty{}}
			skuIDs[i] = c.ID
		}
		if req.Kind != SearchSku || len(items) == 0 {
			return nil
		}
		if s.hints != nil {
			hints, err := s.hints.PriceHints(ctx, q, skuIDs)
			if err != nil {
				return err
			}
			for i := range items {
				if p, ok := hints[items[i].ID]; ok {
					items[i].PriceHint = &p
				}
			}
		}
		if s.stock == nil {
			return nil
		}
		byColor, err := s.stock.OnHandByColor(ctx, q, skuIDs)
		if err != nil {
			return err
		}
		for i := range items {
			if qs := byColor[items[i].ID]; qs != nil {
				items[i].OnHandByColor = qs
			}
		}
		return nil
	})
	return items, err
}

// RankCandidates orders candidates: exact matches, then those whose label or code starts with
// the term, then shorter labels, then by label.
func RankCandidates(cands []SearchCandidate, norm string) []SearchCandidate {
	prefix := func(c SearchCandidate) bool {
		return norm != "" && (strings.HasPrefix(textnorm.SearchNorm(c.Label), norm) ||
			strings.HasPrefix(textnorm.SearchNorm(c.Code), norm))
	}
	rank := func(c SearchCandidate) int {
		switch {
		case c.Exact:
			return 0
		case prefix(c):
			return 1
		}
		return 2
	}
	slices.SortStableFunc(cands, func(a, b SearchCandidate) int {
		return cmp.Or(
			cmp.Compare(rank(a), rank(b)),
			cmp.Compare(utf8.RuneCountInString(a.Label), utf8.RuneCountInString(b.Label)),
			strings.Compare(a.Label, b.Label),
		)
	})
	return cands
}
