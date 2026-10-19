package sales

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
)

func TestValidateOfflineBatch(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	sale := func(id string) OfflineSale { return OfflineSale{ClientID: id, CreatedAt: at} }

	wantFields(t, validateOfflineBatch(OfflineSyncInput{Sales: []OfflineSale{sale("a"), sale("b")}}), nil)
	wantFields(t, validateOfflineBatch(OfflineSyncInput{}), map[string]string{"sales": "required"})
	wantFields(t, validateOfflineBatch(OfflineSyncInput{Sales: []OfflineSale{
		sale("a"), sale(""), sale(strings.Repeat("x", maxOfflineClientID+1)), sale("a"), {ClientID: "c"},
	}}), map[string]string{
		"sales.1.offline_client_id": "required",
		"sales.2.offline_client_id": "too_long",
		"sales.3.offline_client_id": "duplicate",
		"sales.4.created_at":        "required",
	})

	many := OfflineSyncInput{Sales: make([]OfflineSale, maxOfflineBatch+1)}
	for i := range many.Sales {
		many.Sales[i] = sale(strconv.Itoa(i))
	}
	wantFields(t, validateOfflineBatch(many), map[string]string{"sales": "too_many"})
}

func TestOfflineWindow(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	until := now.Add(4 * time.Hour)
	cases := []struct {
		name      string
		enabled   bool
		until     *time.Time
		createdAt time.Time
		want      string
	}{
		{"within the window", true, &until, now.Add(-2 * time.Hour), ""},
		{"device clock slightly ahead", true, &until, now.Add(3 * time.Minute), ""},
		{"offline selling off", false, &until, now.Add(-time.Hour), CodeOfflineDisabled},
		{"session never went offline", true, nil, now.Add(-time.Hour), CodeOfflineExpired},
		{"made after the window closed", true, ptr(now.Add(-3 * time.Hour)), now.Add(-2 * time.Hour), CodeOfflineExpired},
		{"made in the future", true, &until, now.Add(10 * time.Minute), CodeOfflineExpired},
		{"older than the limit", true, &until, now.Add(-25 * time.Hour), CodeOfflineExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := offlineWindow(c.enabled, 24, c.until, c.createdAt, now); got != c.want {
				t.Errorf("offlineWindow = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCheckOfflineTotal(t *testing.T) {
	if err := checkOfflineTotal(vnd("500000"), vnd("500000"), vnd("2000000")); err != nil {
		t.Fatalf("cash sale under the cap: %v", err)
	}
	if err := checkOfflineTotal(vnd("2000000"), vnd("2000000"), vnd("2000000")); err != nil {
		t.Fatalf("cash sale at the cap: %v", err)
	}
	fields := wantCode(t, checkOfflineTotal(vnd("500000"), vnd("200000"), vnd("2000000")), apperr.Validation, CodeOfflineNotAllowed)
	if fields["payments"] != CodeOfflineNotAllowed {
		t.Errorf("fields = %v, want payments %s", fields, CodeOfflineNotAllowed)
	}
	wantCode(t, checkOfflineTotal(vnd("2000001"), vnd("2000001"), vnd("2000000")), apperr.Conflict, CodeOfflineAmount)
}

func TestParkable(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"business conflict", errConflict(CodeInsufficientStock, "short"), true},
		{"validation", errInvalid(map[string]string{"lines": "required"}), true},
		{"locked period", apperr.NewPeriodLocked("locked"), true},
		{"same sale posting from another sync", apperr.NewConflict(idempotency.CodeInProgress, "in progress"), false},
		{"client id reused with other contents", apperr.NewConflict(idempotency.CodeKeyReused, "reused"), false},
		{"shift changed while posting", errConflict(CodeShiftChanged, "changed"), false},
		{"promotion changed while posting", errConflict(CodeSaleChanged, "changed"), false},
		{"read-only subscription", apperr.NewReadOnlyTenant(), false},
		{"plain failure", errors.New("boom"), false},
	} {
		if got := parkable(c.err); got != c.want {
			t.Errorf("%s: parkable = %v, want %v", c.name, got, c.want)
		}
	}
}
