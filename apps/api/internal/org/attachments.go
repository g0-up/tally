package org

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
)

// Attachment upload limits.
const (
	AttachmentMaxBytes = 10 << 20
	// AttachmentUploadTTL bounds the presigned upload URL.
	AttachmentUploadTTL = objectstore.MaxPresignTTL
	// AttachmentDownloadTTL bounds the signed download URL.
	AttachmentDownloadTTL = 5 * time.Minute
	maxFileNameLen        = 255
)

// AttachmentContentTypes are the accepted file types: photos and PDFs.
var AttachmentContentTypes = []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}

// AttachmentOwner reports whether the record a file is attached to exists. It runs in the
// tenant transaction of the upload, so a record of another tenant does not exist.
type AttachmentOwner func(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error)

// AttachmentOwners are the owner types files may be attached to, each with the check of its
// owner. The composition root registers the owners of every domain; an unregistered type is
// refused.
type AttachmentOwners map[string]AttachmentOwner

// NewAttachment declares a file the client is about to upload.
type NewAttachment struct {
	OwnerType   string
	OwnerID     uuid.UUID
	FileName    string
	ContentType string
	SizeBytes   int64
	// SHA256 is the digest the client computed; CompleteAttachment recomputes it from the
	// stored object and rejects the file when they differ.
	SHA256 []byte
}

// Upload is a created attachment and the presigned request that uploads its content.
type Upload struct {
	Attachment Attachment
	Request    objectstore.PresignedRequest
}

func (s *Service) requireObjects() error {
	if s.objects == nil {
		return errors.New("org: object store is not configured")
	}
	return nil
}

