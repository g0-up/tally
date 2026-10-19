package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
	"github.com/g0-up/tally/apps/api/internal/platform/urls"
)

// Per-address limits on mails a stranger can trigger (store lookup, password reset), so the
// endpoints cannot be used to flood someone's inbox. Excess requests still answer 202.
const (
	mailPerAddressPerSecond = 1.0 / 600 // one every 10 minutes after the burst
	mailPerAddressBurst     = 3
	// Ownership transfer requests per owner.
	transferPerSecond = 1.0 / 3600
	transferBurst     = 5
)

// Deps are the collaborators of Service.
type Deps struct {
	DB          *db.DB
	Clock       clock.Clock
	Audit       *audit.Recorder
	Hasher      PasswordHasher
	Keys        Keys
	Provisioner TenantProvisioner
	URLs        urls.Builder
	// Mailer and Renderer deliver the account mails; without them mails are logged as skipped.
	Mailer   mailer.Mailer
	Renderer *mailer.Renderer
	// Location formats times in mails (APP_TZ).
	Location *time.Location
	Logger   *slog.Logger
	// TenantCreated is told the slug of every new tenant, so a host cache that remembered the
	// slug as unknown forgets it. Optional.
	TenantCreated func(slug string)
}

// Service implements sign-up, login, password reset, MFA, sessions, re-authentication and
// ownership transfer, and resolves session cookies and tenant hosts for the middleware.
type Service struct {
	db            *db.DB
	clock         clock.Clock
	audit         *audit.Recorder
	hasher        PasswordHasher
	keys          Keys
	provisioner   TenantProvisioner
	urls          urls.Builder
	mailer        mailer.Mailer
	renderer      *mailer.Renderer
	location      *time.Location
	logger        *slog.Logger
	tenantCreated func(string)

	mailLimiter     *middleware.RateLimiter
	transferLimiter *middleware.RateLimiter
	mailWG          sync.WaitGroup
}

// NewService returns the service; it fails when a required collaborator is missing.
func NewService(d Deps) (*Service, error) {
	if d.DB == nil || d.Clock == nil || d.Audit == nil || d.Hasher == nil || d.Provisioner == nil ||
		d.Keys.Tokens == nil || d.Keys.Recovery == nil || d.Keys.Challenge == nil || d.Keys.TOTP == nil {
		return nil, errors.New("identity: database, clock, audit, hasher, provisioner and keys are required")
	}
	s := &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, hasher: d.Hasher, keys: d.Keys, provisioner: d.Provisioner,
		urls: d.URLs, mailer: d.Mailer, renderer: d.Renderer, location: d.Location, logger: d.Logger,
		tenantCreated:   d.TenantCreated,
		mailLimiter:     middleware.NewRateLimiter(mailPerAddressPerSecond, mailPerAddressBurst),
		transferLimiter: middleware.NewRateLimiter(transferPerSecond, transferBurst),
	}
	if s.location == nil {
		s.location = time.UTC
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s, nil
}

// systemActor is the actor of anonymous account actions (sign-up, verification, reset).
func systemActor(tenantID uuid.UUID, source audit.Source, c Client) audit.Actor {
	return audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: source, RequestID: c.RequestID, IP: c.IP}
}

// ownerActor is the actor of an action of the owner.
func ownerActor(tenantID, userID uuid.UUID, c Client) audit.Actor {
	return audit.Actor{TenantID: tenantID, Type: audit.ActorOwner, ID: userID, Source: audit.SourceWeb, RequestID: c.RequestID, IP: c.IP}
}

// ---------------------------------------------------------------------------------------------
// Tenant creation

// OwnerSpec describes a new tenant and its owner.
type OwnerSpec struct {
	Tenant TenantSpec
	Email  string
	// Phone is the owner's phone number in the form normalizePhone returns; "" for none.
	Phone    string
	FullName string
	// PasswordHash is "" for an owner who chooses the password from the verification link.
	PasswordHash string
	Source       audit.Source
	Client       Client
}

// CreatedTenant is the outcome of CreateTenantWithOwner.
type CreatedTenant struct {
	Tenant TenantInfo
	Owner  User
	// VerifyURL is the one-time verification link that was mailed to the owner.
	VerifyURL string
}

