package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func newEngine(t *testing.T) *gin.Engine {
	t.Helper()
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func serve(h http.Handler, method, target string, body string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, values := range header {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) ErrorPayload {
	t.Helper()
	var b ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	return b.Error
}

func TestStatusOf(t *testing.T) {
	want := map[apperr.Kind]int{
		apperr.Internal: 500, apperr.Validation: 422, apperr.NotFound: 404, apperr.Conflict: 409,
		apperr.ExceptionRequired: 409, apperr.PreconditionFailed: 412, apperr.PreconditionRequired: 428,
		apperr.PeriodLocked: 423, apperr.TenantMaintenance: 423, apperr.ReadOnlyTenant: 403, apperr.Forbidden: 403, apperr.Unauthenticated: 401,
		apperr.RateLimited: 429, apperr.MisdirectedRequest: 421, apperr.Gone: 410, apperr.Timeout: 504, apperr.Kind(99): 500,
	}
	for kind, status := range want {
		if got := StatusOf(kind); got != status {
			t.Errorf("StatusOf(%v) = %d, want %d", kind, got, status)
		}
	}
}

func TestRenderError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	e := newEngine(t)
	e.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(logging.WithLogger(c.Request.Context(), logger))
	})
	e.GET("/app", func(c *gin.Context) {
		RenderError(c, apperr.NewExceptionRequired("negative_stock", "Stock would go negative"))
	})
	e.GET("/validation", func(c *gin.Context) {
		RenderError(c, apperr.NewValidation("validation_failed", "bad", map[string]string{"name": "required"}))
	})
	e.GET("/internal", func(c *gin.Context) { RenderError(c, errors.New("pq: secret detail")) })

	rec := serve(e, "GET", "/app", "", nil)
	if p := errorBody(t, rec); rec.Code != 409 || p.Code != "negative_stock" || p.ExceptionKind != "negative_stock" {
		t.Errorf("exception: %d %+v", rec.Code, p)
	}
	rec = serve(e, "GET", "/validation", "", nil)
	if p := errorBody(t, rec); rec.Code != 422 || p.Fields["name"] != "required" {
		t.Errorf("validation: %d %+v", rec.Code, p)
	}
	rec = serve(e, "GET", "/internal", "", nil)
	if p := errorBody(t, rec); rec.Code != 500 || p.Code != "internal_error" || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("internal: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "pq: secret detail") || !strings.Contains(logs.String(), `"route":"/internal"`) {
		t.Errorf("internal error not logged: %s", logs.String())
	}
	rec = serve(e, "GET", "/nope", "", nil)
	if p := errorBody(t, rec); rec.Code != 404 || p.Code != "not_found" {
		t.Errorf("no route: %d %+v", rec.Code, p)
	}
	rec = serve(e, "POST", "/app", "", nil)
	if rec.Code != 404 {
		t.Errorf("wrong method: %d, want 404", rec.Code)
	}
}

func TestEngineClientIP(t *testing.T) {
	for _, tc := range []struct {
		trusted []string
		want    string
	}{
		{nil, "192.0.2.10"},
		{[]string{"192.0.2.0/24"}, "203.0.113.7"},
	} {
		e, err := NewEngine(EngineOptions{TrustedProxyCIDRs: tc.trusted})
		if err != nil {
			t.Fatal(err)
		}
		e.GET("/ip", func(c *gin.Context) { c.String(200, c.ClientIP()) })
		req := httptest.NewRequest("GET", "/ip", nil)
		req.RemoteAddr = "192.0.2.10:5555"
		req.Header.Set(ClientIPHeader, "203.0.113.7")
		req.Header.Set("X-Forwarded-For", "198.51.100.1")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Body.String() != tc.want {
			t.Errorf("trusted %v: ClientIP = %q, want %q", tc.trusted, rec.Body.String(), tc.want)
		}
	}
	if _, err := NewEngine(EngineOptions{TrustedProxyCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Error("invalid CIDR accepted")
	}
}

func TestMethodIsSafe(t *testing.T) {
	for m, want := range map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "POST": false, "PUT": false, "PATCH": false, "DELETE": false} {
		if MethodIsSafe(m) != want {
			t.Errorf("MethodIsSafe(%s)", m)
		}
	}
}

type bindTarget struct {
	Name   string      `json:"name" validate:"required,max=10"`
	Amount money.Money `json:"amount"`
	Lines  []struct {
		Qty money.Qty `json:"qty"`
		SKU string    `json:"sku" validate:"required"`
	} `json:"lines" validate:"dive"`
}

