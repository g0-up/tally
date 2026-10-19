package pricing

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// promotionCode is the shape of a promotion code once trimmed and upper-cased.
var promotionCode = regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`)

// normalizeCode trims and upper-cases a promotion code; ok is false when it is not well formed.
func normalizeCode(raw string) (string, bool) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	return code, promotionCode.MatchString(code)
}

// PromotionItemInput is one SKU of a promotion; Qty (in base units) defaults to 1.
type PromotionItemInput struct {
	Role  string
	SkuID uuid.UUID
	Qty   *money.Qty
}

// PromotionInput is the promotion form. Kind and status are set on creation only (status changes
// through activate and deactivate); on update nil keeps the stored value, a set Opt with nil
// clears it and a non-nil Items replaces every item.
type PromotionInput struct {
	Code            Opt[string]
	Name            *string
	Kind            *string
	Value           *money.Money
	MaxDiscount     Opt[money.Money]
	MinOrderAmount  *money.Money
	Stackable       *bool
	CustomerGroupID Opt[uuid.UUID]
	ValidFrom       *time.Time
	ValidTo         *time.Time
	UsageLimit      Opt[int]
	Status          *string
	Items           *[]PromotionItemInput
}

// apply copies in onto p and checks the promotion invariants that do not need the database.
func (in PromotionInput) apply(p *Promotion, f fieldErrors, creating bool) {
	if in.Name != nil || creating {
		name := deref(in.Name)
		f.text("name", &name, true, maxNameLen)
		p.Name = name
	}
	if in.Code.Set {
		p.Code = nil
		if in.Code.Value != nil && strings.TrimSpace(*in.Code.Value) != "" {
			code, ok := normalizeCode(*in.Code.Value)
			if !ok {
				f.add("code", "invalid")
			}
			p.Code = &code
		}
	}
	if in.Value != nil {
		p.Value = *in.Value
	}
	if in.MaxDiscount.Set {
		p.MaxDiscount = in.MaxDiscount.Value
	}
	if in.MinOrderAmount != nil {
		p.MinOrderAmount = *in.MinOrderAmount
	}
	if in.Stackable != nil {
		p.Stackable = *in.Stackable
	}
	if in.CustomerGroupID.Set {
		p.CustomerGroupID = in.CustomerGroupID.Value
	}
	if in.ValidFrom != nil {
		p.ValidFrom = clock.TruncateDate(*in.ValidFrom)
	}
	if in.ValidTo != nil {
		p.ValidTo = clock.TruncateDate(*in.ValidTo)
	}
	if in.UsageLimit.Set {
		p.UsageLimit = in.UsageLimit.Value
	}
	if in.Items != nil {
		p.Items = make([]PromotionItem, len(*in.Items))
		for i, it := range *in.Items {
			qty := money.NewQtyFromInt(1)
			if it.Qty != nil {
				qty = *it.Qty
			}
			p.Items[i] = PromotionItem{Role: it.Role, SkuID: it.SkuID, Qty: qty}
		}
	}
	if creating {
		if p.ValidFrom.IsZero() {
			f.add("valid_from", "required")
		}
		if p.ValidTo.IsZero() {
			f.add("valid_to", "required")
		}
	}
	validatePromotion(*p, f)
}

// allowedRoles are the item roles each promotion kind takes.
func allowedRoles(kind string) map[string]bool {
	switch kind {
	case PromoGift:
		return map[string]bool{RoleEligible: true, RoleGift: true}
	case PromoComboPrice:
		return map[string]bool{RoleComboComponent: true}
	default:
		return map[string]bool{RoleEligible: true}
	}
}

// validatePromotion checks the value, cap, dates, limit and items of p against its kind.
func validatePromotion(p Promotion, f fieldErrors) {
	hundred := money.NewMoneyFromInt(100)
	switch p.Kind {
	case PromoLinePercent, PromoOrderPercent:
		if !p.Value.IsPositive() || p.Value.Cmp(hundred) > 0 {
			f.add("value", "out_of_range")
		} else if _, err := money.NewPercent(p.Value.Decimal().String()); err != nil {
			f.add("value", "out_of_range")
		}
	case PromoLineAmount, PromoOrderAmount, PromoComboPrice:
		if !p.Value.IsPositive() {
			f.add("value", "must_be_positive")
		}
	case PromoGift:
		if !p.Value.IsZero() {
			f.add("value", "must_be_zero")
		}
	}
	if p.MaxDiscount != nil && !p.MaxDiscount.IsPositive() {
		f.add("max_discount", "must_be_positive")
	}
	if p.MinOrderAmount.IsNegative() {
		f.add("min_order_amount", "negative")
	}
	if !p.ValidFrom.IsZero() && !p.ValidTo.IsZero() && p.ValidTo.Before(p.ValidFrom) {
		f.add("valid_to", "before_valid_from")
	}
	if p.UsageLimit != nil && *p.UsageLimit <= 0 {
		f.add("usage_limit", "must_be_positive")
	}
	if len(p.Items) > MaxPromotionItems {
		f.add("items", "too_many")
		return
	}
	roles := allowedRoles(p.Kind)
	type roleSku struct {
		role string
		sku  uuid.UUID
	}
	seen := make(map[roleSku]bool, len(p.Items))
	counts := map[string]int{}
	for i, it := range p.Items {
		field := fmt.Sprintf("items[%d]", i)
		if !roles[it.Role] {
			f.add(field+".role", "invalid")
			continue
		}
		if !it.Qty.IsPositive() {
			f.add(field+".qty", "must_be_positive")
		}
		k := roleSku{it.Role, it.SkuID}
		if seen[k] {
			f.add(field, "duplicate")
			continue
		}
		seen[k] = true
		counts[it.Role]++
	}
	switch {
	case p.Kind == PromoGift && counts[RoleGift] == 0:
		f.add("items", "gift_required")
	case p.Kind == PromoComboPrice && counts[RoleComboComponent] < 2:
		f.add("items", "combo_components_required")
	}
}

const promotionColumns = `
	p.id, p.code, p.name, p.kind, p.value, p.max_discount, p.min_order_amount, p.stackable, p.customer_group_id,
	p.campaign_id, p.valid_from, p.valid_to, p.usage_limit, p.status, p.created_at, p.updated_at, p.version,
	(SELECT count(*) FROM promotion_usages u WHERE u.promotion_id = p.id) AS used_count`

// loadPromotionItems fills the items of promos.
func loadPromotionItems(ctx context.Context, q db.Querier, promos []Promotion) error {
	if len(promos) == 0 {
		return nil
	}
	idx := make(map[uuid.UUID]int, len(promos))
	promoIDs := make([]uuid.UUID, len(promos))
	for i, p := range promos {
		idx[p.ID] = i
		promoIDs[i] = p.ID
		promos[i].Items = []PromotionItem{}
	}
	var items []PromotionItem
	if err := sqlx.SelectContext(ctx, q, &items, `
		SELECT pi.id, pi.promotion_id, pi.role, pi.sku_id, pi.qty, s.code AS sku_code, s.display_name AS sku_name
		FROM promotion_items pi JOIN skus s ON s.id = pi.sku_id
		WHERE pi.promotion_id = ANY($1)
		ORDER BY pi.role, s.code, pi.id`, promoIDs); err != nil {
		return fmt.Errorf("pricing: read promotion items: %w", err)
	}
	for _, it := range items {
		i := idx[it.PromotionID]
		promos[i].Items = append(promos[i].Items, it)
	}
	return nil
}

// readPromotion reads one promotion with its items; forUpdate locks the promotion row.
func readPromotion(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Promotion, error) {
	query := `SELECT ` + promotionColumns + ` FROM promotions p WHERE p.id = $1`
	if forUpdate {
		query += ` FOR UPDATE OF p`
	}
	var p Promotion
	err := q.QueryRowxContext(ctx, query, id).StructScan(&p)
	if db.IsNoRows(err) {
		return Promotion{}, errNotFound()
	}
	if err != nil {
		return Promotion{}, fmt.Errorf("pricing: read promotion: %w", err)
	}
	promos := []Promotion{p}
	if err := loadPromotionItems(ctx, q, promos); err != nil {
		return Promotion{}, err
	}
	return promos[0], nil
}

// PromotionFilter selects promotions; Q matches the name or code; AfterTime/AfterID continue after
// a (created_at, id) cursor, newest first.
type PromotionFilter struct {
	Status    string
	Q         string
	AfterTime *time.Time
	AfterID   uuid.UUID
	Limit     int
}

// Promotions pages the promotions, newest first.
func (s *Service) Promotions(ctx context.Context, scope Scope, f PromotionFilter) ([]Promotion, error) {
	if f.Status != "" && f.Status != PromoDraft && f.Status != PromoActive && f.Status != PromoInactive {
		return nil, errInvalid(map[string]string{"status": "invalid"})
	}
	var after time.Time
	if f.AfterTime != nil {
		after = *f.AfterTime
	}
	needle := strings.ToLower(strings.TrimSpace(f.Q))
	var out []Promotion
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			SELECT `+promotionColumns+` FROM promotions p
			WHERE ($1 = '' OR p.status = $1)
			  AND ($2 = '' OR strpos(lower(p.name), $2) > 0 OR strpos(lower(COALESCE(p.code, '')), $2) > 0)
			  AND (NOT $3 OR (p.created_at, p.id) < ($4, $5::uuid))
			ORDER BY p.created_at DESC, p.id DESC
			LIMIT $6`, f.Status, needle, f.AfterTime != nil, after, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("pricing: list promotions: %w", err)
		}
		return loadPromotionItems(ctx, q, out)
	})
	return out, err
}

