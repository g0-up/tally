package app

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.yaml.in/yaml/v3"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/support"
)

// Spec files, relative to this package directory.
var (
	tenantSpecPath = filepath.Join("..", "..", "api", "openapi.yaml")
	adminSpecPath  = filepath.Join("..", "..", "api", "openapi-admin.yaml")
)

// Routes outside the documented API: container probes.
var undocumentedRoutes = map[string]bool{"GET /healthz": true, "GET /readyz": true}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// TestOpenAPIContract checks that every route the server registers is documented in the OpenAPI
// spec of its prefix and that every documented operation is served.
func TestOpenAPIContract(t *testing.T) {
	a := newContractApp(t)
	served, err := servedOperations(a.Handler().Routes(), a.Commands().Routes())
	if err != nil {
		t.Fatal(err)
	}

	byPrefix := map[string]map[string]bool{APIPrefix: {}, AdminAPIPrefix: {}}
	for op := range served {
		if undocumentedRoutes[op] {
			continue
		}
		prefix, rel, ok := splitPrefix(op)
		if !ok {
			t.Errorf("route %s is outside %s and %s", op, APIPrefix, AdminAPIPrefix)
			continue
		}
		byPrefix[prefix][rel] = true
	}

	for _, spec := range []struct{ prefix, path string }{{APIPrefix, tenantSpecPath}, {AdminAPIPrefix, adminSpecPath}} {
		raw, err := os.ReadFile(spec.path)
		if errors.Is(err, fs.ErrNotExist) && spec.prefix == AdminAPIPrefix {
			if len(byPrefix[spec.prefix]) > 0 {
				t.Errorf("%d admin routes are registered but %s does not exist: %v", len(byPrefix[spec.prefix]), spec.path, sortedKeys(byPrefix[spec.prefix]))
			}
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", spec.path, err)
		}
		documented, err := specOperations(raw, spec.prefix)
		if err != nil {
			t.Fatalf("%s: %v", spec.path, err)
		}
		missing, unserved := diffOperations(byPrefix[spec.prefix], documented)
		for _, op := range missing {
			t.Errorf("route %s (prefix %s) is not documented in %s", op, spec.prefix, spec.path)
		}
		for _, op := range unserved {
			t.Errorf("operation %s in %s is not served", op, spec.path)
		}
	}
}

// TestOpenAPIErrorResponses checks that the error statuses the platform middleware can answer are
// listed on the operations that reach it: 403 on routes behind re-authentication, 412 plus 428
// on routes that take If-Match, and 403 subscription_read_only on every posting (the routes that
// take Idempotency-Key), which the posting coordinator refuses for a read-only store. A create that
// takes an optional key lists the key's 409 and 422 codes.
func TestOpenAPIErrorResponses(t *testing.T) {
	raw, err := os.ReadFile(tenantSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Summary    string `yaml:"summary"`
			Parameters []struct {
				Ref string `yaml:"$ref"`
			} `yaml:"parameters"`
			Responses map[string]any `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	guarded, ifMatch, postings, keyed := 0, 0, 0, 0
	for path, item := range doc.Paths {
		for method, op := range item {
			if !slices.Contains(httpMethods, method) {
				continue
			}
			name := strings.ToUpper(method) + " " + path
			if strings.Contains(op.Summary, "re-authentication") {
				guarded++
				if _, ok := op.Responses["403"]; !ok {
					t.Errorf("%s needs re-authentication but does not list 403", name)
				}
			}
			for _, p := range op.Parameters {
				if p.Ref == "#/components/parameters/IdempotencyKey" {
					postings++
					forbidden, _ := op.Responses["403"].(map[string]any)
					if desc, _ := forbidden["description"].(string); !strings.Contains(desc, "subscription_read_only") {
						t.Errorf("%s posts but its 403 does not list subscription_read_only", name)
					}
				}
				if p.Ref == "#/components/parameters/IdempotencyKeyOptional" {
					keyed++
					for status, code := range map[string]string{"409": "idempotency_key_reused", "422": "idempotency_key_invalid"} {
						resp, _ := op.Responses[status].(map[string]any)
						if desc, _ := resp["description"].(string); !strings.Contains(desc, code) {
							t.Errorf("%s takes an optional Idempotency-Key but its %s does not list %s", name, status, code)
						}
					}
				}
				if p.Ref != "#/components/parameters/IfMatch" {
					continue
				}
				ifMatch++
				for _, status := range []string{"412", "428"} {
					if _, ok := op.Responses[status]; !ok {
						t.Errorf("%s takes If-Match but does not list %s", name, status)
					}
				}
			}
		}
	}
	if guarded == 0 || ifMatch == 0 || postings == 0 || keyed == 0 {
		t.Errorf("found %d re-authentication, %d If-Match, %d posting and %d keyed create operations, want all > 0",
			guarded, ifMatch, postings, keyed)
	}
}

// TestOpenAPISharedSchemas pins the shared components every domain spec refers to.
func TestOpenAPISharedSchemas(t *testing.T) {
	raw, err := os.ReadFile(tenantSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI    string `yaml:"openapi"`
		Components struct {
			SecuritySchemes map[string]struct {
				In   string `yaml:"in"`
				Name string `yaml:"name"`
			} `yaml:"securitySchemes"`
			Schemas map[string]struct {
				Pattern string   `yaml:"pattern"`
				Enum    []string `yaml:"enum"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi version = %q, want 3.1.x", doc.OpenAPI)
	}
	for _, name := range []string{"Error", "ErrorResponse", "Money", "Qty", "Cursor", "DocNo", "ColorCode"} {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("schema %s missing", name)
		}
	}
	if s := doc.Components.SecuritySchemes["sessionCookie"]; s.In != "cookie" || s.Name != "__Host-tally_session" {
		t.Errorf("sessionCookie scheme = %+v", s)
	}
	if s := doc.Components.SecuritySchemes["csrfHeader"]; s.In != "header" || s.Name != "X-CSRF-Token" {
		t.Errorf("csrfHeader scheme = %+v", s)
	}
	if got := doc.Components.Schemas["ColorCode"].Enum; !slices.Equal(got, []string{"xanh", "do", "tim", "vang", "nau", "den", "trang"}) {
		t.Errorf("ColorCode enum = %v", got)
	}
	if doc.Components.Schemas["DocNo"].Pattern == "" || doc.Components.Schemas["Money"].Pattern == "" {
		t.Error("DocNo and Money must carry a pattern")
	}
}

