//go:build integration

package app_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/support"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// storeNames reads the slug and display name of a store.
func storeNames(t *testing.T, tenant uuid.UUID) (string, string) {
	t.Helper()
	var slug, name string
	if err := testkit.SuperDB(t).QueryRow(`SELECT slug, display_name FROM tally.tenants WHERE id = $1`, tenant).Scan(&slug, &name); err != nil {
		t.Fatal(err)
	}
	return slug, name
}

// namesStore requires slug and name to be the store's, or both nil when want is empty.
func namesStore(t *testing.T, what string, slug, name *string, wantSlug, wantName string) {
	t.Helper()
	if wantSlug == "" {
		if slug != nil || name != nil {
			t.Errorf("%s names a store: %v %v, want none", what, slug, name)
		}
		return
	}
	if slug == nil || name == nil || *slug != wantSlug || *name != wantName {
		t.Errorf("%s names %v %v, want %s %s", what, slug, name, wantSlug, wantName)
	}
}

func TestFlagValuesAndChangesNameTheStoreOfAnOverride(t *testing.T) {
	s := newControlStack(t)
	tenant := tenantOf(t, s.signIn(t, s.signUp(t)))
	slug, name := storeNames(t, tenant)
	adm, _ := s.adminSignIn(t)
	key := "probe." + unique()

	global := adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", map[string]any{"key": key, "enabled": false})
	namesStore(t, "the global value set", global.TenantSlug, global.TenantName, "", "")
	override := adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", map[string]any{"key": key, "tenant_id": tenant, "enabled": true})
	namesStore(t, "the override set", override.TenantSlug, override.TenantName, slug, name)

	listed := adminSend[dataOf[featureflag.Flag]](t, adm, http.MethodGet, "/feature-flags", nil).Data
	var rows int
	for _, f := range listed {
		if f.Key != key {
			continue
		}
		rows++
		if f.TenantID == nil {
			namesStore(t, "the listed global value", f.TenantSlug, f.TenantName, "", "")
		} else {
			namesStore(t, "the listed override", f.TenantSlug, f.TenantName, slug, name)
		}
	}
	if rows != 2 {
		t.Fatalf("listed values of %s = %d, want the global one and the override", key, rows)
	}

	expect(t, adm.do(http.MethodDelete, adminV1+"/tenants/"+tenant.String()+"/feature-flags/"+key, nil), http.StatusNoContent, "")
	changes := adminSend[dataOf[featureflag.Change]](t, adm, http.MethodGet, "/feature-flags/"+key+"/changes", nil).Data
	if len(changes) != 3 {
		t.Fatalf("changes of %s = %+v, want set global, set override, removal", key, changes)
	}
	for i, c := range changes {
		if c.TenantID == nil {
			namesStore(t, "change "+strconv.Itoa(i), c.TenantSlug, c.TenantName, "", "")
		} else {
			namesStore(t, "change "+strconv.Itoa(i), c.TenantSlug, c.TenantName, slug, name)
		}
	}
	if changes[0].TenantID == nil || changes[2].TenantID != nil {
		t.Errorf("changes = %+v, want the removal newest and the global value oldest", changes)
	}
}

func TestAdminSupportSessionsNameTheirStore(t *testing.T) {
	s := newControlStack(t)
	adm, adminID := s.adminSignIn(t)
	want := map[uuid.UUID][2]string{}
	for i := range 2 {
		b := s.signIn(t, s.signUp(t))
		tenant := tenantOf(t, b)
		slug, name := storeNames(t, tenant)
		ticket := send[support.TicketDTO](t, b, http.MethodPost, "/support-tickets",
			map[string]any{"subject": "Cần hỗ trợ " + strconv.Itoa(i), "body": "Không mở được báo cáo."})
		adminSend[support.TicketDTO](t, adm, http.MethodPatch, "/support-tickets/"+ticket.ID.String(),
			map[string]any{"assigned_admin_id": adminID, "status": "in_progress"})
		session := send[support.SessionDTO](t, b, http.MethodPost, "/support-sessions", map[string]any{
			"ticket_id": ticket.ID, "modules": []string{"reports"}, "hours": 1,
		})
		want[session.ID] = [2]string{slug, name}
	}

	got := adminSend[dataOf[support.AdminSessionDTO]](t, adm, http.MethodGet, "/support-sessions", nil).Data
	if len(got) != len(want) {
		t.Fatalf("admin sessions = %+v, want the %d granted", got, len(want))
	}
	for _, sess := range got {
		names, ok := want[sess.ID]
		if !ok {
			t.Errorf("session %s was not granted to this admin", sess.ID)
			continue
		}
		if sess.TenantSlug != names[0] || sess.TenantName != names[1] {
			t.Errorf("session %s names %s %s, want %s %s", sess.ID, sess.TenantSlug, sess.TenantName, names[0], names[1])
		}
		if sess.State != "active" || sess.AdminID != adminID {
			t.Errorf("session %+v, want active for %s", sess, adminID)
		}
	}
}