// Promotion returns one promotion with its items.
func (s *Service) Promotion(ctx context.Context, scope Scope, id uuid.UUID) (Promotion, error) {
	var out Promotion
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = readPromotion(ctx, q, id, false)
		return err
	})
	return out, err
}

// checkPromotionRefs checks that the customer group and the item SKUs of p exist.
func checkPromotionRefs(ctx context.Context, q db.Querier, p Promotion) error {
	f := fieldErrors{}
	if p.CustomerGroupID != nil {
		found, err := exists(ctx, q, "customer_groups", *p.CustomerGroupID)
		if err != nil {
			return err
		}
		if !found {
			f.add("customer_group_id", "not_found")
		}
	}
	skuIDs := make([]uuid.UUID, len(p.Items))
	for i, it := range p.Items {
		skuIDs[i] = it.SkuID
	}
	found, err := existingIDs(ctx, q, "skus", uniqueIDs(skuIDs))
	if err != nil {
		return err
	}
	for i, it := range p.Items {
		if !found[it.SkuID] {
			f.add(fmt.Sprintf("items[%d].sku_id", i), "not_found")
		}
	}
	return f.err()
}

func mapPromotionWriteError(err error, op string) error {
	if db.IsUniqueViolation(err, "ux_promotions__code") {
		return errCodeTaken()
	}
	return fmt.Errorf("pricing: %s promotion: %w", op, err)
}

