package authn

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// RecoveryCodeCount is the number of recovery codes issued when TOTP is enabled.
const RecoveryCodeCount = 10

// recoveryAlphabet avoids look-alike characters (0/o, 1/l/i).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns RecoveryCodeCount single-use codes formatted "xxxxx-xxxxx" (about 49
// bits each). Only their keyed hashes are stored.
func NewRecoveryCodes() ([]string, error) {
	codes := make([]string, RecoveryCodeCount)
	buf := make([]byte, 10)
	for i := range codes {
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("authn: random recovery code: %w", err)
		}
		var b strings.Builder
		for j, c := range buf {
			if j == 5 {
				b.WriteByte('-')
			}
			// 256 is not a multiple of 31; the bias (<1%) does not matter at this length.
			b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
		}
		codes[i] = b.String()
	}
	return codes, nil
}

// NormalizeRecoveryCode lower-cases a typed code and drops spaces and hyphens, so "ABCDE FGHJK"
// and "abcde-fghjk" match the same stored hash.
func NormalizeRecoveryCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			return -1
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return r
	}, code)
}