func TestIncidentListKeepsTheAskedStatus(t *testing.T) {
	s := newControlStack(t)
	adm, _ := s.adminSignIn(t)

	byStatus := map[string][]string{}
	for i, status := range []string{"investigating", "identified", "investigating", "monitoring", "resolved", "investigating", "resolved"} {
		inc := adminSend[adminRow](t, adm, http.MethodPost, "/incidents", map[string]any{
			"title": "Sự cố " + strconv.Itoa(i), "impact": "minor", "status": status, "message": "Đang kiểm tra.",
		})
		byStatus[status] = append(byStatus[status], field(t, inc, "id"))
		s.clk.Advance(time.Second)
	}
	reopened := byStatus["resolved"][0]
	adminSend[adminRow](t, adm, http.MethodPatch, "/incidents/"+reopened, map[string]any{"status": "identified", "message": "Lỗi quay lại."})
	byStatus["identified"] = append(byStatus["identified"], reopened)
	byStatus["resolved"] = byStatus["resolved"][1:]
	resolved := byStatus["investigating"][0]
	adminSend[adminRow](t, adm, http.MethodPatch, "/incidents/"+resolved, map[string]any{"status": "resolved", "message": "Đã khắc phục."})
	byStatus["resolved"] = append(byStatus["resolved"], resolved)
	byStatus["investigating"] = byStatus["investigating"][1:]

	for _, status := range []string{"investigating", "identified", "monitoring", "resolved"} {
		t.Run(status, func(t *testing.T) {
			rows := samePagesAnyLimit(t, adm, "/incidents?status="+status, "id")
			requireKeys(t, status+" incidents", rowKeys(t, rows, "id"), byStatus[status]...)
			for _, r := range rows {
				if got := field(t, r, "status"); got != status {
					t.Errorf("incident %s is %s, listed under %s", field(t, r, "id"), got, status)
				}
			}
			newestFirst(t, status+" incidents", rows, "started_at")
		})
	}

	t.Run("open", func(t *testing.T) {
		rows := samePagesAnyLimit(t, adm, "/incidents?status=open", "id")
		var want []string
		for _, status := range []string{"investigating", "identified", "monitoring"} {
			want = append(want, byStatus[status]...)
		}
		requireKeys(t, "open incidents", rowKeys(t, rows, "id"), want...)
		for _, r := range rows {
			if field(t, r, "status") == "resolved" || field(t, r, "resolved_at") != "" {
				t.Errorf("open list has the resolved incident %s", field(t, r, "id"))
			}
		}
		newestFirst(t, "open incidents", rows, "started_at")
	})

	t.Run("an unknown status is refused", func(t *testing.T) {
		for _, status := range []string{"closed", "Resolved", "%20"} {
			rec := adm.do(http.MethodGet, adminV1+"/incidents?status="+status, nil)
			expect(t, rec, http.StatusUnprocessableEntity, "validation_failed")
			if f := testkit.DecodeJSON[httpx.ErrorBody](t, rec).Error.Fields["status"]; f != "invalid" {
				t.Errorf("status %q fields.status = %v, want invalid", status, f)
			}
		}
	})
}