// replacePromotionItems rewrites the items of promotion id.
func replacePromotionItems(ctx context.Context, q db.Querier, tenantID, id uuid.UUID, items []PromotionItem) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM promotion_items WHERE promotion_id = $1`, id); err != nil {
		return fmt.Errorf("pricing: clear promotion items: %w", err)
	}
	for _, it := range items {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO promotion_items (id, tenant_id, promotion_id, role, sku_id, qty) VALUES ($1, $2, $3, $4, $5, $6)`,
			ids.New(), tenantID, id, it.Role, it.SkuID, it.Qty); err != nil {
			return fmt.Errorf("pricing: insert promotion item: %w", err)
		}
	}
	return nil
}

// CreatePromotion adds a promotion, as a draft or active (the default).
func (s *Service) CreatePromotion(ctx context.Context, actor audit.Actor, in PromotionInput) (Promotion, error) {
	p := Promotion{Kind: deref(in.Kind), Status: PromoActive, MinOrderAmount: money.ZeroMoney, Items: []PromotionItem{}}
	f := fieldErrors{}
	if !validPromoKind(p.Kind) {
		f.add("kind", "invalid")
	}
	if in.Value == nil && p.Kind != PromoGift {
		f.add("value", "required")
	}
	if in.Status != nil {
		if *in.Status != PromoDraft && *in.Status != PromoActive {
			f.add("status", "invalid")
		}
		p.Status = *in.Status
	}
	in.apply(&p, f, true)
	if err := f.err(); err != nil {
		return Promotion{}, err
	}
	var out Promotion
	err := s.write(ctx, actor, func(q db.Querier) error {
		if err := checkPromotionRefs(ctx, q, p); err != nil {
			return err
		}
		now := s.clock.Now()
		p.ID = ids.New()
		if _, err := q.ExecContext(ctx, `
			INSERT INTO promotions (id, tenant_id, code, name, kind, value, max_discount, min_order_amount, stackable,
			                        customer_group_id, valid_from, valid_to, usage_limit, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15)`,
			p.ID, actor.TenantID, p.Code, p.Name, p.Kind, p.Value, p.MaxDiscount, p.MinOrderAmount, p.Stackable,
			p.CustomerGroupID, p.ValidFrom, p.ValidTo, p.UsageLimit, p.Status, now); err != nil {
			return mapPromotionWriteError(err, "insert")
		}
		if err := replacePromotionItems(ctx, q, actor.TenantID, p.ID, p.Items); err != nil {
			return err
		}
		var err error
		if out, err = readPromotion(ctx, q, p.ID, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "promotion.create", "promotion", p.ID, nil, out)
	})
	return out, err
}

