package identity

import (
	"errors"
	"fmt"

	"github.com/g0-up/tally/apps/api/internal/platform/authn"
)

// Keys are the derived keys of the identity module. Each purpose gets its own key so one secret
// never serves two algorithms.
type Keys struct {
	// Tokens hashes session tokens and e-mail link tokens.
	Tokens *authn.Keyed
	// Recovery hashes MFA recovery codes.
	Recovery *authn.Keyed
	// Challenge signs the MFA step of a login.
	Challenge *authn.Signer
	// TOTP encrypts TOTP secrets at rest.
	TOTP *authn.Cipher
}

// NewKeys derives the keys from SESSION_HASH_KEY and APP_ENCRYPTION_KEY.
func NewKeys(sessionHashKey, encryptionKey string) (Keys, error) {
	if sessionHashKey == "" || encryptionKey == "" {
		return Keys{}, errors.New("identity: the session hash key and the encryption key are required")
	}
	tokens, err := authn.NewKeyed(authn.DeriveKey(sessionHashKey, "session-token"))
	if err != nil {
		return Keys{}, fmt.Errorf("identity: %w", err)
	}
	recovery, err := authn.NewKeyed(authn.DeriveKey(sessionHashKey, "recovery-code"))
	if err != nil {
		return Keys{}, fmt.Errorf("identity: %w", err)
	}
	challenge, err := authn.NewSigner(authn.DeriveKey(sessionHashKey, "mfa-challenge"))
	if err != nil {
		return Keys{}, fmt.Errorf("identity: %w", err)
	}
	totp, err := authn.NewCipher(authn.DeriveKey(encryptionKey, "totp-secret"))
	if err != nil {
		return Keys{}, fmt.Errorf("identity: %w", err)
	}
	return Keys{Tokens: tokens, Recovery: recovery, Challenge: challenge, TOTP: totp}, nil
}
