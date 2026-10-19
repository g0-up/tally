package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// SignatureHeader carries "sha256=<hex HMAC-SHA256 of the raw body>".
const SignatureHeader = "X-Tally-Signature"

// SignedWebhook is a generic provider that calls back with a JSON body signed by a shared
// secret:
//
//	{"event_id": "...", "invoice_number": "SV-...", "amount": "250000", "reference": "..."}
type SignedWebhook struct {
	name   string
	secret []byte
}

// NewSignedWebhook returns the adapter; the secret must be at least 32 bytes.
func NewSignedWebhook(name string, secret []byte) (SignedWebhook, error) {
	if name == "" || name == ManualName {
		return SignedWebhook{}, fmt.Errorf("signed webhook: invalid provider name %q", name)
	}
	if len(secret) < 32 {
		return SignedWebhook{}, fmt.Errorf("signed webhook %s: secret must be at least 32 bytes", name)
	}
	return SignedWebhook{name: name, secret: append([]byte(nil), secret...)}, nil
}

// Name implements Provider.
func (w SignedWebhook) Name() string { return w.name }

// Instructions implements Provider.
func (SignedWebhook) Instructions(invoiceNumber string, amount money.Money) string {
	return fmt.Sprintf("Thanh toán %s đ cho hóa đơn %s qua cổng thanh toán.", amount.FormatVND(), invoiceNumber)
}

// Sign returns the signature header value of body.
func (w SignedWebhook) Sign(body []byte) string {
	mac := hmac.New(sha256.New, w.secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify implements Webhook: the signature is compared in constant time before the body is read.
func (w SignedWebhook) Verify(header http.Header, body []byte) (Payment, error) {
	got, ok := strings.CutPrefix(header.Get(SignatureHeader), "sha256=")
	if !ok {
		return Payment{}, ErrSignature
	}
	sig, err := hex.DecodeString(got)
	if err != nil {
		return Payment{}, ErrSignature
	}
	mac := hmac.New(sha256.New, w.secret)
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Payment{}, ErrSignature
	}
	var in struct {
		EventID       string `json:"event_id"`
		InvoiceNumber string `json:"invoice_number"`
		Amount        string `json:"amount"`
		Reference     string `json:"reference"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return Payment{}, fmt.Errorf("%w: %v", ErrPayload, err)
	}
	in.EventID, in.InvoiceNumber = strings.TrimSpace(in.EventID), strings.TrimSpace(in.InvoiceNumber)
	if in.EventID == "" || len(in.EventID) > 200 || in.InvoiceNumber == "" {
		return Payment{}, fmt.Errorf("%w: event_id and invoice_number are required", ErrPayload)
	}
	amount, err := money.NewMoney(in.Amount)
	if err != nil || !amount.IsPositive() {
		return Payment{}, fmt.Errorf("%w: amount must be a positive decimal", ErrPayload)
	}
	return Payment{
		EventID: in.EventID, InvoiceNumber: in.InvoiceNumber, Amount: amount,
		Reference: strings.TrimSpace(in.Reference), Payload: append([]byte(nil), body...),
	}, nil
}
