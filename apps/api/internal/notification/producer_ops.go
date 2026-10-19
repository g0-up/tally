package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// POSSyncExceptions raises one pos_sync_exception per offline sale parked in the sync exception
// queue and still open.
type POSSyncExceptions struct{}

// Kinds implements Producer.
func (POSSyncExceptions) Kinds() []string { return []string{KindPOSSyncException} }

// Sweep implements Producer.
func (POSSyncExceptions) Sweep(ctx context.Context, q db.Querier, _ SweepTime) ([]Notice, error) {
	return openSyncExceptions(ctx, q, nil)
}

func openSyncExceptions(ctx context.Context, q db.Querier, id *uuid.UUID) ([]Notice, error) {
	var rows []struct {
		ID        uuid.UUID `db:"id"`
		ErrorCode string    `db:"error_code"`
		Detail    *string   `db:"error_detail"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT id, error_code, error_detail, created_at FROM pos_sync_exceptions
		WHERE status = 'open' AND ($1::uuid IS NULL OR id = $1)
		ORDER BY created_at, id`, id); err != nil {
		return nil, fmt.Errorf("notification: scan sync exceptions: %w", err)
	}
	out := make([]Notice, 0, len(rows))
	for _, r := range rows {
		body := "Mã lỗi " + r.ErrorCode
		if r.Detail != nil && *r.Detail != "" {
			body += ": " + *r.Detail
		}
		out = append(out, Notice{
			Kind: KindPOSSyncException, DedupeKey: key(KindPOSSyncException, r.ID), Severity: SeverityBad,
			Title:      "Đơn bán ngoại tuyến chưa đồng bộ được",
			Body:       ptr(body),
			EntityType: ptr("pos_sync_exception"), EntityID: ptr(r.ID), ActionPath: ptr("/sales/pos?exceptions=open"),
		})
	}
	return out, nil
}

// DisputedStatements raises one statement_disputed per statement the partner disputed.
type DisputedStatements struct{}

// Kinds implements Producer.
func (DisputedStatements) Kinds() []string { return []string{KindStatementDisputed} }

// Sweep implements Producer.
func (DisputedStatements) Sweep(ctx context.Context, q db.Querier, _ SweepTime) ([]Notice, error) {
	return disputedStatements(ctx, q, nil)
}

func disputedStatements(ctx context.Context, q db.Querier, id *uuid.UUID) ([]Notice, error) {
	var rows []struct {
		ID    uuid.UUID `db:"id"`
		DocNo string    `db:"doc_no"`
		Name  string    `db:"name"`
		Note  *string   `db:"dispute_note"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT st.id, st.doc_no, p.name, st.dispute_note
		FROM statements st JOIN parties p ON p.id = st.party_id
		WHERE st.status = 'disputed' AND ($1::uuid IS NULL OR st.id = $1)
		ORDER BY st.id`, id); err != nil {
		return nil, fmt.Errorf("notification: scan disputed statements: %w", err)
	}
	out := make([]Notice, 0, len(rows))
	for _, r := range rows {
		n := Notice{
			Kind: KindStatementDisputed, DedupeKey: key(KindStatementDisputed, r.ID), Severity: SeverityWarn,
			Title:      fmt.Sprintf("%s không khớp sao kê %s", r.Name, r.DocNo),
			EntityType: ptr("statement"), EntityID: ptr(r.ID), ActionPath: ptr("/debts/statements?id=" + r.ID.String()),
			DocNo: ptr(r.DocNo),
		}
		if r.Note != nil && *r.Note != "" {
			n.Body = ptr(*r.Note)
		}
		out = append(out, n)
	}
	return out, nil
}

// trialWarningDays is how long before the end of a trial the owner is reminded.
const trialWarningDays = 7

// SubscriptionStatus raises one subscription_status for the tenant while its subscription is in
// grace or read-only, or its trial ends within a week.
type SubscriptionStatus struct{}

// Kinds implements Producer.
func (SubscriptionStatus) Kinds() []string { return []string{KindSubscriptionStatus} }

