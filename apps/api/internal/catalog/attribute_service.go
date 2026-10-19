package catalog

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Attributes, their options and attribute sets ("mẫu ngành").

const (
	attributeColumns = `id, code, name, data_type, unit_label, is_filterable, is_searchable, status, created_at, updated_at, version`
	optionColumns    = `id, attribute_id, label, sort_order, active`
	setColumns       = `id, name, active, created_at, updated_at`
	// maxOptionsPerAttribute bounds the options of one select attribute.
	maxOptionsPerAttribute = 200
)

// attributeCodeFormat mirrors ck_attributes__code: the JSON key of products.attributes.
var attributeCodeFormat = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// AttributeFilter narrows ListAttributes; an empty Status lists all.
type AttributeFilter struct {
	Status string
}

// Attributes returns the attributes by name, each with its options.
func (s *Service) Attributes(ctx context.Context, scope Scope, f AttributeFilter) ([]Attribute, error) {
	var out []Attribute
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = listAttributes(ctx, q, f.Status, nil)
		return err
	})
	return out, err
}

// listAttributes reads attributes (all, or only ids) with their options.
func listAttributes(ctx context.Context, q db.Querier, status string, only []uuid.UUID) ([]Attribute, error) {
	var attrs []Attribute
	query := `SELECT ` + attributeColumns + ` FROM attributes WHERE ($1 = '' OR status = $1)`
	args := []any{status}
	if only != nil {
		query += ` AND id = ANY($2)`
		args = append(args, only)
	}
	if err := sqlx.SelectContext(ctx, q, &attrs, query+` ORDER BY name, code`, args...); err != nil {
		return nil, fmt.Errorf("catalog: list attributes: %w", err)
	}
	if len(attrs) == 0 {
		return attrs, nil
	}
	idList := make([]uuid.UUID, len(attrs))
	for i, a := range attrs {
		idList[i] = a.ID
	}
	var opts []AttributeOption
	if err := sqlx.SelectContext(ctx, q, &opts, `SELECT `+optionColumns+` FROM attribute_options WHERE attribute_id = ANY($1)
		ORDER BY sort_order, label`, idList); err != nil {
		return nil, fmt.Errorf("catalog: list attribute options: %w", err)
	}
	byAttr := map[uuid.UUID][]AttributeOption{}
	for _, o := range opts {
		byAttr[o.AttributeID] = append(byAttr[o.AttributeID], o)
	}
	for i := range attrs {
		attrs[i].Options = byAttr[attrs[i].ID]
	}
	return attrs, nil
}

