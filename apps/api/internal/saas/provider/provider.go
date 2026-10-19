// Package provider is the payment provider port of service billing and its adapters: a manual
// bank transfer an operator confirms, and a generic webhook signed with HMAC-SHA256. No
// commercial provider is chosen yet; a real one is one more adapter.
package provider

import (
	"errors"
	"net/http"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// ErrSignature is returned by Verify for a callback whose signature does not match.
var ErrSignature = errors.New("payment provider: signature invalid")

// ErrPayload is returned by Verify for a signed callback whose body is not a valid payment.
var ErrPayload = errors.New("payment provider: payload invalid")

// Payment is one confirmed payment of a service invoice. EventID is unique per provider: the
// billing service records it first and ignores a repeat.
type Payment struct {
	EventID       string
	InvoiceNumber string
	Amount        money.Money
	Reference     string
	// Payload is the raw callback body (or the operator's confirmation), kept for audit.
	Payload []byte
}

// Provider takes payments of service invoices.
type Provider interface {
	// Name is the provider key stored on invoices and payment events.
	Name() string
	// Instructions tells the owner how to pay the invoice, in Vietnamese.
	Instructions(invoiceNumber string, amount money.Money) string
}

// Webhook is a provider that confirms payments by calling back.
type Webhook interface {
	Provider
	// Verify authenticates a callback and decodes its payment.
	Verify(header http.Header, body []byte) (Payment, error)
}
