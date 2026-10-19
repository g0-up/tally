package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo}, {"info", slog.LevelInfo}, {" DEBUG ", slog.LevelDebug},
		{"warn", slog.LevelWarn}, {"Warning", slog.LevelWarn}, {"error", slog.LevelError},
	}
	for _, tc := range cases {
		got, err := ParseLevel(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Error("ParseLevel(verbose) accepted")
	}
	if _, err := New(&bytes.Buffer{}, "loud"); err == nil {
		t.Error("New with invalid level accepted")
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{"password", "new_password", "Password", "token", "session_token", "sessionToken",
		"csrf_token", "otp", "otp_code", "totp_secret", "client_secret", "Authorization", "cookie",
		"api_key", "apiKey", "OBJECT_STORE_SECRET_KEY", "OBJECT_STORE_ACCESS_KEY", "SESSION_HASH_KEY",
		"recovery_codes", "password_hash", "x-api-key", "privateKey"}
	for _, k := range sensitive {
		if !IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = false", k)
		}
	}
	plain := []string{"notpaid", "idempotency_key", "name", "doc_no", "tokenizer_mode", "request_id", "sku_code", "keyboard"}
	for _, k := range plain {
		if IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = true", k)
		}
	}
}

func TestMaskTail(t *testing.T) {
	cases := map[string]string{"0901234567": "*******567", "an@shop.vn": "*******.vn", "abc": "***", "": "", "Đường": "**ờng"}
	for in, want := range cases {
		if got := MaskTail(in); got != want {
			t.Errorf("MaskTail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoggerRedactsAndMasks(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, "debug")
	if err != nil {
		t.Fatal(err)
	}
	l.Info("login", "password", "hunter2", "session_token", "abc123", "phone", "0901234567",
		"owner_email", "owner@shop.vn", "sku", "KEO-01", slog.Group("req", "authorization", "Bearer x", "path", "/x"))
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	want := map[string]string{"password": Redacted, "session_token": Redacted, "phone": "*******567",
		"owner_email": "**********.vn", "sku": "KEO-01"}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %v, want %q", k, rec[k], v)
		}
	}
	req, _ := rec["req"].(map[string]any)
	if req["authorization"] != Redacted || req["path"] != "/x" {
		t.Errorf("group = %v", req)
	}
	if bytes.Contains(buf.Bytes(), []byte("hunter2")) || bytes.Contains(buf.Bytes(), []byte("0901234")) {
		t.Errorf("secret leaked: %s", buf.String())
	}
}

func TestContextLogger(t *testing.T) {
	if FromContext(context.Background()) != slog.Default() {
		t.Error("empty context should yield the default logger")
	}
	l := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if FromContext(WithLogger(context.Background(), l)) != l {
		t.Error("logger not carried by context")
	}
}
