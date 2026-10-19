package objectstore

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestPolicyCheck(t *testing.T) {
	p := NewPolicy(1024)
	if ct, err := p.Check("Image/JPEG; charset=binary", 10); err != nil || ct != "image/jpeg" {
		t.Fatalf("jpeg: %q %v", ct, err)
	}
	cases := []struct {
		ct   string
		size int64
		code string
	}{
		{"application/x-msdownload", 10, "unsupported_content_type"},
		{";;;", 10, "unsupported_content_type"},
		{"application/pdf", 0, "invalid_file_size"},
		{"application/pdf", 1025, "file_too_large"},
	}
	for _, tc := range cases {
		_, err := p.Check(tc.ct, tc.size)
		if !apperr.Is(err, tc.code) || apperr.KindOf(err) != apperr.Validation {
			t.Errorf("Check(%q, %d) = %v, want %s", tc.ct, tc.size, err, tc.code)
		}
	}
}

func TestValidKey(t *testing.T) {
	for key, want := range map[string]bool{
		"tenants/0190/receipts/a.pdf": true,
		"a":                           true,
		"":                            false,
		"/abs":                        false,
		"a//b":                        false,
		"a/../b":                      false,
		"a/./b":                       false,
		"dir/":                        false,
		"spaces are bad":              false,
		"ünïcode":                     false,
		strings.Repeat("a", 513):      false,
	} {
		if got := ValidKey(key); got != want {
			t.Errorf("ValidKey(%q) = %v", key, got)
		}
	}
}

func TestTenantKey(t *testing.T) {
	id := uuid.MustParse("01920000-0000-7000-8000-000000000001")
	if got := TenantKey(id, "exports", "x.csv"); got != "tenants/01920000-0000-7000-8000-000000000001/exports/x.csv" {
		t.Fatal(got)
	}
}

func TestPresignWithoutNetwork(t *testing.T) {
	if _, err := NewS3(S3Config{Bucket: "b"}); err == nil {
		t.Error("missing endpoint accepted")
	}
	endpoint, _ := url.Parse("http://127.0.0.1:1")
	if _, err := NewS3(S3Config{Endpoint: endpoint}); err == nil {
		t.Error("missing bucket accepted")
	}
	s, err := NewS3(S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "tally-test", AccessKey: "k", SecretKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req, err := s.PresignPut(ctx, "tenants/x/a.pdf", "application/pdf", 42, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "PUT" || req.Headers.Get("Content-Length") != "42" || !strings.Contains(req.URL, "X-Amz-SignedHeaders=content-length%3Bcontent-type%3Bhost") {
		t.Fatalf("presigned put = %+v", req)
	}
	get, err := s.PresignGet(ctx, "tenants/x/a.pdf", "Báo cáo.pdf", time.Minute)
	if err != nil || !strings.Contains(get, "response-content-disposition=attachment") {
		t.Fatalf("presigned get = %q, %v", get, err)
	}
	for name, fn := range map[string]func() error{
		"ttl too long": func() error { _, err := s.PresignGet(ctx, "a", "", MaxPresignTTL+time.Second); return err },
		"zero ttl":     func() error { _, err := s.PresignPut(ctx, "a", "application/pdf", 1, 0); return err },
		"bad key put":  func() error { _, err := s.PresignPut(ctx, "../a", "application/pdf", 1, time.Minute); return err },
		"bad key get":  func() error { _, err := s.PresignGet(ctx, "../a", "", time.Minute); return err },
		"no size":      func() error { _, err := s.PresignPut(ctx, "a", "application/pdf", 0, time.Minute); return err },
		"put bad key":  func() error { return s.Put(ctx, "a/", strings.NewReader("x"), 1, "text/csv") },
		"stat bad key": func() error { _, err := s.Stat(ctx, ""); return err },
		"del bad key":  func() error { return s.Delete(ctx, "a//b") },
	} {
		if fn() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