// UpdatePromotion edits a promotion if it is still at version expected; Items, when given,
// replace every item. Kind and status do not change here.
func (s *Service) UpdatePromotion(ctx context.Context, actor audit.Actor, id uuid.UUID, expected int, in PromotionInput) (Promotion, error) {
	if in.Kind != nil || in.Status != nil {
		f := fieldErrors{}
		if in.Kind != nil {
			f.add("kind", "immutable")
		}
		if in.Status != nil {
			f.add("status", "immutable")
		}
		return Promotion{}, f.err()
	}
	var out Promotion
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readPromotion(ctx, q, id, false)
		if err != nil {
			return err
		}
		p := before
		f := fieldErrors{}
		in.apply(&p, f, false)
		if err := f.err(); err != nil {
			return err
		}
		if err := checkPromotionRefs(ctx, q, p); err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "promotions", id, expected, s.clock.Now(),
			db.Set{Column: "code", Value: p.Code}, db.Set{Column: "name", Value: p.Name},
			db.Set{Column: "value", Value: p.Value}, db.Set{Column: "max_discount", Value: p.MaxDiscount},
			db.Set{Column: "min_order_amount", Value: p.MinOrderAmount}, db.Set{Column: "stackable", Value: p.Stackable},
			db.Set{Column: "customer_group_id", Value: p.CustomerGroupID}, db.Set{Column: "valid_from", Value: p.ValidFrom},
			db.Set{Column: "valid_to", Value: p.ValidTo}, db.Set{Column: "usage_limit", Value: p.UsageLimit}); err != nil {
			if isNotFound(err) {
				return errNotFound()
			}
			return mapPromotionWriteError(err, "update")
		}
		if in.Items != nil {
			if err := replacePromotionItems(ctx, q, actor.TenantID, id, p.Items); err != nil {
				return err
			}
		}
		if out, err = readPromotion(ctx, q, id, false); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "promotion.update", "promotion", id, before, out)
	})
	return out, err
}

// SetPromotionStatus activates or deactivates a promotion; setting the current status again
// changes nothing.
func (s *Service) SetPromotionStatus(ctx context.Context, actor audit.Actor, id uuid.UUID, status string) (Promotion, error) {
	if status != PromoActive && status != PromoInactive {
		return Promotion{}, errInvalid(map[string]string{"status": "invalid"})
	}
	var out Promotion
	err := s.write(ctx, actor, func(q db.Querier) error {
		before, err := readPromotion(ctx, q, id, true)
		if err != nil {
			return err
		}
		if before.Status == status {
			out = before
			return nil
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE promotions SET status = $2, updated_at = $3, version = version + 1 WHERE id = $1`,
			id, status, s.clock.Now()); err != nil {
			return fmt.Errorf("pricing: set promotion status: %w", err)
		}
		if out, err = readPromotion(ctx, q, id, false); err != nil {
			return err
		}
		action := "promotion.activate"
		if status == PromoInactive {
			action = "promotion.deactivate"
		}
		return s.record(ctx, q, actor, action, "promotion", id, before, out)
	})
	return out, err
}
