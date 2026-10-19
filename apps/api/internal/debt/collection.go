package debt

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

const reminderTemplate = "debt_reminder"

//go:embed templates/*.tmpl
var templates embed.FS

// overlayFS opens a name from the first file system that has it: the debt reminder templates,
// then the shared mail layout.
type overlayFS []fs.FS

func (o overlayFS) Open(name string) (fs.File, error) {
	for _, f := range o {
		file, err := f.Open(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func reminderTemplates() fs.FS { return overlayFS{templates, mailer.Templates} }

// Email outcomes of a reminder.
const (
	EmailSent             = "sent"
	EmailFailed           = "failed"
	EmailSkippedNoConsent = "skipped_no_consent"
	EmailSkippedNoEmail   = "skipped_no_email"
	EmailUnavailable      = "unavailable"
	EmailDuplicate        = "duplicate"
	EmailNotRequested     = "not_requested"
)

// consentDebtReminder is the consent purpose an emailed reminder needs.
const consentDebtReminder = "debt_reminder"

// NoteInput is a contact note or a payment promise on a party's ledger.
type NoteInput struct {
	PartyID        uuid.UUID
	Direction      string
	Kind           string
	Body           string
	PromisedDate   *time.Time
	PromisedAmount *money.Money
	Channel        string
}

func validChannel(c string) bool {
	switch c {
	case ChannelPhone, ChannelSMS, ChannelZalo, ChannelEmail, ChannelInPerson:
		return true
	}
	return false
}

func checkNoteInput(in NoteInput) error {
	fields := map[string]string{}
	if in.Direction != Receivable && in.Direction != Payable {
		fields["direction"] = "invalid"
	}
	switch in.Kind {
	case NoteNote, NoteCall:
		if strings.TrimSpace(in.Body) == "" {
			fields["body"] = "required"
		}
		if in.PromisedDate != nil || in.PromisedAmount != nil {
			fields["promised_date"] = "promise_only"
		}
	case NotePromise:
		if in.PromisedDate == nil {
			fields["promised_date"] = "required"
		}
		if in.PromisedAmount != nil && !in.PromisedAmount.IsPositive() {
			fields["promised_amount"] = "must_be_positive"
		}
	default:
		fields["kind"] = "invalid"
	}
	if utf8.RuneCountInString(in.Body) > maxNoteLen {
		fields["body"] = "too_long"
	}
	if in.Channel != "" && !validChannel(in.Channel) {
		fields["channel"] = "invalid"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

const collectionNoteColumns = `n.id, n.tenant_id, n.party_id, n.direction, n.kind, n.body, n.promised_date, n.promised_amount,
	n.channel, n.dedupe_key, n.created_by, n.created_at`

// AddNote records a contact note or a payment promise (promises feed the forecast). A retry with
// the same optional Idempotency-Key replays the note first recorded (replayed true).
func (s *Service) AddNote(ctx context.Context, actor audit.Actor, key *idempotency.Key, in NoteInput) (CollectionNote, bool, error) {
	if err := checkNoteInput(in); err != nil {
		return CollectionNote{}, false, err
	}
	var out CollectionNote
	var replayed bool
	err := s.write(ctx, actor, func(w writeCtx) error {
		var err error
		replayed, err = idempotency.Once(ctx, w.q, s.idem, w.tenantID, ScopeNoteCreate, key, http.StatusCreated, &out, func() error {
			if _, err := pathParty(ctx, w.q, in.PartyID); err != nil {
				return err
			}
			n := CollectionNote{
				ID: ids.New(), TenantID: w.tenantID, PartyID: in.PartyID, Direction: in.Direction, Kind: in.Kind,
				Body: optString(in.Body), PromisedDate: in.PromisedDate, PromisedAmount: in.PromisedAmount, Channel: optString(in.Channel),
				CreatedBy: w.by, CreatedAt: w.now,
			}
			if err := insertNote(ctx, w.q, n); err != nil {
				return err
			}
			out = n
			return s.audit.Record(ctx, w.q, actor, audit.Event{Action: "collection_note.create", EntityType: "collection_note",
				EntityID: n.ID, After: n})
		})
		return err
	})
	return out, replayed, err
}

func insertNote(ctx context.Context, q db.Querier, n CollectionNote) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO collection_notes (id, tenant_id, party_id, direction, kind, body, promised_date, promised_amount, channel,
		                              dedupe_key, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		n.ID, n.TenantID, n.PartyID, n.Direction, n.Kind, n.Body, n.PromisedDate, n.PromisedAmount, n.Channel, n.DedupeKey,
		n.CreatedBy, n.CreatedAt); err != nil {
		return fmt.Errorf("debt: insert collection note: %w", err)
	}
	return nil
}

// NoteFilter pages the notes of a party, newest first.
type NoteFilter struct {
	PartyID        uuid.UUID
	AfterCreatedAt *time.Time
	AfterID        uuid.UUID
	Limit          int
}

// Notes lists the collection notes of a party.
func (s *Service) Notes(ctx context.Context, scope Scope, f NoteFilter) ([]CollectionNote, error) {
	var out []CollectionNote
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := pathParty(ctx, q, f.PartyID); err != nil {
			return err
		}
		if err := selectAll(ctx, q, &out, `SELECT `+collectionNoteColumns+` FROM collection_notes n
			WHERE n.party_id = $1 AND ($2::timestamptz IS NULL OR (n.created_at, n.id) < ($2, $3::uuid))
			ORDER BY n.created_at DESC, n.id DESC LIMIT $4`, f.PartyID, f.AfterCreatedAt, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("debt: list collection notes: %w", err)
		}
		return nil
	})
	return out, err
}

// ReminderResult is a reminder: the text for the owner to send, the logged note, and what
// happened to the email.
type ReminderResult struct {
	Subject string
	Text    string
	Note    CollectionNote
	// Duplicate is true when a reminder on this channel was already logged today: nothing new
	// was logged or sent.
	Duplicate bool
	Email     string
}

type reminderDoc struct {
	DocNo   string
	Open    string
	DueDate string
}

type reminderData struct {
	StoreName  string
	StorePhone string
	PartyName  string
	AsOf       string
	Open       string
	Overdue    string
	Documents  []reminderDoc
}

// Remind builds the payment reminder of a customer's open receivable and logs it once per party,
// channel and day (dedupe key). With the email channel it is also emailed after the log is
// committed, but only to a party whose latest debt_reminder consent by email is granted. The text
// holds only the party's own documents.
func (s *Service) Remind(ctx context.Context, actor audit.Actor, partyID uuid.UUID, channel string) (ReminderResult, error) {
	if !validChannel(channel) {
		return ReminderResult{}, errInvalid(map[string]string{"channel": "invalid"})
	}
	var (
		out      ReminderResult
		rendered mailer.Rendered
		to       *mail.Address
		consent  bool
	)
	err := s.write(ctx, actor, func(w writeCtx) error {
		party, err := pathParty(ctx, w.q, partyID)
		if err != nil {
			return err
		}
		data, err := reminderContent(ctx, w.q, w.tenantID, party, w.date)
		if err != nil {
			return err
		}
		if rendered, err = s.renderer.Render(reminderTemplate, data); err != nil {
			return fmt.Errorf("debt: render reminder: %w", err)
		}
		out.Subject, out.Text = rendered.Subject, strings.TrimSpace(rendered.Text)
		key := fmt.Sprintf("reminder:%s:%s:%s", partyID, channel, w.date.Format(time.DateOnly))
		n := CollectionNote{
			ID: ids.New(), TenantID: w.tenantID, PartyID: partyID, Direction: Receivable, Kind: NoteReminderSent,
			Body: &out.Text, Channel: &channel, DedupeKey: &key, CreatedBy: w.by, CreatedAt: w.now,
		}
		res, err := w.q.ExecContext(ctx, `
			INSERT INTO collection_notes (id, tenant_id, party_id, direction, kind, body, channel, dedupe_key, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`,
			n.ID, n.TenantID, n.PartyID, n.Direction, n.Kind, n.Body, n.Channel, n.DedupeKey, n.CreatedBy, n.CreatedAt)
		if err != nil {
			return fmt.Errorf("debt: log reminder: %w", err)
		}
		if rows, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("debt: log reminder: %w", err)
		} else if rows == 0 {
			out.Duplicate = true
			return getInto(ctx, w.q, &out.Note, `SELECT `+collectionNoteColumns+` FROM collection_notes n WHERE n.dedupe_key = $1`, key)
		}
		out.Note = n
		if channel == ChannelEmail {
			if party.Email != nil {
				if addr, err := mail.ParseAddress(*party.Email); err == nil {
					addr.Name = party.Name
					to = addr
				}
			}
			if s.consent != nil {
				granted, err := s.consent.Granted(ctx, w.q, partyID, ChannelEmail, consentDebtReminder)
				if err != nil {
					return fmt.Errorf("debt: read reminder consent: %w", err)
				}
				consent = granted
			}
		}
		return s.audit.Record(ctx, w.q, actor, audit.Event{Action: "collection_note.remind", EntityType: "collection_note",
			EntityID: n.ID, After: map[string]any{"party_id": partyID, "channel": channel, "dedupe_key": key}})
	})
	if err != nil {
		return ReminderResult{}, err
	}
	switch {
	case out.Duplicate:
		out.Email = EmailDuplicate
	case channel != ChannelEmail:
		out.Email = EmailNotRequested
	case !consent:
		out.Email = EmailSkippedNoConsent
	case to == nil:
		out.Email = EmailSkippedNoEmail
	case s.mailer == nil:
		out.Email = EmailUnavailable
	default:
		out.Email = EmailSent
		if err := s.mailer.Send(ctx, mailer.Message{
			To: []*mail.Address{to}, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML,
		}); err != nil {
			// The reminder stays logged: the owner still has the text to send another way.
			out.Email = EmailFailed
		}
	}
	return out, nil
}

// reminderContent reads the open receivable documents of the party for the reminder text.
func reminderContent(ctx context.Context, q db.Querier, tenantID uuid.UUID, party partyRef, today time.Time) (reminderData, error) {
	var store struct {
		Name  string  `db:"display_name"`
		Phone *string `db:"phone"`
	}
	if err := getInto(ctx, q, &store, `SELECT display_name, phone FROM tenants WHERE id = $1`, tenantID); err != nil {
		return reminderData{}, fmt.Errorf("debt: read store: %w", err)
	}
	var docs []struct {
		DocNo   string      `db:"source_doc_no"`
		Open    money.Money `db:"open"`
		NextDue time.Time   `db:"next_due"`
		Overdue money.Money `db:"overdue"`
	}
	if err := selectAll(ctx, q, &docs, `SELECT o.source_doc_no, o.amount - o.settled_amount AS open, `+nextDueExpr+` AS next_due,
		`+overdueExpr("$2")+` AS overdue
		FROM obligations o WHERE o.party_id = $1 AND o.direction = 'receivable' AND o.status = 'open'
		ORDER BY next_due, o.business_date, o.id`, party.ID, today); err != nil {
		return reminderData{}, fmt.Errorf("debt: read reminder documents: %w", err)
	}
	if len(docs) == 0 {
		return reminderData{}, errNothingOwed()
	}
	data := reminderData{StoreName: store.Name, PartyName: party.Name, AsOf: viDate(today)}
	if store.Phone != nil {
		data.StorePhone = *store.Phone
	}
	var open, overdue money.Money
	for _, d := range docs {
		open, overdue = open.Add(d.Open), overdue.Add(d.Overdue)
		data.Documents = append(data.Documents, reminderDoc{DocNo: d.DocNo, Open: formatVND(d.Open), DueDate: viDate(d.NextDue)})
	}
	data.Open = formatVND(open)
	if overdue.IsPositive() {
		data.Overdue = formatVND(overdue)
	}
	return data, nil
}

func viDate(t time.Time) string { return t.Format("02/01/2006") }

// formatVND formats an amount the way the shop writes it: whole đồng with dot thousands
// separators ("1.250.000 đ").
func formatVND(m money.Money) string {
	d := m.Decimal().Round(0)
	neg := d.IsNegative()
	digits := d.Abs().StringFixed(0)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String() + " đ"
	}
	return b.String() + " đ"
}
