package debt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// TopicStatementDisputed is published when a party disputes a sent statement.
const TopicStatementDisputed = "statement.disputed"

const statementColumns = `st.id, st.tenant_id, st.doc_no, st.party_id, st.direction, st.period_from, st.period_to, st.status,
	st.current_version, st.sent_version, st.sent_at, st.dispute_note, st.created_at, st.updated_at, st.version`

const statementVersionColumns = `v.id, v.statement_id, v.version_no, v.opening_balance, v.closing_balance, v.snapshot,
	v.created_by, v.created_at`

// Statement actions and the statuses each may start from (StatementStatus transitions). Generating
// a sent or disputed statement freezes the old version and starts a new draft one.
const (
	statementGenerate = "statement.generate"
	statementSend     = "statement.send"
	statementConfirm  = "statement.confirm"
	statementDispute  = "statement.dispute"
)

var statementTransitions = map[string]struct {
	from []string
	to   string
}{
	statementGenerate: {from: []string{StatementDraft, StatementSent, StatementDisputed}, to: StatementDraft},
	statementSend:     {from: []string{StatementDraft}, to: StatementSent},
	statementConfirm:  {from: []string{StatementSent}, to: StatementConfirmed},
	statementDispute:  {from: []string{StatementSent}, to: StatementDisputed},
}

// checkTransition returns the status action leads to from status, or the conflict to report.
func checkTransition(action, status string) (string, error) {
	t := statementTransitions[action]
	for _, from := range t.from {
		if from == status {
			return t.to, nil
		}
	}
	if status == StatementConfirmed {
		return "", errStatementConfirmed()
	}
	return "", errStatementState("statement_invalid_state", fmt.Sprintf("A %s statement cannot be %s", status, pastTense(action)))
}

func pastTense(action string) string {
	switch action {
	case statementSend:
		return "sent"
	case statementConfirm:
		return "confirmed"
	case statementDispute:
		return "disputed"
	}
	return "generated again"
}

// StatementInput opens a statement for one party and ledger over a period.
type StatementInput struct {
	PartyID    uuid.UUID
	Direction  string
	PeriodFrom time.Time
	PeriodTo   time.Time
}

// StatementLine is one row of a statement: a document that raised the balance or a settlement
// that lowered it (a reversal raises it again), with the running balance.
type StatementLine struct {
	Date     string      `json:"date"`
	Kind     string      `json:"kind"`
	DocNo    string      `json:"doc_no"`
	DueDate  *string     `json:"due_date,omitempty"`
	Increase money.Money `json:"increase"`
	Decrease money.Money `json:"decrease"`
	Balance  money.Money `json:"balance"`
}

// StatementSnapshot is the frozen content of a version.
type StatementSnapshot struct {
	Party          partyRef        `json:"party"`
	Direction      string          `json:"direction"`
	PeriodFrom     string          `json:"period_from"`
	PeriodTo       string          `json:"period_to"`
	OpeningBalance money.Money     `json:"opening_balance"`
	ClosingBalance money.Money     `json:"closing_balance"`
	Lines          []StatementLine `json:"lines"`
}

// StatementVersionView is one version of a statement with its derived status.
type StatementVersionView struct {
	Statement Statement
	Version   StatementVersion
	Status    string
}

// write runs fn in a tenant transaction with the write context of actor.
func (s *Service) write(ctx context.Context, actor audit.Actor, fn func(w writeCtx) error) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		w, err := s.writeCtx(ctx, q, actor)
		if err != nil {
			return err
		}
		return fn(w)
	})
}

// CreateStatement opens a DS and generates its first version. A retry with the same optional
// Idempotency-Key replays the statement first created (replayed true).
func (s *Service) CreateStatement(ctx context.Context, actor audit.Actor, key *idempotency.Key, in StatementInput) (Statement, bool, error) {
	fields := map[string]string{}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if in.Direction != Receivable && in.Direction != Payable {
		fields["direction"] = "invalid"
	}
	if in.PeriodFrom.IsZero() {
		fields["period_from"] = "required"
	}
	if in.PeriodTo.IsZero() {
		fields["period_to"] = "required"
	} else if in.PeriodTo.Before(in.PeriodFrom) {
		fields["period_to"] = "before_period_from"
	}
	if len(fields) > 0 {
		return Statement{}, false, errInvalid(fields)
	}
	var out Statement
	var replayed bool
	err := s.write(ctx, actor, func(w writeCtx) error {
		var err error
		replayed, err = idempotency.Once(ctx, w.q, s.idem, w.tenantID, ScopeStatementCreate, key, http.StatusCreated, &out, func() error {
			return s.createStatement(ctx, actor, w, in, &out)
		})
		return err
	})
	return out, replayed, err
}

