package partner

import (
	"context"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Field limits of partner records.
const (
	maxNameLen    = 200
	maxPhoneLen   = 30
	maxEmailLen   = 254
	maxAddressLen = 500
	maxNoteLen    = 2000
	maxLabelLen   = 100
	// minPhoneDigits and maxPhoneDigits bound the digits of a phone number (landlines with area
	// code have 10–11, mobiles 10, international numbers up to 15).
	minPhoneDigits = 8
	maxPhoneDigits = 15
	// MaxPaymentTermsDays bounds the payment terms of a profile (ten years).
	MaxPaymentTermsDays = 3650
	// maxSearchTermLen bounds the list filter text, in bytes.
	maxSearchTermLen = 400
)

// maxCreditLimit keeps a credit limit inside numeric(19,4).
var maxCreditLimit = money.MustMoney("999999999999999")

// taxCodePattern is a tax code after normalisation: Vietnamese codes are 10 digits with an
// optional 3-digit branch suffix ("0109245781-001"); foreign codes may carry letters.
var taxCodePattern = regexp.MustCompile(`^[0-9A-Z][0-9A-Z-]{0,19}$`)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Usage answers delete-or-deactivate; nil uses SQLUsageReader.
	Usage UsageReader
}

// Service implements the partner use cases. Every method runs in its own tenant transaction;
// the tenant id always comes from the authenticated session.
type Service struct {
	db     *db.DB
	clock  clock.Clock
	audit  *audit.Recorder
	usage  UsageReader
	dedupe PartyDeduplicator
	idem   *idempotency.Guard
	// tx, when set, is a transaction the caller already holds (the tenant provisioning
	// transaction of the seeders); every use case then runs in it instead of opening its own.
	tx db.Querier
}

// NewService returns the service.
func NewService(d Deps) *Service {
	usage := d.Usage
	if usage == nil {
		usage = SQLUsageReader{}
	}
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit, usage: usage, idem: idempotency.NewGuard(d.Clock)}
}

// within returns a copy of the service whose use cases run in the caller's tenant transaction q.
func (s *Service) within(q db.Querier) *Service {
	c := *s
	c.tx = q
	return &c
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// read runs fn in a tenant transaction suited to scope.
func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// write runs fn in a tenant transaction of the actor's tenant.
func (s *Service) write(ctx context.Context, actor audit.Actor, fn func(q db.Querier) error) error {
	if s.tx != nil {
		return fn(s.tx)
	}
	return s.db.WithTenantTx(ctx, actor.TenantID, fn)
}

// record appends an audit event.
func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, action, entity string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: id, Before: before, After: after})
}

// searchPatterns turns a filter text into LIKE patterns, one per normalised word.
func searchPatterns(q string) ([]string, error) {
	if len(q) > maxSearchTermLen {
		return nil, errInvalid(map[string]string{"q": "too_long"})
	}
	words := strings.Fields(textnorm.SearchNorm(q))
	patterns := make([]string, len(words))
	for i, w := range words {
		patterns[i] = "%" + w + "%"
	}
	return patterns, nil
}

// partySearchNorm is the search text of a party: name, phone digits, tax code and, for a
// supplier, the goods it supplies.
func partySearchNorm(p Party, suppliedGoods *string) string {
	parts := []string{p.Name, deref(p.PhoneKey), deref(p.TaxCode), deref(suppliedGoods)}
	return textnorm.SearchNorm(strings.Join(parts, " "))
}

// fieldErrors collects field-level validation failures.
type fieldErrors map[string]string

func (f fieldErrors) err() error {
	if len(f) == 0 {
		return nil
	}
	return errInvalid(f)
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// text trims v and checks it: required, at most maxLen characters, a single line.
func (f fieldErrors) text(field string, v *string, required bool, maxLen int) {
	*v = strings.TrimSpace(*v)
	switch {
	case *v == "":
		if required {
			f[field] = "required"
		}
	case utf8.RuneCountInString(*v) > maxLen:
		f[field] = "too_long"
	case !utf8.ValidString(*v) || strings.ContainsFunc(*v, isControl):
		f[field] = "invalid_text"
	}
}

// optional checks a clearable single-line text; "" and null clear it.
func (f fieldErrors) optional(field string, v *string, maxLen int) *string {
	if v == nil {
		return nil
	}
	s := *v
	f.text(field, &s, false, maxLen)
	if s == "" {
		return nil
	}
	return &s
}

// multiline is like optional but accepts line breaks (notes).
func (f fieldErrors) multiline(field string, v *string, maxLen int) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	switch {
	case s == "":
		return nil
	case utf8.RuneCountInString(s) > maxLen:
		f[field] = "too_long"
	case !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return isControl(r) && r != '\n' && r != '\r' && r != '\t' }):
		f[field] = "invalid_text"
	}
	return &s
}

