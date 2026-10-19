package authn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TokenBytes is the entropy of an opaque token (sessions, e-mail links).
const TokenBytes = 32

// NewToken returns a random opaque token: 32 bytes, base64url without padding (43 characters).
func NewToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("authn: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DeriveKey turns a configured secret into a 32-byte key bound to purpose, so one secret never
// serves two algorithms with the same key.
func DeriveKey(secret, purpose string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("tally/" + purpose))
	return m.Sum(nil)
}

// Keyed computes keyed hashes (HMAC-SHA256). Tokens and recovery codes are stored only as keyed
// hashes: a database leak alone yields nothing usable.
type Keyed struct {
	key []byte
}

// NewKeyed returns a keyed hasher; key must be at least 32 bytes.
func NewKeyed(key []byte) (*Keyed, error) {
	if len(key) < 32 {
		return nil, errors.New("authn: keyed hash needs a 32-byte key")
	}
	return &Keyed{key: append([]byte(nil), key...)}, nil
}

// Sum returns HMAC-SHA256(key, value).
func (k *Keyed) Sum(value string) []byte {
	m := hmac.New(sha256.New, k.key)
	m.Write([]byte(value))
	return m.Sum(nil)
}

// SumHex returns Sum as lower-case hex.
func (k *Keyed) SumHex(value string) string { return hex.EncodeToString(k.Sum(value)) }

// ErrInvalidSignedToken reports a signed token that is malformed, forged, of another purpose or
// expired.
var ErrInvalidSignedToken = errors.New("authn: invalid signed token")

// Signer issues short-lived self-contained tokens: base64url(expiry ‖ payload) "." base64url(mac),
// the MAC covering purpose, expiry and payload. They carry no secret, only proof of origin.
type Signer struct {
	keyed *Keyed
}

// NewSigner returns a signer with key (at least 32 bytes).
func NewSigner(key []byte) (*Signer, error) {
	k, err := NewKeyed(key)
	if err != nil {
		return nil, err
	}
	return &Signer{keyed: k}, nil
}

// Sign returns a token for payload valid until expires.
func (s *Signer) Sign(purpose string, payload []byte, expires time.Time) string {
	body := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint64(body, uint64(expires.Unix())) //nolint:gosec // Unix seconds of a future time are positive
	body = append(body, payload...)
	enc := base64.RawURLEncoding.EncodeToString(body)
	return enc + "." + base64.RawURLEncoding.EncodeToString(s.mac(purpose, enc))
}

// Verify returns the payload of a token signed for purpose and not expired at now.
func (s *Signer) Verify(purpose, token string, now time.Time) ([]byte, error) {
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, ErrInvalidSignedToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(purpose, enc)) {
		return nil, ErrInvalidSignedToken
	}
	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(body) < 8 {
		return nil, ErrInvalidSignedToken
	}
	expires := time.Unix(int64(binary.BigEndian.Uint64(body[:8])), 0) //nolint:gosec // written by Sign from a Unix time
	if !now.Before(expires) {
		return nil, ErrInvalidSignedToken
	}
	return body[8:], nil
}

func (s *Signer) mac(purpose, enc string) []byte {
	return s.keyed.Sum(purpose + "\x00" + enc)
}