// CreateTenantWithOwner provisions a tenant and its owner (pending verification) in one tenant
// transaction and mails the verification link. Sign-up, `tally tenant create` and `tally seed
// demo` all use it. An e-mail that already has an account anywhere is 422 email_in_use; that is
// found by the unique e-mail key when the owner is inserted, after the tenant was provisioned in
// the same transaction, so a taken e-mail costs the same work as a free one and leaves nothing.
func (s *Service) CreateTenantWithOwner(ctx context.Context, spec OwnerSpec) (CreatedTenant, error) {
	email, err := checkOwner(spec.Email, spec.FullName)
	if err != nil {
		return CreatedTenant{}, err
	}
	var phone *string
	if spec.Phone != "" {
		p, ok := normalizePhone(spec.Phone)
		if !ok {
			return CreatedTenant{}, errInvalid(map[string]string{"phone": "invalid"})
		}
		phone = &p
	}
	if spec.Tenant.ID == uuid.Nil {
		spec.Tenant.ID = ids.New()
	}
	token, err := authn.NewToken()
	if err != nil {
		return CreatedTenant{}, err
	}
	now := s.clock.Now()
	owner := User{
		ID: ids.New(), TenantID: spec.Tenant.ID, Email: email, EmailKey: textnorm.EmailKey(email),
		Phone: phone, FullName: strings.TrimSpace(spec.FullName), PasswordHash: spec.PasswordHash, Status: StatusPending,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	var out CreatedTenant
	err = s.db.WithTenantTx(ctx, spec.Tenant.ID, func(q db.Querier) error {
		tenant, created, err := s.provisioner.Provision(ctx, q, spec.Tenant, systemActor(spec.Tenant.ID, spec.Source, spec.Client))
		if err != nil {
			return err
		}
		if !created {
			return fmt.Errorf("identity: tenant %s already exists", spec.Tenant.ID)
		}
		if err := insertUser(ctx, q, owner); err != nil {
			if db.IsUniqueViolation(err, "ux_users__email_key") {
				return errEmailInUse()
			}
			return err
		}
		if err := insertToken(ctx, q, owner.TenantID, owner.ID, PurposeVerifyEmail, s.keys.Tokens.Sum(token), nil,
			now.Add(VerifyTokenTTL), now); err != nil {
			return err
		}
		out.Tenant = tenant
		return nil
	})
	if err != nil {
		return CreatedTenant{}, err
	}
	out.Owner = owner
	out.VerifyURL = s.urls.Tenant(out.Tenant.Slug, "/verify?token="+token)
	if s.tenantCreated != nil {
		s.tenantCreated(out.Tenant.Slug)
	}
	s.send(mailVerifyEmail, owner.Email, mailData{
		Name: owner.FullName, StoreName: out.Tenant.DisplayName, StoreURL: s.urls.Tenant(out.Tenant.Slug, "/"), Link: out.VerifyURL,
	})
	return out, nil
}

// ---------------------------------------------------------------------------------------------
// Sign-up and store lookup (root host)

// SignupInput is a sign-up form.
type SignupInput struct {
	Email string
	// Phone is optional; when given, the owner can also sign in with it.
	Phone     string
	Password  string
	FullName  string
	StoreName string
	Slug      string
	// ReferralCode is the optional partner code of the sign-up link.
	ReferralCode string
	Client       Client
}

// SignupResult is the answer of a sign-up; it is the same whether or not the e-mail already
// had an account.
type SignupResult struct {
	StoreURL string
}

// Signup creates a tenant with its owner and mails the verification link. When the e-mail
// already has an account, it creates nothing, mails that person the address of their store and
// answers exactly as for a new account. Both paths do the same expensive work (password hashing,
// provisioning the tenant in a transaction that a taken e-mail rolls back, rendering one mail),
// so neither the answer nor its timing tells whether an e-mail is registered. A taken store
// address is 422 slug_taken (the live address check reveals that anyway).
func (s *Service) Signup(ctx context.Context, in SignupInput) (SignupResult, error) {
	fields := map[string]string{}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		fields["email"] = "invalid"
	}
	if reason := passwordProblem(in.Password); reason != "" {
		fields["password"] = reason
	}
	phone := ""
	if strings.TrimSpace(in.Phone) != "" {
		if phone, ok = normalizePhone(in.Phone); !ok {
			fields["phone"] = "invalid"
		}
	}
	for _, err := range []error{validateFullName(in.FullName), s.provisioner.ValidateName("store_name", strings.TrimSpace(in.StoreName))} {
		if ae, ok := apperr.As(err); ok {
			for k, r := range ae.Fields {
				fields[k] = r
			}
		}
	}
	if len(fields) > 0 {
		return SignupResult{}, errInvalid(fields)
	}
	slug := strings.TrimSpace(in.Slug)
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return s.provisioner.RequireFreeSlug(ctx, q, slug)
	}); err != nil {
		return SignupResult{}, err
	}
	result := SignupResult{StoreURL: s.urls.Tenant(slug, "/")}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return SignupResult{}, err
	}
	_, err = s.CreateTenantWithOwner(ctx, OwnerSpec{
		Tenant: TenantSpec{Slug: slug, DisplayName: strings.TrimSpace(in.StoreName), ReferralCode: strings.TrimSpace(in.ReferralCode)},
		Email:  email, Phone: phone,
		FullName: in.FullName, PasswordHash: hash, Source: audit.SourceWeb, Client: in.Client,
	})
	if apperr.Is(err, "email_in_use") {
		// The e-mail has an account: the provisioning was rolled back; answer as for a new one.
		return result, s.accountExists(ctx, email)
	}
	if err != nil {
		return SignupResult{}, err
	}
	return result, nil
}

