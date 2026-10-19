package saas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/saas/provider"
)

// Quote is what buying a plan costs today and what paying for it does to the subscription.
type Quote struct {
	Plan          Plan
	Purpose       string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Amount        money.Money
	ProrationNote string
	// Terms are the published terms the owner accepts before the change.
	Terms string
}

// quote prices planID for the tenant on its business date. Pricing is day-based: an upgrade in
// the middle of a period pays the price difference for the remaining days; a downgrade or a
// switch to a cheaper plan costs nothing and refunds nothing. Buying an add-on again while it is
// live extends it: the new period starts the day after the current one ends.
func (s *Service) quote(ctx context.Context, q db.Querier, tenantID, planID uuid.UUID) (Quote, error) {
	t, err := readTenantState(ctx, q, tenantID, false)
	if err != nil {
		return Quote{}, err
	}
	if t.IsSandbox || t.Status == "replaced" {
		return Quote{}, errSandbox()
	}
	plan, err := readPlan(ctx, q, planID)
	if apperr.KindOf(err) == apperr.NotFound || (err == nil && (!plan.Active || plan.Kind == KindTrial)) {
		return Quote{}, errInvalid(map[string]string{"plan_id": "invalid"})
	}
	if err != nil {
		return Quote{}, err
	}
	today, err := t.today(s.clock.Now())
	if err != nil {
		return Quote{}, err
	}
	out := Quote{Plan: plan, Terms: termsText(plan)}
	newPeriod := func() Quote {
		out.Purpose, out.PeriodStart, out.PeriodEnd, out.Amount = PurposeNewPeriod, today, plan.PeriodEnd(today), plan.Price
		return out
	}
	if plan.Kind == KindAddon {
		start, err := addonStart(ctx, q, tenantID, plan.ID, today)
		if err != nil {
			return Quote{}, err
		}
		out.Purpose, out.PeriodStart, out.PeriodEnd, out.Amount = PurposeAddon, start, plan.PeriodEnd(start), plan.Price
		return out, nil
	}
	sub, found, err := readSubscription(ctx, q, tenantID, false)
	if err != nil {
		return Quote{}, err
	}
	if !found {
		return newPeriod(), nil
	}
	current, err := readPlan(ctx, q, sub.PlanID)
	if err != nil {
		return Quote{}, err
	}
	sub, _ = s.lifecycle.Advance(sub, current, today)
	if sub.Status == StatusClosed || sub.Status == StatusTrialing || current.Kind == KindTrial {
		return newPeriod(), nil
	}
	if current.ID == plan.ID {
		start := today
		if !sub.CurrentPeriodEnd.Before(today) {
			start = sub.CurrentPeriodEnd.AddDate(0, 0, 1)
		}
		out.Purpose, out.PeriodStart, out.PeriodEnd, out.Amount = PurposeRenewal, start, plan.PeriodEnd(start), plan.Price
		return out, nil
	}
	if sub.Status != StatusActive || sub.CurrentPeriodEnd.Before(today) || current.BillingPeriod != plan.BillingPeriod {
		return newPeriod(), nil
	}
	remaining, total := daysInclusive(today, sub.CurrentPeriodEnd), daysInclusive(sub.CurrentPeriodStart, sub.CurrentPeriodEnd)
	diff := plan.Price.Sub(current.Price)
	amount := money.NewMoneyFromInt(0)
	if diff.IsPositive() && total > 0 {
		raw := diff.Decimal().Mul(decimal.NewFromInt(remaining)).Div(decimal.NewFromInt(total))
		if amount, err = money.RoundMoney(raw); err != nil {
			return Quote{}, fmt.Errorf("saas: prorate: %w", err)
		}
		amount = amount.RoundVND()
	}
	out.Purpose, out.PeriodStart, out.PeriodEnd, out.Amount = PurposePlanChange, today, sub.CurrentPeriodEnd, amount
	out.ProrationNote = fmt.Sprintf("Đổi từ gói %s sang gói %s cho %d/%d ngày còn lại của kỳ: (%s đ − %s đ) × %d/%d = %s đ. "+
		"Đổi sang gói rẻ hơn không phát sinh phí và không hoàn tiền.",
		current.Name, plan.Name, remaining, total, plan.Price.FormatVND(), current.Price.FormatVND(), remaining, total,
		amount.FormatVND())
	return out, nil
}

