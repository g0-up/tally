package identity

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// normalizeEmail returns the trimmed address of a bare e-mail ("a@b.vn", no display name).
func normalizeEmail(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > maxEmailLen {
		return "", false
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Name != "" || addr.Address != v {
		return "", false
	}
	at := strings.LastIndexByte(v, '@')
	if at < 1 || !strings.Contains(v[at+1:], ".") {
		return "", false
	}
	return v, true
}

// maxPhoneLen bounds a typed phone number (digits plus separators).
const maxPhoneLen = 30

// normalizePhone returns the domestic form ("0912418226") of a Vietnamese phone number typed
// with or without the country code (+84, 0084, 84) and with spaces, dots, dashes or brackets:
// a mobile number (0 + 9 digits, network prefix 3, 5, 7, 8 or 9) or a landline (02 + 9 digits).
func normalizePhone(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > maxPhoneLen {
		return "", false
	}
	for i, r := range v {
		switch {
		case r >= '0' && r <= '9', r == ' ', r == '.', r == '-', r == '(', r == ')':
		case r == '+' && i == 0:
		default:
			return "", false
		}
	}
	key := textnorm.PhoneKey(v)
	if len(key) < 2 || key[0] != '0' {
		return "", false
	}
	switch key[1] {
	case '3', '5', '7', '8', '9':
		return key, len(key) == 10
	case '2':
		return key, len(key) == 11
	}
	return "", false
}

// loginIdentifier is what a login form names the account by: the e-mail key of an address
// with an "@", otherwise a Vietnamese phone number. ok is false for neither.
type loginIdentifier struct {
	emailKey string
	phone    string
}

func parseLoginIdentifier(raw string) (loginIdentifier, bool) {
	v := strings.TrimSpace(raw)
	if strings.Contains(v, "@") {
		key := textnorm.EmailKey(v)
		return loginIdentifier{emailKey: key}, key != "" && len(key) <= maxEmailLen
	}
	phone, ok := normalizePhone(v)
	return loginIdentifier{phone: phone}, ok
}

// throttleKey is the login_attempts key of the identifier: the e-mail key, or "tel:" and the
// phone (never an e-mail key, which always has an "@").
func (id loginIdentifier) throttleKey() string {
	if id.phone != "" {
		return "tel:" + id.phone
	}
	return id.emailKey
}

// passwordProblem returns the validation reason of a new password, "" when it is acceptable.
func passwordProblem(pw string) string {
	switch {
	case pw == "":
		return "required"
	case len(pw) < MinPasswordBytes:
		return "too_short"
	case len(pw) > authn.MaxPasswordBytes:
		return "too_long"
	}
	return ""
}

// requiredFields returns field → "required" for every empty value, nil when none is empty.
func requiredFields(values map[string]string) map[string]string {
	var out map[string]string
	for field, v := range values {
		if v == "" {
			if out == nil {
				out = map[string]string{}
			}
			out[field] = "required"
		}
	}
	return out
}

// checkNewPassword validates a password being set.
func checkNewPassword(pw string) error {
	if reason := passwordProblem(pw); reason != "" {
		return errInvalid(map[string]string{"password": reason})
	}
	return nil
}

// checkOwner validates the e-mail and name of a new owner account.
func checkOwner(email, fullName string) (string, error) {
	addr, ok := normalizeEmail(email)
	if !ok {
		return "", errInvalid(map[string]string{"email": "invalid"})
	}
	if err := validateFullName(fullName); err != nil {
		return "", err
	}
	return addr, nil
}

// maxFullNameLen bounds the full name of an account.
const maxFullNameLen = 200

// validateFullName checks the required full name of an account: at most maxFullNameLen
// characters on one line once trimmed.
func validateFullName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errInvalid(map[string]string{"full_name": "required"})
	case utf8.RuneCountInString(name) > maxFullNameLen || strings.ContainsAny(name, "\r\n\x00"):
		return errInvalid(map[string]string{"full_name": "invalid"})
	}
	return nil
}

// deviceLabel turns a User-Agent into a short label such as "Chrome on Windows".
func deviceLabel(ua string) string {
	browser := firstMatch(ua, []labelRule{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"CocosBrowser", "Cốc Cốc"}, {"coc_coc_browser", "Cốc Cốc"},
		{"Firefox/", "Firefox"}, {"FxiOS", "Firefox"}, {"CriOS", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	})
	system := firstMatch(ua, []labelRule{
		{"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Android", "Android"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"},
	})
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	return "Unknown device"
}

type labelRule struct{ needle, label string }

func firstMatch(s string, rules []labelRule) string {
	for _, r := range rules {
		if strings.Contains(s, r.needle) {
			return r.label
		}
	}
	return ""
}

// maxUserAgentLen bounds the stored User-Agent.
const maxUserAgentLen = 512

// truncateUA cuts a User-Agent to maxUserAgentLen bytes without leaving a broken rune.
func truncateUA(ua string) string {
	if len(ua) <= maxUserAgentLen {
		return ua
	}
	return strings.ToValidUTF8(ua[:maxUserAgentLen], "")
}