func readAttribute(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Attribute, error) {
	query := `SELECT ` + attributeColumns + ` FROM attributes WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var a Attribute
	err := q.QueryRowxContext(ctx, query, id).StructScan(&a)
	if db.IsNoRows(err) {
		return Attribute{}, errNotFound()
	}
	if err != nil {
		return Attribute{}, fmt.Errorf("catalog: read attribute: %w", err)
	}
	if err := sqlx.SelectContext(ctx, q, &a.Options, `SELECT `+optionColumns+` FROM attribute_options WHERE attribute_id = $1
		ORDER BY sort_order, label`, id); err != nil {
		return Attribute{}, fmt.Errorf("catalog: read attribute options: %w", err)
	}
	return a, nil
}

// Attribute returns one attribute with its options.
func (s *Service) Attribute(ctx context.Context, scope Scope, id uuid.UUID) (Attribute, error) {
	var a Attribute
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		a, err = readAttribute(ctx, q, id, false)
		return err
	})
	return a, err
}

// AttributeInput creates an attribute (all fields) or changes one (nil fields are kept; an
// empty UnitLabel clears it).
type AttributeInput struct {
	Code         *string
	Name         *string
	DataType     *DataType
	UnitLabel    *string
	IsFilterable *bool
	IsSearchable *bool
	Status       *string
	// Options are the labels of a new select or multiselect attribute.
	Options []string
}

// AttributeResult is a saved attribute with the warnings the owner should see.
type AttributeResult struct {
	Attribute Attribute
	Warnings  []string
}

// apply validates in and writes its fields onto a.
func (in AttributeInput) apply(a *Attribute, f fieldErrors) {
	if in.Code != nil {
		a.Code = strings.TrimSpace(*in.Code)
		if !attributeCodeFormat.MatchString(a.Code) {
			f["code"] = "invalid"
		}
	}
	if in.Name != nil {
		a.Name = *in.Name
		f.text("name", &a.Name, true, maxLabelLen)
	}
	if in.DataType != nil {
		a.DataType = *in.DataType
		if !a.DataType.Valid() {
			f["data_type"] = "invalid"
		}
	}
	if in.UnitLabel != nil {
		a.UnitLabel = f.optionalText("unit_label", in.UnitLabel, 20)
	}
	if a.UnitLabel != nil && a.DataType != DataNumber {
		f["unit_label"] = "number_only"
	}
	if in.IsFilterable != nil {
		a.IsFilterable = *in.IsFilterable
	}
	if in.IsSearchable != nil {
		a.IsSearchable = *in.IsSearchable
	}
	if in.Status != nil {
		a.Status = *in.Status
		if a.Status != StatusActive && a.Status != StatusInactive {
			f["status"] = "invalid"
		}
	}
}

// colorWarnings warns when an attribute looks like the fixed colors.
func colorWarnings(ctx context.Context, q db.Querier, name, code string) ([]string, error) {
	var keys []string
	if err := sqlx.SelectContext(ctx, q, &keys, `SELECT code FROM colors`); err != nil {
		return nil, fmt.Errorf("catalog: read colors: %w", err)
	}
	var names []string
	if err := sqlx.SelectContext(ctx, q, &names, `SELECT name FROM colors`); err != nil {
		return nil, fmt.Errorf("catalog: read colors: %w", err)
	}
	for _, n := range names {
		keys = append(keys, nameKey(n))
	}
	if colorLikeAttribute(name, keys) || colorLikeAttribute(strings.ReplaceAll(code, "_", " "), keys) {
		return []string{AttributeWarningColor}, nil
	}
	return nil, nil
}

// CreateAttribute adds an attribute and, for select types, its options. An attribute named
// like a color is accepted with a warning: colors are fixed and live on lots and lines.
func (s *Service) CreateAttribute(ctx context.Context, actor audit.Actor, in AttributeInput) (AttributeResult, error) {
	now := s.clock.Now()
	a := Attribute{ID: ids.New(), Status: StatusActive, CreatedAt: now, UpdatedAt: now, Version: 1}
	f := fieldErrors{}
	for _, req := range []struct {
		field string
		set   bool
	}{{"code", in.Code != nil}, {"name", in.Name != nil}, {"data_type", in.DataType != nil}} {
		if !req.set {
			f[req.field] = "required"
		}
	}
	in.apply(&a, f)
	labels := cleanOptionLabels(in.Options, f)
	if len(labels) > 0 && !a.DataType.HasOptions() {
		f["options"] = "select_only"
	}
	if err := f.err(); err != nil {
		return AttributeResult{}, err
	}
	var res AttributeResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO attributes (id, tenant_id, code, name, data_type, unit_label, is_filterable, is_searchable, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
			a.ID, actor.TenantID, a.Code, a.Name, a.DataType, a.UnitLabel, a.IsFilterable, a.IsSearchable, a.Status, now)
		if db.IsUniqueViolation(err, "ux_attributes__code") {
			return errAttributeCodeTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: insert attribute: %w", err)
		}
		for i, label := range labels {
			o := AttributeOption{ID: ids.New(), AttributeID: a.ID, Label: label, SortOrder: (i + 1) * 10, Active: true}
			if err := insertOption(ctx, q, actor.TenantID, o, now); err != nil {
				return err
			}
			a.Options = append(a.Options, o)
		}
		if res.Warnings, err = colorWarnings(ctx, q, a.Name, a.Code); err != nil {
			return err
		}
		res.Attribute = a
		return s.record(ctx, q, actor, "attribute.create", "attribute", a.ID, nil, a)
	})
	return res, err
}

// cleanOptionLabels trims labels and rejects empty, long and duplicate ones.
func cleanOptionLabels(labels []string, f fieldErrors) []string {
	if len(labels) > maxOptionsPerAttribute {
		f["options"] = "too_many"
		return nil
	}
	out := make([]string, 0, len(labels))
	seen := map[string]bool{}
	for i, l := range labels {
		field := fmt.Sprintf("options[%d]", i)
		f.text(field, &l, true, maxLabelLen)
		if seen[nameKey(l)] && l != "" {
			f[field] = "duplicate"
		}
		seen[nameKey(l)] = true
		out = append(out, l)
	}
	return out
}

func insertOption(ctx context.Context, q db.Querier, tenantID uuid.UUID, o AttributeOption, now any) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO attribute_options (id, tenant_id, attribute_id, label, sort_order, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, o.ID, tenantID, o.AttributeID, o.Label, o.SortOrder, o.Active, now)
	if db.IsUniqueViolation(err, "ux_attribute_options__attribute_id_label") {
		return errOptionLabelTaken()
	}
	if err != nil {
		return fmt.Errorf("catalog: insert attribute option: %w", err)
	}
	return nil
}

// UpdateAttribute changes an attribute guarded by its version. The code and data type are fixed
// once a product uses the attribute (attribute_in_use); renaming, flags and status always change.
func (s *Service) UpdateAttribute(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in AttributeInput) (AttributeResult, error) {
	if in.Options != nil {
		return AttributeResult{}, errInvalid(map[string]string{"options": "use_option_endpoints"})
	}
	var res AttributeResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readAttribute(ctx, q, id, true)
		if err != nil {
			return err
		}
		a := before
		f := fieldErrors{}
		in.apply(&a, f)
		if err := f.err(); err != nil {
			return err
		}
		change := AttributeChange{CodeChanged: a.Code != before.Code, DataTypeChanged: a.DataType != before.DataType}
		if err := s.attrSpec.Check(ctx, q, before, change); err != nil {
			return err
		}
		a.Version, err = db.UpdateWithVersion(ctx, q, "attributes", id, expected, s.clock.Now(),
			db.Set{Column: "code", Value: a.Code}, db.Set{Column: "name", Value: a.Name},
			db.Set{Column: "data_type", Value: a.DataType}, db.Set{Column: "unit_label", Value: a.UnitLabel},
			db.Set{Column: "is_filterable", Value: a.IsFilterable}, db.Set{Column: "is_searchable", Value: a.IsSearchable},
			db.Set{Column: "status", Value: a.Status})
		if db.IsUniqueViolation(err, "ux_attributes__code") {
			return errAttributeCodeTaken()
		}
		if err != nil {
			return err
		}
		a.UpdatedAt = s.clock.Now()
		if a.Name != before.Name || a.IsSearchable != before.IsSearchable {
			if err := s.reindexProductsUsing(ctx, q, a); err != nil {
				return err
			}
		}
		if res.Warnings, err = colorWarnings(ctx, q, a.Name, a.Code); err != nil {
			return err
		}
		res.Attribute = a
		return s.record(ctx, q, actor, "attribute.update", "attribute", id, before, a)
	})
	return res, err
}

// DeleteAttribute removes an attribute no product uses, with its options and set placements.
func (s *Service) DeleteAttribute(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		a, err := readAttribute(ctx, q, id, true)
		if err != nil {
			return err
		}
		if err := s.attrSpec.Check(ctx, q, a, AttributeChange{Delete: true}); err != nil {
			return err
		}
		for _, stmt := range []string{
			`DELETE FROM attribute_set_items WHERE attribute_id = $1`,
			`DELETE FROM attribute_options WHERE attribute_id = $1`,
			`DELETE FROM attributes WHERE id = $1`,
		} {
			if _, err := q.ExecContext(ctx, stmt, id); err != nil {
				if db.IsForeignKeyViolation(err, "") {
					return errAttributeInUse()
				}
				return fmt.Errorf("catalog: delete attribute: %w", err)
			}
		}
		return s.record(ctx, q, actor, "attribute.delete", "attribute", id, a, nil)
	})
}

// OptionInput adds or changes an option; nil fields are kept on update.
type OptionInput struct {
	Label     *string
	SortOrder *int
	Active    *bool
}

// AddOption adds an option to a select or multiselect attribute.
func (s *Service) AddOption(ctx context.Context, actor audit.Actor, attributeID uuid.UUID, in OptionInput) (AttributeOption, error) {
	f := fieldErrors{}
	label := ""
	if in.Label != nil {
		label = *in.Label
	}
	f.text("label", &label, true, maxLabelLen)
	if err := f.err(); err != nil {
		return AttributeOption{}, err
	}
	var o AttributeOption
	err := s.write(ctx, actor, func(q db.Querier) error {
		a, err := readAttribute(ctx, q, attributeID, true)
		if err != nil {
			return err
		}
		if !a.DataType.HasOptions() {
			return errInvalid(map[string]string{"attribute_id": "select_only"})
		}
		if len(a.Options) >= maxOptionsPerAttribute {
			return errInvalid(map[string]string{"label": "too_many_options"})
		}
		if slices.ContainsFunc(a.Options, func(x AttributeOption) bool { return nameKey(x.Label) == nameKey(label) }) {
			return errOptionLabelTaken()
		}
		sortOrder := (len(a.Options) + 1) * 10
		if in.SortOrder != nil {
			sortOrder = *in.SortOrder
		}
		o = AttributeOption{ID: ids.New(), AttributeID: attributeID, Label: label, SortOrder: sortOrder, Active: in.Active == nil || *in.Active}
		if err := insertOption(ctx, q, actor.TenantID, o, s.clock.Now()); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "attribute.option_add", "attribute", attributeID, nil, o)
	})
	return o, err
}

// UpdateOption relabels, reorders or (de)activates an option. A relabel renames the SKUs whose
// variant uses it and refreshes the search text.
func (s *Service) UpdateOption(ctx context.Context, actor audit.Actor, attributeID, optionID uuid.UUID, in OptionInput) (AttributeOption, error) {
	f := fieldErrors{}
	if in.Label != nil {
		f.text("label", in.Label, true, maxLabelLen)
	}
	if err := f.err(); err != nil {
		return AttributeOption{}, err
	}
	var o AttributeOption
	err := s.write(ctx, actor, func(q db.Querier) error {
		a, err := readAttribute(ctx, q, attributeID, true)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(a.Options, func(x AttributeOption) bool { return x.ID == optionID })
		if i < 0 {
			return errNotFound()
		}
		before := a.Options[i]
		o = before
		if in.Label != nil {
			o.Label = *in.Label
			if slices.ContainsFunc(a.Options, func(x AttributeOption) bool { return x.ID != optionID && nameKey(x.Label) == nameKey(o.Label) }) {
				return errOptionLabelTaken()
			}
		}
		if in.SortOrder != nil {
			o.SortOrder = *in.SortOrder
		}
		if in.Active != nil {
			o.Active = *in.Active
		}
		_, err = q.ExecContext(ctx, `UPDATE attribute_options SET label = $2, sort_order = $3, active = $4 WHERE id = $1`,
			optionID, o.Label, o.SortOrder, o.Active)
		if db.IsUniqueViolation(err, "ux_attribute_options__attribute_id_label") {
			return errOptionLabelTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: update attribute option: %w", err)
		}
		if o.Label != before.Label {
			if err := s.relabelVariants(ctx, q, optionID); err != nil {
				return err
			}
			if err := s.reindexProductsUsing(ctx, q, a); err != nil {
				return err
			}
		}
		return s.record(ctx, q, actor, "attribute.option_update", "attribute", attributeID, before, o)
	})
	return o, err
}

// DeleteOption removes an option no product or SKU references.
func (s *Service) DeleteOption(ctx context.Context, actor audit.Actor, attributeID, optionID uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		a, err := readAttribute(ctx, q, attributeID, true)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(a.Options, func(x AttributeOption) bool { return x.ID == optionID })
		if i < 0 {
			return errNotFound()
		}
		if err := s.optionSpec.Check(ctx, q, a, optionID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM attribute_options WHERE id = $1`, optionID); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errOptionInUse()
			}
			return fmt.Errorf("catalog: delete attribute option: %w", err)
		}
		return s.record(ctx, q, actor, "attribute.option_delete", "attribute", attributeID, a.Options[i], nil)
	})
}