// termsText is the published terms of a plan, shown before every change.
func termsText(p Plan) string {
	return fmt.Sprintf("Gói %s không tự động gia hạn và không tự động thu tiền; gói dùng thử miễn phí. "+
		"Khi hết kỳ, cửa hàng có %d ngày gia hạn để thanh toán, sau đó %d ngày chỉ đọc (vẫn xem và xuất dữ liệu, "+
		"không ghi sổ chứng từ mới) trước khi tạm đóng. Dữ liệu không bao giờ bị xóa tự động.",
		p.Name, p.Term(TermGraceDays), p.Term(TermReadOnlyDays))
}

// PreviewChange prices a plan for the tenant without issuing anything.
func (s *Service) PreviewChange(ctx context.Context, tenantID, planID uuid.UUID) (Quote, error) {
	var out Quote
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		out, err = s.quote(ctx, q, tenantID, planID)
		return err
	})
	return out, err
}

// ScopeSubscriptionChange is the idempotency scope of buying a plan or an add-on.
const ScopeSubscriptionChange = "subscription.change"

// ChangePlan issues the invoice of buying planID after the owner accepted the terms; a free
// change (a downgrade, a zero price) is applied at once. An add-on plan is bought the same way: its
// invoice has purpose addon and, once paid, enables the add-on's modules for its period. With a key
// it runs once per Idempotency-Key: a retry of the same request returns the stored invoice
// (replayed true) instead of voiding it and issuing another.
func (s *Service) ChangePlan(ctx context.Context, actor audit.Actor, key *idempotency.Key, planID uuid.UUID,
	acceptTerms bool) (Invoice, bool, error) {
	if !acceptTerms {
		return Invoice{}, false, errTermsNotAccepted()
	}
	return s.issue(ctx, actor, key, planID, nil)
}

// IssueInvoice issues a service invoice for a tenant on behalf of a platform admin; amount
// overrides the quoted price.
func (s *Service) IssueInvoice(ctx context.Context, admin audit.Actor, tenantID, planID uuid.UUID, amount *money.Money) (Invoice, error) {
	if amount != nil && amount.IsNegative() {
		return Invoice{}, errInvalid(map[string]string{"amount": "min"})
	}
	if err := s.adminTenant(ctx, tenantID); err != nil {
		return Invoice{}, err
	}
	admin.TenantID = tenantID
	inv, _, err := s.issue(ctx, admin, nil, planID, amount)
	return inv, err
}

func (s *Service) issue(ctx context.Context, actor audit.Actor, key *idempotency.Key, planID uuid.UUID,
	amount *money.Money) (Invoice, bool, error) {
	var inv Invoice
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeSubscriptionChange, key, http.StatusCreated, &inv, func() (err error) {
			inv, err = s.issueIn(ctx, q, actor, planID, amount)
			return err
		})
		return err
	})
	return inv, replayed, err
}

