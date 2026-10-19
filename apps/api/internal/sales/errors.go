package sales

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Error codes of the sales module.
const (
	CodeComboNotSellable       = "combo_not_sellable"
	CodeSkuNotSellable         = "sku_not_sellable"
	CodeSkuInactive            = "sku_inactive"
	CodeColorRequired          = "color_required"
	CodeMixPartsExceedPack     = "mix_parts_exceed_pack"
	CodeMixPackPartial         = "mix_pack_partial"
	CodeQuoteExpired           = "quote_expired"
	CodeInvalidTransition      = "invalid_status_transition"
	CodeInvoiceNotEditable     = "invoice_not_editable"
	CodeNothingToDeliver       = "nothing_to_deliver"
	CodeDeliveryExceedsReserve = "delivery_exceeds_reserved"
	CodeStockOutReversed       = "stock_out_reversed"
	CodeDocumentHasReturns     = "document_has_returns"
	CodeInsufficientStock      = "insufficient_stock"
	CodePaymentMismatch        = "payment_mismatch"
	CodePaidNotRefundable      = "paid_amount_not_refundable"
	CodePaidAmountChanged      = "paid_amount_changed"
	CodeReturnExceedsSold      = "return_exceeds_sold"
	CodeRefundRequired         = "refund_required"
	CodeExchangeChanged        = "exchange_changed"
	CodeCartSold               = "cart_sold"
	CodeOfflineNotAllowed      = "offline_payment_not_allowed"
	CodeOfflineDisabled        = "offline_pos_disabled"
	CodeOfflineAmount          = "offline_amount_exceeded"
	CodeOfflineExpired         = "offline_window_expired"
	CodeExceptionClosed        = "sync_exception_closed"
	CodeWalkInReverse          = "walk_in_reverse_needs_refund"
	CodeWalkInPaysInFull       = "walk_in_must_pay_in_full"
	CodeInvoiceHasPayments     = "invoice_has_payments"
	CodeNothingToReserve       = "nothing_to_reserve"
	CodeNothingToCancel        = "nothing_to_cancel"
	CodeDocumentReversed       = "document_reversed"
	CodeMethodInactive         = "payment_method_inactive"
	CodeNoChange               = "nothing_changed"
	CodeReturnChanged          = "return_changed"
	CodeSaleChanged            = "sale_changed"
	CodeShiftChanged           = "shift_changed"
	CodeOfflineSalePosted      = "offline_sale_already_posted"
	CodeOfflineSaleParked      = "offline_sale_parked"
	CodeLoyaltyDisabled        = "loyalty_disabled"
)

// errConflict is a 409 the client resolves by changing the request or the data.
func errConflict(code, msg string) error { return apperr.NewConflict(code, msg) }

// errStale is the 412 of a write made against an old version of a record.
func errStale() error {
	return apperr.NewPreconditionFailed("The record was changed by someone else; reload it and try again")
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

func errTransition(from, action string) error {
	return apperr.NewConflict(CodeInvalidTransition, fmt.Sprintf("A %s document cannot be %s", from, action))
}

func errQuoteExpired() error {
	return apperr.NewConflict(CodeQuoteExpired, "The quote has expired; copy it to quote again")
}

func lineField(i int, name string) string { return fmt.Sprintf("lines.%d.%s", i, name) }

// remapLines rewrites the line fields of a validation error raised on derived lines ("lines.N.x"
// or pricing's "lines[N].x") to the request line each one came from: index[N] is the request
// position of derived line N (-1 for a line no request line produced, kept on "lines").
func remapLines(err error, index []int) error {
	var e *apperr.Error
	if !errors.As(err, &e) || len(e.Fields) == 0 {
		return err
	}
	fields := make(map[string]string, len(e.Fields))
	changed := false
	for k, v := range e.Fields {
		n, rest, ok := parseLineField(k)
		if !ok {
			fields[k] = v
			continue
		}
		changed = true
		if n < 0 || n >= len(index) || index[n] < 0 {
			fields["lines"] = v
			continue
		}
		fields[lineField(index[n], rest)] = v
	}
	if !changed {
		return err
	}
	out := *e
	out.Fields = fields
	return out.WithCause(err)
}

// parseLineField splits "lines.3.qty" or "lines[3].qty" into 3 and "qty".
func parseLineField(k string) (int, string, bool) {
	var rest string
	switch {
	case strings.HasPrefix(k, "lines."):
		rest = k[len("lines."):]
		dot := strings.IndexByte(rest, '.')
		if dot < 0 {
			return 0, "", false
		}
		n, err := strconv.Atoi(rest[:dot])
		if err != nil {
			return 0, "", false
		}
		return n, rest[dot+1:], true
	case strings.HasPrefix(k, "lines["):
		rest = k[len("lines["):]
		end := strings.Index(rest, "].")
		if end < 0 {
			return 0, "", false
		}
		n, err := strconv.Atoi(rest[:end])
		if err != nil {
			return 0, "", false
		}
		return n, rest[end+2:], true
	}
	return 0, "", false
}
