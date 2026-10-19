package catalog

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

const categoryColumns = `id, parent_id, name, attribute_set_id, sort_order, active, created_at, updated_at`

// Categories returns every category ordered for display (the client builds the tree).
func (s *Service) Categories(ctx context.Context, scope Scope) ([]Category, error) {
	var out []Category
	err := s.read(ctx, scope, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+categoryColumns+` FROM categories ORDER BY sort_order, name_key`)
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: list categories: %w", err)
	}
	return out, nil
}

// CategoryInput creates a category or changes one; unset fields are kept on update.
type CategoryInput struct {
	Name           *string
	ParentID       Opt[uuid.UUID]
	AttributeSetID Opt[uuid.UUID]
	SortOrder      *int
	Active         *bool
}

// CategoryTree checks moves in the category tree: no cycles, at most MaxCategoryDepth levels.
type CategoryTree map[uuid.UUID]*uuid.UUID

// CheckPlacement validates placing node (uuid.Nil for a new category) under parent.
func (t CategoryTree) CheckPlacement(node uuid.UUID, parent *uuid.UUID) error {
	if parent == nil {
		return t.checkHeight(node, 0)
	}
	if _, ok := t[*parent]; !ok {
		return errInvalid(map[string]string{"parent_id": "unknown_category"})
	}
	depth := 0
	for at := parent; at != nil; at = t[*at] {
		if *at == node {
			return errCategoryCycle()
		}
		depth++
		if depth > MaxCategoryDepth {
			return errCategoryTooDeep() // also stops on a corrupt cycle above the parent
		}
	}
	return t.checkHeight(node, depth)
}

// checkHeight fails when node's subtree placed below depth levels exceeds MaxCategoryDepth.
func (t CategoryTree) checkHeight(node uuid.UUID, depth int) error {
	if depth+t.height(node, 0) > MaxCategoryDepth {
		return errCategoryTooDeep()
	}
	return nil
}

// height is the number of levels of node's subtree (1 for a leaf or a new node).
func (t CategoryTree) height(node uuid.UUID, guard int) int {
	if node == uuid.Nil || guard > MaxCategoryDepth {
		return 1
	}
	h := 1
	for child, parent := range t {
		if parent != nil && *parent == node {
			h = max(h, 1+t.height(child, guard+1))
		}
	}
	return h
}

// lockCategoryTree reads the whole tree FOR UPDATE so concurrent moves cannot form a cycle.
func lockCategoryTree(ctx context.Context, q db.Querier) (CategoryTree, error) {
	var rows []struct {
		ID       uuid.UUID  `db:"id"`
		ParentID *uuid.UUID `db:"parent_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, parent_id FROM categories ORDER BY id FOR UPDATE`); err != nil {
		return nil, fmt.Errorf("catalog: lock categories: %w", err)
	}
	t := make(CategoryTree, len(rows))
	for _, r := range rows {
		t[r.ID] = r.ParentID
	}
	return t, nil
}