// issueIn voids the open invoices it replaces and issues the invoice of buying planID inside q; a
// free invoice is applied at once. A plan invoice replaces the open plan invoices; an add-on
// invoice replaces only the open invoices of the same add-on, so add-ons are bought side by side.
func (s *Service) issueIn(ctx context.Context, q db.Querier, actor audit.Actor, planID uuid.UUID,
	amount *money.Money) (Invoice, error) {
	if err := lockBilling(ctx, q, actor.TenantID); err != nil {
		return Invoice{}, err
	}
	if closed, err := closedOnRequest(ctx, q, actor.TenantID); err != nil || closed {
		if err == nil {
			err = errTenantClosed()
		}
		return Invoice{}, err
	}
	qt, err := s.quote(ctx, q, actor.TenantID, planID)
	if err != nil {
		return Invoice{}, err
	}
	if amount != nil {
		qt.Amount = amount.RoundVND()
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE saas_invoices SET status = 'void'
		WHERE tenant_id = $1 AND status = 'open' AND (purpose = 'addon') = $2 AND (purpose <> 'addon' OR plan_id = $3)`,
		actor.TenantID, qt.Purpose == PurposeAddon, qt.Plan.ID); err != nil {
		return Invoice{}, fmt.Errorf("saas: void open invoices: %w", err)
	}
	sub, found, err := readSubscription(ctx, q, actor.TenantID, false)
	if err != nil {
		return Invoice{}, err
	}
	inv := Invoice{
		TenantID: actor.TenantID, PlanID: &qt.Plan.ID, Purpose: qt.Purpose, PeriodStart: qt.PeriodStart,
		PeriodEnd: qt.PeriodEnd, Amount: qt.Amount, Status: InvoiceOpen, IssuedAt: s.clock.Now(),
	}
	if found && sub.Status != StatusClosed {
		inv.SubscriptionID = &sub.ID
	}
	if qt.ProrationNote != "" {
		inv.ProrationNote = &qt.ProrationNote
	}
	if err := insertInvoice(ctx, q, &inv, qt.PeriodStart); err != nil {
		return Invoice{}, err
	}
	if err := s.audit.Record(ctx, q, actor, audit.Event{
		Action: "saas_invoice.issued", EntityType: "saas_invoice", EntityID: inv.ID, DocNo: inv.Number,
		After: map[string]any{"plan": qt.Plan.Code, "purpose": inv.Purpose, "amount": inv.Amount.String()},
	}); err != nil {
		return Invoice{}, err
	}
	if !inv.Amount.IsZero() {
		return inv, nil
	}
	ref := inv.Number
	pay := provider.Payment{EventID: provider.ManualEventID(inv.ID.String()), InvoiceNumber: inv.Number,
		Amount: inv.Amount, Reference: ref, Payload: []byte(`{"reason":"free"}`)}
	if _, err := s.applyPayment(ctx, q, actor, inv.ID, provider.ManualName, pay); err != nil {
		return Invoice{}, err
	}
	return readInvoice(ctx, q, inv.ID, false)
}

// insertInvoice numbers and inserts inv: "SV-{yyyymmdd}-{6 Crockford characters of the id}",
// with a new id on the rare number collision.
func insertInvoice(ctx context.Context, q db.Querier, inv *Invoice, day time.Time) error {
	for range 5 {
		inv.ID = ids.New()
		inv.Number = "SV-" + day.Format("20060102") + "-" + ids.Crockford(inv.ID, 6)
		res, err := q.ExecContext(ctx, `
			INSERT INTO saas_invoices (id, tenant_id, number, subscription_id, plan_id, purpose, period_start, period_end,
			  amount, proration_note, status, issued_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT ON CONSTRAINT ux_saas_invoices__number DO NOTHING`,
			inv.ID, inv.TenantID, inv.Number, inv.SubscriptionID, inv.PlanID, inv.Purpose, inv.PeriodStart, inv.PeriodEnd,
			inv.Amount, inv.ProrationNote, inv.Status, inv.IssuedAt)
		if err != nil {
			return fmt.Errorf("saas: insert invoice: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("saas: insert invoice: %w", err)
		} else if n == 1 {
			return nil
		}
	}
	return errors.New("saas: insert invoice: no free invoice number")
}

// Invoices lists the tenant's own service invoices, newest first.
func (s *Service) Invoices(ctx context.Context, tenantID uuid.UUID) ([]Invoice, error) {
	var out []Invoice
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `SELECT `+invoiceColumns+` FROM saas_invoices
			WHERE tenant_id = $1 AND status <> 'draft' ORDER BY issued_at DESC, id DESC LIMIT 200`, tenantID)
	})
	if err != nil {
		return nil, fmt.Errorf("saas: list invoices: %w", err)
	}
	return out, nil
}

// Invoice returns one of the tenant's own service invoices.
func (s *Service) Invoice(ctx context.Context, tenantID, id uuid.UUID) (Invoice, error) {
	var out Invoice
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		out, err = tenantInvoice(ctx, q, tenantID, id, false)
		return err
	})
	return out, err
}

func tenantInvoice(ctx context.Context, q db.Querier, tenantID, id uuid.UUID, forUpdate bool) (Invoice, error) {
	inv, err := readInvoice(ctx, q, id, forUpdate)
	if err != nil {
		return Invoice{}, err
	}
	if inv.TenantID != tenantID || inv.Status == "draft" {
		return Invoice{}, errNotFound("Invoice")
	}
	return inv, nil
}

// PayInstructions tells the owner how to pay an open invoice through a provider (manual
// transfer when empty).
type PayInstructions struct {
	Invoice      Invoice
	Provider     string
	Instructions string
}

// Pay returns the payment instructions of an open invoice of the tenant.
func (s *Service) Pay(ctx context.Context, tenantID, id uuid.UUID, providerName string) (PayInstructions, error) {
	if providerName == "" {
		providerName = provider.ManualName
	}
	p, ok := s.providers[providerName]
	if !ok {
		return PayInstructions{}, errInvalid(map[string]string{"provider": "oneof"})
	}
	inv, err := s.Invoice(ctx, tenantID, id)
	if err != nil {
		return PayInstructions{}, err
	}
	if inv.Status != InvoiceOpen {
		return PayInstructions{}, errInvoiceNotOpen()
	}
	return PayInstructions{Invoice: inv, Provider: p.Name(), Instructions: p.Instructions(inv.Number, inv.Amount)}, nil
}

// HandleWebhook authenticates a provider callback and applies its payment once; a repeated event
// is reported as a duplicate and changes nothing.
func (s *Service) HandleWebhook(ctx context.Context, providerName string, header http.Header, body []byte) (bool, error) {
	p, ok := s.providers[providerName]
	if !ok {
		return false, errProvider()
	}
	hook, ok := p.(provider.Webhook)
	if !ok {
		return false, errProvider()
	}
	pay, err := hook.Verify(header, body)
	switch {
	case errors.Is(err, provider.ErrSignature):
		return false, errSignature()
	case errors.Is(err, provider.ErrPayload):
		return false, errInvalid(map[string]string{"body": "invalid"})
	case err != nil:
		return false, err
	}
	var inv Invoice
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		inv, err = readInvoiceByNumber(ctx, q, pay.InvoiceNumber, false)
		return err
	}); err != nil {
		return false, err
	}
	var applied bool
	actor := systemActor(inv.TenantID, audit.SourceWeb)
	err = s.db.WithTenantTx(ctx, inv.TenantID, func(q db.Querier) error {
		var err error
		applied, err = s.applyPayment(ctx, q, actor, inv.ID, hook.Name(), pay)
		return err
	})
	if apperr.Is(err, CodeInvoiceNotOpen) {
		s.reportRefusedPayment(ctx, actor, inv, hook.Name(), pay)
	}
	return !applied, err
}

// reportRefusedPayment tells operators of a provider payment refused because its invoice is no
// longer open (voided, for instance by the store's closure): the provider may have taken the
// money, which is then refunded by hand. Every refusal logs an error; the store's audit log
// records each provider event once, however often the provider retries it.
func (s *Service) reportRefusedPayment(ctx context.Context, actor audit.Actor, inv Invoice, providerName string,
	pay provider.Payment) {
	s.logger.ErrorContext(ctx, "payment refused: its invoice is not open, refund it by hand",
		"tenant_id", inv.TenantID, "invoice", inv.Number, "provider", providerName, "event_id", pay.EventID,
		"amount", pay.Amount.String(), "reference", pay.Reference)
	err := s.db.WithTenantTx(ctx, inv.TenantID, func(q db.Querier) error {
		if err := lockBilling(ctx, q, inv.TenantID); err != nil {
			return err
		}
		cur, err := readInvoice(ctx, q, inv.ID, false)
		if err != nil {
			return err
		}
		var seen bool
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM audit_events WHERE entity_type = 'saas_invoice'
			AND entity_id = $1 AND action = 'saas_invoice.payment_refused' AND after->>'provider' = $2
			AND after->>'event_id' = $3)`, inv.ID, providerName, pay.EventID).Scan(&seen); err != nil {
			return fmt.Errorf("saas: read refused payment: %w", err)
		}
		if seen {
			return nil
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "saas_invoice.payment_refused", EntityType: "saas_invoice", EntityID: inv.ID, DocNo: inv.Number,
			After: map[string]any{
				"provider": providerName, "event_id": pay.EventID, "amount": pay.Amount.String(),
				"reference": pay.Reference, "invoice_status": cur.Status,
			},
		})
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "record refused payment failed", "tenant_id", inv.TenantID, "invoice", inv.Number,
			"error", err)
	}
}

