//go:build integration

package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

const v1 = "/api/v1"

type withID struct {
	ID uuid.UUID `json:"id"`
}

type dataOf[T any] struct {
	Data []T `json:"data"`
}

// send makes a request that must succeed and decodes its answer.
func send[T any](t *testing.T, b *browser, method, path string, body any) T {
	t.Helper()
	rec := b.do(method, v1+path, body)
	if rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("%s %s = %d %s", method, path, rec.Code, rec.Body)
	}
	return testkit.DecodeJSON[T](t, rec)
}

func tenantOf(t *testing.T, b *browser) uuid.UUID {
	t.Helper()
	return send[struct {
		Tenant withID `json:"tenant"`
	}](t, b, http.MethodGet, "/me", nil).Tenant.ID
}

// unitsOf maps the unit names of a store to their ids.
func unitsOf(t *testing.T, b *browser) map[string]uuid.UUID {
	t.Helper()
	out := map[string]uuid.UUID{}
	for _, u := range send[dataOf[struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}]](t, b, http.MethodGet, "/units", nil).Data {
		out[u.Name] = u.ID
	}
	return out
}

type skuRef struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
}

type productRef struct {
	ID   uuid.UUID `json:"id"`
	Skus []skuRef  `json:"skus"`
}

type searchHit struct {
	Kind  string    `json:"kind"`
	ID    uuid.UUID `json:"id"`
	Code  string    `json:"code"`
	Label string    `json:"label"`
}

func search(t *testing.T, b *browser, kind, q string) []searchHit {
	t.Helper()
	return send[dataOf[searchHit]](t, b, http.MethodGet, "/search?type="+kind+"&q="+strings.ReplaceAll(q, " ", "+"), nil).Data
}

// catalogStore is one store's catalog and partners, built over HTTP.
type catalogStore struct {
	tenant                       uuid.UUID
	units                        map[string]uuid.UUID
	unit, taxRate                uuid.UUID
	attribute, option, set, cat  uuid.UUID
	product, sku, barcode        uuid.UUID
	variantProduct, mixGroup     uuid.UUID
	attachment, image            uuid.UUID
	customer, supplier, group    uuid.UUID
	contact, address, reason     uuid.UUID
	all                          []uuid.UUID
	productCode, productBarcode  string
	supplierName, supplierTaxNum string
}

