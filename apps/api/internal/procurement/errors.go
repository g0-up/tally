package procurement

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Error codes of the procurement module.
const (
	CodeOrderNotDraft        = "order_not_draft"
	CodeOrderStatus          = "order_status_invalid"
	CodeOrderNotReceivable   = "order_not_receivable"
	CodeOrderEmpty           = "order_has_no_lines"
	CodeReceiptExceedsOrder  = "receipt_exceeds_order"
	CodeReturnExceedsKept    = "return_exceeds_received"
	CodeReceiptNotPosted     = "receipt_not_posted"
	CodeReceiptHasFollowups  = "receipt_has_followups"
	CodeReceiptStockConsumed = "receipt_stock_consumed"
	CodeReversalNotReversed  = "reversal_not_reversible"
	CodeAlreadyReversed      = "already_reversed"
	CodeDocumentExists       = "supplier_document_exists"
	CodeReceiptMatched       = "receipt_already_matched"
	CodeOrderChanged         = "purchase_order_changed"
)

// Codes of other modules this one reacts to.
const (
	codeInsufficientStock = "insufficient_stock"
)

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

func errNotDraft() error {
	return apperr.NewConflict(CodeOrderNotDraft, "Only a draft purchase order can be changed")
}

func errReceiptNotPosted() error {
	return apperr.NewConflict(CodeReceiptNotPosted, "The receipt was reversed or is a reversal")
}

func errReceiptMatched() error {
	return apperr.NewConflict(CodeReceiptMatched, "A receipt is already matched to another supplier document")
}

func lineField(i int, name string) string { return fmt.Sprintf("lines.%d.%s", i, name) }

// remapLines renames the line fields of a validation or conflict error raised on a list built
// from some of the request lines: index[N] is the request line of entry N (nil keeps the
// numbering). Both "lines.N.x" and the pricing module's "lines[N].x" come out as "lines.M.x".
// Other errors pass through unchanged.
func remapLines(err error, index []int) error {
	e, ok := apperr.As(err)
	if !ok || len(e.Fields) == 0 {
		return err
	}
	fields := make(map[string]string, len(e.Fields))
	changed := false
	for k, v := range e.Fields {
		nk := remapField(k, index)
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

func remapField(key string, index []int) string {
	i, tail, ok := parseLineField(key)
	if !ok {
		return key
	}
	if index != nil {
		if i >= len(index) {
			return key
		}
		i = index[i]
	}
	return lineField(i, tail)
}

// parseLineField splits "lines.N.x" or "lines[N].x" into N and x.
func parseLineField(key string) (int, string, bool) {
	var n, tail string
	if rest, ok := strings.CutPrefix(key, "lines."); ok {
		n, tail, ok = strings.Cut(rest, ".")
		if !ok {
			return 0, "", false
		}
	} else if rest, ok := strings.CutPrefix(key, "lines["); ok {
		n, tail, ok = strings.Cut(rest, "].")
		if !ok {
			return 0, "", false
		}
	} else {
		return 0, "", false
	}
	i, err := strconv.Atoi(n)
	if err != nil || i < 0 {
		return 0, "", false
	}
	return i, tail, true
}