func (s *Service) createStatement(ctx context.Context, actor audit.Actor, w writeCtx, in StatementInput, out *Statement) error {
	if in.PeriodTo.After(w.date) {
		return errField("validation_failed", "period_to", "in_future", "The period cannot end after today")
	}
	if _, err := getParty(ctx, w.q, in.PartyID); err != nil {
		return err
	}
	docNo, err := s.docs.Next(ctx, w.q, w.tenantID, docseq.PrefixStatement)
	if err != nil {
		return err
	}
	st := Statement{
		ID: ids.New(), TenantID: w.tenantID, DocNo: docNo, PartyID: in.PartyID, Direction: in.Direction,
		PeriodFrom: in.PeriodFrom, PeriodTo: in.PeriodTo, Status: StatementDraft, CreatedAt: w.now, UpdatedAt: w.now, Version: 1,
	}
	if _, err := w.q.ExecContext(ctx, `
		INSERT INTO statements (id, tenant_id, doc_no, party_id, direction, period_from, period_to, status, current_version,
		                        created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, $9, 1)`,
		st.ID, st.TenantID, st.DocNo, st.PartyID, st.Direction, st.PeriodFrom, st.PeriodTo, st.Status, st.CreatedAt); err != nil {
		return fmt.Errorf("debt: insert statement: %w", err)
	}
	if err := s.generateVersion(ctx, w, &st); err != nil {
		return err
	}
	if _, err := w.q.ExecContext(ctx, `UPDATE statements SET current_version = $2 WHERE id = $1`, st.ID, st.CurrentVersion); err != nil {
		return fmt.Errorf("debt: update statement: %w", err)
	}
	*out = st
	return s.audit.Record(ctx, w.q, actor, audit.Event{Action: "statement.create", EntityType: "statement", EntityID: st.ID,
		DocNo: st.DocNo, After: st})
}

// GenerateStatement freezes the current version and generates the next one from the ledger as it
// is now; the statement is a draft again. A confirmed statement never changes.
func (s *Service) GenerateStatement(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Statement, error) {
	return s.applyStatementTransition(ctx, actor, id, version, statementGenerate, "")
}

// SendStatement marks the current version as sent to the party; it stays frozen.
func (s *Service) SendStatement(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Statement, error) {
	return s.applyStatementTransition(ctx, actor, id, version, statementSend, "")
}

// ConfirmStatement records that the party agreed with the sent version.
func (s *Service) ConfirmStatement(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) (Statement, error) {
	return s.applyStatementTransition(ctx, actor, id, version, statementConfirm, "")
}

// DisputeStatement records that the party disputes the sent version, with its note.
func (s *Service) DisputeStatement(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, note string) (Statement, error) {
	note = strings.TrimSpace(note)
	switch {
	case note == "":
		return Statement{}, errInvalid(map[string]string{"note": "required"})
	case utf8.RuneCountInString(note) > maxNoteLen:
		return Statement{}, errInvalid(map[string]string{"note": "too_long"})
	}
	return s.applyStatementTransition(ctx, actor, id, version, statementDispute, note)
}

func (s *Service) applyStatementTransition(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, action, note string) (Statement, error) {
	var out Statement
	err := s.write(ctx, actor, func(w writeCtx) error {
		st, err := getStatement(ctx, w.q, id, true)
		if err != nil {
			return err
		}
		if st.Version != version {
			return apperr.NewPreconditionFailed("The statement changed; reload it")
		}
		to, err := checkTransition(action, st.Status)
		if err != nil {
			return err
		}
		before := st
		st.Status, st.UpdatedAt = to, w.now
		switch action {
		case statementGenerate:
			if err := s.generateVersion(ctx, w, &st); err != nil {
				return err
			}
			st.DisputeNote = nil
		case statementSend:
			n, at := st.CurrentVersion, w.now
			st.SentVersion, st.SentAt = &n, &at
		case statementDispute:
			st.DisputeNote = &note
		}
		if _, err := w.q.ExecContext(ctx, `
			UPDATE statements SET status = $2, current_version = $3, sent_version = $4, sent_at = $5, dispute_note = $6,
			       updated_at = $7, version = version + 1
			WHERE id = $1`, st.ID, st.Status, st.CurrentVersion, st.SentVersion, st.SentAt, st.DisputeNote, w.now); err != nil {
			return fmt.Errorf("debt: update statement: %w", err)
		}
		st.Version++
		if action == statementDispute {
			if _, err := s.outbox.Publish(ctx, w.q, w.tenantID, outbox.Event{Topic: TopicStatementDisputed, Payload: map[string]any{
				"statement_id": st.ID, "doc_no": st.DocNo, "party_id": st.PartyID, "version_no": st.CurrentVersion,
			}}); err != nil {
				return err
			}
		}
		out = st
		return s.audit.Record(ctx, w.q, actor, audit.Event{Action: action, EntityType: "statement",
			EntityID: st.ID, DocNo: st.DocNo, Before: before, After: st})
	})
	return out, err
}

