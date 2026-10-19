package kits

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Error codes of the kits module.
const (
	CodeRecipeNotDraft     = "recipe_not_draft"
	CodeDraftExists        = "recipe_draft_exists"
	CodeKitPublished       = "kit_published"
	CodeKitSold            = "kit_sold"
	CodeNoActiveRecipe     = "kit_not_published"
	CodeComboNotSellable   = "combo_not_sellable"
	CodeNotAKit            = "not_a_kit"
	CodeOrderStatus        = "assembly_order_status_invalid"
	CodeOrderNotOpen       = "assembly_order_not_open"
	CodeOrderChanged       = "assembly_order_changed"
	CodeInsufficientStock  = "insufficient_stock"
	CodeExceedsRemaining   = "exceeds_remaining"
	CodeKitLotExhausted    = "kit_lot_exhausted"
	CodeOrderHasPostings   = "assembly_order_has_postings"
	CodeSubstituteConsumed = "substitute_exceeds_remaining"
	CodeColorRequired      = "color_required"
)

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

func errRecipeNotDraft() error {
	return apperr.NewConflict(CodeRecipeNotDraft, "The latest recipe version is published; add a new version to change it")
}

func errOrderNotOpen() error {
	return apperr.NewConflict(CodeOrderNotOpen, "The assembly order is completed or closed")
}

func errOrderChanged() error {
	return apperr.NewConflict(CodeOrderChanged, "The assembly order changed while posting; send the request again")
}

func errNotAKit() error {
	return apperr.NewValidation(CodeNotAKit, "Only a published pre-packed kit can be assembled",
		map[string]string{"kit_id": CodeNotAKit})
}

func lineField(i int, name string) string { return fmt.Sprintf("lines.%d.%s", i, name) }

func itemField(prefix string, i int, name string) string {
	return fmt.Sprintf("%sitems.%d.%s", prefix, i, name)
}

// remapLines renames the line fields of a validation or conflict error raised on a list built
// from some of the request lines: index[N] is the request line of entry N (-1 drops the line
// field to field). Other errors pass through unchanged.
func remapLines(err error, index []int, field string) error {
	e, ok := apperr.As(err)
	if !ok || len(e.Fields) == 0 {
		return err
	}
	fields := make(map[string]string, len(e.Fields))
	changed := false
	for k, v := range e.Fields {
		nk := remapField(k, index, field)
		fields[nk] = v
		changed = changed || nk != k
	}
	if !changed {
		return err
	}
	out := *e
	out.Fields = fields
	return out.WithCause(err)
}

func remapField(key string, index []int, field string) string {
	i, tail, ok := parseLineField(key)
	if !ok || i >= len(index) {
		return key
	}
	if index[i] < 0 {
		return field
	}
	return lineField(index[i], tail)
}

// parseLineField splits "lines.N.x" into N and x.
func parseLineField(key string) (int, string, bool) {
	rest, ok := strings.CutPrefix(key, "lines.")
	if !ok {
		return 0, "", false
	}
	n, tail, ok := strings.Cut(rest, ".")
	if !ok {
		return 0, "", false
	}
	i, err := strconv.Atoi(n)
	if err != nil || i < 0 {
		return 0, "", false
	}
	return i, tail, true
}
