package printing

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func failingProvider(called *bool) Provider {
	return func(context.Context, Scope, uuid.UUID, *int) (Doc, error) {
		*called = true
		return Doc{}, apperr.NewNotFound("not_found", "Document not found")
	}
}

func TestPrintRejectsUnknownTypesAndVersions(t *testing.T) {
	var called bool
	r := NewRegistry()
	r.Register(DocPurchaseOrder, failingProvider(&called), false)
	r.Register(DocStatement, failingProvider(&called), true)
	s := NewService(Deps{Registry: r})
	zero, two := 0, 2
	cases := []struct {
		name, docType string
		version       *int
		field, reason string
	}{
		{"unknown type", "invoice_draft", nil, "doc_type", "unknown"},
		{"version on an unversioned type", DocPurchaseOrder, &two, "version", "unsupported"},
		{"version below one", DocStatement, &zero, "version", "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Print(context.Background(), Scope{TenantID: uuid.New()}, tc.docType, uuid.New(), tc.version)
			e, ok := apperr.As(err)
			if !ok || e.Kind != apperr.Validation || e.Fields[tc.field] != tc.reason {
				t.Fatalf("err = %v, want 422 with %s=%s", err, tc.field, tc.reason)
			}
		})
	}
	if called {
		t.Fatal("a provider ran for a rejected request")
	}
}

func TestPrintReturnsProviderErrors(t *testing.T) {
	var called bool
	r := NewRegistry()
	r.Register(DocStatement, failingProvider(&called), true)
	two := 2
	_, err := NewService(Deps{Registry: r}).Print(context.Background(), Scope{TenantID: uuid.New()}, DocStatement, uuid.New(), &two)
	if e, ok := apperr.As(err); !ok || e.Kind != apperr.NotFound || !called {
		t.Fatalf("err = %v called = %v, want the provider's 404", err, called)
	}
}

func TestRegisterRejectsDuplicatesAndGaps(t *testing.T) {
	var called bool
	p := failingProvider(&called)
	r := NewRegistry()
	r.Register(DocKitLabel, p, false)
	r.Register(DocGoodsReceipt, p, false)
	if got := r.DocTypes(); len(got) != 2 || got[0] != DocGoodsReceipt || got[1] != DocKitLabel {
		t.Fatalf("doc types = %v", got)
	}
	for name, register := range map[string]func(){
		"duplicate":   func() { r.Register(DocKitLabel, p, true) },
		"no type":     func() { r.Register("", p, false) },
		"no provider": func() { r.Register(DocStockCount, nil, false) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()
			register()
		})
	}
}

func TestEachLineVisitsGroupsAndReturnedLines(t *testing.T) {
	d := Doc{
		Groups:   []Group{{Lines: []Line{{LineNo: 1}, {LineNo: 2}}}, {Lines: []Line{{LineNo: 3}}}},
		Returned: []Line{{LineNo: 4}},
	}
	var seen []int
	d.eachLine(func(l *Line) { seen = append(seen, l.LineNo); l.Name = "x" })
	if len(seen) != 4 || d.Groups[1].Lines[0].Name != "x" || d.Returned[0].Name != "x" {
		t.Fatalf("seen = %v doc = %+v", seen, d)
	}
}