// generateVersion appends version current+1 of st from the ledger now; st is updated in memory
// and the caller writes the statement row.
func (s *Service) generateVersion(ctx context.Context, w writeCtx, st *Statement) error {
	snap, err := buildSnapshot(ctx, w.q, *st)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("debt: encode statement snapshot: %w", err)
	}
	n := st.CurrentVersion + 1
	if _, err := w.q.ExecContext(ctx, `
		INSERT INTO statement_versions (id, tenant_id, statement_id, version_no, opening_balance, closing_balance, snapshot,
		                                created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ids.New(), w.tenantID, st.ID, n, snap.OpeningBalance, snap.ClosingBalance, raw, w.by, w.now); err != nil {
		return fmt.Errorf("debt: insert statement version: %w", err)
	}
	st.CurrentVersion = n
	return nil
}

// buildSnapshot computes the statement content: the balance before the period, every document
// and settlement dated inside it (settlements grouped per settling document and day), and the
// balance at its end. Dates are business dates, so a later posting dated after the period never
// changes it.
func buildSnapshot(ctx context.Context, q db.Querier, st Statement) (StatementSnapshot, error) {
	party, err := getParty(ctx, q, st.PartyID)
	if err != nil {
		return StatementSnapshot{}, err
	}
	out := StatementSnapshot{
		Party: party, Direction: st.Direction, PeriodFrom: st.PeriodFrom.Format(time.DateOnly), PeriodTo: st.PeriodTo.Format(time.DateOnly),
	}
	if err := q.QueryRowxContext(ctx, `
		SELECT COALESCE((SELECT SUM(o.amount) FROM obligations o
		                  WHERE o.party_id = $1 AND o.direction = $2 AND o.business_date < $3), 0)
		     - COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s JOIN obligations o ON o.id = s.obligation_id
		                  WHERE o.party_id = $1 AND o.direction = $2 AND s.business_date < $3), 0)`,
		st.PartyID, st.Direction, st.PeriodFrom).Scan(&out.OpeningBalance); err != nil {
		return StatementSnapshot{}, fmt.Errorf("debt: read statement opening balance: %w", err)
	}
	type row struct {
		Date      time.Time   `db:"business_date"`
		Kind      string      `db:"kind"`
		DocNo     string      `db:"doc_no"`
		DueDate   *time.Time  `db:"due_date"`
		Amount    money.Money `db:"amount"`
		CreatedAt time.Time   `db:"created_at"`
	}
	var rows []row
	if err := selectAll(ctx, q, &rows, `
		SELECT o.business_date, o.source_type AS kind, o.source_doc_no AS doc_no, o.due_date, o.amount, o.created_at
		FROM obligations o
		WHERE o.party_id = $1 AND o.direction = $2 AND o.business_date BETWEEN $3 AND $4
		UNION ALL
		SELECT g.business_date,
		       g.settled_by_type || CASE WHEN g.reversal THEN '_reversal' ELSE '' END AS kind,
		       COALESCE(CASE g.settled_by_type
		         WHEN 'payment' THEN (SELECT p.doc_no FROM payments p WHERE p.id = g.settled_by_id)
		         WHEN 'credit_note' THEN (SELECT c.source_doc_no FROM credit_notes c WHERE c.id = g.settled_by_id)
		         WHEN 'debt_adjustment' THEN (SELECT a.doc_no FROM debt_adjustments a WHERE a.id = g.settled_by_id)
		         WHEN 'debt_offset' THEN (SELECT b.doc_no FROM debt_offsets b WHERE b.id = g.settled_by_id)
		         WHEN 'obligation_reversal' THEN (SELECT COALESCE(r.reversal_doc_no, r.source_doc_no)
		                                          FROM obligations r WHERE r.id = g.settled_by_id)
		       END, '') AS doc_no,
		       NULL::date AS due_date, -g.amount AS amount, g.created_at
		FROM (SELECT s.business_date, s.settled_by_type, s.settled_by_id, s.reversal_of IS NOT NULL AS reversal,
		             SUM(s.amount) AS amount, MIN(s.created_at) AS created_at
		      FROM obligation_settlements s JOIN obligations o ON o.id = s.obligation_id
		      WHERE o.party_id = $1 AND o.direction = $2 AND s.business_date BETWEEN $3 AND $4
		      GROUP BY s.business_date, s.settled_by_type, s.settled_by_id, s.reversal_of IS NOT NULL) g`,
		st.PartyID, st.Direction, st.PeriodFrom, st.PeriodTo); err != nil {
		return StatementSnapshot{}, fmt.Errorf("debt: read statement lines: %w", err)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Date.Equal(rows[j].Date) {
			return rows[i].Date.Before(rows[j].Date)
		}
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].DocNo < rows[j].DocNo
	})
	balance := out.OpeningBalance
	out.Lines = make([]StatementLine, 0, len(rows))
	for _, r := range rows {
		l := StatementLine{Date: r.Date.Format(time.DateOnly), Kind: r.Kind, DocNo: r.DocNo}
		if r.DueDate != nil {
			d := r.DueDate.Format(time.DateOnly)
			l.DueDate = &d
		}
		if r.Amount.IsNegative() {
			l.Decrease = r.Amount.Neg()
		} else {
			l.Increase = r.Amount
		}
		balance = balance.Add(r.Amount)
		l.Balance = balance
		out.Lines = append(out.Lines, l)
	}
	out.ClosingBalance = balance
	return out, nil
}

func getStatement(ctx context.Context, q db.Querier, id uuid.UUID, forUpdate bool) (Statement, error) {
	query := `SELECT ` + statementColumns + ` FROM statements st WHERE st.id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var st Statement
	err := getInto(ctx, q, &st, query, id)
	if db.IsNoRows(err) {
		return Statement{}, errNotFound()
	}
	if err != nil {
		return Statement{}, fmt.Errorf("debt: read statement: %w", err)
	}
	return st, nil
}

