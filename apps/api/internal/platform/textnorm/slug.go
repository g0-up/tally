package textnorm

import "strings"

// SlugMaxLen is the longest slug Slug produces (the store address policy allows 30 characters).
const SlugMaxLen = 30

// Slug turns a display name into a hostname label: diacritics removed, đ→d, lowercase, every run
// of characters other than a-z and 0-9 replaced by one hyphen, cut to SlugMaxLen and trimmed of
// hyphens. "Cửa hàng Đường Phèn" → "cua-hang-duong-phen". The result may be empty or too short
// for the slug policy; callers validate it.
func Slug(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range fold(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
			continue
		}
		hyphen = true
	}
	out := b.String()
	if len(out) > SlugMaxLen {
		out = out[:SlugMaxLen]
	}
	return strings.Trim(out, "-")
}
