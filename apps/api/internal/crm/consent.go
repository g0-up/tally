package crm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// ConsentInput records what a customer accepted or refused for a channel and purpose.
type ConsentInput struct {
	Channel  string
	Purpose  string
	Granted  bool
	Source   string
	Evidence *string
}

// Consents is a customer's consent log: Current holds the latest entry per channel and purpose,
// History every entry newest first.
type Consents struct {
	Current []Consent
	History []Consent
}

// maxConsentHistory bounds the history one read returns.
const maxConsentHistory = 200

// RecordConsent appends a consent entry and a consent_change timeline row. The entry is never
// edited: a later entry for the same channel and purpose replaces it. A key replays the first
// result of the request it identifies.
func (s *Service) RecordConsent(ctx context.Context, actor audit.Actor, key *idempotency.Key, partyID uuid.UUID, in ConsentInput) (Consent, bool, error) {
	fields := map[string]string{}
	if !validChannel(in.Channel) {
		fields["channel"] = "invalid"
	}
	if !validPurpose(in.Purpose) {
		fields["purpose"] = "invalid"
	}
	in.Source = strings.TrimSpace(in.Source)
	switch {
	case in.Source == "":
		fields["source"] = "required"
	case len([]rune(in.Source)) > 200:
		fields["source"] = "too_long"
	}
	if in.Evidence != nil && len([]rune(*in.Evidence)) > 2000 {
		fields["evidence"] = "too_long"
	}
	if len(fields) > 0 {
		return Consent{}, false, errInvalid(fields)
	}
	var c Consent
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeConsentRecord, key, http.StatusCreated, &c, func() error {
			c = Consent{
				ID: ids.New(), PartyID: partyID, Channel: in.Channel, Purpose: in.Purpose, Granted: in.Granted,
				Source: in.Source, Evidence: in.Evidence, RecordedBy: actor.ID, RecordedAt: s.clock.Now(),
			}
			return s.recordConsent(ctx, q, actor, c)
		})
		return err
	})
	return c, replayed, err
}

func (s *Service) recordConsent(ctx context.Context, q db.Querier, actor audit.Actor, c Consent) error {
	if err := partyExists(ctx, q, c.PartyID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO consents (id, tenant_id, party_id, channel, purpose, granted, source, evidence, recorded_by, recorded_at)
		VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7, $8, $9)`,
		c.ID, c.PartyID, c.Channel, c.Purpose, c.Granted, c.Source, c.Evidence, c.RecordedBy, c.RecordedAt); err != nil {
		return fmt.Errorf("crm: insert consent: %w", err)
	}
	verb := "Từ chối"
	if c.Granted {
		verb = "Đồng ý"
	}
	ref, by := RefConsent, actor.ID
	if err := insertActivity(ctx, q, Activity{
		ID: ids.New(), PartyID: c.PartyID, Kind: ActivityConsentChange, RefType: &ref, RefID: &c.ID,
		Summary:   fmt.Sprintf("%s nhận %s qua %s (%s)", verb, purposeLabel(c.Purpose), channelLabel(c.Channel), c.Source),
		CreatedBy: &by, CreatedAt: c.RecordedAt,
	}); err != nil {
		return err
	}
	return s.audit.Record(ctx, q, actor, audit.Event{Action: "consent.recorded", EntityType: "party", EntityID: c.PartyID, After: c})
}

func purposeLabel(p string) string {
	switch p {
	case PurposeDebtReminder:
		return "nhắc nợ"
	case PurposeMarketing:
		return "khuyến mãi"
	default:
		return "chăm sóc"
	}
}

func channelLabel(c string) string {
	switch c {
	case ChannelPhone:
		return "điện thoại"
	case ChannelSMS:
		return "SMS"
	case ChannelZalo:
		return "Zalo"
	default:
		return "email"
	}
}

// Consents reads a customer's consent log.
func (s *Service) Consents(ctx context.Context, scope Scope, partyID uuid.UUID) (Consents, error) {
	out := Consents{Current: []Consent{}, History: []Consent{}}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := partyExists(ctx, q, partyID); err != nil {
			return err
		}
		if err := sqlxSelect(ctx, q, &out.History, `
			SELECT id, party_id, channel, purpose, granted, source, evidence, recorded_by, recorded_at
			FROM consents WHERE party_id = $1
			ORDER BY recorded_at DESC, id DESC LIMIT $2`, partyID, maxConsentHistory); err != nil {
			return fmt.Errorf("crm: list consents: %w", err)
		}
		if err := sqlxSelect(ctx, q, &out.Current, `
			SELECT DISTINCT ON (channel, purpose) id, party_id, channel, purpose, granted, source, evidence, recorded_by, recorded_at
			FROM consents WHERE party_id = $1
			ORDER BY channel, purpose, recorded_at DESC, id DESC`, partyID); err != nil {
			return fmt.Errorf("crm: current consents: %w", err)
		}
		return nil
	})
	return out, err
}

// ConsentChecker answers whether a party's latest consent for a channel and purpose is granted;
// a party that never answered has not consented.
type ConsentChecker struct{}

// Granted reads the latest consent row in the caller's transaction.
func (ConsentChecker) Granted(ctx context.Context, q db.Querier, partyID uuid.UUID, channel, purpose string) (bool, error) {
	var granted bool
	err := q.QueryRowxContext(ctx, `
		SELECT granted FROM consents
		WHERE party_id = $1 AND channel = $2 AND purpose = $3
		ORDER BY recorded_at DESC, id DESC LIMIT 1`, partyID, channel, purpose).Scan(&granted)
	switch {
	case db.IsNoRows(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("crm: read consent: %w", err)
	}
	return granted, nil
}
