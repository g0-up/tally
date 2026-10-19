package authn

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// cheap keeps the tests fast; production parameters are covered by TestDefaultParams.
var cheap = Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func TestHasherRoundTrip(t *testing.T) {
	h, err := NewHasher(cheap)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := h.Hash("mật khẩu đúng")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("encoding = %q", enc)
	}
	other, _ := h.Hash("mật khẩu đúng")
	if other == enc {
		t.Error("two hashes of one password share a salt")
	}
	if ok, err := h.Verify(enc, "mật khẩu đúng"); !ok || err != nil {
		t.Errorf("verify right password = %v %v", ok, err)
	}
	if ok, err := h.Verify(enc, "mật khẩu sai"); ok || err != nil {
		t.Errorf("verify wrong password = %v %v", ok, err)
	}
	if ok, err := h.Verify("", "anything"); ok || err != nil {
		t.Errorf("verify against no password = %v %v", ok, err)
	}
	if ok, err := h.Verify(enc, strings.Repeat("x", MaxPasswordBytes+1)); ok || err != nil {
		t.Errorf("verify oversized password = %v %v", ok, err)
	}
	if _, err := h.Hash(strings.Repeat("x", MaxPasswordBytes+1)); err == nil {
		t.Error("hash of an oversized password succeeded")
	}
	h.DummyVerify("whatever")
	h.DummyVerify(strings.Repeat("x", MaxPasswordBytes+1))

	// A hash made with other parameters still verifies: parameters come from the encoding.
	h2, err := NewHasher(Params{Memory: 128, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := h2.Verify(enc, "mật khẩu đúng"); !ok {
		t.Error("verify with a hasher of other parameters failed")
	}
}

func TestHasherRejectsMalformed(t *testing.T) {
	h, err := NewHasher(cheap)
	if err != nil {
		t.Fatal(err)
	}
	for _, enc := range []string{
		"plain",
		"$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=18$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=0,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=9999999999,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=99,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$!!$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$short",
	} {
		if ok, err := h.Verify(enc, "x"); ok || err == nil {
			t.Errorf("Verify(%q) = %v %v, want ErrMalformedHash", enc, ok, err)
		}
	}
	if _, err := NewHasher(Params{}); err == nil {
		t.Error("zero parameters accepted")
	}
}

func TestDefaultParams(t *testing.T) {
	if DefaultParams.Memory != 64*1024 || DefaultParams.Time != 3 || DefaultParams.Threads != 2 {
		t.Errorf("DefaultParams = %+v", DefaultParams)
	}
}

func TestNewTokenIsRandomURLSafe(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b || len(a) != 43 || strings.ContainsAny(a, "+/=") {
		t.Errorf("tokens %q %q", a, b)
	}
}

func TestKeyed(t *testing.T) {
	if _, err := NewKeyed([]byte("short")); err == nil {
		t.Error("short key accepted")
	}
	k1, _ := NewKeyed(DeriveKey("secret-one", "session"))
	k2, _ := NewKeyed(DeriveKey("secret-one", "csrf"))
	if bytes.Equal(k1.Sum("tok"), k2.Sum("tok")) {
		t.Error("purposes share a key")
	}
	if !bytes.Equal(k1.Sum("tok"), k1.Sum("tok")) || len(k1.SumHex("tok")) != 64 {
		t.Error("keyed hash not deterministic or wrong size")
	}
}

func TestSigner(t *testing.T) {
	s, err := NewSigner(DeriveKey("k", "challenge"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC)
	tok := s.Sign("mfa", []byte(`{"u":"1"}`), now.Add(5*time.Minute))
	if got, err := s.Verify("mfa", tok, now); err != nil || string(got) != `{"u":"1"}` {
		t.Errorf("verify = %q %v", got, err)
	}
	if _, err := s.Verify("mfa", tok, now.Add(5*time.Minute)); err == nil {
		t.Error("expired token accepted")
	}
	if _, err := s.Verify("other", tok, now); err == nil {
		t.Error("token of another purpose accepted")
	}
	_, sig, _ := strings.Cut(tok, ".")
	tampered := s.Sign("mfa", []byte(`{"u":"2"}`), now.Add(5*time.Minute))
	tamperedEnc, _, _ := strings.Cut(tampered, ".")
	forged := tamperedEnc + "." + sig // another payload under the original MAC
	for _, bad := range []string{forged, "nodot", tok + "x", "AA.AA", "." + strings.Split(tok, ".")[1]} {
		if _, err := s.Verify("mfa", bad, now); err == nil {
			t.Errorf("forged token %q accepted", bad)
		}
	}
	other, _ := NewSigner(DeriveKey("other", "challenge"))
	if _, err := other.Verify("mfa", tok, now); err == nil {
		t.Error("token of another key accepted")
	}
	if _, err := NewSigner(nil); err == nil {
		t.Error("signer without key")
	}
}

// RFC 4226 appendix D.
func TestHOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := hotp(secret, uint64(i), 6); got != w { //nolint:gosec // small loop index
			t.Errorf("hotp(%d) = %s, want %s", i, got, w)
		}
	}
}

