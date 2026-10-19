package org

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// ReasonKinds are the documents a reason explains; the reasons table checks the same list.
var ReasonKinds = []string{
	"sales_return", "purchase_return", "damage", "loss", "gift", "internal_use", "count_variance", "disposal",
	"write_off", "close_short", "price_adjustment", "debt_adjustment", "reopen", "return_without_source",
}

// maxReasonNameLen bounds a reason name, in characters.
const maxReasonNameLen = 100

// Reason is a selectable explanation of a stock or debt movement. A reason a document
// uses can only be deactivated, so the document keeps pointing at it.
type Reason struct {
	ID        uuid.UUID `db:"id"`
	Kind      string    `db:"kind"`
	Name      string    `db:"name"`
	NameKey   string    `db:"name_key"`
	IsSystem  bool      `db:"is_system"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
}

// ReasonFilter narrows the reason list; empty Kind lists every kind.
type ReasonFilter struct {
	Kind       string
	ActiveOnly bool
}

// ReasonInput is the reason form; on update nil keeps the stored value and the kind cannot change.
type ReasonInput struct {
	Kind   *string
	Name   *string
	Active *bool
}

func errReasonNameTaken() error {
	return apperr.NewConflict("name_taken", "Another reason of this kind already uses this name")
}

func errReasonInUse() error {
	return apperr.NewConflict("reason_in_use", "Documents use the reason; deactivate it instead")
}

func errSystemReason() error {
	return apperr.NewForbidden("read_only", "A built-in reason cannot be deleted")
}

func validReasonKind(kind string) bool { return slices.Contains(ReasonKinds, kind) }

// checkReasonName trims the name and reports a field error code, or "".
func checkReasonName(name *string) string {
	*name = strings.TrimSpace(*name)
	switch {
	case *name == "":
		return "required"
	case utf8.RuneCountInString(*name) > maxReasonNameLen:
		return "too_long"
	case !utf8.ValidString(*name) || strings.ContainsFunc(*name, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return "invalid_text"
	}
	return ""
}

// Reasons lists the reasons by kind and name.
func (s *Service) Reasons(ctx context.Context, scope Scope, f ReasonFilter) ([]Reason, error) {
	if f.Kind != "" && !validReasonKind(f.Kind) {
		return nil, errInvalid(map[string]string{"kind": "invalid"})
	}
	var out []Reason
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		out, err = listReasons(ctx, q, f)
		return err
	})
	return out, err
}

// CreateReason adds a reason; names are unique per kind by name key.
func (s *Service) CreateReason(ctx context.Context, actor audit.Actor, in ReasonInput) (Reason, error) {
	fields := map[string]string{}
	r := Reason{Active: in.Active == nil || *in.Active}
	if in.Kind == nil || !validReasonKind(*in.Kind) {
		fields["kind"] = "invalid"
	} else {
		r.Kind = *in.Kind
	}
	if in.Name != nil {
		r.Name = *in.Name
	}
	if code := checkReasonName(&r.Name); code != "" {
		fields["name"] = code
	}
	if len(fields) > 0 {
		return Reason{}, errInvalid(fields)
	}
	r.ID, r.NameKey, r.CreatedAt = ids.New(), textnorm.Key(r.Name), s.clock.Now()
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		if err := insertReason(ctx, q, actor.TenantID, r, false); err != nil {
			return err
		}
		return s.recordReason(ctx, q, actor, "reason.create", r.ID, nil, r)
	})
	if err != nil {
		return Reason{}, err
	}
	return r, nil
}

// UpdateReason renames or (de)activates a reason.
func (s *Service) UpdateReason(ctx context.Context, actor audit.Actor, id uuid.UUID, in ReasonInput) (Reason, error) {
	if in.Kind != nil {
		return Reason{}, errInvalid(map[string]string{"kind": "immutable"})
	}
	if in.Name != nil {
		if code := checkReasonName(in.Name); code != "" {
			return Reason{}, errInvalid(map[string]string{"name": code})
		}
	}
	var out Reason
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readReason(ctx, q, id, true)
		if err != nil {
			return err
		}
		out = before
		if in.Name != nil {
			out.Name, out.NameKey = *in.Name, textnorm.Key(*in.Name)
		}
		if in.Active != nil {
			out.Active = *in.Active
		}
		if err := updateReason(ctx, q, out); err != nil {
			return err
		}
		return s.recordReason(ctx, q, actor, "reason.update", id, before, out)
	})
	return out, err
}

// DeleteReason removes a custom reason no document uses.
func (s *Service) DeleteReason(ctx context.Context, actor audit.Actor, id uuid.UUID) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readReason(ctx, q, id, true)
		if err != nil {
			return err
		}
		if before.IsSystem {
			return errSystemReason()
		}
		inUse, err := reasonInUse(ctx, q, id)
		if err != nil {
			return err
		}
		if inUse {
			return errReasonInUse()
		}
		if err := deleteReason(ctx, q, id); err != nil {
			return err
		}
		return s.recordReason(ctx, q, actor, "reason.delete", id, before, nil)
	})
}

func (s *Service) recordReason(ctx context.Context, q db.Querier, actor audit.Actor, action string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: "reason", EntityID: id, Before: before, After: after})
}

// defaultReasons are the built-in reasons of a new store, from the prototype's return, damage
// and stock-out screens.
var defaultReasons = []struct{ kind, name string }{
	{"sales_return", "Hàng móp méo khi giao"},
	{"sales_return", "Sai hàng"},
	{"sales_return", "Khách đổi ý (trong chính sách)"},
	{"sales_return", "Hàng gần hết hạn"},
	{"sales_return", "Lỗi chất lượng"},
	{"purchase_return", "Hàng lỗi bao bì"},
	{"damage", "Hỏng"},
	{"loss", "Hao hụt"},
	{"gift", "Biếu tặng"},
	{"internal_use", "Dùng nội bộ"},
	{"disposal", "Hết hạn sử dụng"},
}

// ReasonDefaults is the ReasonSeeder of the provisioner: it creates the built-in reasons and
// leaves existing ones alone, so it is safe to run again.
type ReasonDefaults struct{}

// SeedReasons implements ReasonSeeder.
func (ReasonDefaults) SeedReasons(ctx context.Context, q db.Querier, sc SeedContext) error {
	for _, d := range defaultReasons {
		r := Reason{ID: ids.New(), Kind: d.kind, Name: d.name, NameKey: textnorm.Key(d.name), IsSystem: true, Active: true, CreatedAt: sc.Now}
		if err := insertReason(ctx, q, sc.TenantID, r, true); err != nil {
			return fmt.Errorf("org: seed reason %s: %w", d.name, err)
		}
	}
	return nil
}