// TestOpenAPIAttachmentOwnerTypes checks that POST /attachments documents exactly the owner types
// the domains register, so a generated client accepts every one of them.
func TestOpenAPIAttachmentOwnerTypes(t *testing.T) {
	raw, err := os.ReadFile(tenantSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Properties map[string]struct {
							Enum []string `yaml:"enum"`
						} `yaml:"properties"`
					} `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"requestBody"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	documented := slices.Sorted(slices.Values(
		spec.Paths["/attachments"]["post"].RequestBody.Content["application/json"].Schema.Properties["owner_type"].Enum))
	registered := slices.Sorted(maps.Keys(Domains{}.AttachmentOwners()))
	if !slices.Equal(documented, registered) {
		t.Errorf("POST /attachments owner_type enum = %v, want the registered owner types %v", documented, registered)
	}
}

// TestOpenAPISkuUnitLockReasons checks that SkuUnit.locked_by documents exactly the reasons the
// catalog gives, in the order they are checked, plus null.
func TestOpenAPISkuUnitLockReasons(t *testing.T) {
	raw, err := os.ReadFile(tenantSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []*string `yaml:"enum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, v := range spec.Components.Schemas["SkuUnit"].Properties["locked_by"].Enum {
		if v == nil {
			documented = append(documented, "null")
			continue
		}
		documented = append(documented, *v)
	}
	want := []string{catalog.UnitLockedByDocuments, catalog.UnitLockedByPriceHistory, catalog.UnitLockedByBarcodes, "null"}
	if !slices.Equal(documented, want) {
		t.Errorf("SkuUnit.locked_by enum = %v, want %v", documented, want)
	}
}