// MarkPaid records a manual bank transfer an admin confirmed with the bank reference; a repeated
// confirmation of the same invoice is a no-op, and confirming an invoice a provider already paid
// is refused.
func (s *Service) MarkPaid(ctx context.Context, admin audit.Actor, id uuid.UUID, reference string) (Invoice, error) {
	if reference == "" || len(reference) > 120 {
		return Invoice{}, errInvalid(map[string]string{"reference": "required"})
	}
	inv, err := s.adminInvoice(ctx, id)
	if err != nil {
		return Invoice{}, err
	}
	admin.TenantID = inv.TenantID
	payload, err := json.Marshal(map[string]string{"reference": reference, "admin_id": admin.ID.String()})
	if err != nil {
		return Invoice{}, fmt.Errorf("saas: encode payment: %w", err)
	}
	err = s.db.WithTenantTx(ctx, inv.TenantID, func(q db.Querier) error {
		pay := provider.Payment{EventID: provider.ManualEventID(inv.ID.String()), InvoiceNumber: inv.Number,
			Amount: inv.Amount, Reference: reference, Payload: payload}
		if _, err := s.applyPayment(ctx, q, admin, inv.ID, provider.ManualName, pay); err != nil {
			return err
		}
		inv, err = readInvoice(ctx, q, inv.ID, false)
		return err
	})
	return inv, err
}

