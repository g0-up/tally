package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestSignedWebhookVerify(t *testing.T) {
	w, err := NewSignedWebhook("gateway", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"event_id":"evt-1","invoice_number":"SV-20261018-ABC123","amount":"250000","reference":"FT1"}`)
	h := http.Header{}
	h.Set(SignatureHeader, w.Sign(body))
	p, err := w.Verify(h, body)
	if err != nil {
		t.Fatalf("Verify = %v", err)
	}
	if p.EventID != "evt-1" || p.InvoiceNumber != "SV-20261018-ABC123" || !p.Amount.Equal(money.NewMoneyFromInt(250000)) {
		t.Fatalf("payment = %+v", p)
	}

	tampered := []byte(strings.Replace(string(body), "250000", "1", 1))
	if _, err := w.Verify(h, tampered); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered body: err = %v, want ErrSignature", err)
	}
	for _, sig := range []string{"", "sha256=zz", "md5=" + strings.TrimPrefix(w.Sign(body), "sha256=")} {
		bad := http.Header{}
		bad.Set(SignatureHeader, sig)
		if _, err := w.Verify(bad, body); !errors.Is(err, ErrSignature) {
			t.Fatalf("signature %q: err = %v, want ErrSignature", sig, err)
		}
	}

	noAmount := []byte(`{"event_id":"evt-2","invoice_number":"SV-1","amount":"-5"}`)
	h.Set(SignatureHeader, w.Sign(noAmount))
	if _, err := w.Verify(h, noAmount); !errors.Is(err, ErrPayload) {
		t.Fatalf("negative amount: err = %v, want ErrPayload", err)
	}
}

func TestNewSignedWebhookRejectsWeakSecret(t *testing.T) {
	if _, err := NewSignedWebhook("gateway", []byte("short")); err == nil {
		t.Fatal("short secret accepted")
	}
	if _, err := NewSignedWebhook(ManualName, []byte(strings.Repeat("k", 32))); err == nil {
		t.Fatal("reserved name accepted")
	}
}
