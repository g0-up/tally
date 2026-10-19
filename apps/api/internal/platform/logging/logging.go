// Package logging builds the structured JSON logger and keeps secrets and personal data out of
// logs: credential-like attributes are replaced, phone numbers and e-mail addresses are masked
// to their last three characters.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"unicode"
)

// Redacted replaces the value of a sensitive attribute.
const Redacted = "[REDACTED]"

// New returns a JSON logger writing to w at the given level ("" means info).
func New(w io.Writer, level string) (*slog.Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl, ReplaceAttr: replaceAttr})), nil
}

// ParseLevel accepts debug, info, warn (warning) or error, case-insensitive; empty means info.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid LOG_LEVEL %q: want debug, info, warn or error", raw)
	}
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		return a
	}
	switch {
	case IsSensitiveKey(a.Key):
		return slog.String(a.Key, Redacted)
	case isPIIKey(a.Key):
		return slog.String(a.Key, MaskTail(a.Value.String()))
	default:
		return a
	}
}

// sensitiveWords are key segments that always mark a secret.
var sensitiveWords = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "secret": true, "token": true, "otp": true,
	"totp": true, "authorization": true, "cookie": true, "credential": true, "credentials": true,
	"apikey": true, "hash": true, "signature": true,
}

// sensitivePairs are two-segment combinations that mark a secret ("access_key", "private_key").
var sensitivePairs = map[string]bool{
	"api_key": true, "access_key": true, "secret_key": true, "private_key": true, "encryption_key": true,
	"hash_key": true, "csrf_key": true, "recovery_code": true, "recovery_codes": true,
}

// IsSensitiveKey reports whether an attribute or JSON key names a credential. Keys are split on
// '_', '-', '.', spaces and camel-case boundaries, so "otp_code", "sessionToken" and
// "OBJECT_STORE_SECRET_KEY" match while "notpaid" or "idempotency_key" do not.
func IsSensitiveKey(key string) bool {
	segs := segments(key)
	for i, s := range segs {
		if sensitiveWords[s] {
			return true
		}
		if i > 0 && sensitivePairs[segs[i-1]+"_"+s] {
			return true
		}
	}
	return false
}

func isPIIKey(key string) bool {
	for _, s := range segments(key) {
		if s == "phone" || s == "email" || s == "mobile" {
			return true
		}
	}
	return false
}

func segments(key string) []string {
	var segs []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(key)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r):
			if i > 0 && (unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))) {
				flush()
			}
			cur.WriteRune(unicode.ToLower(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return segs
}

// MaskTail keeps the last three characters of s and replaces the rest with '*'.
// Values of three characters or fewer are fully masked.
func MaskTail(s string) string {
	r := []rune(s)
	if len(r) <= 3 {
		return strings.Repeat("*", len(r))
	}
	return strings.Repeat("*", len(r)-3) + string(r[len(r)-3:])
}

type ctxKey struct{}

// WithLogger returns a context carrying l (request-scoped fields such as request_id).
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the logger stored by WithLogger, or slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
