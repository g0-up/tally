package identity

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"chu@cuahang.vn", "chu@cuahang.vn", true},
		{"  Chu.Hang+1@Example.COM  ", "Chu.Hang+1@Example.COM", true},
		{"", "", false},
		{"no-at-sign", "", false},
		{"user@localhost", "", false},
		{"@example.com", "", false},
		{"Chủ <chu@example.com>", "", false},
		{"chu@example.com, other@example.com", "", false},
		{strings.Repeat("a", 250) + "@b.vn", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeEmail(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normalizeEmail(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPasswordProblem(t *testing.T) {
	cases := map[string]string{
		"":                        "required",
		"1234567":                 "too_short",
		"12345678":                "",
		"mật khẩu":                "", // 8 runes, more bytes
		strings.Repeat("x", 1025): "too_long",
	}
	for pw, want := range cases {
		if got := passwordProblem(pw); got != want {
			t.Errorf("passwordProblem(%d bytes) = %q, want %q", len(pw), got, want)
		}
	}
	if err := checkNewPassword("short"); !apperr.Is(err, "validation_failed") {
		t.Errorf("checkNewPassword(short) = %v", err)
	}
}

func TestRequiredFields(t *testing.T) {
	if got := requiredFields(map[string]string{"email": "a", "password": "b"}); got != nil {
		t.Errorf("all present = %v", got)
	}
	got := requiredFields(map[string]string{"email": "", "password": "b"})
	if len(got) != 1 || got["email"] != "required" {
		t.Errorf("missing email = %v", got)
	}
}

func TestCheckOwner(t *testing.T) {
	email, err := checkOwner(" chu@example.com ", "Nguyễn Văn A")
	if err != nil || email != "chu@example.com" {
		t.Fatalf("checkOwner = %q, %v", email, err)
	}
	for _, tc := range []struct{ email, name, field string }{
		{"not-an-email", "Nguyễn Văn A", "email"},
		{"chu@example.com", "  ", "full_name"},
		{"chu@example.com", "line\nbreak", "full_name"},
	} {
		_, err := checkOwner(tc.email, tc.name)
		ae, ok := apperr.As(err)
		if !ok || ae.Kind != apperr.Validation || ae.Fields[tc.field] == "" {
			t.Errorf("checkOwner(%q, %q) = %v, want a 422 on %s", tc.email, tc.name, err, tc.field)
		}
	}
}

func TestDeviceLabel(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36":               "Chrome on Windows",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36 Edg/129.0":     "Edge on Windows",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/604.1": "Safari on iOS",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) coc_coc_browser/130.0 Chrome/124.0 Safari/537.36":   "Cốc Cốc on Android",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:130.0) Gecko/20100101 Firefox/130.0":                                       "Firefox on macOS",
		"curl/8.5.0": "Unknown device",
		"":           "Unknown device",
	}
	for ua, want := range cases {
		if got := deviceLabel(ua); got != want {
			t.Errorf("deviceLabel(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestTruncateUA(t *testing.T) {
	short := "Mozilla/5.0"
	if truncateUA(short) != short {
		t.Error("a short User-Agent must be kept")
	}
	// A two-byte rune straddles the limit: the cut must not leave half of it.
	long := strings.Repeat("a", maxUserAgentLen-1) + "ữ" + strings.Repeat("b", 10)
	got := truncateUA(long)
	if len(got) > maxUserAgentLen || !utf8.ValidString(got) || got != strings.Repeat("a", maxUserAgentLen-1) {
		t.Errorf("truncateUA = %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestToSessionDTO(t *testing.T) {
	now := time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC)
	s := Session{ID: uuid.New(), DeviceLabel: "Chrome on Windows", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionAbsoluteTTL)}
	dto := toSessionDTO(s, uuid.New())
	if dto.Current || dto.MFAVerified || dto.DeviceLabel != s.DeviceLabel {
		t.Errorf("other session = %+v", dto)
	}
	s.MFAVerifiedAt = &now
	if dto := toSessionDTO(s, s.ID); !dto.Current || !dto.MFAVerified {
		t.Errorf("current verified session = %+v", dto)
	}
}

func TestRegisterNeedsEveryGroup(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Register without route groups must panic")
		}
	}()
	NewHandler(nil).Register(Routes{})
}

func TestNewServiceRequiresCollaborators(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Error("NewService without collaborators must fail")
	}
	if _, err := NewKeys("", "key"); err == nil {
		t.Error("NewKeys without the session hash key must fail")
	}
	keys, err := NewKeys("session-hash-key", "encryption-key")
	if err != nil || keys.Tokens == nil || keys.Recovery == nil || keys.Challenge == nil || keys.TOTP == nil {
		t.Fatalf("NewKeys = %+v, %v", keys, err)
	}
	if string(keys.Tokens.Sum("x")) == string(keys.Recovery.Sum("x")) {
		t.Error("token and recovery code hashes must use different keys")
	}
}

func TestThrottledErrorRendersRateLimited(t *testing.T) {
	err := newThrottled(90 * time.Second)
	ae, ok := apperr.As(err)
	if !ok || ae.Kind != apperr.RateLimited || ae.Code != "rate_limited" || err.RetryAfter != 90*time.Second {
		t.Errorf("throttled = %+v", err)
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"0912418226", "0912418226", true},
		{" 0912 418 226 ", "0912418226", true},
		{"091.241.8226", "0912418226", true},
		{"(091) 241-8226", "0912418226", true},
		{"+84 912 418 226", "0912418226", true},
		{"+84 0912 418 226", "0912418226", true},
		{"84912418226", "0912418226", true},
		{"0084912418226", "0912418226", true},
		{"0386123456", "0386123456", true},
		{"02838123456", "02838123456", true}, // landline, Ho Chi Minh City
		{"+84 28 3812 3456", "02838123456", true},
		{"", "", false},
		{"12345", "", false},
		{"912418226", "", false},       // no trunk or country prefix
		{"091241822", "", false},       // a digit short
		{"09124182261", "", false},     // a digit long
		{"0112418226", "", false},      // no such network prefix
		{"0212418226", "", false},      // landline needs 11 digits
		{"0912418226x", "", false},     // letters
		{"0912+418226", "", false},     // a plus inside
		{"+1 415 555 0100", "", false}, // foreign
		{strings.Repeat("0", 31), "", false},
	}
	for _, tc := range cases {
		got, ok := normalizePhone(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("normalizePhone(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseLoginIdentifier(t *testing.T) {
	cases := []struct {
		in, throttleKey string
		ok              bool
	}{
		{" Chu@CuaHang.vn ", "chu@cuahang.vn", true},
		{"+84 912 418 226", "tel:0912418226", true},
		{"0912418226", "tel:0912418226", true},
		{"not-an-identifier", "", false},
		{"@", "@", true},
	}
	for _, tc := range cases {
		id, ok := parseLoginIdentifier(tc.in)
		if ok != tc.ok || (ok && id.throttleKey() != tc.throttleKey) {
			t.Errorf("parseLoginIdentifier(%q) = %+v, %v; want key %q, %v", tc.in, id, ok, tc.throttleKey, tc.ok)
		}
	}
}