func TestNormalizeGinPath(t *testing.T) {
	cases := map[string]string{
		"/api/v1/invoices":                   "/api/v1/invoices",
		"/api/v1/invoices/:id":               "/api/v1/invoices/{id}",
		"/api/v1/invoices/:id/lines/:lineId": "/api/v1/invoices/{id}/lines/{lineId}",
		`/api/v1/invoices\:search`:           "/api/v1/invoices:search",
		"/files/*path":                       "/files/{path}",
		"/api/v1/user_:name":                 "/api/v1/user_{name}",
	}
	for in, want := range cases {
		if got := normalizeGinPath(in); got != want {
			t.Errorf("normalizeGinPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServedOperationsExpandsCommands(t *testing.T) {
	routes := gin.RoutesInfo{
		{Method: "GET", Path: "/api/v1/invoices/:id"},
		{Method: "POST", Path: "/api/v1/invoices/:id"},
		{Method: "POST", Path: `/api/v1/invoices\:search`},
		{Method: "GET", Path: "/healthz"},
	}
	commands := []httpx.CommandRoute{
		{Method: "POST", Path: "/api/v1/invoices/{id}:confirm", GinPath: "/api/v1/invoices/:id"},
		{Method: "POST", Path: "/api/v1/invoices/{id}:void", GinPath: "/api/v1/invoices/:id"},
		{Method: "POST", Path: "/api/v1/invoices:search", GinPath: "/api/v1/invoices:search"},
	}
	got, err := servedOperations(routes, commands)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/v1/invoices/{id}",
		"GET /healthz",
		"POST /api/v1/invoices/{id}:confirm",
		"POST /api/v1/invoices/{id}:void",
		"POST /api/v1/invoices:search",
	}
	if keys := sortedKeys(got); !slices.Equal(keys, want) {
		t.Errorf("served = %v, want %v", keys, want)
	}

	// A collection verb reported unescaped (gin unescapes after the first request) maps the same.
	routes[2].Path = "/api/v1/invoices:search"
	if got, err = servedOperations(routes, commands); err != nil || !got["POST /api/v1/invoices:search"] {
		t.Errorf("unescaped collection route: %v %v", sortedKeys(got), err)
	}

	// A command index entry without its gin route means the router was bypassed.
	if _, err := servedOperations(routes[:1], commands); err == nil {
		t.Error("missing dispatch route: want error")
	}
}

func TestSpecOperations(t *testing.T) {
	spec := []byte(`
openapi: 3.1.0
servers: [{url: /api/v1}]
paths:
  /invoices:
    parameters: []
    get: {}
    post: {}
  /invoices/{id}:confirm:
    post: {}
`)
	got, err := specOperations(spec, APIPrefix)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /invoices", "POST /invoices", "POST /invoices/{id}:confirm"}
	if keys := sortedKeys(got); !slices.Equal(keys, want) {
		t.Errorf("operations = %v, want %v", keys, want)
	}

	if _, err := specOperations([]byte("servers: [{url: /api/v2}]\npaths: {}\n"), APIPrefix); err == nil {
		t.Error("wrong server url: want error")
	}
	if _, err := specOperations([]byte("paths: [\n"), APIPrefix); err == nil {
		t.Error("invalid yaml: want error")
	}
	if _, err := specOperations([]byte("servers: [{url: /api/v1}]\npaths:\n  invoices:\n    get: {}\n"), APIPrefix); err == nil {
		t.Error("path without leading slash: want error")
	}
}

func TestDiffOperations(t *testing.T) {
	served := map[string]bool{"GET /a": true, "POST /b": true}
	documented := map[string]bool{"GET /a": true, "DELETE /c": true}
	missing, unserved := diffOperations(served, documented)
	if !slices.Equal(missing, []string{"POST /b"}) || !slices.Equal(unserved, []string{"DELETE /c"}) {
		t.Errorf("missing=%v unserved=%v", missing, unserved)
	}
}

func TestSplitPrefix(t *testing.T) {
	cases := []struct {
		op, prefix, rel string
		ok              bool
	}{
		{"GET /api/v1/invoices", APIPrefix, "GET /invoices", true},
		{"POST /api/admin/v1/tenants/{id}:suspend", AdminAPIPrefix, "POST /tenants/{id}:suspend", true},
		{"GET /api/v1", APIPrefix, "GET /", true},
		{"GET /api/v10/x", "", "", false},
		{"GET /metrics", "", "", false},
	}
	for _, tc := range cases {
		prefix, rel, ok := splitPrefix(tc.op)
		if prefix != tc.prefix || rel != tc.rel || ok != tc.ok {
			t.Errorf("splitPrefix(%q) = %q %q %v", tc.op, prefix, rel, ok)
		}
	}
}

func newContractApp(t *testing.T) *App {
	t.Helper()
	base, err := url.Parse("https://tally.test")
	if err != nil {
		t.Fatal(err)
	}
	// Handlers without services register every route; the contract covers them all.
	a, err := New(Options{
		PublicBaseURL: base,
		CSRFKey:       testCSRFKey,
		Modules: Modules{
			Identity: identity.NewHandler(nil), Org: org.NewHandler(nil), Reasons: org.NewReasonHandler(nil),
			Catalog: catalog.NewHandler(nil), Partner: partner.NewHandler(nil), Pricing: pricing.NewHandler(nil),
			Debt: debt.NewHandler(nil), Cash: cash.NewHandler(nil), Inventory: inventory.NewHandler(nil),
			Procurement: procurement.NewHandler(nil), Sales: sales.NewHandler(nil), Kits: kits.NewHandler(nil),
			Campaign: campaign.NewHandler(nil), CRM: crm.NewHandler(nil), Notification: notification.NewHandler(nil),
			Reporting: reporting.NewHandler(nil), Analytics: analytics.NewHandler(nil), Printing: printing.NewHandler(nil),
			Scanner: scanner.NewHandler(nil),
			SaaS:    saas.NewHandler(nil), Support: support.NewHandler(nil), SupportAdmin: support.NewService(support.Deps{}),
			Onboarding: org.NewOnboardingHandler(nil), Feedback: admin.NewFeedbackHandler(nil),
			Status: admin.NewStatusHandler(nil), Admin: admin.NewHandler(admin.HandlerDeps{}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// servedOperations lists the registered routes as "METHOD /openapi/path", replacing the gin
// routes of custom methods by the documented paths recorded in the command index.
func servedOperations(routes gin.RoutesInfo, commands []httpx.CommandRoute) (map[string]bool, error) {
	byGin := map[string][]string{}
	for _, cr := range commands {
		key := cr.Method + " " + cr.GinPath
		byGin[key] = append(byGin[key], cr.Method+" "+cr.Path)
	}
	out := map[string]bool{}
	seen := map[string]bool{}
	for _, r := range routes {
		key := r.Method + " " + strings.ReplaceAll(r.Path, `\:`, ":")
		if expanded, ok := byGin[key]; ok {
			seen[key] = true
			for _, op := range expanded {
				out[op] = true
			}
			continue
		}
		out[r.Method+" "+normalizeGinPath(r.Path)] = true
	}
	for key := range byGin {
		if !seen[key] {
			return nil, fmt.Errorf("command route %s is indexed but not registered", key)
		}
	}
	return out, nil
}

// normalizeGinPath converts gin wildcards (":name", "*name") to OpenAPI "{name}"; an escaped
// colon is a literal colon.
func normalizeGinPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch {
		case p[i] == '\\' && i+1 < len(p) && p[i+1] == ':':
			b.WriteByte(':')
			i++
		case p[i] == ':' || p[i] == '*':
			end := strings.IndexByte(p[i:], '/')
			if end < 0 {
				end = len(p) - i
			}
			b.WriteString("{" + p[i+1:i+end] + "}")
			i += end - 1
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String()
}

// splitPrefix maps "METHOD /api/v1/x" to (APIPrefix, "METHOD /x").
func splitPrefix(op string) (prefix, rel string, ok bool) {
	method, path, _ := strings.Cut(op, " ")
	for _, p := range []string{AdminAPIPrefix, APIPrefix} {
		if path == p || strings.HasPrefix(path, p+"/") || strings.HasPrefix(path, p+":") {
			rest := strings.TrimPrefix(path, p)
			if rest == "" {
				rest = "/"
			}
			return p, method + " " + rest, true
		}
	}
	return "", "", false
}

// specOperations lists the operations of an OpenAPI document as "METHOD /path", checking that
// its single server URL is prefix.
func specOperations(raw []byte, prefix string) (map[string]bool, error) {
	var doc struct {
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != prefix {
		return nil, fmt.Errorf("servers must be exactly [{url: %s}]", prefix)
	}
	out := map[string]bool{}
	for path, item := range doc.Paths {
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("path %q must start with /", path)
		}
		for _, m := range httpMethods {
			if _, ok := item[m]; ok {
				out[strings.ToUpper(m)+" "+path] = true
			}
		}
	}
	return out, nil
}

// diffOperations returns the served operations missing from the spec and the documented
// operations nobody serves, both sorted.
func diffOperations(served, documented map[string]bool) (missing, unserved []string) {
	for op := range served {
		if !documented[op] {
			missing = append(missing, op)
		}
	}
	for op := range documented {
		if !served[op] {
			unserved = append(unserved, op)
		}
	}
	slices.Sort(missing)
	slices.Sort(unserved)
	return missing, unserved
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