// accountExists mails the owner of an existing account (unless disabled) the address of their
// store.
func (s *Service) accountExists(ctx context.Context, email string) error {
	var (
		u     User
		slug  string
		found bool
	)
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		u, slug, found, err = accountByEmail(ctx, q, textnorm.EmailKey(email))
		return err
	}); err != nil {
		return err
	}
	if found && u.Status != StatusDisabled {
		s.send(mailAccountExists, u.Email, mailData{
			Name: u.FullName, StoreURL: s.urls.Tenant(slug, "/"),
			Link: s.urls.Tenant(slug, "/reset-password"),
		})
	}
	return nil
}

// CheckSlug answers the live store address check of the sign-up form.
func (s *Service) CheckSlug(ctx context.Context, slug string) (SlugCheck, error) {
	var out SlugCheck
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		out, err = s.provisioner.CheckSlug(ctx, q, slug)
		return err
	})
	return out, err
}

// StoreLookup mails the address of the store of an e-mail ("find your store"). It never tells
// whether the e-mail has an account; repeated lookups of one address are dropped silently.
func (s *Service) StoreLookup(ctx context.Context, email string) error {
	addr, ok := normalizeEmail(email)
	if !ok {
		return errInvalid(map[string]string{"email": "invalid"})
	}
	key := textnorm.EmailKey(addr)
	if allowed, _ := s.mailLimiter.Allow("lookup:" + key); !allowed {
		return nil
	}
	var (
		u     User
		slug  string
		found bool
	)
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		u, slug, found, err = accountByEmail(ctx, q, key)
		return err
	}); err != nil {
		return err
	}
	if !found || u.Status == StatusDisabled {
		return nil
	}
	s.send(mailStoreLookup, u.Email, mailData{Name: u.FullName, StoreURL: s.urls.Tenant(slug, "/"), Link: s.urls.Tenant(slug, "/login")})
	return nil
}

// ---------------------------------------------------------------------------------------------
// E-mail verification and password reset (tenant host)

// VerifyInput confirms the e-mail of an owner. Password is required when the account has none
// yet (created by `tally tenant create`).
type VerifyInput struct {
	HostTenant uuid.UUID
	Token      string
	Password   string
	Client     Client
}