// VoidInvoice cancels an open invoice.
func (s *Service) VoidInvoice(ctx context.Context, admin audit.Actor, id uuid.UUID) (Invoice, error) {
	inv, err := s.adminInvoice(ctx, id)
	if err != nil {
		return Invoice{}, err
	}
	admin.TenantID = inv.TenantID
	err = s.db.WithTenantTx(ctx, inv.TenantID, func(q db.Querier) error {
		if err := lockBilling(ctx, q, inv.TenantID); err != nil {
			return err
		}
		cur, err := readInvoice(ctx, q, id, true)
		if err != nil {
			return err
		}
		if cur.Status != InvoiceOpen {
			return errInvoiceNotOpen()
		}
		if _, err := q.ExecContext(ctx, `UPDATE saas_invoices SET status = 'void' WHERE id = $1`, id); err != nil {
			return fmt.Errorf("saas: void invoice: %w", err)
		}
		cur.Status, inv = InvoiceVoid, cur
		return s.audit.Record(ctx, q, admin, audit.Event{
			Action: "saas_invoice.voided", EntityType: "saas_invoice", EntityID: id, DocNo: cur.Number,
		})
	})
	return inv, err
}

// AdminInvoice is a service invoice in the admin list, with its store.
type AdminInvoice struct {
	Invoice
	TenantSlug string `db:"tenant_slug"`
	TenantName string `db:"tenant_name"`
}

// InvoiceFilter narrows the admin invoice list.
type InvoiceFilter struct {
	TenantID *uuid.UUID
	Status   string
}

