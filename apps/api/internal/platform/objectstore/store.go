// Package objectstore keeps uploaded files (attachments, exports) in a private S3-compatible
// bucket (MinIO in dev, R2/MinIO in production). Browsers upload and download through short-lived
// presigned URLs whose content type and size are signed, so the API never streams large files.
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// MaxPresignTTL bounds every presigned URL.
const MaxPresignTTL = 10 * time.Minute

// ErrNotFound reports a missing object.
var ErrNotFound = errors.New("object not found")

// DefaultContentTypes is the upload allow list: photos, PDFs and spreadsheets.
var DefaultContentTypes = []string{
	"image/jpeg",
	"image/png",
	"image/webp",
	"application/pdf",
	"text/csv",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// Store is the port domains use.
type Store interface {
	// Put uploads r (exactly size bytes) under key.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// PresignPut returns a URL the browser PUTs the file to, with the given headers.
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (PresignedRequest, error)
	// PresignGet returns a download URL; downloadName (optional) sets Content-Disposition.
	PresignGet(ctx context.Context, key, downloadName string, ttl time.Duration) (string, error)
	// Stat returns object metadata or ErrNotFound.
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	// Open streams the object content, or returns ErrNotFound. The caller closes it.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object; deleting a missing object is not an error.
	Delete(ctx context.Context, key string) error
	// Copy copies the object at srcKey (content and content type) to dstKey inside the store,
	// replacing dstKey; a missing source is ErrNotFound.
	Copy(ctx context.Context, srcKey, dstKey string) error
}

// PresignedRequest is what the client needs to perform a direct upload.
type PresignedRequest struct {
	Method    string
	URL       string
	Headers   http.Header // must be sent verbatim (signed)
	ExpiresAt time.Time
}

// ObjectInfo is object metadata.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	ETag        string
}

// Policy limits uploads.
type Policy struct {
	MaxBytes     int64
	ContentTypes []string
}

// NewPolicy returns a policy with the default allow list.
func NewPolicy(maxBytes int64) Policy {
	return Policy{MaxBytes: maxBytes, ContentTypes: DefaultContentTypes}
}

// Check validates a declared upload; the returned content type is normalised (lower case, no
// parameters).
func (p Policy) Check(contentType string, size int64) (string, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", apperr.NewValidation("unsupported_content_type", "This file type is not allowed",
			map[string]string{"content_type": "unsupported"})
	}
	allowed := false
	for _, t := range p.ContentTypes {
		if t == mediaType {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", apperr.NewValidation("unsupported_content_type", "This file type is not allowed",
			map[string]string{"content_type": "unsupported"})
	}
	if size <= 0 {
		return "", apperr.NewValidation("invalid_file_size", "The file is empty", map[string]string{"size_bytes": "too_small"})
	}
	if size > p.MaxBytes {
		return "", apperr.NewValidation("file_too_large", fmt.Sprintf("The file is larger than %d bytes", p.MaxBytes),
			map[string]string{"size_bytes": "too_large"})
	}
	return mediaType, nil
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/._-]{0,511}$`)

// ValidKey reports a safe object key: ASCII letters, digits, '/', '.', '_' and '-', no empty or
// dot-only segments.
func ValidKey(key string) bool {
	if !keyPattern.MatchString(key) || strings.HasSuffix(key, "/") {
		return false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// TenantKey builds "tenants/<tenant_id>/<parts…>", the only prefix tenant files live under.
func TenantKey(tenantID uuid.UUID, parts ...string) string {
	return "tenants/" + tenantID.String() + "/" + strings.Join(parts, "/")
}

func checkTTL(ttl time.Duration) error {
	if ttl <= 0 || ttl > MaxPresignTTL {
		return fmt.Errorf("presign ttl %s outside (0, %s]", ttl, MaxPresignTTL)
	}
	return nil
}

// contentDisposition builds an attachment disposition with an RFC 5987 encoded file name.
func contentDisposition(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
