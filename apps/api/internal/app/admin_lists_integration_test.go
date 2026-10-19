//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

type adminRow = map[string]json.RawMessage

type adminPage struct {
	Data       []adminRow `json:"data"`
	NextCursor *string    `json:"next_cursor"`
}

// adminPages follows next_cursor from path with pages of limit rows and returns every row; it
// fails when a page is longer than limit or the walk does not end.
func adminPages(t *testing.T, b *browser, path string, limit int) []adminRow {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var out []adminRow
	cursor := ""
	for range 1000 {
		q := path + sep + "limit=" + strconv.Itoa(limit)
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		page := adminSend[adminPage](t, b, http.MethodGet, q, nil)
		if len(page.Data) > limit {
			t.Fatalf("%s answered %d rows, more than the limit %d", q, len(page.Data), limit)
		}
		out = append(out, page.Data...)
		if page.NextCursor == nil {
			return out
		}
		if len(page.Data) != limit {
			t.Fatalf("%s answered a short page of %d rows with a next cursor", q, len(page.Data))
		}
		cursor = *page.NextCursor
	}
	t.Fatalf("%s: the pages do not end", path)
	return nil
}

func field(t *testing.T, row adminRow, name string) string {
	t.Helper()
	raw, ok := row[name]
	if !ok {
		t.Fatalf("row %v has no %s", row, name)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw)
	}
	return s
}

// rowKeys are the keys of rows, made of the named fields.
func rowKeys(t *testing.T, rows []adminRow, names ...string) []string {
	t.Helper()
	out := make([]string, len(rows))
	for i, r := range rows {
		parts := make([]string, len(names))
		for j, n := range names {
			parts[j] = field(t, r, n)
		}
		out[i] = strings.Join(parts, "|")
	}
	return out
}

// samePagesAnyLimit walks path with pages of two rows and of 200 rows and requires the same rows
// in the same order, each once; it returns the rows.
func samePagesAnyLimit(t *testing.T, b *browser, path string, key ...string) []adminRow {
	t.Helper()
	small, large := adminPages(t, b, path, 2), adminPages(t, b, path, 200)
	sk, lk := rowKeys(t, small, key...), rowKeys(t, large, key...)
	if !slices.Equal(sk, lk) {
		t.Fatalf("%s by pages of 2 = %v, by pages of 200 = %v", path, sk, lk)
	}
	seen := map[string]bool{}
	for _, k := range sk {
		if seen[k] {
			t.Fatalf("%s lists %s twice", path, k)
		}
		seen[k] = true
	}
	return small
}

func requireKeys(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s = %v, missing %s", what, got, w)
		}
	}
}

// newestFirst requires rows ordered by the time field, then id, both descending.
func newestFirst(t *testing.T, what string, rows []adminRow, at string) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		prevAt, err1 := time.Parse(time.RFC3339Nano, field(t, rows[i-1], at))
		curAt, err2 := time.Parse(time.RFC3339Nano, field(t, rows[i], at))
		if err1 != nil || err2 != nil {
			t.Fatalf("%s %s: %v %v", what, at, err1, err2)
		}
		prevID, curID := field(t, rows[i-1], "id"), field(t, rows[i], "id")
		if curAt.After(prevAt) || (curAt.Equal(prevAt) && curID >= prevID) {
			t.Fatalf("%s row %d (%s %s) comes after (%s %s)", what, i, curAt, curID, prevAt, prevID)
		}
	}
}