func readCategory(ctx context.Context, q db.Querier, id uuid.UUID) (Category, error) {
	var c Category
	err := q.QueryRowxContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE id = $1`, id).StructScan(&c)
	if db.IsNoRows(err) {
		return Category{}, errNotFound()
	}
	if err != nil {
		return Category{}, fmt.Errorf("catalog: read category: %w", err)
	}
	return c, nil
}

func checkAttributeSet(ctx context.Context, q db.Querier, id *uuid.UUID, field string) error {
	if id == nil {
		return nil
	}
	found, err := exists(ctx, q, `SELECT 1 FROM attribute_sets WHERE id = $1`, *id)
	if err != nil {
		return err
	}
	if !found {
		return errInvalid(map[string]string{field: "unknown_attribute_set"})
	}
	return nil
}

func categoryNameTaken(err error) bool {
	return db.IsUniqueViolation(err, "ux_categories__parent_id_name_key")
}

// CreateCategory adds a category; sibling names are unique by name key.
func (s *Service) CreateCategory(ctx context.Context, actor audit.Actor, in CategoryInput) (Category, error) {
	f := fieldErrors{}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	f.text("name", &name, true, maxLabelLen)
	if err := f.err(); err != nil {
		return Category{}, err
	}
	now := s.clock.Now()
	c := Category{ID: ids.New(), ParentID: in.ParentID.Value, Name: name, AttributeSetID: in.AttributeSetID.Value,
		Active: in.Active == nil || *in.Active, CreatedAt: now, UpdatedAt: now}
	if in.SortOrder != nil {
		c.SortOrder = *in.SortOrder
	}
	err := s.write(ctx, actor, func(q db.Querier) error {
		tree, err := lockCategoryTree(ctx, q)
		if err != nil {
			return err
		}
		if err := tree.CheckPlacement(uuid.Nil, c.ParentID); err != nil {
			return err
		}
		if err := checkAttributeSet(ctx, q, c.AttributeSetID, "attribute_set_id"); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `
			INSERT INTO categories (id, tenant_id, parent_id, name, name_key, attribute_set_id, sort_order, active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			c.ID, actor.TenantID, c.ParentID, c.Name, nameKey(c.Name), c.AttributeSetID, c.SortOrder, c.Active, now)
		if categoryNameTaken(err) {
			return errNameTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: insert category: %w", err)
		}
		return s.record(ctx, q, actor, "category.create", "category", c.ID, nil, c)
	})
	return c, err
}

// UpdateCategory renames, moves, (de)activates a category or changes its attribute set.
func (s *Service) UpdateCategory(ctx context.Context, actor audit.Actor, id uuid.UUID, in CategoryInput) (Category, error) {
	f := fieldErrors{}
	if in.Name != nil {
		f.text("name", in.Name, true, maxLabelLen)
	}
	if err := f.err(); err != nil {
		return Category{}, err
	}
	var out Category
	err := s.write(ctx, actor, func(q db.Querier) error {
		tree, err := lockCategoryTree(ctx, q)
		if err != nil {
			return err
		}
		before, err := readCategory(ctx, q, id)
		if err != nil {
			return err
		}
		out = before
		if in.Name != nil {
			out.Name = *in.Name
		}
		if in.ParentID.Set {
			out.ParentID = in.ParentID.Value
			if err := tree.CheckPlacement(id, out.ParentID); err != nil {
				return err
			}
		}
		if in.AttributeSetID.Set {
			out.AttributeSetID = in.AttributeSetID.Value
			if err := checkAttributeSet(ctx, q, out.AttributeSetID, "attribute_set_id"); err != nil {
				return err
			}
		}
		if in.SortOrder != nil {
			out.SortOrder = *in.SortOrder
		}
		if in.Active != nil {
			out.Active = *in.Active
		}
		out.UpdatedAt = s.clock.Now()
		_, err = q.ExecContext(ctx, `
			UPDATE categories SET parent_id = $2, name = $3, name_key = $4, attribute_set_id = $5, sort_order = $6,
			  active = $7, updated_at = $8 WHERE id = $1`,
			id, out.ParentID, out.Name, nameKey(out.Name), out.AttributeSetID, out.SortOrder, out.Active, out.UpdatedAt)
		if categoryNameTaken(err) {
			return errNameTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: update category: %w", err)
		}
		return s.record(ctx, q, actor, "category.update", "category", id, before, out)
	})
	return out, err
}

// DeleteCategory removes a category without sub-categories, products or stock counts.
func (s *Service) DeleteCategory(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		if _, err := lockCategoryTree(ctx, q); err != nil {
			return err
		}
		c, err := readCategory(ctx, q, id)
		if err != nil {
			return err
		}
		used, err := s.usage.CategoryInUse(ctx, q, id)
		if err != nil {
			return err
		}
		if used {
			return errCategoryInUse()
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM categories WHERE id = $1`, id); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errCategoryInUse()
			}
			return fmt.Errorf("catalog: delete category: %w", err)
		}
		return s.record(ctx, q, actor, "category.delete", "category", id, c, nil)
	})
}
