package inventory

import (
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Error codes of the inventory module.
const (
	CodeInsufficientStock    = "insufficient_stock"
	CodeColorRequired        = "color_required"
	CodeExpiredLotBlocked    = "expired_lot_blocked"
	CodeLotQuarantined       = "lot_quarantined"
	CodeLotMismatch          = "lot_mismatch"
	CodeOverReserve          = "insufficient_sellable"
	CodeSkuNotStockable      = "sku_not_stockable"
	CodeAlreadyReversed      = "already_reversed"
	CodeCountNotCounting     = "count_not_counting"
	CodeCountNotFinished     = "count_not_finished"
	CodeCountIncomplete      = "count_incomplete"
	CodeCountPosted          = "count_already_posted"
	CodeCountLotRequired     = "count_lot_required"
	CodeRecostNotPending     = "recost_not_pending"
	CodeRecostStale          = "recost_stale"
	CodeReservationNotActive = "reservation_not_active"
)

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

// colorKey names a color in error fields: its code, or "none" for "Chưa gán màu".
func colorKey(code *string) string {
	if code == nil || *code == "" {
		return "none"
	}
	return *code
}

// ColorAvailability is the quantity of one color that an issue could take.
type ColorAvailability struct {
	ColorID   *uuid.UUID
	ColorCode *string
	Qty       money.Qty
}

// errInsufficientStock is the 409 of an issue the stock cannot cover. Fields list what is
// available per color ("available.<color code>"), and the requested color and quantity, so the
// client can show "đỏ: 0 · xanh: 12" without guessing; another color is never substituted.
func errInsufficientStock(skuCode string, requestedColor *string, requested money.Qty, avail []ColorAvailability) error {
	fields := map[string]string{"sku": skuCode, "requested_qty": requested.String()}
	if requestedColor != nil {
		fields["requested_color"] = colorKey(requestedColor)
	}
	for _, a := range avail {
		fields["available."+colorKey(a.ColorCode)] = a.Qty.String()
	}
	return &apperr.Error{Kind: apperr.Conflict, Code: CodeInsufficientStock,
		Message: "Not enough stock of " + skuCode + " for this issue", Fields: fields}
}

func errColorRequired(skuCode string) error {
	return apperr.NewValidation(CodeColorRequired, "Choose a color: "+skuCode+" has stock by color",
		map[string]string{"color_id": "required"})
}

func errExpiredLot(lotNo string) error {
	return apperr.NewConflict(CodeExpiredLotBlocked, "Lot "+lotNo+" is expired and may not be sold")
}

func errLotQuarantined(lotNo string) error {
	return apperr.NewConflict(CodeLotQuarantined, "Lot "+lotNo+" is quarantined")
}

func errAlreadyReversed() error {
	return apperr.NewConflict(CodeAlreadyReversed, "This document was already reversed")
}

func errNotStockable(skuCode string) error {
	return apperr.NewValidation(CodeSkuNotStockable, skuCode+" holds no stock (combo)", map[string]string{"sku_id": "not_stockable"})
}

func errLotMismatch(field string) error {
	return apperr.NewValidation(CodeLotMismatch, "The lot does not belong to this SKU", map[string]string{field: "lot_mismatch"})
}