func (in *NewAttachment) validate(owners AttachmentOwners) error {
	fields := map[string]string{}
	in.OwnerType = strings.TrimSpace(in.OwnerType)
	in.FileName = strings.TrimSpace(in.FileName)
	if owners[in.OwnerType] == nil {
		fields["owner_type"] = "invalid"
	}
	if in.OwnerID == uuid.Nil {
		fields["owner_id"] = "required"
	}
	if !validFileName(in.FileName) {
		fields["file_name"] = "invalid"
	}
	if len(in.SHA256) != 32 {
		fields["sha256"] = "invalid"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

func validFileName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > maxFileNameLen || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// uploadKey is where the client uploads an attachment's content. The presigned PUT stays valid
// after completion, so it never targets the attachment's storage key: CompleteAttachment copies
// the upload there and checks the copy, which no client can overwrite.
func uploadKey(tenantID, id uuid.UUID) string {
	return objectstore.TenantKey(tenantID, "attachments", "uploads", id.String())
}

// withinStorageLimit refuses an upload that would take the tenant's stored files past the plan's
// storage limit (409 plan_limit_reached).
func (s *Service) withinStorageLimit(ctx context.Context, q db.Querier, tenantID uuid.UUID, size int64) error {
	if s.limits == nil {
		return nil
	}
	var used int64
	if err := q.QueryRowxContext(ctx,
		`SELECT COALESCE(sum(size_bytes), 0) FROM attachments WHERE tenant_id = $1`, tenantID).Scan(&used); err != nil {
		return fmt.Errorf("org: sum attachment storage: %w", err)
	}
	const mib = 1 << 20
	return s.limits.Within(ctx, q, tenantID, LimitStorageMB, (used+size+mib-1)/mib)
}

// CreateAttachment records a pending attachment and returns a presigned upload for it. The file
// only becomes downloadable after CompleteAttachment confirmed it in the object store. Its owner
// must be of a registered type (422 owner_type invalid) and exist in the tenant (422 owner_id
// not_found).
func (s *Service) CreateAttachment(ctx context.Context, actor audit.Actor, in NewAttachment) (Upload, error) {
	if err := s.requireObjects(); err != nil {
		return Upload{}, err
	}
	if err := in.validate(s.owners); err != nil {
		return Upload{}, err
	}
	contentType, err := objectstore.Policy{MaxBytes: AttachmentMaxBytes, ContentTypes: AttachmentContentTypes}.
		Check(in.ContentType, in.SizeBytes)
	if err != nil {
		return Upload{}, err
	}
	id := ids.New()
	a := Attachment{
		ID: id, TenantID: actor.TenantID, OwnerType: in.OwnerType, OwnerID: in.OwnerID,
		StorageKey: objectstore.TenantKey(actor.TenantID, "attachments", id.String()), FileName: in.FileName,
		ContentType: contentType, SizeBytes: in.SizeBytes, SHA256: in.SHA256, ScanStatus: ScanPending, VersionNo: 1,
		UploadedBy: actor.ID, CreatedAt: s.clock.Now(),
	}
	var req objectstore.PresignedRequest
	err = s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		exists, err := s.owners[a.OwnerType](ctx, q, a.OwnerID)
		if err != nil {
			return fmt.Errorf("org: read attachment owner: %w", err)
		}
		if !exists {
			return errInvalid(map[string]string{"owner_id": "not_found"})
		}
		if err := s.withinStorageLimit(ctx, q, a.TenantID, a.SizeBytes); err != nil {
			return err
		}
		// Only an upload for an existing owner is signed.
		if req, err = s.objects.PresignPut(ctx, uploadKey(a.TenantID, a.ID), a.ContentType, a.SizeBytes, AttachmentUploadTTL); err != nil {
			return fmt.Errorf("org: presign upload: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attachments (id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type, size_bytes,
			  sha256, scan_status, version_no, uploaded_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			a.ID, a.TenantID, a.OwnerType, a.OwnerID, a.StorageKey, a.FileName, a.ContentType, a.SizeBytes, a.SHA256,
			a.ScanStatus, a.VersionNo, a.UploadedBy, a.CreatedAt); err != nil {
			return fmt.Errorf("org: insert attachment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Upload{}, err
	}
	return Upload{Attachment: a, Request: req}, nil
}

func readAttachment(ctx context.Context, q db.Querier, tenantID, id uuid.UUID, forUpdate bool) (Attachment, error) {
	query := `SELECT ` + attachmentColumns + ` FROM attachments WHERE tenant_id = $1 AND id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var a Attachment
	err := q.QueryRowxContext(ctx, query, tenantID, id).StructScan(&a)
	if db.IsNoRows(err) {
		return Attachment{}, errNotFound()
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("org: read attachment: %w", err)
	}
	return a, nil
}

// CompleteAttachment checks the uploaded object against the declaration: a missing upload is
// 422 upload_missing (the client may upload and retry); a size, type or SHA-256 mismatch rejects
// the attachment for good and deletes its objects. The upload is first copied to the storage
// key and the checks run on that copy, so the verified content cannot change afterwards even
// though the presigned upload URL is still valid. The digest is computed by reading the object
// (at most AttachmentMaxBytes), never taken from the client or the ETag, whose value is not a
// content hash for multipart uploads. Completing a clean attachment again is a no-op.
//
// The attachment row stays locked while the object is copied and checked: a concurrent
// completion waits and then sees the outcome instead of copying a re-uploaded file over content
// that was already verified.
func (s *Service) CompleteAttachment(ctx context.Context, actor audit.Actor, id uuid.UUID) (Attachment, error) {
	if err := s.requireObjects(); err != nil {
		return Attachment{}, err
	}
	var (
		a       Attachment
		changed bool
	)
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		var err error
		if a, err = readAttachment(ctx, q, actor.TenantID, id, true); err != nil || a.ScanStatus != ScanPending {
			return err
		}
		status, info, err := s.checkUpload(ctx, a)
		if err != nil {
			return err
		}
		action := "attachment.upload"
		if status == ScanRejected {
			action = "attachment.reject"
		}
		if _, err := q.ExecContext(ctx, `UPDATE attachments SET scan_status = $3 WHERE tenant_id = $1 AND id = $2`,
			actor.TenantID, id, status); err != nil {
			return fmt.Errorf("org: update attachment: %w", err)
		}
		if err := s.audit.Record(ctx, q, actor, audit.Event{
			Action: action, EntityType: "attachment", EntityID: id,
			After: map[string]any{
				"owner_type": a.OwnerType, "owner_id": a.OwnerID, "file_name": a.FileName,
				"content_type": a.ContentType, "size_bytes": a.SizeBytes, "stored_size_bytes": info.Size,
				"stored_content_type": info.ContentType,
			},
		}); err != nil {
			return err
		}
		a.ScanStatus, changed = status, true
		return nil
	})
	if err != nil {
		return Attachment{}, err
	}
	switch {
	case a.ScanStatus == ScanRejected && changed:
		for _, key := range []string{a.StorageKey, uploadKey(a.TenantID, a.ID)} {
			if err := s.objects.Delete(ctx, key); err != nil {
				return Attachment{}, fmt.Errorf("org: delete rejected attachment: %w", err)
			}
		}
		return Attachment{}, errUploadRejected()
	case a.ScanStatus == ScanRejected:
		return Attachment{}, errUploadRejected()
	case changed:
		// Only the verified copy is served, so the upload is no longer needed. A failed delete
		// merely leaves an unreferenced object under uploads/ and must not fail a completion
		// that is already committed.
		_ = s.objects.Delete(ctx, uploadKey(a.TenantID, a.ID))
	}
	return a, nil
}

// checkUpload copies a pending attachment's upload to its storage key and checks the copy
// against the declaration. It returns the scan outcome and the stored object's metadata; a
// missing upload is upload_missing.
func (s *Service) checkUpload(ctx context.Context, a Attachment) (string, objectstore.ObjectInfo, error) {
	upload := uploadKey(a.TenantID, a.ID)
	info, err := s.objects.Stat(ctx, upload)
	if errors.Is(err, objectstore.ErrNotFound) {
		return "", objectstore.ObjectInfo{}, errUploadMissing()
	}
	if err != nil {
		return "", objectstore.ObjectInfo{}, fmt.Errorf("org: stat attachment upload: %w", err)
	}
	if !a.declares(info) {
		return ScanRejected, info, nil
	}
	if err := s.objects.Copy(ctx, upload, a.StorageKey); errors.Is(err, objectstore.ErrNotFound) {
		return "", objectstore.ObjectInfo{}, errUploadMissing()
	} else if err != nil {
		return "", objectstore.ObjectInfo{}, fmt.Errorf("org: store attachment: %w", err)
	}
	// The upload may have been replaced between Stat and Copy, so the copy is checked again.
	if info, err = s.objects.Stat(ctx, a.StorageKey); err != nil {
		return "", objectstore.ObjectInfo{}, fmt.Errorf("org: stat attachment: %w", err)
	}
	if !a.declares(info) {
		return ScanRejected, info, nil
	}
	ok, err := s.objectDigestIs(ctx, a.StorageKey, a.SizeBytes, a.SHA256)
	if err != nil {
		return "", objectstore.ObjectInfo{}, err
	}
	if !ok {
		return ScanRejected, info, nil
	}
	return ScanClean, info, nil
}

// declares reports whether an object has the attachment's declared size and media type.
func (a Attachment) declares(info objectstore.ObjectInfo) bool {
	return info.Size == a.SizeBytes && mediaType(info.ContentType) == a.ContentType
}

// objectDigestIs reads the object and reports whether it is exactly size bytes with the given
// SHA-256. A missing object (deleted since Stat) is upload_missing.
func (s *Service) objectDigestIs(ctx context.Context, key string, size int64, want []byte) (bool, error) {
	rc, err := s.objects.Open(ctx, key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return false, errUploadMissing()
	}
	if err != nil {
		return false, fmt.Errorf("org: read attachment: %w", err)
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(rc, size+1))
	if err != nil {
		return false, fmt.Errorf("org: read attachment: %w", err)
	}
	return n == size && subtle.ConstantTimeCompare(h.Sum(nil), want) == 1, nil
}

func errUploadMissing() error {
	return apperr.NewValidation("upload_missing", "The file has not been uploaded yet", nil)
}

func errUploadRejected() error {
	return apperr.NewValidation("upload_rejected", "The uploaded file does not match its declared size, type or checksum", nil)
}

func mediaType(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return mt
}

// AttachmentURL is a short-lived signed download URL.
type AttachmentURL struct {
	URL       string
	ExpiresAt time.Time
}

// AttachmentURL signs a download URL for a clean attachment of the tenant; another tenant's id
// answers 404 and a file that is not confirmed yet 409 attachment_not_ready.
func (s *Service) AttachmentURL(ctx context.Context, scope Scope, id uuid.UUID) (AttachmentURL, error) {
	if err := s.requireObjects(); err != nil {
		return AttachmentURL{}, err
	}
	var a Attachment
	if err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		a, err = readAttachment(ctx, q, scope.TenantID, id, false)
		return err
	}); err != nil {
		return AttachmentURL{}, err
	}
	if a.ScanStatus != ScanClean {
		return AttachmentURL{}, apperr.NewConflict("attachment_not_ready", "The file is not available")
	}
	expires := s.clock.Now().Add(AttachmentDownloadTTL)
	u, err := s.objects.PresignGet(ctx, a.StorageKey, a.FileName, AttachmentDownloadTTL)
	if err != nil {
		return AttachmentURL{}, fmt.Errorf("org: presign download: %w", err)
	}
	return AttachmentURL{URL: u, ExpiresAt: expires}, nil
}