// AdminInvoices lists one page of the service invoices of every store except replaced sandboxes,
// newest first; it reads page.FetchLimit rows.
func (s *Service) AdminInvoices(ctx context.Context, f InvoiceFilter, page httpx.Page) ([]AdminInvoice, error) {
	after, afterID, err := page.AfterTime()
	if err != nil {
		return nil, err
	}
	var out []AdminInvoice
	err = s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT i.id, i.tenant_id, i.number, i.subscription_id, i.plan_id, i.purpose, i.period_start, i.period_end,
			  i.amount, i.proration_note, i.status, i.provider, i.provider_ref, i.issued_at, i.paid_at,
			  t.slug AS tenant_slug, t.display_name AS tenant_name
			FROM saas_invoices i JOIN tenants t ON t.id = i.tenant_id
			WHERE t.status <> 'replaced' AND ($1::uuid IS NULL OR i.tenant_id = $1) AND ($2 = '' OR i.status = $2)
			  AND ($3::timestamptz IS NULL OR (i.issued_at, i.id) < ($3, $4))
			ORDER BY i.issued_at DESC, i.id DESC LIMIT $5`, f.TenantID, f.Status, after, afterID, page.FetchLimit())
	})
	if err != nil {
		return nil, fmt.Errorf("saas: list admin invoices: %w", err)
	}
	return out, nil
}

func (s *Service) adminInvoice(ctx context.Context, id uuid.UUID) (Invoice, error) {
	var inv Invoice
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		if inv, err = readInvoice(ctx, q, id, false); err != nil {
			return err
		}
		return s.adminTenantIn(ctx, q, inv.TenantID)
	})
	return inv, err
}

// adminTenant answers 404 for a tenant the admin console does not show (a replaced sandbox).
func (s *Service) adminTenant(ctx context.Context, tenantID uuid.UUID) error {
	return s.db.WithPlatformTx(ctx, func(q db.Querier) error { return s.adminTenantIn(ctx, q, tenantID) })
}

func (s *Service) adminTenantIn(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	t, err := readTenantState(ctx, q, tenantID, false)
	if err != nil {
		return err
	}
	if t.Status == "replaced" {
		return errNotFound("Tenant")
	}
	return nil
}

// applyPayment applies one confirmed payment of an invoice, exactly once: the payment event is
// recorded first (a repeat of the same provider event changes nothing), then the invoice row is
// locked and moves open → paid, and only that transition applies the purchase; another event for
// an invoice no longer open is refused. It reports whether the payment changed anything. q is the
// tenant transaction.
func (s *Service) applyPayment(ctx context.Context, q db.Querier, actor audit.Actor, invoiceID uuid.UUID,
	providerName string, pay provider.Payment) (bool, error) {
	payload := pay.Payload
	if !json.Valid(payload) {
		payload = []byte(`{}`)
	}
	now := s.clock.Now()
	res, err := q.ExecContext(ctx, `
		INSERT INTO saas_payment_events (id, provider, event_id, saas_invoice_id, payload, received_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT ON CONSTRAINT ux_saas_payment_events__provider_event_id DO NOTHING`,
		ids.New(), providerName, pay.EventID, invoiceID, payload, now)
	if err != nil {
		return false, fmt.Errorf("saas: record payment event: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, fmt.Errorf("saas: record payment event: %w", err)
	} else if n == 0 {
		return false, nil
	}
	if err := lockBilling(ctx, q, actor.TenantID); err != nil {
		return false, err
	}
	inv, err := readInvoice(ctx, q, invoiceID, true)
	if err != nil {
		return false, err
	}
	if inv.TenantID != actor.TenantID {
		return false, errNotFound("Invoice")
	}
	if inv.Status != InvoiceOpen {
		// A paid invoice was paid by another event: this one is a second payment, refused like a
		// payment of a void invoice.
		return false, errInvoiceNotOpen()
	}
	if !pay.Amount.Equal(inv.Amount) {
		return false, errAmountMismatch()
	}
	var ref *string
	if pay.Reference != "" {
		ref = &pay.Reference
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE saas_invoices SET status = 'paid', provider = $2, provider_ref = $3, paid_at = $4 WHERE id = $1`,
		inv.ID, providerName, ref, now); err != nil {
		if db.IsUniqueViolation(err, "ux_saas_invoices__provider_provider_ref") {
			return false, errInvalid(map[string]string{"reference": "unique"})
		}
		return false, fmt.Errorf("saas: mark invoice paid: %w", err)
	}
	sub, err := s.applyPurchase(ctx, q, &inv, now)
	if err != nil {
		return false, err
	}
	if err := s.markEventProcessed(ctx, q, providerName, pay.EventID, now); err != nil {
		return false, err
	}
	return true, s.audit.Record(ctx, q, actor, audit.Event{
		Action: "saas_invoice.paid", EntityType: "saas_invoice", EntityID: inv.ID, DocNo: inv.Number,
		After: map[string]any{
			"provider": providerName, "reference": pay.Reference, "amount": inv.Amount.String(),
			"subscription_status": sub.Status, "period_end": sub.CurrentPeriodEnd.Format(time.DateOnly),
		},
	})
}