// VerifyEmail consumes a verification token of the host tenant and activates the owner.
func (s *Service) VerifyEmail(ctx context.Context, in VerifyInput) error {
	if in.HostTenant == uuid.Nil || in.Token == "" {
		return errTokenInvalid()
	}
	var newHash string
	if in.Password != "" {
		if err := checkNewPassword(in.Password); err != nil {
			return err
		}
		h, err := s.hasher.Hash(in.Password)
		if err != nil {
			return err
		}
		newHash = h
	}
	now := s.clock.Now()
	return s.withAccountTx(ctx, in.HostTenant, func(q db.Querier) error {
		tok, found, err := consumeToken(ctx, q, in.HostTenant, PurposeVerifyEmail, s.keys.Tokens.Sum(in.Token), now)
		if err != nil {
			return err
		}
		if !found {
			return errTokenInvalid()
		}
		u, found, err := userByID(ctx, q, tok.TenantID, tok.UserID, true)
		if err != nil {
			return err
		}
		if !found || u.Status == StatusDisabled {
			return errTokenInvalid()
		}
		hash := u.PasswordHash
		if hash == "" {
			if newHash == "" {
				return errInvalid(map[string]string{"password": "required"})
			}
			hash = newHash
		}
		status := u.Status
		if status == StatusPending {
			status = StatusActive
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE users SET status = $3, password_hash = $4, email_verified_at = COALESCE(email_verified_at, $5),
			       updated_at = $5, version = version + 1
			WHERE tenant_id = $1 AND id = $2`, u.TenantID, u.ID, status, hash, now); err != nil {
			return fmt.Errorf("identity: verify e-mail: %w", err)
		}
		return s.audit.Record(ctx, q, systemActor(u.TenantID, audit.SourceWeb, in.Client), audit.Event{
			Action: "user.verify_email", EntityType: "user", EntityID: u.ID,
		})
	})
}

// RequestPasswordReset mails a reset link to the owner of the host tenant when email is theirs.
// It answers the same whatever the outcome.
func (s *Service) RequestPasswordReset(ctx context.Context, hostTenant uuid.UUID, email string) error {
	addr, ok := normalizeEmail(email)
	if !ok {
		return errInvalid(map[string]string{"email": "invalid"})
	}
	if hostTenant == uuid.Nil {
		return nil
	}
	key := textnorm.EmailKey(addr)
	if allowed, _ := s.mailLimiter.Allow("reset:" + key); !allowed {
		return nil
	}
	token, err := authn.NewToken()
	if err != nil {
		return err
	}
	now := s.clock.Now()
	var (
		u     User
		store TenantInfo
		sent  bool
	)
	err = s.withAccountTx(ctx, hostTenant, func(q db.Querier) error {
		var found bool
		var err error
		u, found, err = userByEmail(ctx, q, hostTenant, key)
		if err != nil || !found || u.Status == StatusDisabled {
			return err
		}
		if store, err = readTenantInfo(ctx, q, hostTenant); err != nil {
			return err
		}
		if err := retireTokens(ctx, q, u.TenantID, u.ID, PurposeResetPassword, now); err != nil {
			return err
		}
		sent = true
		return insertToken(ctx, q, u.TenantID, u.ID, PurposeResetPassword, s.keys.Tokens.Sum(token), nil, now.Add(ResetTokenTTL), now)
	})
	if err != nil || !sent {
		return err
	}
	s.send(mailResetPassword, u.Email, mailData{
		Name: u.FullName, StoreName: store.DisplayName, StoreURL: s.urls.Tenant(store.Slug, "/"),
		Link: s.urls.Tenant(store.Slug, "/reset-password?token="+token),
	})
	return nil
}

// ResetInput sets a new password from a reset link.
type ResetInput struct {
	HostTenant uuid.UUID
	Token      string
	Password   string
	Client     Client
}

// ResetPassword consumes a reset token, sets the password and signs out every session. A
// pending owner is activated: the link proved they own the address.
func (s *Service) ResetPassword(ctx context.Context, in ResetInput) error {
	if err := checkNewPassword(in.Password); err != nil {
		return err
	}
	if in.HostTenant == uuid.Nil || in.Token == "" {
		return errTokenInvalid()
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.withAccountTx(ctx, in.HostTenant, func(q db.Querier) error {
		tok, found, err := consumeToken(ctx, q, in.HostTenant, PurposeResetPassword, s.keys.Tokens.Sum(in.Token), now)
		if err != nil {
			return err
		}
		if !found {
			return errTokenInvalid()
		}
		u, found, err := userByID(ctx, q, tok.TenantID, tok.UserID, true)
		if err != nil {
			return err
		}
		if !found || u.Status == StatusDisabled {
			return errTokenInvalid()
		}
		status := u.Status
		if status == StatusPending {
			status = StatusActive
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE users SET password_hash = $3, status = $4, email_verified_at = COALESCE(email_verified_at, $5),
			       updated_at = $5, version = version + 1
			WHERE tenant_id = $1 AND id = $2`, u.TenantID, u.ID, hash, status, now); err != nil {
			return fmt.Errorf("identity: reset password: %w", err)
		}
		n, err := revokeSessions(ctx, q, u.TenantID, u.ID, uuid.Nil, "password_reset", now)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, q, systemActor(u.TenantID, audit.SourceWeb, in.Client), audit.Event{
			Action: "user.reset_password", EntityType: "user", EntityID: u.ID,
			After: map[string]any{"sessions_revoked": n},
		})
	})
}