// AttributeSets returns the attribute sets by name with their items.
func (s *Service) AttributeSets(ctx context.Context, scope Scope) ([]AttributeSet, error) {
	var out []AttributeSet
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+setColumns+` FROM attribute_sets ORDER BY name`); err != nil {
			return fmt.Errorf("catalog: list attribute sets: %w", err)
		}
		for i := range out {
			items, err := readSetItems(ctx, q, out[i].ID)
			if err != nil {
				return err
			}
			out[i].Items = items
		}
		return nil
	})
	return out, err
}

func readSetItems(ctx context.Context, q db.Querier, setID uuid.UUID) ([]AttributeSetItem, error) {
	var items []AttributeSetItem
	if err := sqlx.SelectContext(ctx, q, &items, `
		SELECT i.attribute_id, a.code, a.name, i.is_required, i.sort_order
		FROM attribute_set_items i JOIN attributes a ON a.id = i.attribute_id
		WHERE i.attribute_set_id = $1 ORDER BY i.sort_order, a.name`, setID); err != nil {
		return nil, fmt.Errorf("catalog: read attribute set items: %w", err)
	}
	return items, nil
}

func readSet(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (AttributeSet, error) {
	query := `SELECT ` + setColumns + ` FROM attribute_sets WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var set AttributeSet
	err := q.QueryRowxContext(ctx, query, id).StructScan(&set)
	if db.IsNoRows(err) {
		return AttributeSet{}, errNotFound()
	}
	if err != nil {
		return AttributeSet{}, fmt.Errorf("catalog: read attribute set: %w", err)
	}
	set.Items, err = readSetItems(ctx, q, id)
	return set, err
}

// AttributeSet returns one attribute set with its items.
func (s *Service) AttributeSet(ctx context.Context, scope Scope, id uuid.UUID) (AttributeSet, error) {
	var set AttributeSet
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		set, err = readSet(ctx, q, id, false)
		return err
	})
	return set, err
}

// AttributeSetInput creates a set (Name required, optional Items) or changes its name and status.
type AttributeSetInput struct {
	Name   *string
	Active *bool
	Items  []SetItem
}

// SetItemsResult is an attribute set after its items changed, with the codes of the attributes
// whose stored product values are now hidden.
type SetItemsResult struct {
	Set    AttributeSet
	Hidden []string
}

// CreateAttributeSet adds an attribute set with its items.
func (s *Service) CreateAttributeSet(ctx context.Context, actor audit.Actor, in AttributeSetInput) (AttributeSet, error) {
	f := fieldErrors{}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	f.text("name", &name, true, maxLabelLen)
	if err := f.err(); err != nil {
		return AttributeSet{}, err
	}
	now := s.clock.Now()
	set := AttributeSet{ID: ids.New(), Name: name, Active: in.Active == nil || *in.Active, CreatedAt: now, UpdatedAt: now}
	err := s.write(ctx, actor, func(q db.Querier) error {
		if err := insertSet(ctx, q, actor.TenantID, set); err != nil {
			return err
		}
		if _, err := s.replaceSetItems(ctx, q, actor.TenantID, set, in.Items); err != nil {
			return err
		}
		var err error
		if set, err = readSet(ctx, q, set.ID, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "attribute_set.create", "attribute_set", set.ID, nil, set)
	})
	return set, err
}

func insertSet(ctx context.Context, q db.Querier, tenantID uuid.UUID, set AttributeSet) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO attribute_sets (id, tenant_id, name, active, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $5)`,
		set.ID, tenantID, set.Name, set.Active, set.CreatedAt)
	if db.IsUniqueViolation(err, "ux_attribute_sets__name") {
		return errNameTaken()
	}
	if err != nil {
		return fmt.Errorf("catalog: insert attribute set: %w", err)
	}
	return nil
}