func (s *Service) markEventProcessed(ctx context.Context, q db.Querier, providerName, eventID string, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE saas_payment_events SET processed_at = $3 WHERE provider = $1 AND event_id = $2 AND processed_at IS NULL`,
		providerName, eventID, now); err != nil {
		return fmt.Errorf("saas: mark payment event processed: %w", err)
	}
	return nil
}

// applyPurchase applies what a paid invoice bought. A new period starts on the payment day when
// it was issued earlier, so a late payment never buys days already gone; a renewal extends the
// live period; a plan change switches the plan for the rest of the period; an add-on enables its
// modules for its period, which starts after the add-on's live period as of the payment day.
func (s *Service) applyPurchase(ctx context.Context, q db.Querier, inv *Invoice, now time.Time) (Subscription, error) {
	if inv.PlanID == nil {
		return Subscription{}, errInvalid(map[string]string{"plan_id": "required"})
	}
	if closed, err := closedOnRequest(ctx, q, inv.TenantID); err != nil || closed {
		if err == nil {
			err = errTenantClosed()
		}
		return Subscription{}, err
	}
	t, err := readTenantState(ctx, q, inv.TenantID, false)
	if err != nil {
		return Subscription{}, err
	}
	today, err := t.today(now)
	if err != nil {
		return Subscription{}, err
	}
	plan, err := readPlan(ctx, q, *inv.PlanID)
	if err != nil {
		return Subscription{}, err
	}
	sub, found, err := readSubscription(ctx, q, inv.TenantID, true)
	if err != nil {
		return Subscription{}, err
	}
	if found {
		current, err := readPlan(ctx, q, sub.PlanID)
		if err != nil {
			return Subscription{}, err
		}
		sub, _ = s.lifecycle.Advance(sub, current, today)
	}
	live := found && sub.Status != StatusClosed
	shiftPeriod := func(start time.Time) error {
		if start.Equal(inv.PeriodStart) {
			return nil
		}
		inv.PeriodStart, inv.PeriodEnd = start, plan.PeriodEnd(start)
		if _, err := q.ExecContext(ctx, `UPDATE saas_invoices SET period_start = $2, period_end = $3 WHERE id = $1`,
			inv.ID, inv.PeriodStart, inv.PeriodEnd); err != nil {
			return fmt.Errorf("saas: update invoice period: %w", err)
		}
		return nil
	}
	purpose := inv.Purpose
	if purpose != PurposeAddon && !live {
		purpose = PurposeNewPeriod
	}
	switch purpose {
	case PurposeAddon:
		start, err := addonStart(ctx, q, inv.TenantID, plan.ID, today)
		if err != nil {
			return Subscription{}, err
		}
		if err := shiftPeriod(start); err != nil {
			return Subscription{}, err
		}
		for _, m := range plan.Modules {
			if err := grantAddon(ctx, q, inv.TenantID, plan.ID, m, inv.PeriodStart, inv.PeriodEnd, now); err != nil {
				return Subscription{}, err
			}
		}
		if !live {
			return sub, nil
		}
	case PurposeNewPeriod:
		if err := shiftPeriod(laterDate(inv.PeriodStart, today)); err != nil {
			return Subscription{}, err
		}
		if !live {
			sub = Subscription{ID: ids.New(), TenantID: inv.TenantID, CreatedAt: now}
		}
		sub.PlanID, sub.Status = plan.ID, StatusActive
		sub.CurrentPeriodStart, sub.CurrentPeriodEnd = inv.PeriodStart, inv.PeriodEnd
		sub.TrialEndsOn, sub.GraceEndsOn, sub.ReadOnlyEndsOn, sub.CancelAtPeriodEnd = nil, nil, nil, false
		if !live {
			if err := insertSubscription(ctx, q, sub); err != nil {
				return Subscription{}, err
			}
			if _, err := q.ExecContext(ctx, `UPDATE saas_invoices SET subscription_id = $2 WHERE id = $1`,
				inv.ID, sub.ID); err != nil {
				return Subscription{}, fmt.Errorf("saas: link invoice: %w", err)
			}
		}
		if err := syncPlanEntitlements(ctx, q, sub, plan, inv.PeriodStart, now); err != nil {
			return Subscription{}, err
		}
	case PurposeRenewal:
		if sub.CurrentPeriodEnd.Before(inv.PeriodStart.AddDate(0, 0, -1)) {
			sub.CurrentPeriodStart = inv.PeriodStart
		}
		sub.CurrentPeriodEnd = laterDate(sub.CurrentPeriodEnd, inv.PeriodEnd)
		sub.Status, sub.GraceEndsOn, sub.ReadOnlyEndsOn, sub.CancelAtPeriodEnd = StatusActive, nil, nil, false
		if sub.PlanID != plan.ID {
			sub.PlanID = plan.ID
			if err := syncPlanEntitlements(ctx, q, sub, plan, today, now); err != nil {
				return Subscription{}, err
			}
		}
	case PurposePlanChange:
		sub.PlanID = plan.ID
		if err := syncPlanEntitlements(ctx, q, sub, plan, today, now); err != nil {
			return Subscription{}, err
		}
	}
	if live {
		if err := updateSubscription(ctx, q, &sub, now); err != nil {
			return Subscription{}, err
		}
	}
	if err := syncTenantStatus(ctx, q, inv.TenantID, sub.Status, now); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}