func TestDecodeJSON(t *testing.T) {
	var ok bindTarget
	if err := DecodeJSON([]byte(`{"name":"a","amount":"1000.5","lines":[{"qty":"2","sku":"X"}]}`), &ok); err != nil {
		t.Fatal(err)
	}
	if ok.Amount.String() != "1000.5" {
		t.Errorf("amount %s", ok.Amount.String())
	}
	cases := []struct {
		body   string
		code   string
		fields map[string]string
	}{
		{``, "invalid_json", nil},
		{`{"name":`, "invalid_json", nil},
		{`{"name":"a"} {}`, "invalid_json", nil},
		{`{"name":"a","extra":1}`, "validation_failed", map[string]string{"extra": "unknown_field"}},
		{`{"name":"a","amount":1000}`, "validation_failed", map[string]string{"amount": "invalid_decimal"}},
		{`{"name":"a","amount":"1e3"}`, "validation_failed", map[string]string{"amount": "invalid_decimal"}},
		{`{"name":"a","lines":[{"qty":2,"sku":"X"}]}`, "validation_failed", map[string]string{"lines.qty": "invalid_decimal"}},
		{`{"name":5}`, "validation_failed", map[string]string{"name": "invalid_type"}},
		{`[]`, "validation_failed", map[string]string{"$": "invalid_type"}},
		{`{"name":""}`, "validation_failed", map[string]string{"name": "required"}},
		{`{"name":"abcdefghijk"}`, "validation_failed", map[string]string{"name": "max"}},
		{`{"name":"a","lines":[{"qty":"1"}]}`, "validation_failed", map[string]string{"lines[0].sku": "required"}},
	}
	for _, tc := range cases {
		var dst bindTarget
		err := DecodeJSON([]byte(tc.body), &dst)
		e, isApp := apperr.As(err)
		if !isApp || e.Kind != apperr.Validation || e.Code != tc.code || (tc.fields != nil && !reflect.DeepEqual(e.Fields, tc.fields)) {
			t.Errorf("DecodeJSON(%s) = %v (fields %v), want %s %v", tc.body, err, fieldsOf(e), tc.code, tc.fields)
		}
	}
	if err := Validate(42); err != nil {
		t.Errorf("non-struct: %v", err)
	}
}

func fieldsOf(e *apperr.Error) map[string]string {
	if e == nil {
		return nil
	}
	return e.Fields
}

func TestBindJSONBodyLimit(t *testing.T) {
	e := newEngine(t)
	e.POST("/bind", func(c *gin.Context) {
		var dst bindTarget
		if err := BindJSON(c, &dst); err != nil {
			RenderError(c, err)
			return
		}
		c.String(200, dst.Name)
	})
	e.POST("/raw", func(c *gin.Context) {
		body, err := ReadBody(c, 4)
		if err != nil {
			RenderError(c, err)
			return
		}
		c.String(200, string(body))
	})
	if rec := serve(e, "POST", "/bind", `{"name":"ok"}`, nil); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("bind: %d %s", rec.Code, rec.Body.String())
	}
	big := `{"name":"` + strings.Repeat("x", int(DefaultBodyLimit)) + `"}`
	if rec := serve(e, "POST", "/bind", big, nil); errorBody(t, rec).Code != "body_too_large" {
		t.Errorf("too large: %s", rec.Body.String())
	}
	if rec := serve(e, "POST", "/raw", "12345", nil); errorBody(t, rec).Code != "body_too_large" {
		t.Errorf("custom limit: %s", rec.Body.String())
	}
	if rec := serve(e, "POST", "/raw", "1234", nil); rec.Body.String() != "1234" {
		t.Errorf("within limit: %s", rec.Body.String())
	}
}