// Sweep implements Producer. subscriptions is a platform table without row-level security, so the
// tenant filter is explicit.
func (SubscriptionStatus) Sweep(ctx context.Context, q db.Querier, at SweepTime) ([]Notice, error) {
	var s struct {
		TenantID    uuid.UUID  `db:"tenant_id"`
		Status      string     `db:"status"`
		TrialEnds   *time.Time `db:"trial_ends_on"`
		GraceEnds   *time.Time `db:"grace_ends_on"`
		ReadOnlyEnd *time.Time `db:"read_only_ends_on"`
	}
	err := q.QueryRowxContext(ctx, `
		SELECT tenant_id, status, trial_ends_on, grace_ends_on, read_only_ends_on FROM subscriptions
		WHERE tenant_id = current_setting('app.tenant_id')::uuid
		ORDER BY created_at DESC, id DESC LIMIT 1`).StructScan(&s)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notification: read subscription: %w", err)
	}
	n := Notice{
		Kind: KindSubscriptionStatus, DedupeKey: key(KindSubscriptionStatus, s.TenantID),
		EntityType: ptr("subscription"), ActionPath: ptr("/settings/subscription"),
	}
	switch {
	case s.Status == "grace":
		n.Severity, n.Title = SeverityWarn, "Gói dịch vụ đã quá hạn thanh toán"
		n.Body = untilBody("Cửa hàng vẫn dùng đầy đủ đến", s.GraceEnds)
	case s.Status == "read_only":
		n.Severity, n.Title = SeverityBad, "Cửa hàng đang ở chế độ chỉ xem"
		n.Body = untilBody("Gia hạn để ghi chứng từ; dữ liệu được giữ đến", s.ReadOnlyEnd)
	case s.Status == "trialing" && s.TrialEnds != nil && !s.TrialEnds.After(at.Today.AddDate(0, 0, trialWarningDays)):
		n.Severity, n.Title = SeverityInfo, "Thời gian dùng thử sắp kết thúc"
		n.Body = untilBody("Dùng thử đến hết", s.TrialEnds)
	default:
		return nil, nil
	}
	return []Notice{n}, nil
}

func untilBody(prefix string, day *time.Time) *string {
	if day == nil {
		return nil
	}
	return ptr(prefix + " " + day.Format("02/01/2006") + ".")
}

// ExportReady raises one export_ready per finished export whose download link has not expired.
type ExportReady struct{}

// Kinds implements Producer.
func (ExportReady) Kinds() []string { return []string{KindExportReady} }

// Sweep implements Producer.
func (ExportReady) Sweep(ctx context.Context, q db.Querier, at SweepTime) ([]Notice, error) {
	return readyExports(ctx, q, at, nil)
}

func readyExports(ctx context.Context, q db.Querier, at SweepTime, id *uuid.UUID) ([]Notice, error) {
	var rows []struct {
		ID        uuid.UUID `db:"id"`
		Kind      string    `db:"kind"`
		ReportKey *string   `db:"report_key"`
		RowCount  *int64    `db:"row_count"`
		ExpiresAt time.Time `db:"expires_at"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT id, kind, params->>'report_key' AS report_key, row_count, expires_at FROM export_jobs
		WHERE status = 'succeeded' AND expires_at > $1 AND ($2::uuid IS NULL OR id = $2)
		ORDER BY id`, at.Now, id); err != nil {
		return nil, fmt.Errorf("notification: scan finished exports: %w", err)
	}
	out := make([]Notice, 0, len(rows))
	for _, r := range rows {
		what := "Xuất toàn bộ dữ liệu"
		if r.ReportKey != nil {
			what = "Xuất báo cáo " + *r.ReportKey
		}
		body := "Tải về trước " + r.ExpiresAt.In(at.Loc).Format("15:04 02/01/2006") + "."
		if r.RowCount != nil {
			body = fmt.Sprintf("%d dòng. %s", *r.RowCount, body)
		}
		// No action path: the file is downloaded through the export job, not opened on a screen.
		out = append(out, Notice{
			Kind: KindExportReady, DedupeKey: key(KindExportReady, r.ID), Severity: SeverityInfo,
			Title: what + " đã xong", Body: ptr(body),
			EntityType: ptr("export_job"), EntityID: ptr(r.ID),
		})
	}
	return out, nil
}
