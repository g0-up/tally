//go:build integration

package objectstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
)

func TestS3CopyKeepsContentAndType(t *testing.T) {
	ctx := context.Background()
	s := devStore(t, env("OBJECT_STORE_BUCKET", "tally-dev"))
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("MinIO unreachable (start it with `make db-up`): %v", err)
	}
	tenant := ids.New()
	src, dst := objectstore.TenantKey(tenant, "test", "src.pdf"), objectstore.TenantKey(tenant, "test", "dst.pdf")
	t.Cleanup(func() {
		_ = s.Delete(context.Background(), src)
		_ = s.Delete(context.Background(), dst)
	})

	content := []byte("%PDF-1.7 copy")
	if err := s.Put(ctx, src, bytes.NewReader(content), int64(len(content)), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(ctx, src, dst); err != nil {
		t.Fatalf("copy = %v", err)
	}
	// Changing the source afterwards leaves the copy alone.
	if err := s.Put(ctx, src, bytes.NewReader([]byte("changed")), 7, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Stat(ctx, dst)
	if err != nil || info.Size != int64(len(content)) || info.ContentType != "application/pdf" {
		t.Fatalf("stat copy = %+v, %v", info, err)
	}
	rc, err := s.Open(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("copy content = %q, %v", got, err)
	}

	if err := s.Copy(ctx, objectstore.TenantKey(tenant, "test", "missing"), dst); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("copy of a missing object = %v, want ErrNotFound", err)
	}
	if err := s.Copy(ctx, "../escape", dst); err == nil {
		t.Error("copy with an invalid key must fail")
	}
}