func buildCatalogStore(t *testing.T, b *browser) catalogStore {
	t.Helper()
	st := catalogStore{tenant: tenantOf(t, b), units: unitsOf(t, b), productCode: "BDX-1", productBarcode: "8930000000017",
		supplierName: "Công ty Đậu Xanh Hải Dương", supplierTaxNum: "0101234567"}
	hop := st.units["hộp"]
	st.unit = send[withID](t, b, http.MethodPost, "/units", map[string]any{"name": "Bao thử"}).ID
	st.taxRate = send[withID](t, b, http.MethodPost, "/tax-rates", map[string]any{"name": "Thuế thử", "rate_percent": "7"}).ID
	attr := send[struct {
		ID      uuid.UUID `json:"id"`
		Options []withID  `json:"options"`
	}](t, b, http.MethodPost, "/attributes", map[string]any{
		"code": "mau_vo", "name": "Màu vỏ", "data_type": "select", "options": []string{"Đỏ", "Xanh"},
	})
	st.attribute = attr.ID
	st.option = send[withID](t, b, http.MethodPost, "/attributes/"+attr.ID.String()+"/options", map[string]any{"label": "Vàng"}).ID
	st.set = send[withID](t, b, http.MethodPost, "/attribute-sets", map[string]any{
		"name": "Bộ hộp quà", "items": []map[string]any{{"attribute_id": attr.ID, "is_required": false, "sort_order": 1}},
	}).ID
	st.cat = send[withID](t, b, http.MethodPost, "/categories", map[string]any{"name": "Bánh thử", "attribute_set_id": st.set}).ID

	p := send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"type": "goods", "name": "Bánh đậu xanh", "category_id": st.cat,
		"sku": map[string]any{"code": st.productCode, "barcode": st.productBarcode, "base_unit_id": hop},
	})
	st.product, st.sku = p.ID, p.Skus[0].ID
	send[map[string]any](t, b, http.MethodPost, "/skus/"+st.sku.String()+"/units", map[string]any{"unit_id": st.units["thùng"], "factor": "24"})
	st.barcode = send[withID](t, b, http.MethodPost, "/skus/"+st.sku.String()+"/barcodes", map[string]any{"code": "8930000000024"}).ID

	// Variants: the preview is exactly what generation creates, and generating again adds nothing.
	st.variantProduct = send[withID](t, b, http.MethodPost, "/products", map[string]any{"name": "Hộp quà Tết"}).ID
	vp := "/products/" + st.variantProduct.String()
	send[map[string]any](t, b, http.MethodPut, vp+"/variant-axes", map[string]any{"attribute_ids": []uuid.UUID{attr.ID}})
	req := map[string]any{"code_prefix": "HQ", "defaults": map[string]any{"base_unit_id": hop}}
	preview := send[dataOf[struct {
		Code   string `json:"code"`
		Exists bool   `json:"exists"`
	}]](t, b, http.MethodPost, vp+"/variants:preview", req).Data
	created := send[struct {
		Created  []skuRef `json:"created"`
		Existing int      `json:"existing"`
	}](t, b, http.MethodPost, vp+"/variants", req)
	if len(preview) != 3 || len(created.Created) != 3 {
		t.Fatalf("preview %d rows, generated %d", len(preview), len(created.Created))
	}
	for i, row := range preview {
		if row.Exists || created.Created[i].Code != row.Code {
			t.Errorf("row %d: preview %+v, generated %+v", i, row, created.Created[i])
		}
	}
	again := send[struct {
		Created  []skuRef `json:"created"`
		Existing int      `json:"existing"`
	}](t, b, http.MethodPost, vp+"/variants", req)
	if len(again.Created) != 0 || again.Existing != 3 {
		t.Errorf("regeneration = %+v", again)
	}

	cola := send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"name": "Nước ngọt Cola", "sku": map[string]any{"code": "NG-1", "base_unit_id": hop}})
	cam := send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"name": "Nước ngọt Cam", "sku": map[string]any{"code": "NG-2", "base_unit_id": hop}})
	st.mixGroup = send[withID](t, b, http.MethodPost, "/mix-groups", map[string]any{
		"name": "Nước ngọt lon", "pack_unit_id": st.units["thùng"], "units_per_pack": 24, "part_unit_id": st.units["lốc"],
		"units_per_part": 6, "items": []map[string]any{
			{"sku_id": cola.Skus[0].ID, "flavor_label": "Cola", "active": true},
			{"sku_id": cam.Skus[0].ID, "flavor_label": "Cam", "active": true},
		},
	}).ID
	quick := send[struct {
		Product productRef `json:"product"`
	}](t, b, http.MethodPost, "/products:quick-create", map[string]any{"name": "Kẹo dừa Bến Tre", "base_unit_id": hop})
	if len(quick.Product.Skus) != 1 {
		t.Fatalf("quick-create = %+v", quick)
	}

	// A product image is a clean image attachment uploaded for the product.
	st.attachment = send[struct {
		Attachment withID `json:"attachment"`
	}](t, b, http.MethodPost, "/attachments", map[string]any{
		"owner_type": "product", "owner_id": st.product, "file_name": "banh-dau-xanh.jpg", "content_type": "image/jpeg",
		"size_bytes": 2048, "sha256": strings.Repeat("ab", 32),
	}).Attachment.ID
	expect(t, b.do(http.MethodPost, v1+"/products/"+st.product.String()+"/images", map[string]any{"attachment_id": st.attachment}),
		http.StatusUnprocessableEntity, "")
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `UPDATE attachments SET scan_status = 'clean' WHERE id = $1`, st.attachment)
		return err
	})
	st.image = send[withID](t, b, http.MethodPost, "/products/"+st.product.String()+"/images", map[string]any{"attachment_id": st.attachment}).ID

	st.group = send[withID](t, b, http.MethodPost, "/customer-groups", map[string]any{"name": "Đại lý cấp 2", "base_price_kind": "wholesale"}).ID
	st.customer = send[withID](t, b, http.MethodPost, "/customers", map[string]any{
		"kind": "person", "name": "Chị Lan", "phone": "0901234567", "customer_group_id": st.group}).ID
	st.supplier = send[withID](t, b, http.MethodPost, "/suppliers", map[string]any{
		"kind": "organization", "name": st.supplierName, "tax_code": st.supplierTaxNum}).ID
	party := "/parties/" + st.customer.String()
	st.contact = send[withID](t, b, http.MethodPost, party+"/contacts", map[string]any{"name": "Anh Minh", "phone": "0907654321"}).ID
	st.address = send[withID](t, b, http.MethodPost, party+"/addresses", map[string]any{"label": "Kho", "address_line": "12 Lê Lợi, Hải Dương"}).ID
	st.reason = send[withID](t, b, http.MethodPost, "/reasons", map[string]any{"kind": "damage", "name": "Vỡ khi bốc xếp"}).ID

	st.all = []uuid.UUID{st.unit, st.taxRate, st.attribute, st.option, st.set, st.cat, st.product, st.sku, st.barcode,
		st.variantProduct, st.mixGroup, st.attachment, st.image, st.customer, st.supplier, st.group, st.contact,
		st.address, st.reason, cola.ID, cam.ID, quick.Product.ID}
	return st
}

