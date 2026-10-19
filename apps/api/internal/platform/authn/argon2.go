// Package authn holds the authentication primitives: argon2id password hashing, opaque tokens
// stored as keyed hashes, signed short-lived tokens, TOTP (RFC 6238) with encrypted secrets and
// recovery codes. Every comparison of a secret is constant-time.
package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost parameters.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams are the production parameters: m=64 MiB, t=3, p=2.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// MaxPasswordBytes bounds the hashed input so a huge password cannot be used to burn CPU.
const MaxPasswordBytes = 256

// ErrMalformedHash reports a stored hash that is not a PHC argon2id string.
var ErrMalformedHash = errors.New("authn: malformed argon2id hash")

// Hasher hashes and verifies passwords with argon2id, encoded as PHC strings
// ($argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>).
type Hasher struct {
	params Params
	dummy  string
}

// NewHasher returns a hasher with p. It hashes a random password once so DummyVerify costs the
// same as a real verification.
func NewHasher(p Params) (*Hasher, error) {
	if p.Memory < 8*uint32(p.Threads) || p.Time == 0 || p.Threads == 0 || p.SaltLen < 8 || p.KeyLen < 16 {
		return nil, errors.New("authn: invalid argon2id parameters")
	}
	h := &Hasher{params: p}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("authn: random: %w", err)
	}
	dummy, err := h.Hash(base64.RawStdEncoding.EncodeToString(random))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

// Hash returns the PHC encoding of password with a fresh random salt.
func (h *Hasher) Hash(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", errors.New("authn: password too long")
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("authn: random salt: %w", err)
	}
	p := h.params
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded, using the parameters stored in encoded. An
// empty encoded hash (an account without a password yet) never matches.
func (h *Hasher) Verify(encoded, password string) (bool, error) {
	if encoded == "" {
		h.DummyVerify(password)
		return false, nil
	}
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	if len(password) > MaxPasswordBytes {
		h.DummyVerify("")
		return false, nil
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyVerify spends the time of one verification without a stored hash, so an unknown account
// answers as slowly as a known one.
func (h *Hasher) DummyVerify(password string) {
	if len(password) > MaxPasswordBytes {
		password = ""
	}
	p, salt, want, err := decodeHash(h.dummy)
	if err != nil {
		return
	}
	_ = subtle.ConstantTimeCompare(argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen), want)
}

// Upper bounds accepted from a stored hash, so a corrupted row cannot request absurd work.
const (
	maxMemoryKiB = 1 << 20 // 1 GiB
	maxTime      = 20
	maxKeyLen    = 1024
)

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, ErrMalformedHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return Params{}, nil, nil, ErrMalformedHash
	}
	if p.Memory == 0 || p.Memory > maxMemoryKiB || p.Time == 0 || p.Time > maxTime || p.Threads == 0 {
		return Params{}, nil, nil, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return Params{}, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > maxKeyLen {
		return Params{}, nil, nil, ErrMalformedHash
	}
	p.KeyLen = uint32(len(key)) //nolint:gosec // bounded by maxKeyLen above
	return p, salt, key, nil
}