func TestIdempotencyKey(t *testing.T) {
	e := newEngine(t)
	var got []string
	e.POST("/x", func(c *gin.Context) {
		body, _ := ReadBody(c, 0)
		k, err := IdempotencyKey(c, body)
		if err != nil {
			RenderError(c, err)
			return
		}
		got = append(got, string(k.RequestHash))
		WritePosted(c, 201, []byte(`{"ok":true}`), len(got) > 1)
	})
	h := func(v string) http.Header { return http.Header{IdempotencyKeyHeader: {v}} }
	if rec := serve(e, "POST", "/x", `{}`, nil); errorBody(t, rec).Code != "idempotency_key_required" {
		t.Errorf("missing: %s", rec.Body.String())
	}
	for _, bad := range []string{"has space", strings.Repeat("k", 201), "ünï"} {
		if rec := serve(e, "POST", "/x", `{}`, h(bad)); errorBody(t, rec).Code != "idempotency_key_invalid" {
			t.Errorf("invalid %q: %s", bad, rec.Body.String())
		}
	}
	if rec := serve(e, "POST", "/x", `{`, h("k1")); errorBody(t, rec).Code != "invalid_json" {
		t.Errorf("bad json: %s", rec.Body.String())
	}
	first := serve(e, "POST", "/x", `{"a":1,"b":2}`, h("k1"))
	second := serve(e, "POST", "/x", `{ "b":2, "a":1 }`, h("k1"))
	if first.Code != 201 || first.Header().Get("Idempotent-Replay") != "" || second.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("headers %v / %v", first.Header(), second.Header())
	}
	if first.Header().Get("Content-Type") != "application/json; charset=utf-8" || first.Body.String() != `{"ok":true}` {
		t.Errorf("posted response %v %s", first.Header(), first.Body.String())
	}
	if len(got) != 2 || got[0] != got[1] {
		t.Error("equivalent JSON bodies hash differently")
	}
}

func TestBindJSONKeyed(t *testing.T) {
	e := newEngine(t)
	var keys []*idempotency.Key
	e.POST("/x", func(c *gin.Context) {
		var dst bindTarget
		key, err := BindJSONKeyed(c, &dst)
		if err != nil {
			RenderError(c, err)
			return
		}
		keys = append(keys, key)
		MarkReplay(c, key != nil && len(keys) > 2)
		c.String(200, dst.Name)
	})
	h := func(v string) http.Header { return http.Header{IdempotencyKeyHeader: {v}} }
	if rec := serve(e, "POST", "/x", `{"name":"a"}`, nil); rec.Code != 200 || rec.Body.String() != "a" || keys[0] != nil {
		t.Errorf("without a key: %d %s %v", rec.Code, rec.Body.String(), keys)
	}
	if rec := serve(e, "POST", "/x", `{"name":"a"}`, h("  ")); rec.Code != 200 || keys[1] != nil {
		t.Errorf("blank key: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(e, "POST", "/x", `{"name":"a"}`, h("has space")); errorBody(t, rec).Code != "idempotency_key_invalid" {
		t.Errorf("invalid key: %s", rec.Body.String())
	}
	if rec := serve(e, "POST", "/x", `{"name":"a","x":1}`, h("k1")); errorBody(t, rec).Code != "validation_failed" {
		t.Errorf("strict decode: %s", rec.Body.String())
	}
	rec := serve(e, "POST", "/x", `{"name":"a"}`, h("k1"))
	if rec.Code != 200 || len(keys) != 3 || keys[2] == nil || keys[2].Value != "k1" || !keys[2].Valid() {
		t.Fatalf("with a key: %d %s %v", rec.Code, rec.Body.String(), keys)
	}
	if rec.Header().Get(IdempotentReplayHeader) != "true" {
		t.Errorf("replay header: %v", rec.Header())
	}
	if first := serve(e, "POST", "/x", `{"name":"a"}`, nil); first.Header().Get(IdempotentReplayHeader) != "" {
		t.Errorf("fresh response flagged as replay: %v", first.Header())
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{SortKey: "2026-03-15T03:00:00Z", ID: uuid.MustParse("01920000-0000-7000-8000-000000000001")}
	got, err := DecodeCursor(c.Encode())
	if err != nil || got != c {
		t.Fatalf("round trip %+v, %v", got, err)
	}
	for _, bad := range []string{"!!", "bm90IGpzb24", Cursor{SortKey: "x"}.Encode(), "WyJ4Iiwibm9wZSJd"} {
		if _, err := DecodeCursor(bad); !apperr.Is(err, "invalid_cursor") {
			t.Errorf("DecodeCursor(%q) = %v", bad, err)
		}
	}
}

func TestTimeCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 18, 21, 30, 0, 123456000, time.FixedZone("ICT", 7*3600))
	id := uuid.MustParse("01920000-0000-7000-8000-000000000003")
	cur, err := DecodeCursor(TimeCursor(at, id).Encode())
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := Page{After: &cur}.AfterTime()
	if err != nil || got == nil || !got.Equal(at) || gotID != id {
		t.Fatalf("AfterTime = %v %s %v, want %s %s", got, gotID, err, at, id)
	}
	if got, gotID, err := (Page{}).AfterTime(); got != nil || gotID != uuid.Nil || err != nil {
		t.Errorf("first page AfterTime = %v %s %v", got, gotID, err)
	}
	if _, _, err := (Page{After: &Cursor{SortKey: "x", ID: id}}).AfterTime(); !apperr.Is(err, "invalid_cursor") {
		t.Errorf("AfterTime of a name cursor = %v", err)
	}
}