// idRoute is an operation that names a resource by id; args fill its path parameters in order.
type idRoute struct {
	method, path string
	args         []uuid.UUID
	body         any
	ifMatch      bool
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func (r idRoute) url() string {
	i := 0
	return pathParam.ReplaceAllStringFunc(r.path, func(string) string {
		i++
		return r.args[i-1].String()
	})
}

// idRoutesOf are the id-bearing catalog, partner and reason operations against st's records. own
// supplies valid ids of the calling store wherever a body must name one, so that only the path
// can decide the answer.
func idRoutesOf(st catalogStore, own catalogStore) []idRoute {
	u := func(args ...uuid.UUID) []uuid.UUID { return args }
	thung := st.units["thùng"]
	return []idRoute{
		{method: "PATCH", path: "/reasons/{id}", args: u(st.reason), body: map[string]any{"name": "x"}},
		{method: "DELETE", path: "/reasons/{id}", args: u(st.reason)},
		{method: "PATCH", path: "/units/{id}", args: u(st.unit), body: map[string]any{"name": "x"}},
		{method: "PATCH", path: "/tax-rates/{id}", args: u(st.taxRate), body: map[string]any{"name": "x"}},
		{method: "GET", path: "/attributes/{id}", args: u(st.attribute)},
		{method: "PATCH", path: "/attributes/{id}", args: u(st.attribute), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "DELETE", path: "/attributes/{id}", args: u(st.attribute)},
		{method: "POST", path: "/attributes/{id}/options", args: u(st.attribute), body: map[string]any{"label": "x"}},
		{method: "PATCH", path: "/attributes/{id}/options/{optionId}", args: u(st.attribute, st.option), body: map[string]any{"label": "x"}},
		{method: "DELETE", path: "/attributes/{id}/options/{optionId}", args: u(st.attribute, st.option)},
		{method: "GET", path: "/attribute-sets/{id}", args: u(st.set)},
		{method: "PATCH", path: "/attribute-sets/{id}", args: u(st.set), body: map[string]any{"name": "x"}},
		{method: "PUT", path: "/attribute-sets/{id}/items", args: u(st.set), body: map[string]any{"items": []any{}}},
		{method: "PATCH", path: "/categories/{id}", args: u(st.cat), body: map[string]any{"name": "x"}},
		{method: "DELETE", path: "/categories/{id}", args: u(st.cat)},
		{method: "GET", path: "/products/{id}", args: u(st.product)},
		{method: "PATCH", path: "/products/{id}", args: u(st.product), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "DELETE", path: "/products/{id}", args: u(st.product)},
		{method: "GET", path: "/products/{id}/variant-axes", args: u(st.variantProduct)},
		{method: "PUT", path: "/products/{id}/variant-axes", args: u(st.variantProduct), body: map[string]any{"attribute_ids": []uuid.UUID{own.attribute}}},
		{method: "POST", path: "/products/{id}/variants:preview", args: u(st.variantProduct), body: map[string]any{"code_prefix": "X"}},
		{method: "POST", path: "/products/{id}/variants", args: u(st.variantProduct),
			body: map[string]any{"code_prefix": "X", "defaults": map[string]any{"base_unit_id": own.units["hộp"]}}},
		{method: "GET", path: "/products/{id}/images", args: u(st.product)},
		{method: "POST", path: "/products/{id}/images", args: u(st.product), body: map[string]any{"attachment_id": own.attachment}},
		{method: "DELETE", path: "/products/{id}/images/{imageId}", args: u(st.product, st.image)},
		{method: "GET", path: "/skus/{id}", args: u(st.sku)},
		{method: "PATCH", path: "/skus/{id}", args: u(st.sku), body: map[string]any{"spec_text": "x"}, ifMatch: true},
		{method: "DELETE", path: "/skus/{id}", args: u(st.sku)},
		{method: "GET", path: "/skus/{id}/units", args: u(st.sku)},
		{method: "POST", path: "/skus/{id}/units", args: u(st.sku), body: map[string]any{"unit_id": own.units["lốc"], "factor": "6"}},
		{method: "PATCH", path: "/skus/{id}/units/{unitId}", args: u(st.sku, thung), body: map[string]any{"active": false}},
		{method: "DELETE", path: "/skus/{id}/units/{unitId}", args: u(st.sku, thung)},
		{method: "PUT", path: "/skus/{id}/units", args: u(st.sku), body: map[string]any{"units": []any{}}, ifMatch: true},
		{method: "GET", path: "/skus/{id}/barcodes", args: u(st.sku)},
		{method: "POST", path: "/skus/{id}/barcodes", args: u(st.sku), body: map[string]any{"code": "8930000000031"}},
		{method: "PATCH", path: "/skus/{id}/barcodes/{barcodeId}", args: u(st.sku, st.barcode), body: map[string]any{"unit_id": own.units["thùng"]}},
		{method: "DELETE", path: "/skus/{id}/barcodes/{barcodeId}", args: u(st.sku, st.barcode)},
		{method: "GET", path: "/mix-groups/{id}", args: u(st.mixGroup)},
		{method: "PATCH", path: "/mix-groups/{id}", args: u(st.mixGroup), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "DELETE", path: "/mix-groups/{id}", args: u(st.mixGroup)},
		{method: "POST", path: "/mix-groups/{id}:deactivate", args: u(st.mixGroup)},
		{method: "POST", path: "/mix-groups/{id}:reactivate", args: u(st.mixGroup)},
		{method: "GET", path: "/customers/{id}", args: u(st.customer)},
		{method: "PATCH", path: "/customers/{id}", args: u(st.customer), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "DELETE", path: "/customers/{id}", args: u(st.customer)},
		{method: "GET", path: "/suppliers/{id}", args: u(st.supplier)},
		{method: "PATCH", path: "/suppliers/{id}", args: u(st.supplier), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "DELETE", path: "/suppliers/{id}", args: u(st.supplier)},
		{method: "POST", path: "/suppliers/{id}:stop", args: u(st.supplier)},
		{method: "POST", path: "/suppliers/{id}:reopen", args: u(st.supplier)},
		{method: "PATCH", path: "/customer-groups/{id}", args: u(st.group), body: map[string]any{"name": "x"}},
		{method: "DELETE", path: "/customer-groups/{id}", args: u(st.group)},
		{method: "GET", path: "/parties/{id}/contacts", args: u(st.customer)},
		{method: "POST", path: "/parties/{id}/contacts", args: u(st.customer), body: map[string]any{"name": "x"}},
		{method: "PATCH", path: "/parties/{id}/contacts/{contactId}", args: u(st.customer, st.contact), body: map[string]any{"name": "x"}},
		{method: "DELETE", path: "/parties/{id}/contacts/{contactId}", args: u(st.customer, st.contact)},
		{method: "GET", path: "/parties/{id}/addresses", args: u(st.customer)},
		{method: "POST", path: "/parties/{id}/addresses", args: u(st.customer), body: map[string]any{"label": "x", "address_line": "y"}},
		{method: "PATCH", path: "/parties/{id}/addresses/{addressId}", args: u(st.customer, st.address), body: map[string]any{"label": "x"}},
		{method: "DELETE", path: "/parties/{id}/addresses/{addressId}", args: u(st.customer, st.address)},
	}
}

// idOperationsInSpec are the documented catalog, partner and reason operations whose path names
// a resource.
func idOperationsInSpec(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Tags []string `yaml:"tags"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var out []string
	for path, ops := range spec.Paths {
		if !strings.Contains(path, "{") {
			continue
		}
		for method, op := range ops {
			if slices.Contains(op.Tags, "catalog") || slices.Contains(op.Tags, "partners") || strings.HasPrefix(path, "/reasons/") {
				out = append(out, strings.ToUpper(method)+" "+path)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestCatalogAndPartnersOverHTTP(t *testing.T) {
	s := newStack(t)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	storeA, storeB := buildCatalogStore(t, a), buildCatalogStore(t, b)

	t.Run("search finds by unaccented name and by exact barcode", func(t *testing.T) {
		hits := search(t, a, "sku", "banh dau xanh")
		if len(hits) == 0 || hits[0].ID != storeA.sku || hits[0].Label != "Bánh đậu xanh" {
			t.Errorf("unaccented search = %+v", hits)
		}
		hits = search(t, a, "sku", "8930000000024")
		if len(hits) == 0 || hits[0].ID != storeA.sku {
			t.Errorf("barcode search = %+v", hits)
		}
		expect(t, a.do(http.MethodGet, v1+"/search?type=product&q=x", nil), http.StatusUnprocessableEntity, "")
	})

	t.Run("a stopped supplier is not offered", func(t *testing.T) {
		found := func() bool {
			return slices.ContainsFunc(search(t, a, "supplier", "dau xanh hai duong"), func(h searchHit) bool { return h.ID == storeA.supplier })
		}
		if !found() {
			t.Fatal("an active supplier is not found")
		}
		send[map[string]any](t, a, http.MethodPost, "/suppliers/"+storeA.supplier.String()+":stop", nil)
		if found() {
			t.Error("a stopped supplier is offered")
		}
		send[map[string]any](t, a, http.MethodPost, "/suppliers/"+storeA.supplier.String()+":reopen", nil)
		if !found() {
			t.Error("a reopened supplier is not offered")
		}
	})

	t.Run("every id-bearing operation is covered", func(t *testing.T) {
		var covered []string
		for _, r := range idRoutesOf(storeA, storeB) {
			covered = append(covered, r.method+" "+r.path)
		}
		slices.Sort(covered)
		if documented := idOperationsInSpec(t); !slices.Equal(covered, documented) {
			t.Errorf("covered operations\n%v\ndiffer from the documented ones\n%v", covered, documented)
		}
	})

	// Each check signs in afresh: the API limit is per client address.
	t.Run("ids of another store answer 404", func(t *testing.T) {
		b := s.signIn(t, ownerB)
		for _, r := range idRoutesOf(storeA, storeB) {
			header := http.Header{}
			if r.ifMatch {
				header.Set("If-Match", `"1"`)
			}
			rec := b.doWith(r.method, v1+r.url(), r.body, header)
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not_found"`) {
				t.Errorf("%s %s = %d %s", r.method, r.path, rec.Code, rec.Body)
			}
		}
		// Nothing of A changed.
		p := send[struct {
			Name string `json:"name"`
		}](t, a, http.MethodGet, "/products/"+storeA.product.String(), nil)
		sup := send[struct {
			Status string `json:"status"`
		}](t, a, http.MethodGet, "/suppliers/"+storeA.supplier.String(), nil)
		if p.Name != "Bánh đậu xanh" || sup.Status != "active" {
			t.Errorf("A's records changed: product %q, supplier %q", p.Name, sup.Status)
		}
		type barcode struct {
			ID     uuid.UUID `json:"id"`
			UnitID uuid.UUID `json:"unit_id"`
		}
		barcodes := send[struct {
			Data []barcode `json:"data"`
		}](t, a, http.MethodGet, "/skus/"+storeA.sku.String()+"/barcodes", nil)
		i := slices.IndexFunc(barcodes.Data, func(bc barcode) bool { return bc.ID == storeA.barcode })
		if i < 0 || barcodes.Data[i].UnitID != storeA.units["hộp"] {
			t.Errorf("A's barcode is gone or moved: %+v", barcodes.Data)
		}
	})

	t.Run("lists and search show only the store's own records", func(t *testing.T) {
		b := s.signIn(t, ownerB)
		for _, path := range []string{"/units", "/tax-rates", "/attributes", "/attribute-sets", "/categories", "/products",
			"/skus", "/mix-groups", "/customers", "/suppliers", "/customer-groups", "/reasons",
			"/products/" + storeB.product.String() + "/images", "/parties/" + storeB.customer.String() + "/contacts",
			"/parties/" + storeB.customer.String() + "/addresses"} {
			rec := b.do(http.MethodGet, v1+path, nil)
			expect(t, rec, http.StatusOK, "")
			for _, id := range storeA.all {
				if strings.Contains(rec.Body.String(), id.String()) {
					t.Errorf("GET %s in B lists A's %s", path, id)
				}
			}
		}
		for _, q := range []struct{ kind, q string }{{"sku", "banh dau xanh"}, {"sku", "8930000000024"}, {"customer", "chi lan"},
			{"supplier", storeA.supplierTaxNum}} {
			for _, h := range search(t, b, q.kind, q.q) {
				if slices.Contains(storeA.all, h.ID) {
					t.Errorf("B's search %s %q finds A's %+v", q.kind, q.q, h)
				}
			}
		}
	})

	t.Run("records cannot reference another store", func(t *testing.T) {
		b := s.signIn(t, ownerB)
		bp := "/products/" + storeB.product.String()
		bs := "/skus/" + storeB.sku.String()
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"POST", "/products", map[string]any{"name": "Lạ", "category_id": storeA.cat}},
			{"POST", "/products", map[string]any{"name": "Lạ", "sku": map[string]any{"code": "LA-1", "base_unit_id": storeA.units["hộp"]}}},
			{"POST", "/products:quick-create", map[string]any{"name": "Lạ", "base_unit_id": storeA.units["hộp"]}},
			{"POST", "/categories", map[string]any{"name": "Lạ", "parent_id": storeA.cat}},
			{"POST", "/categories", map[string]any{"name": "Lạ", "attribute_set_id": storeA.set}},
			{"POST", "/attribute-sets", map[string]any{"name": "Lạ", "items": []map[string]any{{"attribute_id": storeA.attribute}}}},
			{"PUT", bp + "/variant-axes", map[string]any{"attribute_ids": []uuid.UUID{storeA.attribute}}},
			{"POST", bp + "/images", map[string]any{"attachment_id": storeA.attachment}},
			{"POST", bs + "/units", map[string]any{"unit_id": storeA.unit, "factor": "10"}},
			{"POST", "/mix-groups", map[string]any{"name": "Lạ", "pack_unit_id": storeB.units["thùng"], "units_per_pack": 24,
				"part_unit_id": storeB.units["lốc"], "units_per_part": 6, "items": []map[string]any{
					{"sku_id": storeA.sku, "flavor_label": "A"}, {"sku_id": storeB.sku, "flavor_label": "B"}}}},
			{"POST", "/customers", map[string]any{"name": "Lạ", "customer_group_id": storeA.group}},
			{"POST", "/customers", map[string]any{"party_id": storeA.supplier}},
			{"POST", "/suppliers", map[string]any{"party_id": storeA.customer}},
		} {
			rec := b.do(c.method, v1+c.path, c.body)
			if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusNotFound {
				t.Errorf("%s %s %v = %d %s", c.method, c.path, c.body, rec.Code, rec.Body)
			}
		}
	})
}