// RFC 6238 appendix B, SHA-1, 8 digits.
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := map[int64]string{
		59: "94287082", 1111111109: "07081804", 1111111111: "14050471",
		1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130",
	}
	for ts, w := range cases {
		if got := hotp(secret, uint64(ts/30), 8); got != w { //nolint:gosec // positive test vectors
			t.Errorf("totp(%d) = %s, want %s", ts, got, w)
		}
	}
}

func TestValidateTOTPWindow(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 15, 3, 0, 15, 0, time.UTC)
	code := TOTPCode(secret, now)
	if len(code) != 6 {
		t.Fatalf("code %q", code)
	}
	for _, at := range []time.Time{now, now.Add(-30 * time.Second), now.Add(30 * time.Second)} {
		if !ValidateTOTP(secret, code, at) {
			t.Errorf("code rejected at %s", at)
		}
	}
	for _, at := range []time.Time{now.Add(-61 * time.Second), now.Add(61 * time.Second)} {
		if ValidateTOTP(secret, code, at) {
			t.Errorf("code accepted at %s outside the window", at)
		}
	}
	if ValidateTOTP(secret, "12345", now) || ValidateTOTP(secret, "", now) {
		t.Error("short code accepted")
	}
	if !ValidateTOTP(secret, " "+code+" ", now) {
		t.Error("surrounding spaces not trimmed")
	}
}

func TestMatchTOTPReturnsTheMatchedStep(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 15, 3, 0, 15, 0, time.UTC)
	step := now.Unix() / 30
	for d := int64(-1); d <= 1; d++ {
		code := TOTPCode(secret, now.Add(time.Duration(d)*TOTPPeriod))
		if got, ok := MatchTOTP(secret, code, now); !ok || got != step+d {
			t.Errorf("code of step %+d: MatchTOTP = %d, %v; want %d", d, got, ok, step+d)
		}
	}
	if _, ok := MatchTOTP(secret, TOTPCode(secret, now.Add(2*TOTPPeriod)), now); ok {
		t.Error("code two steps ahead accepted")
	}
	if got, ok := MatchTOTP(secret, "abcdef", now); ok || got >= 0 {
		t.Errorf("non-numeric code = %d, %v", got, ok)
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("Tally", "a@example.test", []byte("12345678901234567890"))
	if !strings.HasPrefix(uri, "otpauth://totp/Tally:a@example.test?") || !strings.Contains(uri, "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ") {
		t.Errorf("uri = %s", uri)
	}
	if EncodeTOTPSecret([]byte("12345678901234567890")) != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Error("base32 encoding")
	}
}

func TestCipher(t *testing.T) {
	if _, err := NewCipher([]byte("short")); err == nil {
		t.Error("short key accepted")
	}
	c, err := NewCipher(DeriveKey("enc", "totp"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal([]byte("secret"), []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := c.Seal([]byte("secret"), []byte("user-1"))
	if bytes.Equal(sealed, again) || bytes.Contains(sealed, []byte("secret")) {
		t.Error("ciphertext repeats or leaks the plain text")
	}
	if plain, err := c.Open(sealed, []byte("user-1")); err != nil || string(plain) != "secret" {
		t.Errorf("open = %q %v", plain, err)
	}
	if _, err := c.Open(sealed, []byte("user-2")); err == nil {
		t.Error("ciphertext opened under another row")
	}
	if _, err := c.Open(sealed[:5], nil); err == nil {
		t.Error("truncated ciphertext opened")
	}
	other, _ := NewCipher(DeriveKey("other", "totp"))
	if _, err := other.Open(sealed, []byte("user-1")); err == nil {
		t.Error("opened with another key")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d codes", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] || strings.ContainsAny(c, "01ilo") {
			t.Errorf("code %q", c)
		}
		seen[c] = true
	}
	if NormalizeRecoveryCode(" ABCDE-fghjk ") != "abcdefghjk" || NormalizeRecoveryCode("abcde fghjk") != "abcdefghjk" {
		t.Error("normalisation")
	}
}