func TestSettingAFlagKeepsItsHistoryAndAuditsAStoreOverride(t *testing.T) {
	s := newControlStack(t)
	tenant := tenantOf(t, s.signIn(t, s.signUp(t)))
	adm, adminID := s.adminSignIn(t)
	key := "probe." + unique()
	set := func(tenantID *uuid.UUID, enabled bool) {
		t.Helper()
		body := map[string]any{"key": key, "enabled": enabled}
		if tenantID != nil {
			body["tenant_id"] = *tenantID
		}
		adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", body)
	}
	events := func() int {
		return countRows(t, tenant, `SELECT count(*) FROM audit_events WHERE action = 'feature_flag.override_set'`)
	}

	// The global value's audit is its history row: no store's audit log gets it.
	set(nil, true)
	if n := count(t, `SELECT count(*) FROM tally.feature_flag_changes WHERE key = $1 AND tenant_id IS NULL AND enabled
		AND admin_id = $2`, key, adminID); n != 1 {
		t.Errorf("history rows of the global value = %d, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE entity_type = 'feature_flag'
		AND (before->>'key' = $1 OR after->>'key' = $1)`, key); n != 0 {
		t.Errorf("the global value went to a store's audit log: %d events", n)
	}

	set(&tenant, true)
	set(&tenant, false)
	if n := count(t, `SELECT count(*) FROM tally.feature_flag_changes WHERE key = $1 AND tenant_id = $2 AND admin_id = $3`,
		key, tenant, adminID); n != 2 {
		t.Errorf("history rows of the override = %d, want 2", n)
	}
	if n := events(); n != 2 {
		t.Fatalf("override audit events = %d, want 2", n)
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE entity_type = 'feature_flag' AND tenant_id <> $2
		AND (before->>'key' = $1 OR after->>'key' = $1)`, key, tenant); n != 0 {
		t.Errorf("the override went to another store's audit log: %d events", n)
	}
	if n := countRows(t, tenant, `SELECT count(*) FROM audit_events WHERE action = 'feature_flag.override_set'
		AND entity_type = 'feature_flag' AND actor_type = 'platform_admin' AND actor_id = $1 AND source = 'web'
		AND request_id IS NOT NULL AND before IS NULL AND after = jsonb_build_object('key', $2::text, 'enabled', true)`,
		adminID, key); n != 1 {
		t.Errorf("first override events with no value before = %d, want 1", n)
	}
	if n := countRows(t, tenant, `SELECT count(*) FROM audit_events WHERE action = 'feature_flag.override_set'
		AND actor_id = $1 AND before = jsonb_build_object('key', $2::text, 'enabled', true)
		AND after = jsonb_build_object('key', $2::text, 'enabled', false)`, adminID, key); n != 1 {
		t.Errorf("changed override events from on to off = %d, want 1", n)
	}

	expect(t, adm.do(http.MethodPut, adminV1+"/feature-flags", map[string]any{"key": key, "tenant_id": uuid.New(), "enabled": true}),
		http.StatusNotFound, "not_found")
	if n := count(t, `SELECT count(*) FROM tally.feature_flag_changes WHERE key = $1`, key); n != 3 {
		t.Errorf("changes after a refused override = %d, want 3", n)
	}
	if n := events(); n != 2 {
		t.Errorf("override audit events after a refused override = %d, want 2", n)
	}
}

func TestFlagValuesAndChangesLeaveOutAReplacedSandbox(t *testing.T) {
	s := newControlStack(t)
	tenant := tenantOf(t, s.signIn(t, s.signUp(t)))
	adm, _ := s.adminSignIn(t)
	key := "probe." + unique()

	adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", map[string]any{"key": key, "enabled": false})
	adminSend[featureflag.Flag](t, adm, http.MethodPut, "/feature-flags", map[string]any{"key": key, "tenant_id": tenant, "enabled": true})
	replaceTenant(t, tenant, true)

	for _, f := range adminSend[dataOf[featureflag.Flag]](t, adm, http.MethodGet, "/feature-flags", nil).Data {
		if f.Key == key && f.TenantID != nil {
			t.Errorf("listed value of a replaced sandbox: %+v", f)
		}
	}
	changes := adminSend[dataOf[featureflag.Change]](t, adm, http.MethodGet, "/feature-flags/"+key+"/changes", nil).Data
	if len(changes) != 1 || changes[0].TenantID != nil {
		t.Errorf("changes of %s = %+v, want only the global value", key, changes)
	}
}