// ---------------------------------------------------------------------------------------------
// Login, logout and re-authentication (tenant host)

// LoginInput is a login form on a tenant host.
type LoginInput struct {
	HostTenant uuid.UUID
	// Login is the owner's e-mail address or Vietnamese phone number.
	Login    string
	Password string
	Client   Client
}

// LoginResult is either a session or, for an owner with MFA, a challenge to answer with a code.
type LoginResult struct {
	Session      *IssuedSession
	MFAChallenge string
}

// Login checks the throttle, then the credentials of the owner of the host tenant only: an
// unknown e-mail or phone, the owner of another store and a wrong password get the identical
// invalid_credentials after the same hashing work. The throttle counts attempts per identifier
// as typed (e-mail or phone), so it never tells which phone belongs to which e-mail.
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	raw := strings.TrimSpace(in.Login)
	if fields := requiredFields(map[string]string{"login": raw, "password": in.Password}); fields != nil {
		return LoginResult{}, errInvalid(fields)
	}
	id, valid := parseLoginIdentifier(raw)
	throttleKey := textnorm.EmailKey(raw)
	if valid {
		throttleKey = id.throttleKey()
	}
	if err := s.checkThrottle(ctx, throttleKey, in.Client.IP); err != nil {
		return LoginResult{}, err
	}
	var (
		u     User
		found bool
	)
	if valid && in.HostTenant != uuid.Nil {
		if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
			var err error
			if id.phone != "" {
				u, found, err = userByPhone(ctx, q, in.HostTenant, id.phone)
			} else {
				u, found, err = userByEmail(ctx, q, in.HostTenant, id.emailKey)
			}
			return err
		}); err != nil {
			return LoginResult{}, err
		}
	}
	ok := false
	if found && u.Status != StatusDisabled {
		var err error
		if ok, err = s.hasher.Verify(u.PasswordHash, in.Password); err != nil {
			return LoginResult{}, err
		}
	} else {
		s.hasher.DummyVerify(in.Password)
		found = false
	}
	if !ok {
		return LoginResult{}, s.loginFailed(ctx, throttleKey, in.Client, found, u, "password")
	}
	switch u.Status {
	case StatusLocked:
		return LoginResult{}, errAccountLocked()
	case StatusPending:
		return LoginResult{}, errEmailNotVerified()
	}
	if throttleKey != u.EmailKey {
		// The session records the success under the e-mail key; clear the phone's failures too.
		if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
			return recordAttempt(ctx, q, throttleKey, in.Client.IP, true, s.clock.Now())
		}); err != nil {
			return LoginResult{}, err
		}
	}
	if u.MFAEnabled {
		return LoginResult{MFAChallenge: s.newChallenge(u)}, nil
	}
	issued, err := s.startSession(ctx, u, in.Client, false, nil)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Session: &issued}, nil
}

// checkThrottle refuses a login of a locked e-mail and client address pair.
func (s *Service) checkThrottle(ctx context.Context, emailKey, ip string) error {
	now := s.clock.Now()
	var until time.Time
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		until, err = lockedUntil(ctx, q, emailKey, ip, now)
		return err
	}); err != nil {
		return err
	}
	if !until.IsZero() {
		return newThrottled(until.Sub(now))
	}
	return nil
}

// loginFailed records a failed attempt (and, for a real account, a login_failed audit event)
// and returns invalid_credentials (invalid_code for the MFA step).
func (s *Service) loginFailed(ctx context.Context, emailKey string, c Client, known bool, u User, step string) error {
	now := s.clock.Now()
	record := func(q db.Querier) error { return recordAttempt(ctx, q, emailKey, c.IP, false, now) }
	var err error
	if known {
		err = s.db.WithTenantTx(ctx, u.TenantID, func(q db.Querier) error {
			if err := record(q); err != nil {
				return err
			}
			return s.audit.Record(ctx, q, systemActor(u.TenantID, audit.SourceWeb, c), audit.Event{
				Action: "user.reject_login", EntityType: "user", EntityID: u.ID, After: map[string]any{"step": step},
			})
		})
	} else {
		err = s.db.WithPlatformTx(ctx, record)
	}
	if err != nil {
		return err
	}
	if step == "mfa" {
		return errInvalidCode()
	}
	return errInvalidCredentials()
}

