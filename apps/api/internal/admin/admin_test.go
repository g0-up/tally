package admin

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestCommissionIsPercentOfPaidServiceInvoicesRoundedToDong(t *testing.T) {
	tests := []struct {
		paid, pct, want string
	}{
		{"2400000", "20", "480000"},
		{"1990000", "12.5", "248750"},
		{"333333", "10", "33333"},
		{"5", "10", "1"},
		{"0", "30", "0"},
	}
	for _, tc := range tests {
		got := commission(money.MustMoney(tc.paid), money.MustPercent(tc.pct))
		if !got.Equal(money.MustMoney(tc.want)) {
			t.Errorf("commission(%s, %s%%) = %s, want %s", tc.paid, tc.pct, got, tc.want)
		}
	}
}

func TestFeedbackInputValidate(t *testing.T) {
	nine := 9
	eleven := 11
	body, page, err := FeedbackInput{Kind: "nps", Score: &nine, PagePath: "/pos?q=son-trang#top"}.validate()
	if err != nil || body != nil || page == nil || *page != "/pos" {
		t.Fatalf("nps = body %v, page %v, err %v; want no body and page /pos", body, page, err)
	}
	body, _, err = FeedbackInput{Kind: "idea", Body: "  Cho phép in tem mã vạch  "}.validate()
	if err != nil || body == nil || *body != "Cho phép in tem mã vạch" {
		t.Fatalf("idea = body %v, err %v; want the trimmed text", body, err)
	}
	tests := []struct {
		name  string
		in    FeedbackInput
		field string
	}{
		{"unknown kind", FeedbackInput{Kind: "complaint", Body: "x"}, "kind"},
		{"nps without score", FeedbackInput{Kind: "nps"}, "score"},
		{"score out of range", FeedbackInput{Kind: "nps", Score: &eleven}, "score"},
		{"idea without text", FeedbackInput{Kind: "idea", Body: "  "}, "body"},
		{"text too long", FeedbackInput{Kind: "bug", Body: strings.Repeat("ă", maxFeedbackBody+1)}, "body"},
		{"relative page path", FeedbackInput{Kind: "bug", Body: "x", PagePath: "pos"}, "page_path"},
		{"page path too long", FeedbackInput{Kind: "bug", Body: "x", PagePath: "/" + strings.Repeat("p", maxPagePath)}, "page_path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tc.in.validate()
			e, ok := apperr.As(err)
			if !ok || e.Fields[tc.field] == "" {
				t.Fatalf("validate = %v, want a %s field error", err, tc.field)
			}
		})
	}
}

func TestIncidentFieldChecks(t *testing.T) {
	tests := []struct {
		name            string
		title, message  string
		messageRequired bool
		want            []string
	}{
		{"valid", "Chậm đồng bộ", "Đang kiểm tra", true, nil},
		{"blank title", " ", "Đang kiểm tra", true, []string{"title"}},
		{"long title", strings.Repeat("t", 201), "x", true, []string{"title"}},
		{"required message missing", "Sự cố", "", true, []string{"message"}},
		{"optional message missing", "Sự cố", "", false, nil},
		{"long message", "Sự cố", strings.Repeat("m", 2001), false, []string{"message"}},
	}
	for _, tc := range tests {
		fields := map[string]string{}
		checkTitle(fields, tc.title)
		checkMessage(fields, tc.message, tc.messageRequired)
		if len(fields) != len(tc.want) {
			t.Errorf("%s: fields = %v, want %v", tc.name, fields, tc.want)
			continue
		}
		for _, f := range tc.want {
			if fields[f] == "" {
				t.Errorf("%s: fields = %v, want %s", tc.name, fields, f)
			}
		}
	}
}

func TestReferralCodePattern(t *testing.T) {
	for _, code := range []string{"SON-HN", "DAILY2026", "ABC"} {
		if !referralCodePattern.MatchString(code) {
			t.Errorf("%s should be a valid referral code", code)
		}
	}
	for _, code := range []string{"AB", "-ABC", "son-hn", "SON HN", strings.Repeat("A", 33)} {
		if referralCodePattern.MatchString(code) {
			t.Errorf("%s should not be a valid referral code", code)
		}
	}
}

func TestNewAdminValidate(t *testing.T) {
	email, err := NewAdmin{Email: " ops@tally.vn ", FullName: "Vận hành", Password: strings.Repeat("p", MinPasswordBytes)}.validate()
	if err != nil || email != "ops@tally.vn" {
		t.Fatalf("validate = %q, %v; want the trimmed e-mail", email, err)
	}
	tests := []struct {
		name  string
		in    NewAdmin
		field string
	}{
		{"display name in e-mail", NewAdmin{Email: "Ops <ops@tally.vn>", FullName: "Ops", Password: strings.Repeat("p", 12)}, "email"},
		{"not an e-mail", NewAdmin{Email: "ops", FullName: "Ops", Password: strings.Repeat("p", 12)}, "email"},
		{"blank name", NewAdmin{Email: "ops@tally.vn", FullName: " ", Password: strings.Repeat("p", 12)}, "full_name"},
		{"name with a line break", NewAdmin{Email: "ops@tally.vn", FullName: "Ops\nAdmin", Password: strings.Repeat("p", 12)}, "full_name"},
		{"short password", NewAdmin{Email: "ops@tally.vn", FullName: "Ops", Password: strings.Repeat("p", MinPasswordBytes-1)}, "password"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.validate()
			e, ok := apperr.As(err)
			if !ok || e.Fields[tc.field] == "" {
				t.Fatalf("validate = %v, want a %s field error", err, tc.field)
			}
		})
	}
}

func TestDirectoryQueriesReadOnlyPlatformTables(t *testing.T) {
	allowed := []string{"tenants", "subscriptions", "plans", "tenant_usage_daily"}
	from := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-z_][a-z0-9_.]*)`)
	for name, query := range map[string]string{"directory": directorySelect, "detail usage": detailUsageSelect} {
		tables := from.FindAllStringSubmatch(query, -1)
		if len(tables) == 0 {
			t.Fatalf("%s query names no table", name)
		}
		for _, m := range tables {
			if m[1] != "LATERAL" && !slices.Contains(allowed, m[1]) {
				t.Errorf("%s query reads %s; the directory reads only %v", name, m[1], allowed)
			}
		}
	}
}
