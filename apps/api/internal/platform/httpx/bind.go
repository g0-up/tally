package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
)

// DefaultBodyLimit caps JSON request bodies (uploads go straight to the object store).
const DefaultBodyLimit int64 = 1 << 20

// IdempotencyKeyHeader carries the client-chosen key of a posting or payment request.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotentReplayHeader marks a response replayed from an earlier request with the same key.
const IdempotentReplayHeader = "Idempotent-Replay"

// maxIdempotencyKeyLen bounds the stored key; offline POS uses a UUID, browsers a UUID too.
const maxIdempotencyKeyLen = 200

var moneyPkg = "github.com/g0-up/tally/apps/api/internal/platform/money"

// ReadBody reads the whole request body, at most limit bytes (DefaultBodyLimit when ≤ 0).
func ReadBody(c *gin.Context, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultBodyLimit
	}
	if c.Request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, apperr.NewValidation("body_too_large", "The request body is too large", nil)
		}
		return nil, apperr.NewValidation("invalid_body", "The request body could not be read", nil).WithCause(err)
	}
	return body, nil
}

// BindJSON reads the body and decodes it with DecodeJSON.
func BindJSON(c *gin.Context, dst any) error {
	body, err := ReadBody(c, 0)
	if err != nil {
		return err
	}
	return DecodeJSON(body, dst)
}

// DecodeJSON decodes body into dst strictly: unknown fields, trailing data and wrong JSON types
// are validation errors (money and quantities must be decimal strings, never numbers), then runs
// the `validate` struct tags. Field paths in errors use the JSON names.
func DecodeJSON(body []byte, dst any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return apperr.NewValidation("invalid_json", "The request body is empty", nil)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return apperr.NewValidation("invalid_json", "The request body must be a single JSON value", nil)
	}
	return Validate(dst)
}

func decodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "$"
		}
		reason := "invalid_type"
		if typeErr.Type != nil && typeErr.Type.PkgPath() == moneyPkg {
			reason = "invalid_decimal"
		}
		return apperr.NewValidation("validation_failed", "Some fields are not valid", map[string]string{field: reason})
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return apperr.NewValidation("validation_failed", "Some fields are not valid",
			map[string]string{strings.Trim(name, `"`): "unknown_field"})
	}
	return apperr.NewValidation("invalid_json", "The request body is not valid JSON", nil)
}

var (
	validateOnce sync.Once
	validate     *validator.Validate
)

func structValidator() *validator.Validate {
	validateOnce.Do(func() {
		validate = validator.New(validator.WithRequiredStructEnabled())
		validate.RegisterTagNameFunc(func(f reflect.StructField) string {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			switch name {
			case "-":
				return ""
			case "":
				return f.Name
			default:
				return name
			}
		})
	})
	return validate
}

// Validate runs `validate` struct tags on v and maps failures to field → tag reasons.
func Validate(v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	err := structValidator().Struct(v)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperr.NewValidation("validation_failed", "Some fields are not valid", nil).WithCause(err)
	}
	fields := make(map[string]string, len(verrs))
	for _, fe := range verrs {
		path := fe.Namespace()
		if _, rest, ok := strings.Cut(path, "."); ok {
			path = rest // drop the root struct name
		}
		fields[path] = fe.Tag()
	}
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

// IdempotencyKey reads the Idempotency-Key header and binds it to the request hash of method,
// path and body. Posting and payment endpoints call it before Coordinator.Post.
func IdempotencyKey(c *gin.Context, body []byte) (idempotency.Key, error) {
	value := strings.TrimSpace(c.GetHeader(IdempotencyKeyHeader))
	if value == "" {
		return idempotency.Key{}, apperr.NewValidation("idempotency_key_required", "The Idempotency-Key header is required",
			map[string]string{IdempotencyKeyHeader: "required"})
	}
	if len(value) > maxIdempotencyKeyLen || !printableASCII(value) {
		return idempotency.Key{}, apperr.NewValidation("idempotency_key_invalid", "The Idempotency-Key header is not valid",
			map[string]string{IdempotencyKeyHeader: "invalid"})
	}
	hash, err := idempotency.RequestHash(c.Request.Method, c.Request.URL.Path, body)
	if err != nil {
		return idempotency.Key{}, apperr.NewValidation("invalid_json", "The request body is not valid JSON", nil)
	}
	return idempotency.Key{Value: value, RequestHash: hash}, nil
}

// OptionalIdempotencyKey is IdempotencyKey for create requests that are safe to retry only when
// the client asks for it: without the header it returns nil, and the request runs as before.
func OptionalIdempotencyKey(c *gin.Context, body []byte) (*idempotency.Key, error) {
	if strings.TrimSpace(c.GetHeader(IdempotencyKeyHeader)) == "" {
		return nil, nil
	}
	key, err := IdempotencyKey(c, body)
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// BindJSONKeyed reads the body, binds the optional Idempotency-Key to it (OptionalIdempotencyKey)
// and decodes it into dst with DecodeJSON.
func BindJSONKeyed(c *gin.Context, dst any) (*idempotency.Key, error) {
	body, err := ReadBody(c, 0)
	if err != nil {
		return nil, err
	}
	key, err := OptionalIdempotencyKey(c, body)
	if err != nil {
		return nil, err
	}
	if err := DecodeJSON(body, dst); err != nil {
		return nil, err
	}
	return key, nil
}

// OptionalCommandKey is OptionalIdempotencyKey for a custom method without a request body: whatever body
// the client sent is read only to bind the key to it.
func OptionalCommandKey(c *gin.Context) (*idempotency.Key, error) {
	body, err := ReadBody(c, 0)
	if err != nil {
		return nil, err
	}
	return OptionalIdempotencyKey(c, body)
}

// MarkReplay flags a response replayed from a stored idempotent request (Idempotent-Replay: true).
func MarkReplay(c *gin.Context, replayed bool) {
	if replayed {
		c.Header(IdempotentReplayHeader, "true")
	}
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// WritePosted writes a posting response: the stored body for a replay (with Idempotent-Replay:
// true) or the fresh one.
func WritePosted(c *gin.Context, status int, body []byte, replayed bool) {
	MarkReplay(c, replayed)
	c.Data(status, "application/json; charset=utf-8", body)
}
