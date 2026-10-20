package scanner

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
)

func TestVerifyCodeIsStableFourDigits(t *testing.T) {
	sum := sha256.Sum256([]byte("phone secret"))
	got := VerifyCode(sum[:])
	if len(got) != 4 || strings.Trim(got, "0123456789") != "" {
		t.Fatalf("verify code %q is not four digits", got)
	}
	if again := VerifyCode(sum[:]); again != got {
		t.Fatalf("verify code changed: %q then %q", got, again)
	}
	// First two bytes big-endian modulo 10000, zero-padded.
	for _, tc := range []struct {
		hash []byte
		want string
	}{
		{[]byte{0x00, 0x07, 0xff}, "0007"},
		{[]byte{0x27, 0x10}, "0000"}, // 10000
		{[]byte{0xff, 0xff}, "5535"}, // 65535
		{[]byte{0x30, 0x39, 0x01}, "2345"},
		{[]byte{0x01}, ""},
	} {
		if got := VerifyCode(tc.hash); got != tc.want {
			t.Errorf("VerifyCode(%x) = %q, want %q", tc.hash, got, tc.want)
		}
	}
}

func TestDeviceLabel(t *testing.T) {
	for _, tc := range []struct{ ua, want string }{
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "iPhone · Safari"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0 Mobile/15E148 Safari/604.1", "iPhone · Chrome"},
		{"Mozilla/5.0 (Linux; Android 14; SM-A546E) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0 Mobile Safari/537.36", "Android · Samsung Internet"},
		{"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36", "Android · Chrome"},
		{"Mozilla/5.0 (Linux; Android 12; Redmi) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36 Zalo android/12100", "Android · Zalo"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36 Edg/126.0", "Windows · Edge"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) Gecko/20100101 Firefox/127.0", "Mac · Firefox"},
		{"Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "iPad · Safari"},
		{"Mozilla/5.0 (X11; Linux x86_64)", "Linux"},
		{"curl/8.5.0", ""},
		{"", ""},
	} {
		if got := DeviceLabel(tc.ua); got != tc.want {
			t.Errorf("DeviceLabel(%q) = %q, want %q", tc.ua, got, tc.want)
		}
	}
	// Only the head of a huge header is read.
	if got := DeviceLabel(strings.Repeat("x", 600) + " iPhone Safari"); got != "" {
		t.Errorf("label from beyond the inspected head: %q", got)
	}
}

// fakeCatalog knows one SKU with one unit.
type fakeCatalog struct{ sku, unit uuid.UUID }

func (fakeCatalog) ValidateCode(string) error { return nil }

func (f fakeCatalog) ItemNames(_ context.Context, _, skuID, unitID uuid.UUID) (string, string, error) {
	switch {
	case skuID != f.sku:
		return "", "", ErrUnknownSKU
	case unitID != f.unit:
		return "", "", ErrUnknownUnit
	}
	return "Green tea 500ml", "Bottle", nil
}

func validationFields(t *testing.T, err error) map[string]string {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Kind != apperr.Validation {
		t.Fatalf("error %v, want a validation error", err)
	}
	return ae.Fields
}

func TestResultValidation(t *testing.T) {
	cat := fakeCatalog{sku: uuid.New(), unit: uuid.New()}
	svc := NewService(Deps{Catalog: cat})
	ctx, tenant := context.Background(), uuid.New()
	ptr := func(id uuid.UUID) *uuid.UUID { return &id }

	for _, o := range []Outcome{OutcomeNotFound, OutcomeNotAccepting, OutcomeNeedsDesktopEdit, OutcomeNewProduct, OutcomeNotInOrder} {
		r, err := svc.resultOf(ctx, tenant, ResultInput{Outcome: o, SKUID: ptr(uuid.New())})
		if err != nil || r != (ScanResult{Outcome: o}) {
			t.Errorf("%s: %+v %v (names must only come with added)", o, r, err)
		}
	}
	for _, o := range []Outcome{OutcomeDesktopUnavailable, OutcomeTimeout, "", "deleted"} {
		err := ResultInput{Outcome: o}.check()
		if f := validationFields(t, err); f["outcome"] != "oneof" {
			t.Errorf("outcome %q: fields %v", o, f)
		}
	}
	err := ResultInput{Outcome: OutcomeAdded}.check()
	if f := validationFields(t, err); f["sku_id"] != "required" || f["unit_id"] != "required" {
		t.Errorf("added without ids: %v", f)
	}
	_, err = svc.resultOf(ctx, tenant, ResultInput{Outcome: OutcomeAdded, SKUID: ptr(uuid.New()), UnitID: ptr(cat.unit)})
	if f := validationFields(t, err); f["sku_id"] != "not_found" {
		t.Errorf("unknown SKU: %v", f)
	}
	_, err = svc.resultOf(ctx, tenant, ResultInput{Outcome: OutcomeAdded, SKUID: ptr(cat.sku), UnitID: ptr(uuid.New())})
	if f := validationFields(t, err); f["unit_id"] != "not_found" {
		t.Errorf("unit of another SKU: %v", f)
	}
	r, err := svc.resultOf(ctx, tenant, ResultInput{Outcome: OutcomeAdded, SKUID: ptr(cat.sku), UnitID: ptr(cat.unit)})
	if err != nil || r != (ScanResult{Outcome: OutcomeAdded, ProductName: "Green tea 500ml", UnitName: "Bottle"}) {
		t.Errorf("added: %+v %v", r, err)
	}
}

func TestEndedConditions(t *testing.T) {
	got := endedSQL("$9")
	for _, want := range []string{
		"s.revoked_at IS NOT NULL",
		"s.desktop_seen_at <= $9::timestamptz - interval '60 seconds'",
		"(s.claimed_at IS NOT NULL AND s.approved_at IS NULL AND s.claimed_at <= $9::timestamptz - interval '120 seconds')",
		"(s.approved_at IS NOT NULL AND s.approved_at <= $9::timestamptz - interval '43200 seconds')",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ended predicate %q lacks %q", got, want)
		}
	}
	state := stateSQL("$9")
	if !strings.HasPrefix(state, "CASE WHEN "+got+" THEN 'ended'") ||
		!strings.Contains(state, "WHEN s.claimed_at IS NULL THEN 'waiting'") ||
		!strings.Contains(state, "WHEN s.approved_at IS NULL THEN 'pending'") || !strings.HasSuffix(state, "ELSE 'claimed' END") {
		t.Errorf("state expression %q", state)
	}
}