func TestAdminListsPageThroughEveryRowOnce(t *testing.T) {
	s := newControlStack(t)
	adm, _ := s.adminSignIn(t)

	stores := make([]owner, 3)
	tenants := make([]string, 3)
	var shop *browser
	for i := range stores {
		stores[i] = s.signUp(t)
		b := s.signIn(t, stores[i])
		tenants[i] = tenantOf(t, b).String()
		if i == 0 {
			shop = b
		}
	}

	t.Run("tenants", func(t *testing.T) {
		rows := samePagesAnyLimit(t, adm, "/tenants", "id")
		requireKeys(t, "tenant ids", rowKeys(t, rows, "id"), tenants...)
		newestFirst(t, "tenants", rows, "created_at")
	})

	t.Run("saas-invoices", func(t *testing.T) {
		var issued []string
		for _, amount := range []string{"100000", "200000", "300000"} {
			inv := adminSend[adminRow](t, adm, http.MethodPost, "/saas-invoices",
				map[string]any{"tenant_id": tenants[0], "plan_id": storePlanID, "amount": amount})
			issued = append(issued, field(t, inv, "id"))
		}
		rows := samePagesAnyLimit(t, adm, "/saas-invoices?tenant_id="+tenants[0], "id")
		requireKeys(t, "invoice ids", rowKeys(t, rows, "id"), issued...)
		for _, r := range rows {
			if field(t, r, "tenant_id") != tenants[0] {
				t.Errorf("invoice of %s listed for %s", field(t, r, "tenant_id"), tenants[0])
			}
		}
		newestFirst(t, "invoices", rows, "issued_at")
	})

	t.Run("incidents", func(t *testing.T) {
		var opened []string
		for i := range 3 {
			inc := adminSend[adminRow](t, adm, http.MethodPost, "/incidents", map[string]any{
				"title": "Sự cố " + strconv.Itoa(i), "impact": "minor", "status": "investigating", "message": "Đang kiểm tra.",
			})
			opened = append(opened, field(t, inc, "id"))
			s.clk.Advance(time.Second)
		}
		rows := samePagesAnyLimit(t, adm, "/incidents", "id")
		requireKeys(t, "incident ids", rowKeys(t, rows, "id"), opened...)
		newestFirst(t, "incidents", rows, "started_at")
	})

	t.Run("partners", func(t *testing.T) {
		var made []string
		for range 3 {
			p := adminSend[adminRow](t, adm, http.MethodPost, "/partners", map[string]any{
				"name": "Đối tác " + unique(), "referral_code": "P-" + unique(), "commission_percent": "5",
			})
			made = append(made, field(t, p, "id"))
		}
		rows := samePagesAnyLimit(t, adm, "/partners", "id")
		requireKeys(t, "partner ids", rowKeys(t, rows, "id"), made...)
		newestFirst(t, "partners", rows, "created_at")
	})

	t.Run("support-tickets", func(t *testing.T) {
		var opened []string
		for i := range 3 {
			ticket := send[adminRow](t, shop, http.MethodPost, "/support-tickets",
				map[string]any{"subject": "Câu hỏi " + strconv.Itoa(i), "body": "Cần hỗ trợ cài đặt máy in."})
			opened = append(opened, field(t, ticket, "id"))
		}
		rows := samePagesAnyLimit(t, adm, "/support-tickets?tenant_id="+tenants[0], "id")
		got := rowKeys(t, rows, "id")
		if len(got) != 3 {
			t.Fatalf("tickets of the store = %v, want the 3 opened", got)
		}
		requireKeys(t, "ticket ids", got, opened...)
	})

	t.Run("usage", func(t *testing.T) {
		if _, err := s.domains.SaaS.RecordUsage(context.Background()); err != nil {
			t.Fatal(err)
		}
		day := s.clk.Now().UTC().Format(time.DateOnly)
		rows := samePagesAnyLimit(t, adm, "/usage?from="+day+"&to="+day, "usage_date", "tenant_id")
		byTenant := map[string]adminRow{}
		for i, r := range rows {
			byTenant[field(t, r, "tenant_id")] = r
			if i > 0 && field(t, r, "tenant_id") <= field(t, rows[i-1], "tenant_id") {
				t.Fatalf("usage rows of %s are not ordered by store: %v", day, rowKeys(t, rows, "tenant_id"))
			}
		}
		for i, tenant := range tenants {
			r, ok := byTenant[tenant]
			if !ok {
				t.Fatalf("usage of %s has no row for %s", day, tenant)
			}
			if field(t, r, "slug") != stores[i].slug || field(t, r, "tenant_name") != "Cửa hàng "+stores[i].slug {
				t.Errorf("usage row of %s = slug %s name %s", stores[i].slug, field(t, r, "slug"), field(t, r, "tenant_name"))
			}
		}
	})

	t.Run("feedback", func(t *testing.T) {
		byKind := map[string][]string{}
		for i, kind := range []string{"idea", "bug", "idea", "nps", "idea"} {
			body := map[string]any{"kind": kind, "body": "Góp ý " + strconv.Itoa(i), "page_path": "/sales"}
			if kind == "nps" {
				body["score"] = 9
			}
			fb := send[adminRow](t, shop, http.MethodPost, "/feedback", body)
			byKind[kind] = append(byKind[kind], field(t, fb, "id"))
			s.clk.Advance(time.Second)
		}
		rows := samePagesAnyLimit(t, adm, "/feedback", "id")
		requireKeys(t, "feedback ids", rowKeys(t, rows, "id"), slices.Concat(byKind["idea"], byKind["bug"], byKind["nps"])...)
		newestFirst(t, "feedback", rows, "created_at")
		ideas := samePagesAnyLimit(t, adm, "/feedback?kind=idea", "id")
		requireKeys(t, "idea ids", rowKeys(t, ideas, "id"), byKind["idea"]...)
		for _, r := range ideas {
			if field(t, r, "kind") != "idea" {
				t.Errorf("feedback %s of kind %s listed under idea", field(t, r, "id"), field(t, r, "kind"))
			}
		}
		newestFirst(t, "idea feedback", ideas, "created_at")
		expect(t, adm.do(http.MethodGet, adminV1+"/feedback?kind=complaint", nil), http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("bad page", func(t *testing.T) {
		expect(t, adm.do(http.MethodGet, adminV1+"/feedback?limit=0", nil), http.StatusUnprocessableEntity, "invalid_limit")
		expect(t, adm.do(http.MethodGet, adminV1+"/partners?cursor=not-a-cursor", nil), http.StatusUnprocessableEntity, "invalid_cursor")
		expect(t, adm.do(http.MethodGet, adminV1+"/tenants?limit=201", nil), http.StatusUnprocessableEntity, "invalid_limit")
		expect(t, adm.do(http.MethodGet, adminV1+"/incidents?limit=0", nil), http.StatusUnprocessableEntity, "invalid_limit")
	})
}

func TestAdminDirectoryListsEveryAdminByName(t *testing.T) {
	s := newControlStack(t)
	adm, _ := s.adminSignIn(t)

	prefix := "Danh bạ " + unique()
	made := map[string]string{}
	for _, suffix := range []string{"C", "A", "B"} {
		enrol, err := s.auth.CreateAdmin(context.Background(), admin.NewAdmin{
			Email: uniqueEmail("admin"), FullName: prefix + " " + suffix, Password: adminPassword,
		})
		if err != nil {
			t.Fatal(err)
		}
		made[suffix] = enrol.AdminID.String()
	}
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.platform_admins SET status = 'disabled' WHERE id = $1`, made["B"]); err != nil {
		t.Fatal(err)
	}

	rows := samePagesAnyLimit(t, adm, "/admins", "id")
	// Names sort by the database collation, so only the order of these three is asserted here.
	var mine []adminRow
	for _, r := range rows {
		if strings.HasPrefix(field(t, r, "full_name"), prefix) {
			mine = append(mine, r)
		}
	}
	if len(mine) != 3 {
		t.Fatalf("admins named %s = %v, want 3", prefix, mine)
	}
	for i, suffix := range []string{"A", "B", "C"} {
		r := mine[i]
		if field(t, r, "id") != made[suffix] || field(t, r, "full_name") != prefix+" "+suffix || !strings.HasPrefix(field(t, r, "email"), "admin-") {
			t.Errorf("admin %d = %v, want %s %s", i, r, prefix, suffix)
		}
		if active := field(t, r, "active"); active != strconv.FormatBool(suffix != "B") {
			t.Errorf("admin %s active = %s", suffix, active)
		}
	}
	for _, r := range rows {
		for k := range r {
			if !slices.Contains([]string{"id", "full_name", "email", "active"}, k) {
				t.Errorf("admin directory row has the field %q", k)
			}
		}
	}

	expect(t, s.adminBrowser(t).do(http.MethodGet, adminV1+"/admins", nil), http.StatusUnauthorized, "")
}

type flagChangeOut struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      *uuid.UUID `json:"tenant_id"`
	Enabled       *bool      `json:"enabled"`
	AdminID       uuid.UUID  `json:"admin_id"`
	AdminFullName string     `json:"admin_full_name"`
}

func TestRemovingAStoreFlagOverrideFallsBackToTheGlobalValue(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	tenant := tenantOf(t, s.signIn(t, o))
	adm, adminID := s.adminSignIn(t)
	key := "probe." + unique()
	ctx := context.Background()

	adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", map[string]any{"key": key, "enabled": false})
	adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", map[string]any{"key": key, "tenant_id": tenant, "enabled": true})
	// flagOf reads the flag through the server's cached reader.
	flagOf := func() (bool, bool, error) {
		tx, err := testkit.SuperDB(t).BeginTxx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SET LOCAL search_path = tally`); err != nil {
			t.Fatal(err)
		}
		return s.domains.Flags.Flag(ctx, tx, key, tenant)
	}
	if on, _, err := flagOf(); err != nil || !on {
		t.Fatalf("flag with the store's override = %v %v, want on", on, err)
	}

	path := adminV1 + "/tenants/" + tenant.String() + "/feature-flags/" + key
	rec := adm.do(http.MethodDelete, path, nil)
	expect(t, rec, http.StatusNoContent, "")
	if rec.Body.Len() != 0 {
		t.Errorf("removal body = %s, want none", rec.Body)
	}

	// The cached override is gone with the row: the store reads the global value at once.
	if on, found, err := flagOf(); err != nil || on || !found {
		t.Errorf("flag after the removal = %v found %v %v, want the global off", on, found, err)
	}
	listed := adminSend[struct {
		Data      []featureflag.Flag `json:"data"`
		KnownKeys []string           `json:"known_keys"`
	}](t, adm, http.MethodGet, "/feature-flags", nil)
	for _, f := range listed.Data {
		if f.Key == key && f.TenantID != nil {
			t.Errorf("the store's override is still listed: %+v", f)
		}
	}
	if !slices.Contains(listed.KnownKeys, "module:pos") || !slices.Contains(listed.KnownKeys, "module:ai_qa") || slices.Contains(listed.KnownKeys, key) {
		t.Errorf("known_keys = %v, want the module flags only", listed.KnownKeys)
	}

	changes := adminSend[dataOf[flagChangeOut]](t, adm, http.MethodGet, "/feature-flags/"+key+"/changes", nil).Data
	if len(changes) != 3 {
		t.Fatalf("changes of %s = %+v, want set global, set override, removal", key, changes)
	}
	removal := changes[0]
	if removal.Enabled != nil || removal.TenantID == nil || *removal.TenantID != tenant || removal.AdminID != adminID {
		t.Errorf("newest change = %+v, want the removal by %s for %s", removal, adminID, tenant)
	}
	for _, c := range changes {
		if c.AdminFullName != "Quản trị viên" {
			t.Errorf("change %s admin_full_name = %q", c.ID, c.AdminFullName)
		}
	}
	if changes[1].Enabled == nil || !*changes[1].Enabled || changes[2].Enabled == nil || *changes[2].Enabled {
		t.Errorf("earlier changes = %+v %+v, want override on then global off", changes[1], changes[2])
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'feature_flag.override_removed'
		AND entity_type = 'feature_flag' AND actor_id = $2 AND before->>'key' = $3 AND (before->>'enabled')::boolean`,
		tenant, adminID, key); n != 1 {
		t.Errorf("removal audit events = %d, want 1", n)
	}

	expect(t, adm.do(http.MethodDelete, path, nil), http.StatusNotFound, "flag_override_not_found")
	expect(t, adm.do(http.MethodDelete, adminV1+"/tenants/"+uuid.NewString()+"/feature-flags/"+key, nil), http.StatusNotFound, "not_found")
	expect(t, adm.do(http.MethodDelete, adminV1+"/tenants/"+tenant.String()+"/feature-flags/Bad%20Key", nil), http.StatusNotFound, "not_found")
	if n := count(t, `SELECT count(*) FROM tally.feature_flag_changes WHERE key = $1`, key); n != 3 {
		t.Errorf("changes after refused removals = %d, want 3", n)
	}
	noCSRF := *adm
	noCSRF.csrf = ""
	expect(t, noCSRF.do(http.MethodDelete, path, nil), http.StatusForbidden, "")
}