// StatementFilter narrows the statement list.
type StatementFilter struct {
	PartyID *uuid.UUID
	Status  string
	// After is the keyset position (created_at, id) of the last row of the previous page.
	AfterCreatedAt *time.Time
	AfterID        uuid.UUID
	Limit          int
}

// StatementRow is a statement with its party's name and the balances of its current version.
type StatementRow struct {
	Statement
	PartyName      string      `db:"party_name"`
	OpeningBalance money.Money `db:"opening_balance"`
	ClosingBalance money.Money `db:"closing_balance"`
}

// statementRowQuery reads statements with the party name and the current version's balances
// (every statement has its first version from creation on).
const statementRowQuery = `SELECT ` + statementColumns + `, pa.name AS party_name,
		v.opening_balance, v.closing_balance
	FROM statements st JOIN parties pa ON pa.id = st.party_id
	JOIN statement_versions v ON v.statement_id = st.id AND v.version_no = st.current_version`

// Statements lists statements, newest first.
func (s *Service) Statements(ctx context.Context, scope Scope, f StatementFilter) ([]StatementRow, error) {
	var out []StatementRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		return selectAll(ctx, q, &out, statementRowQuery+`
			WHERE ($1::uuid IS NULL OR st.party_id = $1) AND ($2 = '' OR st.status = $2)
			  AND ($3::timestamptz IS NULL OR (st.created_at, st.id) < ($3, $4::uuid))
			ORDER BY st.created_at DESC, st.id DESC LIMIT $5`, f.PartyID, f.Status, f.AfterCreatedAt, f.AfterID, f.Limit)
	})
	if err != nil {
		return nil, fmt.Errorf("debt: list statements: %w", err)
	}
	return out, nil
}

// Statement returns a statement of the tenant with its party name and current balances.
func (s *Service) Statement(ctx context.Context, scope Scope, id uuid.UUID) (StatementRow, error) {
	var out StatementRow
	err := s.read(ctx, scope, func(q db.Querier) error {
		err := getInto(ctx, q, &out, statementRowQuery+` WHERE st.id = $1`, id)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("debt: read statement: %w", err)
		}
		return nil
	})
	return out, err
}

// StatementVersion returns version n of a statement with its status (superseded once a later
// version exists).
func (s *Service) StatementVersion(ctx context.Context, scope Scope, id uuid.UUID, n int) (StatementVersionView, error) {
	var out StatementVersionView
	err := s.read(ctx, scope, func(q db.Querier) error {
		st, err := getStatement(ctx, q, id, false)
		if err != nil {
			return err
		}
		var v StatementVersion
		err = getInto(ctx, q, &v, `SELECT `+statementVersionColumns+` FROM statement_versions v
			WHERE v.statement_id = $1 AND v.version_no = $2`, id, n)
		if db.IsNoRows(err) {
			return errNotFound()
		}
		if err != nil {
			return fmt.Errorf("debt: read statement version: %w", err)
		}
		out = StatementVersionView{Statement: st, Version: v, Status: st.VersionStatus(n)}
		return nil
	})
	return out, err
}