func TestSessionViews(t *testing.T) {
	sum := sha256.Sum256([]byte("secret"))
	now, label := time.Now(), "iPhone · Safari"
	pending := Session{State: StatePending, PhoneSecretHash: sum[:], ClaimedAt: &now, DeviceLabel: &label}
	st := pending.status()
	if st.State != StatePending || st.VerifyCode != VerifyCode(sum[:]) || st.DeviceLabel != label || st.ClaimedAt == nil {
		t.Errorf("pending desktop view %+v", st)
	}
	if ps := pending.phoneStatus(); ps != (PhoneStatus{State: StatePending, VerifyCode: VerifyCode(sum[:])}) {
		t.Errorf("pending phone view %+v", ps)
	}
	// An ended session shows no phone details, even while the secret is still stored.
	ended := pending
	ended.State = StateEnded
	if st := ended.status(); st != (Status{State: StateEnded}) {
		t.Errorf("ended desktop view %+v", st)
	}
	if ps := ended.phoneStatus(); ps != (PhoneStatus{State: StateEnded}) {
		t.Errorf("ended phone view %+v", ps)
	}
	if st := (Session{State: StateWaiting}).status(); st != (Status{State: StateWaiting}) {
		t.Errorf("waiting desktop view %+v", st)
	}
}

func TestOwnerOwnsOnlyItsLoginSession(t *testing.T) {
	o := Owner{TenantID: uuid.New(), UserID: uuid.New(), LoginSessionID: uuid.New()}
	s := Session{TenantID: o.TenantID, CreatedBy: o.UserID, LoginSessionID: o.LoginSessionID}
	if !o.owns(s) {
		t.Fatal("owner does not own its session")
	}
	for name, other := range map[string]Owner{
		"other user":          {TenantID: o.TenantID, UserID: uuid.New(), LoginSessionID: o.LoginSessionID},
		"other login session": {TenantID: o.TenantID, UserID: o.UserID, LoginSessionID: uuid.New()},
		"other tenant":        {TenantID: uuid.New(), UserID: o.UserID, LoginSessionID: o.LoginSessionID},
	} {
		if other.owns(s) {
			t.Errorf("%s owns the session", name)
		}
	}
}

func TestClaimRefusesObviousJunkWithoutTheDatabase(t *testing.T) {
	svc := NewService(Deps{Catalog: fakeCatalog{}})
	for name, tc := range map[string]struct {
		tenant uuid.UUID
		code   string
	}{
		"unknown host": {uuid.Nil, "CODE"},
		"empty":        {uuid.New(), "   "},
		"too long":     {uuid.New(), strings.Repeat("A", maxClaimCodeLen+1)},
	} {
		_, err := svc.Claim(context.Background(), tc.tenant, tc.code, "")
		if !apperr.Is(err, CodeClaimInvalid) || apperr.KindOf(err) != apperr.Gone {
			t.Errorf("%s: %v, want 410 claim_invalid", name, err)
		}
	}
}

func TestScanValidatesTheCodeFirst(t *testing.T) {
	refuse := errors.New("refused")
	svc := NewService(Deps{Catalog: refusingCatalog{err: refuse}})
	if _, err := svc.Scan(context.Background(), uuid.New(), nil, idempotencyKey("k"), " x "); !errors.Is(err, refuse) {
		t.Fatalf("scan of an invalid code: %v", err)
	}
}

type refusingCatalog struct {
	fakeCatalog
	err error
}

func (r refusingCatalog) ValidateCode(string) error { return r.err }

func idempotencyKey(v string) idempotency.Key {
	sum := sha256.Sum256([]byte(v))
	return idempotency.Key{Value: v, RequestHash: sum[:]}
}

func TestSessionToCloseSparesClaimedSessions(t *testing.T) {
	waiting, claimed := openSession{Waiting: true}, openSession{}
	for _, tc := range []struct {
		name string
		open []openSession
		want int
	}{
		{"oldest waiting one behind claimed ones", []openSession{claimed, claimed, waiting}, 2},
		{"oldest of several waiting ones", []openSession{claimed, waiting, waiting}, 1},
		{"oldest when every one is waiting", []openSession{waiting, waiting, waiting}, 0},
		{"oldest when phones claimed them all", []openSession{claimed, claimed, claimed}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionToClose(tc.open); got != tc.want {
				t.Fatalf("sessionToClose = %d, want %d", got, tc.want)
			}
		})
	}
}
