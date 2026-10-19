package admin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// referralCodePattern is a referral code as partners hand it out; sign-up matches it ignoring case.
var referralCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,31}$`)

// Partner is a reseller or referrer. It has no access to store data.
type Partner struct {
	ID                uuid.UUID     `db:"id"`
	Name              string        `db:"name"`
	ReferralCode      string        `db:"referral_code"`
	CommissionPercent money.Percent `db:"commission_percent"`
	Active            bool          `db:"active"`
	CreatedAt         time.Time     `db:"created_at"`
	Referrals         int           `db:"referrals"`
}

// Referral is a store that signed up with a partner's code, with the service invoices it paid.
// Commission applies to paid subscription (service) invoices only, never to the store's sales.
type Referral struct {
	TenantID           uuid.UUID   `db:"tenant_id"`
	Slug               string      `db:"slug"`
	DisplayName        string      `db:"display_name"`
	ReferredAt         time.Time   `db:"referred_at"`
	SubscriptionStatus *string     `db:"subscription_status"`
	PaidInvoices       money.Money `db:"paid_invoices"`
	Commission         money.Money `db:"-"`
}

// Partners runs partners and their referrals.
type Partners struct {
	db    *db.DB
	clock clock.Clock
}

// NewPartners returns the service.
func NewPartners(d *db.DB, clk clock.Clock) *Partners { return &Partners{db: d, clock: clk} }

// NewPartner is a partner to add.
type NewPartner struct {
	Name              string
	ReferralCode      string
	CommissionPercent money.Percent
}

const partnerSelect = `
	SELECT p.id, p.name, p.referral_code, p.commission_percent, p.active, p.created_at,
	  (SELECT count(*) FROM tenant_referrals r JOIN tenants t ON t.id = r.tenant_id
	   WHERE r.partner_id = p.id AND t.status <> 'replaced') AS referrals
	FROM partners p`

// Create adds a partner; a taken code answers 409 referral_code_taken.
func (s *Partners) Create(ctx context.Context, in NewPartner) (Partner, error) {
	fields := map[string]string{}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 200 {
		fields["name"] = "invalid"
	}
	code := strings.ToUpper(strings.TrimSpace(in.ReferralCode))
	if !referralCodePattern.MatchString(code) {
		fields["referral_code"] = "invalid"
	}
	if pct := in.CommissionPercent.Decimal(); pct.IsNegative() || pct.GreaterThan(decimal.NewFromInt(100)) {
		fields["commission_percent"] = "invalid"
	}
	if len(fields) > 0 {
		return Partner{}, errInvalid(fields)
	}
	out := Partner{
		ID: ids.New(), Name: name, ReferralCode: code, CommissionPercent: in.CommissionPercent, Active: true, CreatedAt: s.clock.Now(),
	}
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var taken bool
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM partners WHERE upper(referral_code) = $1)`, code).Scan(&taken); err != nil {
			return fmt.Errorf("admin: check referral code: %w", err)
		}
		if taken {
			return errCodeTaken()
		}
		_, err := q.ExecContext(ctx, `
			INSERT INTO partners (id, name, referral_code, commission_percent, active, created_at) VALUES ($1, $2, $3, $4, true, $5)`,
			out.ID, out.Name, out.ReferralCode, out.CommissionPercent, out.CreatedAt)
		if db.IsUniqueViolation(err, "ux_partners__referral_code") {
			return errCodeTaken()
		}
		if err != nil {
			return fmt.Errorf("admin: insert partner: %w", err)
		}
		return nil
	})
	return out, err
}

func errCodeTaken() error {
	return apperr.NewConflict("referral_code_taken", "This referral code is already in use")
}

// List returns one page of the partners, newest first; it reads page.FetchLimit rows.
func (s *Partners) List(ctx context.Context, page httpx.Page) ([]Partner, error) {
	after, afterID, err := page.AfterTime()
	if err != nil {
		return nil, err
	}
	out := []Partner{}
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, partnerSelect+`
			WHERE $1::timestamptz IS NULL OR (p.created_at, p.id) < ($1, $2)
			ORDER BY p.created_at DESC, p.id DESC LIMIT $3`, after, afterID, page.FetchLimit())
	})
	if err != nil {
		return nil, fmt.Errorf("admin: list partners: %w", err)
	}
	return out, nil
}

// Referrals returns the partner and the stores it referred, newest first, with the commission
// of each store's paid service invoices.
func (s *Partners) Referrals(ctx context.Context, partnerID uuid.UUID) (Partner, []Referral, error) {
	var (
		p   Partner
		out = []Referral{}
	)
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &p, partnerSelect+` WHERE p.id = $1`, partnerID)
		if db.IsNoRows(err) {
			return apperr.NewNotFound("not_found", "Partner not found")
		}
		if err != nil {
			return fmt.Errorf("admin: read partner: %w", err)
		}
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT r.tenant_id, t.slug, t.display_name, r.referred_at,
			  (SELECT status FROM subscriptions WHERE tenant_id = t.id
			   ORDER BY status <> 'closed' DESC, created_at DESC, id DESC LIMIT 1) AS subscription_status,
			  (SELECT COALESCE(sum(amount), 0) FROM saas_invoices WHERE tenant_id = t.id AND status = 'paid') AS paid_invoices
			FROM tenant_referrals r JOIN tenants t ON t.id = r.tenant_id
			WHERE r.partner_id = $1 AND t.status <> 'replaced'
			ORDER BY r.referred_at DESC, r.tenant_id LIMIT 1000`, partnerID)
	})
	if err != nil {
		return Partner{}, nil, err
	}
	for i := range out {
		out[i].Commission = commission(out[i].PaidInvoices, p.CommissionPercent)
	}
	return p, out, nil
}

// commission is paid × percent / 100, rounded to whole đồng.
func commission(paid money.Money, pct money.Percent) money.Money {
	out, err := money.RoundMoney(paid.Decimal().Mul(pct.Decimal()).Div(decimal.NewFromInt(100)).Round(0))
	if err != nil {
		return money.ZeroMoney
	}
	return out
}
