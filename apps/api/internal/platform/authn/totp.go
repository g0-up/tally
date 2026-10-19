package authn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1; authenticator apps expect it
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP settings shared with authenticator apps (RFC 6238 defaults).
const (
	TOTPDigits     = 6
	TOTPPeriod     = 30 * time.Second
	TOTPSkew       = 1 // accepted steps before and after the current one
	totpSecretSize = 20
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit TOTP secret.
func NewTOTPSecret() ([]byte, error) {
	s := make([]byte, totpSecretSize)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("authn: random totp secret: %w", err)
	}
	return s, nil
}

// EncodeTOTPSecret is the base32 form users type into an authenticator app.
func EncodeTOTPSecret(secret []byte) string { return b32.EncodeToString(secret) }

// TOTPURI is the otpauth:// URI shown as a QR code.
func TOTPURI(issuer, account string, secret []byte) string {
	q := url.Values{}
	q.Set("secret", EncodeTOTPSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(int(TOTPPeriod.Seconds())))
	label := url.PathEscape(issuer + ":" + account)
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPCode returns the 6-digit code of secret at t.
func TOTPCode(secret []byte, t time.Time) string {
	return hotp(secret, uint64(t.Unix())/uint64(TOTPPeriod.Seconds()), TOTPDigits) //nolint:gosec // Unix time after 1970
}

// ValidateTOTP reports whether code is the code of secret at t or within TOTPSkew steps of it.
// It does not stop a replay; sign-in uses MatchTOTP and records the step it accepted.
func ValidateTOTP(secret []byte, code string, t time.Time) bool {
	_, ok := MatchTOTP(secret, code, t)
	return ok
}

// MatchTOTP returns the time step whose code equals code, searching t and TOTPSkew steps on each
// side (the latest step wins when two collide). Every candidate is compared in constant time.
// A caller stores the step and refuses a later code whose step is not greater, so one code
// cannot be used twice.
func MatchTOTP(secret []byte, code string, t time.Time) (step int64, ok bool) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return 0, false
	}
	now := t.Unix() / int64(TOTPPeriod.Seconds())
	step = -1
	for d := int64(-TOTPSkew); d <= TOTPSkew; d++ {
		candidate := now + d
		if candidate < 0 {
			continue
		}
		eq := subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(candidate), TOTPDigits)), []byte(code)) //nolint:gosec // non-negative
		// mask is all ones on a match and zero otherwise.
		mask := -int64(eq)
		step = candidate&mask | step&^mask
	}
	return step, step >= 0
}

// hotp is RFC 4226 HOTP with HMAC-SHA1 and dynamic truncation.
func hotp(secret []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, v%mod)
}

// Cipher encrypts small secrets at rest (TOTP secrets) with AES-256-GCM. The sealed form is
// nonce ‖ ciphertext; additional data binds a ciphertext to its row so it cannot be moved.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher returns a cipher with a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("authn: cipher needs a 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("authn: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("authn: cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts plain, authenticating aad.
func (c *Cipher) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize(), c.aead.NonceSize()+len(plain)+c.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("authn: random nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plain, aad), nil
}

// Open decrypts a value produced by Seal with the same aad.
func (c *Cipher) Open(sealed, aad []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n+c.aead.Overhead() {
		return nil, errors.New("authn: sealed value too short")
	}
	plain, err := c.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, errors.New("authn: sealed value does not authenticate")
	}
	return plain, nil
}