// phone checks a phone number and returns it with its search key; "" and null clear both.
func (f fieldErrors) phone(field string, v *string) (phone, key *string) {
	p := f.optional(field, v, maxPhoneLen)
	if p == nil {
		return nil, nil
	}
	k := textnorm.PhoneKey(*p)
	if n := len(k); n < minPhoneDigits || n > maxPhoneDigits || strings.ContainsFunc(*p, notPhoneRune) {
		f[field] = "invalid"
		return nil, nil
	}
	return p, &k
}

func notPhoneRune(r rune) bool {
	return (r < '0' || r > '9') && !strings.ContainsRune("+-. ()", r)
}

// email checks an e-mail address and returns it lower-cased; "" and null clear it.
func (f fieldErrors) email(field string, v *string) *string {
	e := f.optional(field, v, maxEmailLen)
	if e == nil {
		return nil
	}
	addr, err := mail.ParseAddress(*e)
	if err != nil || addr.Address != *e || !strings.Contains(addr.Address, ".") {
		f[field] = "invalid"
		return nil
	}
	key := textnorm.EmailKey(*e)
	return &key
}

// taxCode normalises a tax code (spaces and dots removed, upper case) and checks it.
func (f fieldErrors) taxCode(field string, v *string) *string {
	t := f.optional(field, v, 40)
	if t == nil {
		return nil
	}
	n := strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '.' {
			return -1
		}
		return r
	}, *t))
	if !taxCodePattern.MatchString(n) {
		f[field] = "invalid"
		return nil
	}
	return &n
}

// terms checks payment terms in days.
func (f fieldErrors) terms(field string, v int) {
	if v < 0 || v > MaxPaymentTermsDays {
		f[field] = "out_of_range"
	}
}

// PartyInput are the party fields of a customer or supplier form. On update a nil pointer or an
// unset Opt keeps the stored value; an Opt set to null (or "") clears it.
type PartyInput struct {
	Kind    *string
	Name    *string
	Phone   Opt[string]
	Email   Opt[string]
	TaxCode Opt[string]
	Address Opt[string]
	Note    Opt[string]
}

// given lists the JSON names of the party fields present in the input.
func (in PartyInput) given() []string {
	var out []string
	add := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	add(in.Kind != nil, "kind")
	add(in.Name != nil, "name")
	add(in.Phone.Set, "phone")
	add(in.Email.Set, "email")
	add(in.TaxCode.Set, "tax_code")
	add(in.Address.Set, "address")
	add(in.Note.Set, "note")
	return out
}

// apply validates in onto p; creating requires a name. It reports whether a dedupe key changed.
func (in PartyInput) apply(p *Party, f fieldErrors, creating bool) bool {
	before := dedupeKeysOf(*p)
	if creating && p.Kind == "" {
		p.Kind = KindOrganization
	}
	if in.Kind != nil {
		if *in.Kind != KindPerson && *in.Kind != KindOrganization {
			f["kind"] = "invalid"
		} else {
			p.Kind = *in.Kind
		}
	}
	if in.Name != nil || creating {
		name := ""
		if in.Name != nil {
			name = *in.Name
		}
		f.text("name", &name, true, maxNameLen)
		p.Name, p.NameKey = name, textnorm.Key(name)
	}
	if in.Phone.Set {
		p.Phone, p.PhoneKey = f.phone("phone", in.Phone.Value)
	}
	if in.Email.Set {
		p.Email = f.email("email", in.Email.Value)
	}
	if in.TaxCode.Set {
		p.TaxCode = f.taxCode("tax_code", in.TaxCode.Value)
	}
	if in.Address.Set {
		p.Address = f.optional("address", in.Address.Value, maxAddressLen)
	}
	if in.Note.Set {
		p.Note = f.multiline("note", in.Note.Value, maxNoteLen)
	}
	return dedupeKeysOf(*p) != before
}

// creditLimit checks a credit limit: nil is no limit, zero allows no credit sale.
func (f fieldErrors) creditLimit(field string, v *money.Money) {
	if v == nil {
		return
	}
	if v.IsNegative() {
		f[field] = "negative"
	} else if v.Cmp(maxCreditLimit) > 0 {
		f[field] = "too_large"
	}
}

func validStatus(s string) bool { return s == StatusActive || s == StatusInactive }
