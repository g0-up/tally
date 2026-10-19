package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Request describes one in-process HTTP call. URL is absolute so the Host header is part of the
// test ("http://demo.tally.test/api/v1/...").
type Request struct {
	Method  string
	URL     string
	Body    any // []byte or string are sent verbatim, anything else as JSON
	Header  http.Header
	Cookies []*http.Cookie
}

// Do serves r through h and returns the recorded response.
func Do(tb testing.TB, h http.Handler, r Request) *httptest.ResponseRecorder {
	tb.Helper()
	var body io.Reader
	switch b := r.Body.(type) {
	case nil:
	case []byte:
		body = bytes.NewReader(b)
	case string:
		body = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			tb.Fatalf("testkit: marshal request body: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, r.URL, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for _, c := range r.Cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// DecodeJSON decodes the recorded body into T and fails the test on error.
func DecodeJSON[T any](tb testing.TB, rec *httptest.ResponseRecorder) T {
	tb.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		tb.Fatalf("testkit: decode response %d %q: %v", rec.Code, rec.Body.String(), err)
	}
	return v
}