func TestParsePageAndNewList(t *testing.T) {
	e := newEngine(t)
	e.GET("/list", func(c *gin.Context) {
		p, err := ParsePage(c)
		if err != nil {
			RenderError(c, err)
			return
		}
		after := ""
		if p.After != nil {
			after = p.After.SortKey
		}
		c.JSON(200, gin.H{"limit": p.Limit, "fetch": p.FetchLimit(), "after": after})
	})
	cur := Cursor{SortKey: "b", ID: uuid.MustParse("01920000-0000-7000-8000-000000000002")}.Encode()
	for target, want := range map[string]string{
		"/list":                       `{"after":"","fetch":51,"limit":50}`,
		"/list?limit=200":             `{"after":"","fetch":201,"limit":200}`,
		"/list?limit=2&cursor=" + cur: `{"after":"b","fetch":3,"limit":2}`,
	} {
		if rec := serve(e, "GET", target, "", nil); rec.Body.String() != want {
			t.Errorf("%s = %s", target, rec.Body.String())
		}
	}
	for _, target := range []string{"/list?limit=0", "/list?limit=201", "/list?limit=x", "/list?cursor=zz"} {
		if rec := serve(e, "GET", target, "", nil); rec.Code != 422 {
			t.Errorf("%s = %d", target, rec.Code)
		}
	}

	type row struct {
		ID   uuid.UUID
		Name string
	}
	rows := []row{{uuid.MustParse("01920000-0000-7000-8000-00000000000a"), "a"}, {uuid.MustParse("01920000-0000-7000-8000-00000000000b"), "b"}, {uuid.New(), "c"}}
	cursorOf := func(r row) Cursor { return Cursor{SortKey: r.Name, ID: r.ID} }
	page := NewList(rows, Page{Limit: 2}, cursorOf)
	if len(page.Data) != 2 || page.NextCursor == nil {
		t.Fatalf("page %+v", page)
	}
	if next, _ := DecodeCursor(*page.NextCursor); next.SortKey != "b" || next.ID != rows[1].ID {
		t.Errorf("next cursor %+v", next)
	}
	last := NewList(rows[:2], Page{Limit: 2}, cursorOf)
	empty := NewList[row](nil, Page{Limit: 2}, cursorOf)
	if last.NextCursor != nil || empty.Data == nil {
		t.Errorf("last %+v empty %+v", last, empty)
	}
	names := NewListOf(rows, Page{Limit: 2}, cursorOf, func(r row) string { return r.Name })
	if len(names.Data) != 2 || names.Data[1] != "b" || names.NextCursor == nil || *names.NextCursor != *page.NextCursor {
		t.Errorf("converted page %+v", names)
	}
	if none := NewListOf[row](nil, Page{Limit: 2}, cursorOf, func(r row) string { return r.Name }); none.Data == nil || none.NextCursor != nil {
		t.Errorf("converted empty page %+v", none)
	}
	raw, _ := json.Marshal(empty)
	if string(raw) != `{"data":[],"next_cursor":null}` {
		t.Errorf("empty list JSON %s", raw)
	}
}

func TestETagIfMatch(t *testing.T) {
	e := newEngine(t)
	e.PUT("/r", func(c *gin.Context) {
		v, err := IfMatch(c)
		if err != nil {
			RenderError(c, err)
			return
		}
		SetETag(c, v+1)
		c.Status(204)
	})
	rec := serve(e, "PUT", "/r", "", http.Header{"If-Match": {`"3"`}})
	if rec.Code != 204 || rec.Header().Get("ETag") != `"4"` {
		t.Errorf("match: %d %q", rec.Code, rec.Header().Get("ETag"))
	}
	if rec := serve(e, "PUT", "/r", "", nil); rec.Code != 428 {
		t.Errorf("missing: %d", rec.Code)
	}
	for _, bad := range []string{`3`, `W/"3"`, `"x"`, `"0"`, `""`} {
		if rec := serve(e, "PUT", "/r", "", http.Header{"If-Match": {bad}}); rec.Code != 412 {
			t.Errorf("If-Match %s: %d", bad, rec.Code)
		}
	}
}

