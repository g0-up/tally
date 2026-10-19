//go:build integration

package objectstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// devStore connects to the MinIO of the dev stack (make db-up) or OBJECT_STORE_* from the
// environment.
func devStore(t *testing.T, bucket string) *objectstore.S3 {
	t.Helper()
	endpoint, err := url.Parse(env("OBJECT_STORE_ENDPOINT", "http://localhost:9000"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint:  endpoint,
		Region:    env("OBJECT_STORE_REGION", "us-east-1"),
		Bucket:    bucket,
		AccessKey: env("OBJECT_STORE_ACCESS_KEY", "tally-dev"),
		SecretKey: env("OBJECT_STORE_SECRET_KEY", "tally-dev-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestS3RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := devStore(t, env("OBJECT_STORE_BUCKET", "tally-dev"))
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("MinIO unreachable (start it with `make db-up`): %v", err)
	}
	key := objectstore.TenantKey(ids.New(), "test", "hello.csv")
	t.Cleanup(func() { _ = s.Delete(context.Background(), key) })

	content := []byte("sku,qty\nA,1.5\n")
	if err := s.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/csv"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Stat(ctx, key)
	if err != nil || info.Size != int64(len(content)) || info.ContentType != "text/csv" {
		t.Fatalf("stat = %+v, %v", info, err)
	}

	rc, err := s.Open(ctx, key)
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	read, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(read, content) {
		t.Fatalf("open read %q, %v", read, err)
	}

	getURL, err := s.PresignGet(ctx, key, "Tồn kho.csv", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(getURL) //nolint:noctx // test download of a presigned URL
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
		t.Fatalf("download %d %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("content-disposition %q", cd)
	}

	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("stat after delete: %v", err)
	}
	if _, err := s.Open(ctx, key); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("open after delete: %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
}

func TestS3PresignedUploadEnforcesSignedHeaders(t *testing.T) {
	ctx := context.Background()
	s := devStore(t, env("OBJECT_STORE_BUCKET", "tally-dev"))
	key := objectstore.TenantKey(ids.New(), "test", "upload.pdf")
	t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
	content := []byte("%PDF-1.4 test")

	req, err := s.PresignPut(ctx, key, "application/pdf", int64(len(content)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	put := func(ct string, body []byte) int {
		r, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", ct)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := put("image/png", content); code != http.StatusForbidden {
		t.Fatalf("wrong content type: %d, want 403", code)
	}
	if code := put("application/pdf", append(content, '!')); code != http.StatusForbidden {
		t.Fatalf("wrong size: %d, want 403", code)
	}
	if code := put("application/pdf", content); code != http.StatusOK {
		t.Fatalf("signed upload: %d", code)
	}
	if info, err := s.Stat(ctx, key); err != nil || info.Size != int64(len(content)) {
		t.Fatalf("uploaded object: %+v, %v", info, err)
	}
}

func TestS3PingMissingBucket(t *testing.T) {
	if err := devStore(t, "tally-no-such-bucket").Ping(context.Background()); err == nil {
		t.Fatal("missing bucket reported healthy")
	}
}