// startSession creates a session for u, records the successful login and mails a new-device
// notice when the device was never seen before (not on the very first login). A non-nil admit runs
// first in the same transaction and refuses the session by returning an error: whatever it
// consumes is kept only if the session is created.
func (s *Service) startSession(ctx context.Context, u User, c Client, mfaVerified bool,
	admit func(q db.Querier, now time.Time) error,
) (IssuedSession, error) {
	token, err := authn.NewToken()
	if err != nil {
		return IssuedSession{}, err
	}
	now := s.clock.Now()
	ua := truncateUA(c.UserAgent)
	sess := Session{
		ID: ids.New(), TenantID: u.TenantID, UserID: u.ID, DeviceLabel: deviceLabel(ua), UserAgent: &ua, IP: &c.IP,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionAbsoluteTTL), ReauthenticatedAt: &now,
	}
	if mfaVerified {
		sess.MFAVerifiedAt = &now
	}
	var notify bool
	err = s.withAccountTx(ctx, u.TenantID, func(q db.Querier) error {
		if admit != nil {
			if err := admit(q, now); err != nil {
				return err
			}
		}
		hadSession, known, err := deviceHistory(ctx, q, u.TenantID, u.ID, sess.DeviceLabel)
		if err != nil {
			return err
		}
		notify = hadSession && !known
		if err := insertSession(ctx, q, sess, s.keys.Tokens.Sum(token)); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE users SET last_login_at = $3 WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, u.ID, now); err != nil {
			return fmt.Errorf("identity: record login: %w", err)
		}
		if err := recordAttempt(ctx, q, u.EmailKey, c.IP, true, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, ownerActor(u.TenantID, u.ID, c), audit.Event{
			Action: "session.create", EntityType: "session", EntityID: sess.ID,
			After: map[string]any{"device": sess.DeviceLabel, "mfa": mfaVerified},
		})
	})
	if err != nil {
		return IssuedSession{}, err
	}
	if notify {
		if info, err := s.tenantInfo(ctx, u.TenantID); err == nil {
			s.send(mailNewDevice, u.Email, mailData{
				Name: u.FullName, StoreName: info.DisplayName, StoreURL: s.urls.Tenant(info.Slug, "/"),
				Link: s.urls.Tenant(info.Slug, "/settings/security"), Device: sess.DeviceLabel, Time: s.localTime(now),
			})
		} else {
			s.logger.Error("new device mail not sent", "error", err.Error())
		}
	}
	return IssuedSession{ID: sess.ID, Token: token, ExpiresAt: sess.ExpiresAt}, nil
}

// Logout revokes the session. It finds the row by its id (and owner), not by tenant, so it also
// signs out a session a sandbox reset moved to the new tenant while the request ran. A session
// that is no longer open needs nothing.
func (s *Service) Logout(ctx context.Context, p middleware.Principal) error {
	now := s.clock.Now()
	return s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			UPDATE sessions SET revoked_at = $3, revoked_reason = 'logout'
			WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, p.SessionID, p.UserID, now); err != nil {
			return fmt.Errorf("identity: log out: %w", err)
		}
		return nil
	})
}

