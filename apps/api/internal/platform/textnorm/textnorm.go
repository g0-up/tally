// Package textnorm builds the normalised keys that replace database expression indexes and
// extensions: uniqueness keys (name_key) and search text (search_norm).
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// fold strips Vietnamese (and other Latin) diacritics, maps đ/Đ to d and lowercases.
func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		// The chain only fails on invalid transformer state, never on input; fall back to s.
		out = s
	}
	out = strings.NewReplacer("đ", "d", "Đ", "d").Replace(out)
	return strings.ToLower(out)
}

// Key is the uniqueness key of a name: diacritics removed, đ→d, lowercase, inner whitespace
// collapsed to one space, trimmed. "  Đường  Phèn " → "duong phen".
func Key(s string) string {
	return strings.Join(strings.Fields(fold(s)), " ")
}

// SearchNorm is the text searched with LIKE: like Key, but punctuation also separates words, so
// "Bánh-quy (hộp)" → "banh quy hop".
func SearchNorm(s string) string {
	folded := fold(s)
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, folded)
	return strings.Join(strings.Fields(cleaned), " ")
}

// EmailKey is the uniqueness key of an e-mail address (users.email_key): surrounding whitespace
// removed and lower-cased. " Owner@Example.TEST " → "owner@example.test".
func EmailKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// PhoneKey is the search and dedupe key of a phone number: digits only, with the Vietnamese
// country code +84 (or 0084, or a bare 84 on an 11-digit number) folded to the domestic leading
// 0. "+84 912-418-226" → "0912418226". It returns "" when s has no digit.
func PhoneKey(s string) string {
	trimmed := strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	var national string
	switch {
	case strings.HasPrefix(trimmed, "+84"):
		national = strings.TrimPrefix(digits, "84")
	case strings.HasPrefix(digits, "0084"):
		national = strings.TrimPrefix(digits, "0084")
	case strings.HasPrefix(digits, "84") && len(digits) == 11:
		national = digits[2:]
	default:
		return digits
	}
	return "0" + strings.TrimPrefix(national, "0")
}
