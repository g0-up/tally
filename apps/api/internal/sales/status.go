package sales

import (
	"slices"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Payment state codes of an invoice or sales document.
const (
	PaymentUnpaid   = "unpaid"
	PaymentPartial  = "partial"
	PaymentPaid     = "paid"
	PaymentOverpaid = "overpaid"
)

// paymentLabels are the labels the screens and prints show for each payment state.
var paymentLabels = map[string]string{
	PaymentUnpaid:   "Chưa thanh toán",
	PaymentPartial:  "Thanh toán một phần",
	PaymentPaid:     "Đã thanh toán đủ",
	PaymentOverpaid: "Thừa tiền",
}

// PaymentState is how much of a document's total the customer has paid. Open is what is still
// owed (never negative); an overpayment stays the customer's advance.
type PaymentState struct {
	Total money.Money `json:"total_amount"`
	Paid  money.Money `json:"paid_amount"`
	Open  money.Money `json:"open_amount"`
	// Pending is transferred money still waiting for verification; it counts as paid only once
	// verified.
	Pending money.Money `json:"pending_amount"`
	Code    string      `json:"code"`
	Label   string      `json:"label"`
}

// paymentStateOf derives the payment state of total with paid received.
func paymentStateOf(total, paid money.Money) PaymentState {
	st := PaymentState{Total: total, Paid: paid, Open: total.Sub(paid)}
	switch c := paid.Cmp(total); {
	case c > 0:
		st.Code, st.Open = PaymentOverpaid, money.Money{}
	case c == 0:
		st.Code = PaymentPaid
	case !paid.IsPositive():
		st.Code = PaymentUnpaid
	default:
		st.Code = PaymentPartial
	}
	st.Label = paymentLabels[st.Code]
	return st
}

// quoteStatus is the status a quote shows on businessDate: a draft or sent quote past its
// validity is expired, which is derived and never stored.
func quoteStatus(stored string, validUntil Date, businessDate time.Time) string {
	if (stored == QuoteDraft || stored == QuoteSent) && validUntil.Before(businessDate) {
		return QuoteExpired
	}
	return stored
}

// Quote actions and the statuses (as shown, expiry derived) each may start from.
const (
	quoteEdit    = "edited"
	quoteSend    = "sent"
	quoteAccept  = "accepted"
	quoteReject  = "rejected"
	quoteConvert = "converted"
)

var quoteFrom = map[string][]string{
	quoteEdit:    {QuoteDraft, QuoteSent},
	quoteSend:    {QuoteDraft},
	quoteAccept:  {QuoteDraft, QuoteSent},
	quoteReject:  {QuoteDraft, QuoteSent},
	quoteConvert: {QuoteDraft, QuoteSent, QuoteAccepted},
}

// checkQuoteAction allows action on a quote showing status: an expired quote can only be copied
// (409 quote_expired), any other disallowed start is 409 invalid_status_transition.
func checkQuoteAction(status, action string) error {
	if slices.Contains(quoteFrom[action], status) {
		return nil
	}
	if status == QuoteExpired {
		return errQuoteExpired()
	}
	return errTransition(status, action)
}

// invoiceOpen reports the statuses with goods still to deliver.
func invoiceOpen(status string) bool {
	return status == InvoiceConfirmed || status == InvoicePartiallyDelivered
}

// deliveryStatus is the status of a confirmed invoice from its stocked lines: completed when
// every line is delivered or cancelled, partially delivered once anything was delivered.
// A cancelled remainder stays cancelled_remaining.
func deliveryStatus(current string, lines []InvoiceLine) string {
	if current == InvoiceCancelledRemaining {
		return current
	}
	delivered, open := false, false
	for _, l := range lines {
		if !stocked(l.Kind) {
			continue
		}
		if l.Delivered.IsPositive() {
			delivered = true
		}
		if l.remaining().IsPositive() {
			open = true
		}
	}
	switch {
	case !open:
		return InvoiceCompleted
	case delivered:
		return InvoicePartiallyDelivered
	default:
		return InvoiceConfirmed
	}
}
