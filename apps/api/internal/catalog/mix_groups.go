package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

const mixGroupColumns = `id, name, pack_unit_id, units_per_pack, part_unit_id, units_per_part, piece_unit_id, source_product_id,
	status, created_at, updated_at, version`

// MixGroupInput creates a mix group or replaces one.
type MixGroupInput struct {
	Name            string
	PackUnitID      uuid.UUID
	UnitsPerPack    int
	PartUnitID      uuid.UUID
	UnitsPerPart    int
	SourceProductID *uuid.UUID
	Items           []MixGroupItemDraft
}

func readMixItems(ctx context.Context, q db.Querier, groupIDs []uuid.UUID) (map[uuid.UUID][]MixGroupItem, error) {
	var rows []struct {
		MixGroupID uuid.UUID `db:"mix_group_id"`
		MixGroupItem
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT i.mix_group_id, i.sku_id, s.code AS sku_code, s.display_name AS sku_name, i.flavor_label, i.sort_order, i.active
		FROM mix_group_items i JOIN skus s ON s.id = i.sku_id
		WHERE i.mix_group_id = ANY($1) ORDER BY i.sort_order, i.flavor_label`, groupIDs); err != nil {
		return nil, fmt.Errorf("catalog: read mix group items: %w", err)
	}
	out := map[uuid.UUID][]MixGroupItem{}
	for _, r := range rows {
		out[r.MixGroupID] = append(out[r.MixGroupID], r.MixGroupItem)
	}
	return out, nil
}

func readMixGroup(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (MixGroup, error) {
	query := `SELECT ` + mixGroupColumns + ` FROM mix_groups WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var g MixGroup
	err := q.QueryRowxContext(ctx, query, id).StructScan(&g)
	if db.IsNoRows(err) {
		return MixGroup{}, errNotFound()
	}
	if err != nil {
		return MixGroup{}, fmt.Errorf("catalog: read mix group: %w", err)
	}
	items, err := readMixItems(ctx, q, []uuid.UUID{id})
	if err != nil {
		return MixGroup{}, err
	}
	g.Items = items[id]
	if g.Items == nil {
		g.Items = []MixGroupItem{}
	}
	return g, nil
}

// MixGroups lists the mix groups (with their flavors) by name; status filters when not "".
func (s *Service) MixGroups(ctx context.Context, scope Scope, status string) ([]MixGroup, error) {
	if status != "" && status != StatusActive && status != StatusInactive {
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	var out []MixGroup
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+mixGroupColumns+` FROM mix_groups WHERE ($1 = '' OR status = $1)
			ORDER BY name_key, id`, status); err != nil {
			return fmt.Errorf("catalog: list mix groups: %w", err)
		}
		idList := make([]uuid.UUID, len(out))
		for i, g := range out {
			idList[i] = g.ID
		}
		items, err := readMixItems(ctx, q, idList)
		if err != nil {
			return err
		}
		for i := range out {
			out[i].Items = items[out[i].ID]
			if out[i].Items == nil {
				out[i].Items = []MixGroupItem{}
			}
			if err := s.fillMixUsage(ctx, q, &out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// MixGroup returns a mix group with its flavors.
func (s *Service) MixGroup(ctx context.Context, scope Scope, id uuid.UUID) (MixGroup, error) {
	var g MixGroup
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		if g, err = readMixGroup(ctx, q, id, false); err != nil {
			return err
		}
		return s.fillMixUsage(ctx, q, &g)
	})
	return g, err
}

// fillMixUsage adds what documents use of the group, so screens lock what an edit cannot change.
func (s *Service) fillMixUsage(ctx context.Context, q db.Querier, g *MixGroup) error {
	usage, err := s.usage.MixGroupUsage(ctx, q, g.ID)
	if err != nil {
		return err
	}
	used, err := s.usage.MixGroupUsedSkus(ctx, q, g.ID)
	if err != nil {
		return err
	}
	if used == nil {
		used = []uuid.UUID{}
	}
	g.Usage, g.UsedSkuIDs = usage, used
	return nil
}

// mixFacts gathers what MixGroupPolicy checks a draft against; self is the group being replaced.
func mixFacts(ctx context.Context, q db.Querier, d MixGroupDraft, self uuid.UUID) (MixFacts, error) {
	facts := MixFacts{UnitNames: map[uuid.UUID]string{}, Skus: map[uuid.UUID]MixSku{}}
	var err error
	if strings.TrimSpace(d.Name) != "" {
		facts.NameTaken, err = exists(ctx, q, `SELECT 1 FROM mix_groups WHERE name_key = $1 AND id <> $2`, nameKey(d.Name), self)
		if err != nil {
			return MixFacts{}, err
		}
	}
	var units []Unit
	if err := sqlx.SelectContext(ctx, q, &units, `SELECT `+unitColumns+` FROM units WHERE active`); err != nil {
		return MixFacts{}, fmt.Errorf("catalog: read units: %w", err)
	}
	for _, u := range units {
		facts.UnitNames[u.ID] = u.Name
	}
	skuIDs := make([]uuid.UUID, len(d.Items))
	for i, it := range d.Items {
		skuIDs[i] = it.SkuID
	}
	var skus []struct {
		ID          uuid.UUID `db:"id"`
		Name        string    `db:"display_name"`
		ProductType string    `db:"type"`
		BaseUnitID  uuid.UUID `db:"base_unit_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &skus, `SELECT s.id, s.display_name, p.type, s.base_unit_id
		FROM skus s JOIN products p ON p.id = s.product_id WHERE s.id = ANY($1)`, skuIDs); err != nil {
		return MixFacts{}, fmt.Errorf("catalog: read flavor skus: %w", err)
	}
	var ones []struct {
		SkuID  uuid.UUID `db:"sku_id"`
		UnitID uuid.UUID `db:"unit_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &ones, `SELECT sku_id, unit_id FROM sku_units WHERE sku_id = ANY($1) AND factor = 1 AND active`,
		skuIDs); err != nil {
		return MixFacts{}, fmt.Errorf("catalog: read flavor units: %w", err)
	}
	for _, s := range skus {
		facts.Skus[s.ID] = MixSku{ID: s.ID, Name: s.Name, ProductType: s.ProductType, BaseUnitID: s.BaseUnitID}
	}
	for _, o := range ones {
		ms := facts.Skus[o.SkuID]
		ms.FactorOneUnits = append(ms.FactorOneUnits, o.UnitID)
		facts.Skus[o.SkuID] = ms
	}
	return facts, nil
}

// checkMixGroup validates the input and returns the group to store (without id and timestamps).
func (s *Service) checkMixGroup(ctx context.Context, q db.Querier, in MixGroupInput, self uuid.UUID) (MixGroup, error) {
	f := fieldErrors{}
	name := in.Name
	f.text("name", &name, false, maxNameLen)
	if err := f.err(); err != nil {
		return MixGroup{}, err
	}
	d := MixGroupDraft{Name: name, PackUnitID: in.PackUnitID, PartUnitID: in.PartUnitID, UnitsPerPack: in.UnitsPerPack,
		UnitsPerPart: in.UnitsPerPart, Items: in.Items}
	facts, err := mixFacts(ctx, q, d, self)
	if err != nil {
		return MixGroup{}, err
	}
	piece, err := s.mixPolicy.Check(d, facts)
	if err != nil {
		return MixGroup{}, err
	}
	g := MixGroup{Name: name, PackUnitID: in.PackUnitID, UnitsPerPack: in.UnitsPerPack, PartUnitID: in.PartUnitID,
		UnitsPerPart: in.UnitsPerPart, PieceUnitID: piece, SourceProductID: in.SourceProductID, Status: StatusActive}
	if in.SourceProductID != nil {
		if _, err := readProduct(ctx, q, *in.SourceProductID); apperrNotFound(err) {
			return MixGroup{}, errInvalid(map[string]string{"source_product_id": "unknown_product"})
		} else if err != nil {
			return MixGroup{}, err
		}
	}
	labels := map[string]bool{}
	for i, it := range in.Items {
		label := strings.TrimSpace(it.FlavorLabel)
		if labels[nameKey(label)] {
			return MixGroup{}, errInvalid(map[string]string{fmt.Sprintf("items[%d].flavor_label", i): "duplicate"})
		}
		labels[nameKey(label)] = true
		ms := facts.Skus[it.SkuID]
		g.Items = append(g.Items, MixGroupItem{SkuID: it.SkuID, SkuName: ms.Name, FlavorLabel: label, SortOrder: (i + 1) * 10,
			Active: it.Active})
	}
	return g, nil
}

func (s *Service) writeMixItems(ctx context.Context, q db.Querier, tenantID uuid.UUID, g MixGroup, previous []MixGroupItem) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM mix_group_items WHERE mix_group_id = $1`, g.ID); err != nil {
		return fmt.Errorf("catalog: clear mix group items: %w", err)
	}
	touched := make([]uuid.UUID, 0, len(g.Items)+len(previous))
	for _, it := range g.Items {
		if _, err := q.ExecContext(ctx, `INSERT INTO mix_group_items (tenant_id, mix_group_id, sku_id, flavor_label, sort_order, active)
			VALUES ($1, $2, $3, $4, $5, $6)`, tenantID, g.ID, it.SkuID, it.FlavorLabel, it.SortOrder, it.Active); err != nil {
			return fmt.Errorf("catalog: insert mix group item: %w", err)
		}
		touched = append(touched, it.SkuID)
	}
	for _, it := range previous {
		if !slices.Contains(touched, it.SkuID) {
			touched = append(touched, it.SkuID)
		}
	}
	return s.indexer.Reindex(ctx, q, touched)
}

func mixNameTaken(err error) bool { return db.IsUniqueViolation(err, "ux_mix_groups__name_key") }

// CreateMixGroup adds a mix group after MixGroupPolicy accepts it.
func (s *Service) CreateMixGroup(ctx context.Context, actor audit.Actor, in MixGroupInput) (MixGroup, error) {
	var out MixGroup
	err := s.write(ctx, actor, func(q db.Querier) error {
		g, err := s.checkMixGroup(ctx, q, in, uuid.Nil)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		g.ID, g.CreatedAt, g.UpdatedAt, g.Version = ids.New(), now, now, 1
		_, err = q.ExecContext(ctx, `
			INSERT INTO mix_groups (id, tenant_id, name, name_key, pack_unit_id, units_per_pack, part_unit_id, units_per_part,
			  piece_unit_id, source_product_id, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`,
			g.ID, actor.TenantID, g.Name, nameKey(g.Name), g.PackUnitID, g.UnitsPerPack, g.PartUnitID, g.UnitsPerPart,
			g.PieceUnitID, g.SourceProductID, g.Status, now)
		if mixNameTaken(err) {
			return errMixNameTaken()
		}
		if err != nil {
			return fmt.Errorf("catalog: insert mix group: %w", err)
		}
		if err := s.writeMixItems(ctx, q, actor.TenantID, g, nil); err != nil {
			return err
		}
		if out, err = readMixGroup(ctx, q, g.ID, false); err != nil {
			return err
		}
		if err := s.record(ctx, q, actor, "mix_group.create", "mix_group", g.ID, nil, out); err != nil {
			return err
		}
		return s.fillMixUsage(ctx, q, &out)
	})
	return out, err
}

// UpdateMixGroup replaces a mix group guarded by its version. Once documents use the group its
// pack spec is fixed and flavors already sold stay (MixGroupChangeSpec).
func (s *Service) UpdateMixGroup(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in MixGroupInput) (MixGroup, error) {
	var out MixGroup
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readMixGroup(ctx, q, id, true)
		if err != nil {
			return err
		}
		g, err := s.checkMixGroup(ctx, q, in, id)
		if err != nil {
			return err
		}
		g.ID, g.Status = id, before.Status
		names := map[uuid.UUID]string{}
		for _, it := range before.Items {
			names[it.SkuID] = it.SkuName
		}
		if err := s.mixSpec.CheckUpdate(ctx, q, before, g, func(id uuid.UUID) string { return names[id] }); err != nil {
			return err
		}
		_, err = db.UpdateWithVersion(ctx, q, "mix_groups", id, expected, s.clock.Now(),
			db.Set{Column: "name", Value: g.Name}, db.Set{Column: "name_key", Value: nameKey(g.Name)},
			db.Set{Column: "pack_unit_id", Value: g.PackUnitID}, db.Set{Column: "units_per_pack", Value: g.UnitsPerPack},
			db.Set{Column: "part_unit_id", Value: g.PartUnitID}, db.Set{Column: "units_per_part", Value: g.UnitsPerPart},
			db.Set{Column: "piece_unit_id", Value: g.PieceUnitID}, db.Set{Column: "source_product_id", Value: g.SourceProductID})
		if mixNameTaken(err) {
			return errMixNameTaken()
		}
		if err != nil {
			return err
		}
		if err := s.writeMixItems(ctx, q, actor.TenantID, g, before.Items); err != nil {
			return err
		}
		if out, err = readMixGroup(ctx, q, id, false); err != nil {
			return err
		}
		if err := s.record(ctx, q, actor, "mix_group.update", "mix_group", id, before, out); err != nil {
			return err
		}
		return s.fillMixUsage(ctx, q, &out)
	})
	return out, err
}

// SetMixGroupStatus deactivates or reactivates a mix group (always allowed; documents keep it).
func (s *Service) SetMixGroupStatus(ctx context.Context, actor audit.Actor, id uuid.UUID, status string) (MixGroup, error) {
	var out MixGroup
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readMixGroup(ctx, q, id, true)
		if err != nil {
			return err
		}
		if before.Status == status {
			out = before
			return s.fillMixUsage(ctx, q, &out)
		}
		if _, err := q.ExecContext(ctx, `UPDATE mix_groups SET status = $2, version = version + 1, updated_at = $3 WHERE id = $1`,
			id, status, s.clock.Now()); err != nil {
			return fmt.Errorf("catalog: set mix group status: %w", err)
		}
		if out, err = readMixGroup(ctx, q, id, false); err != nil {
			return err
		}
		action := "mix_group.deactivate"
		if status == StatusActive {
			action = "mix_group.reactivate"
		}
		if err := s.record(ctx, q, actor, action, "mix_group", id, before, out); err != nil {
			return err
		}
		return s.fillMixUsage(ctx, q, &out)
	})
	return out, err
}

// DeleteMixGroup removes a mix group no document uses.
func (s *Service) DeleteMixGroup(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.write(ctx, actor, func(q db.Querier) error {
		before, err := readMixGroup(ctx, q, id, true)
		if err != nil {
			return err
		}
		if err := s.mixSpec.CheckDelete(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM mix_group_items WHERE mix_group_id = $1`, id); err != nil {
			return fmt.Errorf("catalog: delete mix group items: %w", err)
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM mix_groups WHERE id = $1`, id); err != nil {
			if db.IsForeignKeyViolation(err, "") {
				return errMixGroupReferenced()
			}
			return fmt.Errorf("catalog: delete mix group: %w", err)
		}
		skuIDs := make([]uuid.UUID, len(before.Items))
		for i, it := range before.Items {
			skuIDs[i] = it.SkuID
		}
		if err := s.indexer.Reindex(ctx, q, skuIDs); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "mix_group.delete", "mix_group", id, before, nil)
	})
}
