package app

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
)

// The live store address check has its own per-address budget: a client that keeps calling it
// gets 429 rate_limited with Retry-After instead of unbounded lookups.
func TestSlugCheckIsRateLimitedPerAddress(t *testing.T) {
	a, err := New(Options{
		PublicBaseURL: mustURL(t, "http://localhost:8080"),
		Logger:        quiet,
		CSRFKey:       testCSRFKey,
		Modules:       Modules{Identity: identity.NewHandler(nil), Org: org.NewHandler(nil)},
		Ports:         Ports{Sessions: fakeSessions{}, TenantHosts: fakeHosts{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// An empty slug is refused by the handler before any lookup, so only the limiter is exercised.
	check := call{method: "GET", host: "localhost:8080", path: "/api/v1/signup/slug-check"}
	var last int
	for i := 0; i < 100; i++ {
		w := serve(a.Handler(), check)
		last = w.Code
		if w.Code == http.StatusTooManyRequests {
			if i < 20 {
				t.Errorf("rate limited after %d checks; typing a store address needs more", i)
			}
			if !strings.Contains(w.Body.String(), `"rate_limited"`) {
				t.Errorf("429 body = %s, want rate_limited", w.Body)
			}
			if secs, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || secs < 1 {
				t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
			}
			return
		}
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("check %d = %d %s, want 422 before the limit", i, w.Code, w.Body)
		}
	}
	t.Fatalf("100 checks from one address were never limited (last %d)", last)
}