// Reauth checks the password of the signed-in owner and opens the re-authentication window. The
// session token is rotated (the old cookie value stops working). Wrong passwords count towards
// the login throttle and are audited. Every step answers sandbox_replaced once a sandbox reset has
// moved the session to a new tenant, whatever the password.
func (s *Service) Reauth(ctx context.Context, p middleware.Principal, password string, c Client) (IssuedSession, error) {
	var (
		u     User
		found bool
	)
	if err := s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		var err error
		u, found, err = userByID(ctx, q, p.TenantID, p.UserID, false)
		return err
	}); err != nil {
		return IssuedSession{}, err
	}
	if !found || u.Status != StatusActive {
		return IssuedSession{}, apperr.NewUnauthenticated()
	}
	if err := s.checkThrottle(ctx, u.EmailKey, c.IP); err != nil {
		return IssuedSession{}, err
	}
	ok, err := s.hasher.Verify(u.PasswordHash, password)
	if err != nil {
		return IssuedSession{}, err
	}
	now := s.clock.Now()
	if !ok {
		if err := s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
			if err := recordAttempt(ctx, q, u.EmailKey, c.IP, false, now); err != nil {
				return err
			}
			return s.audit.Record(ctx, q, ownerActor(u.TenantID, u.ID, c), audit.Event{
				Action: "user.reject_reauth", EntityType: "user", EntityID: u.ID,
				After: map[string]any{"session_id": p.SessionID},
			})
		}); err != nil {
			return IssuedSession{}, err
		}
		return IssuedSession{}, errInvalidPassword()
	}
	token, err := authn.NewToken()
	if err != nil {
		return IssuedSession{}, err
	}
	var expires time.Time
	err = s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			UPDATE sessions SET token_hash = $4, reauthenticated_at = $5, last_seen_at = $5
			WHERE tenant_id = $1 AND user_id = $2 AND id = $3 AND revoked_at IS NULL
			RETURNING expires_at`, p.TenantID, p.UserID, p.SessionID, s.keys.Tokens.Sum(token), now).Scan(&expires)
		if db.IsNoRows(err) {
			return apperr.NewUnauthenticated()
		}
		if err != nil {
			return fmt.Errorf("identity: re-authenticate: %w", err)
		}
		return recordAttempt(ctx, q, u.EmailKey, c.IP, true, now)
	})
	if err != nil {
		return IssuedSession{}, err
	}
	return IssuedSession{ID: p.SessionID, Token: token, ExpiresAt: expires}, nil
}

// ChangePassword sets a new password for the signed-in owner (inside the re-authentication
// window) and signs out every other session; the current one stays signed in.
func (s *Service) ChangePassword(ctx context.Context, p middleware.Principal, password string, c Client) (int64, error) {
	if err := checkNewPassword(password); err != nil {
		return 0, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return 0, err
	}
	now := s.clock.Now()
	var n int64
	err = s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		u, found, err := userByID(ctx, q, p.TenantID, p.UserID, true)
		if err != nil {
			return err
		}
		if !found || u.Status != StatusActive {
			return apperr.NewUnauthenticated()
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE users SET password_hash = $3, updated_at = $4, version = version + 1
			WHERE tenant_id = $1 AND id = $2`, u.TenantID, u.ID, hash, now); err != nil {
			return fmt.Errorf("identity: change password: %w", err)
		}
		if n, err = revokeSessions(ctx, q, u.TenantID, u.ID, p.SessionID, "password_changed", now); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, ownerActor(u.TenantID, u.ID, c), audit.Event{
			Action: "user.change_password", EntityType: "user", EntityID: u.ID,
			After: map[string]any{"sessions_revoked": n},
		})
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ---------------------------------------------------------------------------------------------
// Tenant information

// TenantInfo is the store of an owner as the identity endpoints show it.
type TenantInfo struct {
	ID          uuid.UUID  `db:"id"`
	Slug        string     `db:"slug"`
	DisplayName string     `db:"display_name"`
	Status      string     `db:"status"`
	IsSandbox   bool       `db:"is_sandbox"`
	SandboxOf   *uuid.UUID `db:"sandbox_of"`
	Timezone    string     `db:"timezone"`
}

func readTenantInfo(ctx context.Context, q db.Querier, id uuid.UUID) (TenantInfo, error) {
	var t TenantInfo
	if err := q.QueryRowxContext(ctx, `SELECT id, slug, display_name, status, is_sandbox, sandbox_of, timezone FROM tenants WHERE id = $1`,
		id).StructScan(&t); err != nil {
		return TenantInfo{}, fmt.Errorf("identity: read tenant: %w", err)
	}
	return t, nil
}

func (s *Service) tenantInfo(ctx context.Context, id uuid.UUID) (TenantInfo, error) {
	var t TenantInfo
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		t, err = readTenantInfo(ctx, q, id)
		return err
	})
	return t, err
}

// marshalPayload encodes a token payload.
func marshalPayload(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("identity: encode token payload: %w", err)
	}
	return b, nil
}
