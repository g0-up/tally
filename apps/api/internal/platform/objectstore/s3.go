package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures the S3 adapter.
type S3Config struct {
	Endpoint  *url.URL // http(s)://host[:port]
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// S3 is the Store adapter for S3-compatible services (path-style addressing).
type S3 struct {
	client *minio.Client
	bucket string
}

var _ Store = (*S3)(nil)

// NewS3 builds the adapter; it does not contact the server.
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == nil || cfg.Endpoint.Host == "" {
		return nil, errors.New("object store: endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("object store: bucket is required")
	}
	client, err := minio.New(cfg.Endpoint.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.Endpoint.Scheme == "https",
		Region:       cfg.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("object store: %w", err)
	}
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

// Ping checks that the bucket exists and the credentials work.
func (s *S3) Ping(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("object store: %w", err)
	}
	if !ok {
		return fmt.Errorf("object store: bucket %q does not exist", s.bucket)
	}
	return nil
}

// Put implements Store.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if !ValidKey(key) {
		return fmt.Errorf("object store: invalid key %q", key)
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("object store: put %s: %w", key, err)
	}
	return nil
}

// PresignPut implements Store. Content-Type and Content-Length are signed headers: the upload
// fails unless the browser sends exactly the declared type and size.
func (s *S3) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (PresignedRequest, error) {
	if !ValidKey(key) {
		return PresignedRequest{}, fmt.Errorf("object store: invalid key %q", key)
	}
	if err := checkTTL(ttl); err != nil {
		return PresignedRequest{}, err
	}
	if contentType == "" || size <= 0 {
		return PresignedRequest{}, errors.New("object store: presigned upload needs content type and size")
	}
	headers := http.Header{}
	headers.Set("Content-Type", contentType)
	headers.Set("Content-Length", strconv.FormatInt(size, 10))
	u, err := s.client.PresignHeader(ctx, http.MethodPut, s.bucket, key, ttl, nil, headers)
	if err != nil {
		return PresignedRequest{}, fmt.Errorf("object store: presign put %s: %w", key, err)
	}
	return PresignedRequest{Method: http.MethodPut, URL: u.String(), Headers: headers, ExpiresAt: time.Now().Add(ttl)}, nil
}

// PresignGet implements Store.
func (s *S3) PresignGet(ctx context.Context, key, downloadName string, ttl time.Duration) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("object store: invalid key %q", key)
	}
	if err := checkTTL(ttl); err != nil {
		return "", err
	}
	params := url.Values{}
	if downloadName != "" {
		params.Set("response-content-disposition", contentDisposition(downloadName))
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, params)
	if err != nil {
		return "", fmt.Errorf("object store: presign get %s: %w", key, err)
	}
	return u.String(), nil
}

// Stat implements Store.
func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if !ValidKey(key) {
		return ObjectInfo{}, fmt.Errorf("object store: invalid key %q", key)
	}
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, fmt.Errorf("object store: stat %s: %w", key, err)
	}
	return ObjectInfo{Key: info.Key, Size: info.Size, ContentType: info.ContentType, ETag: info.ETag}, nil
}

// Open implements Store.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !ValidKey(key) {
		return nil, fmt.Errorf("object store: invalid key %q", key)
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err == nil {
		// GetObject is lazy: Stat sends the request and surfaces a missing key.
		_, err = obj.Stat()
	}
	if err != nil {
		if obj != nil {
			_ = obj.Close()
		}
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("object store: open %s: %w", key, err)
	}
	return obj, nil
}

func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == minio.NoSuchKey || resp.StatusCode == http.StatusNotFound
}

// Delete implements Store.
func (s *S3) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return fmt.Errorf("object store: invalid key %q", key)
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("object store: delete %s: %w", key, err)
	}
	return nil
}

// Copy implements Store with a server-side copy.
func (s *S3) Copy(ctx context.Context, srcKey, dstKey string) error {
	if !ValidKey(srcKey) || !ValidKey(dstKey) {
		return fmt.Errorf("object store: invalid copy %q -> %q", srcKey, dstKey)
	}
	_, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: dstKey},
		minio.CopySrcOptions{Bucket: s.bucket, Object: srcKey})
	if err != nil {
		if isNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("object store: copy %s to %s: %w", srcKey, dstKey, err)
	}
	return nil
}