func TestOnHosts(t *testing.T) {
	e := newEngine(t)
	setHost := func(kind HostKind) gin.HandlerFunc {
		return func(c *gin.Context) {
			if kind != "" {
				SetHost(c, Host{Kind: kind})
			}
		}
	}
	for _, kind := range []HostKind{HostRoot, HostTenant, HostAdmin, ""} {
		g := e.Group("/"+string(kind)+"x", setHost(kind), OnHosts(HostRoot, HostTenant))
		g.GET("/ok", func(c *gin.Context) { c.Status(204) })
	}
	for path, want := range map[string]int{"/rootx/ok": 204, "/tenantx/ok": 204, "/adminx/ok": 404, "/x/ok": 404} {
		if rec := serve(e, "GET", path, "", nil); rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}
	h := Host{Kind: HostTenant, Slug: "demo"}
	if h.KnownTenant() {
		t.Error("unknown slug reported as known")
	}
	h.TenantID = uuid.New()
	if !h.KnownTenant() || (Host{Kind: HostRoot, TenantID: h.TenantID}).KnownTenant() {
		t.Error("KnownTenant")
	}
}

func TestCommandRouter(t *testing.T) {
	e := newEngine(t)
	var index CommandIndex
	api := e.Group("/api/v1")
	r := NewCommandRouter(api, "invoices", &index)
	r.Item("confirm", func(c *gin.Context) { c.String(200, "confirm "+CommandID(c).String()) })
	r.Item("void", func(c *gin.Context) { c.String(200, "void") })
	r.Collection("bulk-delete", func(c *gin.Context) { c.String(200, "bulk") })
	api.GET("/invoices/:id", func(c *gin.Context) {
		id, err := PathID(c, "id")
		if err != nil {
			RenderError(c, err)
			return
		}
		c.String(200, "get "+id.String())
	})
	id := "01920000-0000-7000-8000-000000000001"
	for target, want := range map[string]string{
		"/api/v1/invoices/" + id + ":confirm": "confirm " + id,
		"/api/v1/invoices/" + id + ":void":    "void",
		"/api/v1/invoices:bulk-delete":        "bulk",
	} {
		if rec := serve(e, "POST", target, "", nil); rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("POST %s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	for _, target := range []string{
		"/api/v1/invoices/" + id + ":refund", "/api/v1/invoices/" + id, "/api/v1/invoices/not-a-uuid:confirm",
		"/api/v1/invoices/" + id + ":", "/api/v1/invoices:unknown",
	} {
		if rec := serve(e, "POST", target, "", nil); rec.Code != 404 || errorBody(t, rec).Code != "not_found" {
			t.Errorf("POST %s = %d", target, rec.Code)
		}
	}
	if rec := serve(e, "GET", "/api/v1/invoices/"+id, "", nil); rec.Body.String() != "get "+id {
		t.Errorf("GET item: %s", rec.Body.String())
	}
	if rec := serve(e, "GET", "/api/v1/invoices/123", "", nil); rec.Code != 404 {
		t.Errorf("GET bad id: %d", rec.Code)
	}

	want := []CommandRoute{
		{Method: "POST", Path: "/api/v1/invoices/{id}:confirm", GinPath: "/api/v1/invoices/:id"},
		{Method: "POST", Path: "/api/v1/invoices/{id}:void", GinPath: "/api/v1/invoices/:id"},
		{Method: "POST", Path: "/api/v1/invoices:bulk-delete", GinPath: "/api/v1/invoices:bulk-delete"},
	}
	if got := index.Routes(); !reflect.DeepEqual(got, want) {
		t.Errorf("index %+v", got)
	}
	var nilIndex *CommandIndex
	nilIndex.add(CommandRoute{})
	if nilIndex.Routes() != nil {
		t.Error("nil index")
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if CommandID(c) != uuid.Nil {
		t.Error("CommandID outside a command")
	}
}

func TestCommandRouterPanics(t *testing.T) {
	e := newEngine(t)
	expectPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		fn()
	}
	noop := func(*gin.Context) {}
	r := NewCommandRouter(e.Group("/v"), "orders", nil)
	r.Item("ship", noop)
	expectPanic("duplicate verb", func() { r.Item("ship", noop) })
	expectPanic("invalid item verb", func() { r.Item("Ship", noop) })
	expectPanic("empty verb", func() { r.Item("", noop) })
	expectPanic("invalid collection verb", func() { r.Collection("a b", noop) })
}

func TestHostOfWithoutHostContext(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil).WithContext(context.Background())
	if _, ok := HostOf(c); ok {
		t.Error("host without HostContext")
	}
	c.Set(hostKey, "wrong type")
	if _, ok := HostOf(c); ok {
		t.Error("wrong type accepted")
	}
}
