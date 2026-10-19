//go:build integration

package audit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestRecordWritesRedactedRow(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	rec := audit.NewRecorder(testkit.Clock())
	user, entity := ids.New(), ids.New()
	actor := audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: user, Source: audit.SourceWeb, RequestID: "req-1", IP: "203.0.113.9"}

	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		return rec.Record(ctx, q, actor, audit.Event{
			Action: "user.update", EntityType: "user", EntityID: entity, DocNo: "PN-0001",
			Before: map[string]any{"password_hash": "x"}, After: map[string]any{"display_name": "An"},
		})
	})

	var row struct {
		ActorType string `db:"actor_type"`
		Action    string `db:"action"`
		DocNo     string `db:"doc_no"`
		Before    string `db:"before"`
		After     string `db:"after"`
		IP        string `db:"ip"`
		RequestID string `db:"request_id"`
	}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT actor_type, action, doc_no, before::text, after::text, host(ip) AS ip, request_id
			FROM audit_events WHERE entity_id = $1`, entity).StructScan(&row)
	})
	if row.ActorType != "owner" || row.Action != "user.update" || row.DocNo != "PN-0001" || row.IP != "203.0.113.9" || row.RequestID != "req-1" {
		t.Errorf("row = %+v", row)
	}
	if strings.Contains(row.Before, `"x"`) || !strings.Contains(row.After, "An") {
		t.Errorf("before=%s after=%s", row.Before, row.After)
	}
}

func TestRecordRejectsIncompleteInput(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	rec := audit.NewRecorder(testkit.Clock())
	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		if err := rec.Record(ctx, q, audit.Actor{TenantID: tn.ID, Type: "bot", Source: audit.SourceWeb}, audit.Event{Action: "a", EntityType: "b"}); err == nil {
			t.Error("invalid actor accepted")
		}
		sys := audit.Actor{TenantID: tn.ID, Type: audit.ActorSystem, Source: audit.SourceWorker, IP: "garbage"}
		if err := rec.Record(ctx, q, sys, audit.Event{Action: "a"}); err == nil {
			t.Error("event without entity type accepted")
		}
		if err := rec.Record(ctx, q, sys, audit.Event{Action: "a", EntityType: "b", After: make(chan int)}); err == nil {
			t.Error("unmarshalable snapshot accepted")
		}
		return rec.Record(ctx, q, sys, audit.Event{Action: "job.run", EntityType: "job"})
	})
	if err != nil {
		t.Fatal(err)
	}
}