// UpdateAttributeSet renames or (de)activates a set.
func (s *Service) UpdateAttributeSet(ctx context.Context, actor audit.Actor, id uuid.UUID, in AttributeSetInput) (AttributeSet, error) {
	f := fieldErrors{}
	if in.Name != nil {
		f.text("name", in.Name, true, maxLabelLen)
	}
	if err := f.err(); err != nil {
		return AttributeSet{}, err
	}
	var out AttributeSet
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readSet(ctx, q, id, true)
		if err != nil {
			return err
		}
		out = before
		if in.Name != nil {
			out.Name = *in.Name
		}
		if in.Active != nil {
			out.Active = *in.Active
		}
		out.UpdatedAt = s.clock.Now()
		_, err = q.ExecContext(ctx, `UPDATE attribute_sets SET name = $2, active = $3, updated_at = $4 WHERE id = $1`,
			id, out.Name, out.Active, out.UpdatedAt)
		if db.IsUniqueViolation(err, "ux_attribute_sets__name") {
			return errNameTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: update attribute set: %w", err)
		}
		return s.record(ctx, q, actor, "attribute_set.update", "attribute_set", id, before, out)
	})
	return out, err
}

// ReplaceAttributeSetItems sets the ordered attributes of a set. Values products store for a
// removed attribute stay and are reported as hidden.
func (s *Service) ReplaceAttributeSetItems(ctx context.Context, actor audit.Actor, id uuid.UUID, items []SetItem) (SetItemsResult, error) {
	var res SetItemsResult
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readSet(ctx, q, id, true)
		if err != nil {
			return err
		}
		if res.Hidden, err = s.replaceSetItems(ctx, q, actor.TenantID, before, items); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE attribute_sets SET updated_at = $2 WHERE id = $1`, id, s.clock.Now()); err != nil {
			return fmt.Errorf("catalog: touch attribute set: %w", err)
		}
		if res.Set, err = readSet(ctx, q, id, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "attribute_set.items_replace", "attribute_set", id, before.Items,
			map[string]any{"items": res.Set.Items, "hidden": res.Hidden})
	})
	return res, err
}

func (s *Service) replaceSetItems(ctx context.Context, q db.Querier, tenantID uuid.UUID, set AttributeSet, items []SetItem) ([]string, error) {
	idList := make([]uuid.UUID, len(items))
	for i, it := range items {
		idList[i] = it.AttributeID
	}
	attrs, err := listAttributes(ctx, q, "", idList)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]Attribute, len(attrs))
	for _, a := range attrs {
		byID[a.ID] = a
	}
	hidden, err := s.setSpec.Check(set.Items, items, byID)
	if err != nil {
		return nil, err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM attribute_set_items WHERE attribute_set_id = $1`, set.ID); err != nil {
		return nil, fmt.Errorf("catalog: clear attribute set items: %w", err)
	}
	for i, it := range items {
		order := it.SortOrder
		if order == 0 {
			order = (i + 1) * 10
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attribute_set_items (tenant_id, attribute_set_id, attribute_id, is_required, sort_order)
			VALUES ($1, $2, $3, $4, $5)`, tenantID, set.ID, it.AttributeID, it.IsRequired, order); err != nil {
			return nil, fmt.Errorf("catalog: insert attribute set item: %w", err)
		}
	}
	return hidden, nil
}

// setAttributes returns the attributes of a set, as product validation sees them.
func setAttributes(ctx context.Context, q db.Querier, setID *uuid.UUID) ([]SetAttribute, error) {
	if setID == nil {
		return nil, nil
	}
	items, err := readSetItems(ctx, q, *setID)
	if err != nil {
		return nil, err
	}
	idList := make([]uuid.UUID, len(items))
	for i, it := range items {
		idList[i] = it.AttributeID
	}
	attrs, err := listAttributes(ctx, q, "", idList)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]Attribute{}
	for _, a := range attrs {
		byID[a.ID] = a
	}
	out := make([]SetAttribute, 0, len(items))
	for _, it := range items {
		out = append(out, SetAttribute{Attribute: byID[it.AttributeID], Required: it.IsRequired})
	}
	return out, nil
}
