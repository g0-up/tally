package support

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestLooksLikePasswordCatchesCredentialPhrases(t *testing.T) {
	for _, text := range []string{
		"Mật khẩu của tôi là abc123",
		"mat khau: 123456",
		"my password is hunter2",
		"Mã OTP 482913",
		"otp 123456",
		"mk: hoaphat2026",
		"PWD=secret",
	} {
		if !LooksLikePassword(text) {
			t.Errorf("LooksLikePassword(%q) = false, want true", text)
		}
	}
	for _, text := range []string{
		"Không in được hoá đơn bán lẻ",
		"Báo cáo tồn kho sai số lượng sơn trắng",
		"Topic about shopping carts",
	} {
		if LooksLikePassword(text) {
			t.Errorf("LooksLikePassword(%q) = true, want false", text)
		}
	}
}

func TestNewMessageValidate(t *testing.T) {
	if err := (NewMessage{Body: "Màn hình bán hàng bị treo"}).validate(); err != nil {
		t.Fatalf("valid message: %v", err)
	}
	tests := []struct {
		name string
		msg  NewMessage
		code string
	}{
		{"blank body", NewMessage{Body: "   "}, "validation_failed"},
		{"too long", NewMessage{Body: string(make([]rune, 10001))}, "validation_failed"},
		{"too many files", NewMessage{Body: "x", AttachmentIDs: make([]uuid.UUID, 11)}, "validation_failed"},
		{"password without acknowledgement", NewMessage{Body: "mật khẩu là 1234"}, CodePossiblePassword},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.msg.validate(); !apperr.Is(err, tc.code) {
				t.Fatalf("validate = %v, want %s", err, tc.code)
			}
		})
	}
	if err := (NewMessage{Body: "mật khẩu là 1234", AcknowledgeWarning: true}).validate(); err != nil {
		t.Fatalf("acknowledged warning: %v", err)
	}
}

func TestNewSessionValidate(t *testing.T) {
	in := NewSession{TicketID: uuid.New(), Modules: []string{"catalog"}}
	if err := in.validate(); err != nil || in.Hours != DefaultSessionHours {
		t.Fatalf("validate = %v with hours %d, want nil with %d", err, in.Hours, DefaultSessionHours)
	}
	tests := []struct {
		name  string
		in    NewSession
		field string
	}{
		{"no ticket", NewSession{Modules: []string{"catalog"}}, "ticket_id"},
		{"no module", NewSession{TicketID: uuid.New()}, "modules"},
		{"unknown module", NewSession{TicketID: uuid.New(), Modules: []string{"attachments"}}, "modules"},
		{"too long", NewSession{TicketID: uuid.New(), Modules: []string{"sales"}, Hours: MaxSessionHours + 1}, "hours"},
		{"negative hours", NewSession{TicketID: uuid.New(), Modules: []string{"sales"}, Hours: -1}, "hours"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := apperr.As(tc.in.validate())
			if !ok || e.Fields[tc.field] == "" {
				t.Fatalf("validate = %v, want a %s field error", e, tc.field)
			}
		})
	}
	if got := sortedModules([]string{"sales", "catalog", "sales"}); len(got) != 2 || got[0] != "catalog" || got[1] != "sales" {
		t.Errorf("sortedModules = %v, want [catalog sales]", got)
	}
}

func TestSupportViewModulesMapPathSegments(t *testing.T) {
	tests := []struct {
		module, path, segment string
		want                  bool
	}{
		{"catalog", "/products/0b6f", "products", true},
		{"catalog", "products:quick-create", "products", true},
		{"sales", "/pos/sessions", "pos", true},
		{"catalog", "/parties", "parties", false},
		{"reports", "/attachments/1", "attachments", false},
		{"unknown", "/products", "products", false},
	}
	for _, tc := range tests {
		a := Access{Module: tc.module, Path: tc.path}
		if a.Segment() != tc.segment {
			t.Errorf("Segment(%q) = %q, want %q", tc.path, a.Segment(), tc.segment)
		}
		if got := ModuleOfSegment(tc.module, a.Segment()); got != tc.want {
			t.Errorf("ModuleOfSegment(%s, %s) = %v, want %v", tc.module, a.Segment(), got, tc.want)
		}
	}
	for module, segments := range ViewModules {
		for _, s := range segments {
			for _, forbidden := range []string{"attachments", "export-jobs", "notifications", "analytics", "auth", "users"} {
				if s == forbidden {
					t.Errorf("module %s opens %s, which a support view must never expose", module, s)
				}
			}
		}
	}
}

func TestSessionStateAndGrants(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	revoked := now.Add(-time.Minute)
	tests := []struct {
		name string
		s    Session
		want string
	}{
		{"active", Session{ExpiresAt: now.Add(time.Hour)}, "active"},
		{"expires exactly now", Session{ExpiresAt: now}, "expired"},
		{"expired", Session{ExpiresAt: now.Add(-time.Hour)}, "expired"},
		{"revoked before expiry", Session{ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked}, "revoked"},
	}
	for _, tc := range tests {
		if got := tc.s.State(now); got != tc.want {
			t.Errorf("%s: State = %s, want %s", tc.name, got, tc.want)
		}
	}
	s := Session{Modules: []string{"catalog", "sales"}}
	if !s.Grants("sales") || s.Grants("debt_cash") {
		t.Errorf("Grants = sales %v, debt_cash %v; want true, false", s.Grants("sales"), s.Grants("debt_cash"))
	}
}