type skuUnitOut struct {
	UnitID            uuid.UUID `json:"unit_id"`
	Factor            string    `json:"factor"`
	Active            bool      `json:"active"`
	IsDefaultPurchase bool      `json:"is_default_purchase"`
	RetailPrice       *string   `json:"retail_price"`
	WholesalePrice    *string   `json:"wholesale_price"`
	LockedBy          *string   `json:"locked_by"`
}

type skuUnitsOut struct {
	ID      uuid.UUID    `json:"id"`
	Version int          `json:"version"`
	Units   []skuUnitOut `json:"units"`
}

func (d skuUnitsOut) unit(t *testing.T, unitID uuid.UUID) skuUnitOut {
	t.Helper()
	for _, u := range d.Units {
		if u.UnitID == unitID {
			return u
		}
	}
	t.Fatalf("units %+v lack %s", d.Units, unitID)
	return skuUnitOut{}
}

func strOr(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

// A SKU is created with any number of units priced each, and its whole unit table is saved in one
// request: locked units can only go inactive, prices fall back to base × factor when removed and
// the reads of stock and barcodes follow the active units.
func TestSkuUnitTableOverHTTP(t *testing.T) {
	s, _, _ := newProcurementStack(t)
	owner := s.signUp(t)
	a := s.signIn(t, owner)
	store := buildPurchaseStore(t, a)
	cai, day, thung, loc := store.units["cái"], store.units["dây"], store.units["thùng"], store.units["lốc"]

	created := send[productRef](t, a, http.MethodPost, "/products", map[string]any{"type": "goods", "name": "Kẹo sữa", "sku": map[string]any{
		"code": "KEO-1", "base_unit_id": cai, "retail_price": "500", "units": []map[string]any{
			{"unit_id": day, "factor": "50", "retail_price": "24000"}, {"unit_id": thung, "factor": "500"}}}})
	sku := created.Skus[0].ID
	skuPath := "/skus/" + sku.String()
	read := func() skuUnitsOut { return send[skuUnitsOut](t, a, http.MethodGet, skuPath, nil) }
	put := func(version int, body map[string]any) *httptest.ResponseRecorder {
		return a.doWith(http.MethodPut, v1+skuPath+"/units", body, ifMatchOf(version))
	}
	quote := func(unit uuid.UUID) string {
		t.Helper()
		return send[dataOf[struct {
			Amount string `json:"amount"`
		}]](t, a, http.MethodPost, "/pricing/quote", map[string]any{"lines": []map[string]any{
			{"sku_id": sku, "unit_id": unit, "qty": "1"}}}).Data[0].Amount
	}

	t.Run("create prices each unit and buys by the biggest", func(t *testing.T) {
		d := read()
		if len(d.Units) != 3 || !d.unit(t, thung).IsDefaultPurchase || strOr(d.unit(t, day).RetailPrice) != "24000" ||
			d.unit(t, thung).RetailPrice != nil || strOr(d.unit(t, cai).RetailPrice) != "500" {
			t.Fatalf("units = %+v", d.Units)
		}
		if n := countRows(t, store.tenant, `SELECT count(*) FROM price_list_items WHERE sku_id = $1`, sku); n != 2 {
			t.Errorf("price rows = %d, want cái and dây only", n)
		}
		if got := quote(day); got != "24000" {
			t.Errorf("1 dây = %s, want its own 24000", got)
		}
		if got := quote(thung); got != "250000" {
			t.Errorf("1 thùng = %s, want 500 × 500", got)
		}
	})

	t.Run("create names the bad unit row", func(t *testing.T) {
		for _, c := range []struct {
			units []map[string]any
			field string
		}{
			{[]map[string]any{{"unit_id": day, "factor": "50"}, {"unit_id": day, "factor": "60"}}, `"sku.units[1].unit_id":"duplicate"`},
			{[]map[string]any{{"unit_id": cai, "factor": "1"}}, `"sku.units[0].unit_id":"is_base"`},
			{[]map[string]any{{"unit_id": day, "factor": "50"}, {"unit_id": thung, "factor": "0"}}, `"sku.units[1].factor":"not_positive"`},
		} {
			rec := a.do(http.MethodPost, v1+"/products", map[string]any{"name": "Kẹo lỗi", "sku": map[string]any{
				"code": "KEO-X", "base_unit_id": cai, "units": c.units}})
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), c.field) {
				t.Errorf("units %v = %d %s, want 422 %s", c.units, rec.Code, rec.Body, c.field)
			}
		}
	})

	// 11 dây received: 550 cái, which the stock card writes with the thùng while it is active.
	postReceipt(t, a, map[string]any{"supplier_party_id": store.supplier, "lines": []map[string]any{
		{"sku_id": sku, "unit_id": day, "qty": "11", "unit_price": "20000"}}})
	const code = "8936000999018"
	send[withID](t, a, http.MethodPost, skuPath+"/barcodes", map[string]any{"code": code, "unit_id": thung})
	dayRow := map[string]any{"unit_id": day, "factor": "50", "retail_price": "24000"}
	thungRow := map[string]any{"unit_id": thung, "factor": "500"}

	t.Run("each unit says why it cannot be deleted", func(t *testing.T) {
		d := read()
		rec := put(d.Version, map[string]any{"units": []map[string]any{dayRow, thungRow, {"unit_id": loc, "factor": "10"}}})
		expect(t, rec, http.StatusOK, "")
		d = testkit.DecodeJSON[skuUnitsOut](t, rec)
		if etag := rec.Header().Get("ETag"); etag != `"`+strconv.Itoa(d.Version)+`"` {
			t.Errorf("ETag %s, version %d", etag, d.Version)
		}
		for unit, want := range map[uuid.UUID]string{cai: "null", day: "documents", thung: "barcodes", loc: "null"} {
			if got := strOr(d.unit(t, unit).LockedBy); got != want {
				t.Errorf("unit %s locked by %s, want %s", unit, got, want)
			}
		}
		// A unit that had its own price keeps its history and is locked by it.
		priced := put(d.Version, map[string]any{"units": []map[string]any{dayRow, thungRow,
			{"unit_id": loc, "factor": "10", "wholesale_price": "4500"}}})
		expect(t, priced, http.StatusOK, "")
		d = testkit.DecodeJSON[skuUnitsOut](t, priced)
		if got := strOr(d.unit(t, loc).LockedBy); got != "price_history" || strOr(d.unit(t, loc).WholesalePrice) != "4500" {
			t.Errorf("priced lốc = %+v, want locked by price_history", d.unit(t, loc))
		}
	})

	t.Run("a stale version is refused", func(t *testing.T) {
		d := read()
		expect(t, put(d.Version-1, map[string]any{"units": []map[string]any{dayRow, thungRow}}), http.StatusPreconditionFailed, "")
	})

	t.Run("deleting a used unit refuses the whole table", func(t *testing.T) {
		d := read()
		rec := put(d.Version, map[string]any{"retail_price": "600", "units": []map[string]any{
			{"unit_id": thung, "factor": "400"}, {"unit_id": loc, "factor": "10", "wholesale_price": "4500"}}})
		expect(t, rec, http.StatusConflict, "unit_in_use")
		if !strings.Contains(rec.Body.String(), `"units[`+day.String()+`]":"documents"`) {
			t.Errorf("refusal = %s, want the dây named with documents", rec.Body)
		}
		after := read()
		if after.Version != d.Version || len(after.Units) != 4 || after.unit(t, thung).Factor != "500" ||
			strOr(after.unit(t, cai).RetailPrice) != "500" {
			t.Errorf("after the refused table = %+v", after)
		}
		// A unit with price history cannot be deleted either.
		expect(t, put(d.Version, map[string]any{"units": []map[string]any{dayRow, thungRow}}), http.StatusConflict, "price_history")
	})

	t.Run("the table names the bad row", func(t *testing.T) {
		d := read()
		rec := put(d.Version, map[string]any{"units": []map[string]any{dayRow, dayRow, {"unit_id": cai, "factor": "1"},
			{"unit_id": loc, "factor": "0"}}})
		expect(t, rec, http.StatusUnprocessableEntity, "validation_failed")
		for _, field := range []string{`"units[1].unit_id":"duplicate"`, `"units[2].unit_id":"is_base"`, `"units[3].factor":"not_positive"`} {
			if !strings.Contains(rec.Body.String(), field) {
				t.Errorf("refusal = %s, want %s", rec.Body, field)
			}
		}
	})

	t.Run("an inactive thùng leaves the stock card, the scanner and the purchase default", func(t *testing.T) {
		onHand := func() string {
			return send[struct {
				OnHand struct {
					Display string `json:"display"`
				} `json:"on_hand"`
			}](t, a, http.MethodGet, "/stock-card?unit_mode=auto&sku_id="+sku.String(), nil).OnHand.Display
		}
		if got := onHand(); !strings.Contains(got, "thùng") {
			t.Fatalf("on hand with the thùng active = %q", got)
		}
		lookup := send[struct {
			UnitID uuid.UUID `json:"unit_id"`
		}](t, a, http.MethodGet, "/sku-lookup?code="+code, nil)
		if lookup.UnitID != thung {
			t.Fatalf("lookup = %+v, want the thùng", lookup)
		}

		d := read()
		rec := put(d.Version, map[string]any{"units": []map[string]any{dayRow, {"unit_id": thung, "factor": "500", "active": false},
			{"unit_id": loc, "factor": "10", "active": false, "wholesale_price": "4500"}}})
		expect(t, rec, http.StatusOK, "")
		d = testkit.DecodeJSON[skuUnitsOut](t, rec)
		if th, dy := d.unit(t, thung), d.unit(t, day); th.Active || th.IsDefaultPurchase || !dy.IsDefaultPurchase {
			t.Errorf("thùng %+v, dây %+v, want dây the purchase unit", th, dy)
		}
		if got := onHand(); strings.Contains(got, "thùng") || !strings.Contains(got, "11 dây") {
			t.Errorf("on hand with the thùng inactive = %q, want 11 dây", got)
		}
		expect(t, a.do(http.MethodGet, v1+"/sku-lookup?code="+code, nil), http.StatusNotFound, "code_not_found")
	})

	t.Run("removing the dây price sells it at base × factor", func(t *testing.T) {
		d := read()
		rec := put(d.Version, map[string]any{"units": []map[string]any{{"unit_id": day, "factor": "50", "retail_price": nil},
			{"unit_id": thung, "factor": "500", "active": false}, {"unit_id": loc, "factor": "10", "active": false, "wholesale_price": "4500"}}})
		expect(t, rec, http.StatusOK, "")
		if got := testkit.DecodeJSON[skuUnitsOut](t, rec).unit(t, day); got.RetailPrice != nil || strOr(got.LockedBy) != "documents" {
			t.Errorf("dây = %+v, want no own price and still locked", got)
		}
		if got := quote(day); got != "25000" {
			t.Errorf("1 dây = %s, want 500 × 50", got)
		}
	})

	t.Run("a purchase order line creates a product with units", func(t *testing.T) {
		order := confirmedOrder(t, a, map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "2", "unit_price": "240000", "new_product": map[string]any{
				"name": "Kẹo dừa", "base_unit_id": cai, "purchase_unit_id": thung, "retail_price": "500", "units": []map[string]any{
					{"unit_id": day, "factor": "50", "retail_price": "24000"}, {"unit_id": thung, "factor": "500"}}}}}})
		if order.Lines[0].BaseQty != "1000" {
			t.Fatalf("order line = %+v, want 2 thùng of 500", order.Lines[0])
		}
		receiveOrder(t, a, order, "2")
		row := skuNamed(t, a, "Kẹo dừa")
		if got := stockOf(t, store.tenant, row.ID).OnHand.String(); got != "1000" {
			t.Errorf("on hand = %s, want 1000 cái", got)
		}
		d := send[skuUnitsOut](t, a, http.MethodGet, "/skus/"+row.ID.String(), nil)
		if len(d.Units) != 3 || !d.unit(t, thung).IsDefaultPurchase || strOr(d.unit(t, day).RetailPrice) != "24000" {
			t.Errorf("units = %+v", d.Units)
		}
		expect(t, a.do(http.MethodPost, v1+"/purchase-orders", map[string]any{"supplier_party_id": store.supplier,
			"lines": []map[string]any{{"qty": "1", "unit_price": "1000", "new_product": map[string]any{
				"name": "Kẹo lạc", "base_unit_id": cai, "purchase_unit_id": loc, "units": []map[string]any{
					{"unit_id": day, "factor": "50"}}}}}}),
			http.StatusUnprocessableEntity, "lines.0.new_product.purchase_unit_id")
	})
}
