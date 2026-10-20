package scanner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Catalog is the part of the catalog the scanner needs.
type Catalog interface {
	// ValidateCode answers 422 when a trimmed code cannot be a barcode.
	ValidateCode(code string) error
	// ItemNames returns the display name of a SKU and the name of one of its units, read under the
	// tenant's row-level security. It returns ErrUnknownSKU or ErrUnknownUnit when the tenant has
	// no such SKU, or the SKU no such unit.
	ItemNames(ctx context.Context, tenantID, skuID, unitID uuid.UUID) (product, unit string, err error)
}

// Errors of Catalog.ItemNames.
var (
	ErrUnknownSKU  = errors.New("scanner: unknown SKU")
	ErrUnknownUnit = errors.New("scanner: unknown unit")
)

// maxClaimCodeLen bounds a claim code; longer input is never one (rand.Text is 26 characters).
const maxClaimCodeLen = 64

// Deps are the collaborators of the service.
type Deps struct {
	DB      *db.DB
	Clock   clock.Clock
	Catalog Catalog
	// Hub carries the events and scan results; nil gets a MemoryHub.
	Hub Hub
}

// Service runs the scanner sessions. Every database step is one short transaction: no request
// holds a connection while it waits for the other side.
type Service struct {
	db      *db.DB
	clock   clock.Clock
	catalog Catalog
	hub     Hub
}

// NewService returns the service on d.
func NewService(d Deps) *Service {
	if d.Hub == nil {
		d.Hub = NewMemoryHub()
	}
	return &Service{db: d.DB, clock: d.Clock, catalog: d.Catalog, hub: d.Hub}
}

// Hub returns the hub the service publishes on; the server closes it when it shuts down.
func (s *Service) Hub() Hub { return s.hub }

// scanEvent is the data of the scan event the desktop receives.
type scanEvent struct {
	ScanID uuid.UUID `json:"scan_id"`
	Code   string    `json:"code"`
	TTLMs  int64     `json:"ttl_ms"`
}

// claimRequestedEvent is the data of the claim_requested event the desktop receives.
type claimRequestedEvent struct {
	DeviceLabel string `json:"device_label,omitempty"`
	VerifyCode  string `json:"verify_code"`
}

// revokedEvent is the data of the revoked event.
type revokedEvent struct {
	By RevokedBy `json:"by"`
}

// Create opens a session for the desktop of o and returns its claim code, which is never shown
// again. A user keeps at most maxOpenSessions sessions; the ones closed to make room are chosen by
// sessionToClose.
func (s *Service) Create(ctx context.Context, o Owner) (Created, error) {
	code, codeHash := newSecret()
	id, now := ids.New(), s.clock.Now()
	var revoked []uuid.UUID
	err := s.db.WithTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		open, err := lockOpenSessions(ctx, q, o.TenantID, o.UserID, now)
		if err != nil {
			return err
		}
		for len(open) >= maxOpenSessions {
			i := sessionToClose(open)
			if _, err := revokeSession(ctx, q, open[i].ID, RevokedBySystem, now); err != nil {
				return err
			}
			revoked, open = append(revoked, open[i].ID), slices.Delete(open, i, i+1)
		}
		return insertSession(ctx, q, newSession{
			id: id, tenantID: o.TenantID, createdBy: o.UserID, loginSessionID: o.LoginSessionID, claimCodeHash: codeHash, now: now,
		})
	})
	if err != nil {
		return Created{}, err
	}
	for _, old := range revoked {
		s.ended(Key{TenantID: o.TenantID, SessionID: old}, RevokedBySystem)
	}
	return Created{ID: id, ClaimCode: ClaimCode{Code: code, ExpiresAt: now.Add(claimTTL)}}, nil
}

// sessionToClose picks, among open sessions sorted oldest first, the one a new session replaces:
// the oldest that no phone claimed yet, else the oldest. A card still showing its QR code loses
// nothing a person set up, and a phone in use is closed only when every session has one.
func sessionToClose(open []openSession) int {
	for i, o := range open {
		if o.Waiting {
			return i
		}
	}
	return 0
}

// RefreshCode replaces the claim code of a waiting session (409 already_claimed otherwise).
func (s *Service) RefreshCode(ctx context.Context, o Owner, id uuid.UUID) (ClaimCode, error) {
	code, codeHash := newSecret()
	now := s.clock.Now()
	err := s.db.WithTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		if _, err := s.owned(ctx, q, o, id, now); err != nil {
			return err
		}
		ok, err := replaceClaimCode(ctx, q, id, codeHash, now)
		if err == nil && !ok {
			err = errAlreadyClaimed()
		}
		return err
	})
	if err != nil {
		return ClaimCode{}, err
	}
	return ClaimCode{Code: code, ExpiresAt: now.Add(claimTTL)}, nil
}

// Approve lets the pending phone send codes (409 not_pending otherwise) and tells the phone.
func (s *Service) Approve(ctx context.Context, o Owner, id uuid.UUID) (Status, error) {
	now := s.clock.Now()
	var sess Session
	err := s.db.WithTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		if _, err := s.owned(ctx, q, o, id, now); err != nil {
			return err
		}
		ok, err := approveSession(ctx, q, id, now)
		if err != nil {
			return err
		}
		if !ok {
			return errNotPending()
		}
		sess, err = getSession(ctx, q, id, now)
		return err
	})
	if err != nil {
		return Status{}, err
	}
	s.hub.Publish(Key{TenantID: o.TenantID, SessionID: id}, RolePhone, Event{Name: EventApproved, Data: struct{}{}})
	return sess.status(), nil
}

// Reject sends the pending phone away (409 not_pending otherwise): the session forgets it and
// waits on a new claim code, which it returns.
func (s *Service) Reject(ctx context.Context, o Owner, id uuid.UUID) (ClaimCode, error) {
	code, codeHash := newSecret()
	now := s.clock.Now()
	err := s.db.WithTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		if _, err := s.owned(ctx, q, o, id, now); err != nil {
			return err
		}
		ok, err := rejectSession(ctx, q, id, codeHash, now)
		if err == nil && !ok {
			err = errNotPending()
		}
		return err
	})
	if err != nil {
		return ClaimCode{}, err
	}
	s.hub.Publish(Key{TenantID: o.TenantID, SessionID: id}, RolePhone, Event{Name: EventRejected, Data: struct{}{}})
	return ClaimCode{Code: code, ExpiresAt: now.Add(claimTTL)}, nil
}

// Status returns the desktop view of a session of o.
func (s *Service) Status(ctx context.Context, o Owner, id uuid.UUID) (Status, error) {
	now := s.clock.Now()
	var sess Session
	err := s.db.WithReadOnlyTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		var err error
		sess, err = s.owned(ctx, q, o, id, now)
		return err
	})
	if err != nil {
		return Status{}, err
	}
	return sess.status(), nil
}

// Revoke ends a session of o from the desktop and tells both sides.
func (s *Service) Revoke(ctx context.Context, o Owner, id uuid.UUID) error {
	now := s.clock.Now()
	var changed bool
	err := s.db.WithTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		if _, err := s.owned(ctx, q, o, id, now); err != nil {
			return err
		}
		var err error
		changed, err = revokeSession(ctx, q, id, RevokedByDesktop, now)
		return err
	})
	if err != nil {
		return err
	}
	if changed {
		s.ended(Key{TenantID: o.TenantID, SessionID: id}, RevokedByDesktop)
	}
	return nil
}

// RevokeForSignOut ends a session whose desktop login session is gone and tells both sides. The
// desktop and the phone streams both check the login session, so two of them can revoke at once:
// only the one whose UPDATE changed the row tells the streams and closes them. The other leaves
// them alone, since closing them could cut a stream before the winner's end event reaches it; every
// revocation goes through ended, so the winner always closes them.
func (s *Service) RevokeForSignOut(ctx context.Context, key Key) error {
	now := s.clock.Now()
	var changed bool
	err := s.db.WithTenantTx(ctx, key.TenantID, func(q db.Querier) error {
		var err error
		changed, err = revokeSession(ctx, q, key.SessionID, RevokedBySystem, now)
		return err
	})
	if err != nil {
		return err
	}
	if changed {
		s.ended(key, RevokedBySystem)
	}
	return nil
}

// DesktopTick runs on every tick of a desktop stream: it records that the desktop is alive
// (unless touch is false, while the tenant is in maintenance) and returns the session state.
func (s *Service) DesktopTick(ctx context.Context, o Owner, id uuid.UUID, touch bool) (Status, error) {
	now := s.clock.Now()
	var sess Session
	read := func(q db.Querier) error {
		if touch {
			if err := touchDesktop(ctx, q, id, now); err != nil {
				return err
			}
		}
		var err error
		sess, err = s.owned(ctx, q, o, id, now)
		return err
	}
	var err error
	if touch {
		err = s.db.WithTenantTx(ctx, o.TenantID, read)
	} else {
		err = s.db.WithReadOnlyTenantTx(ctx, o.TenantID, read)
	}
	if err != nil {
		return Status{}, err
	}
	return sess.status(), nil
}

// CheckClaimCode answers 410 claim_invalid, read-only, unless code is the live claim code of a
// waiting session of the tenant. The phone route asks it before the tenant's maintenance and
// subscription state, which only a holder of a real code may learn.
func (s *Service) CheckClaimCode(ctx context.Context, tenantID uuid.UUID, code string) error {
	code = strings.TrimSpace(code)
	if tenantID == uuid.Nil || code == "" || len(code) > maxClaimCodeLen {
		return errClaimInvalid()
	}
	now := s.clock.Now()
	return s.db.WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
		found, err := claimableSession(ctx, q, hashOf(code), now)
		if err == nil && !found {
			err = errClaimInvalid()
		}
		return err
	})
}

// Claim hands the session of a live claim code to a phone of the tenant and tells the desktop.
// Every failure is the same 410 claim_invalid, so nobody learns which codes exist or where.
func (s *Service) Claim(ctx context.Context, tenantID uuid.UUID, code, userAgent string) (Claimed, error) {
	code = strings.TrimSpace(code)
	if tenantID == uuid.Nil || code == "" || len(code) > maxClaimCodeLen {
		return Claimed{}, errClaimInvalid()
	}
	secret, secretHash := newSecret()
	var label *string
	if l := DeviceLabel(userAgent); l != "" {
		label = &l
	}
	now := s.clock.Now()
	var id uuid.UUID
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var found bool
		var err error
		id, found, err = claimSession(ctx, q, hashOf(code), secretHash, label, now)
		if err == nil && !found {
			err = errClaimInvalid()
		}
		return err
	})
	if err != nil {
		return Claimed{}, err
	}
	verify := VerifyCode(secretHash)
	ev := claimRequestedEvent{VerifyCode: verify}
	if label != nil {
		ev.DeviceLabel = *label
	}
	s.hub.Publish(Key{TenantID: tenantID, SessionID: id}, RoleDesktop, Event{Name: EventClaimRequested, Data: ev})
	return Claimed{SessionID: id, Secret: secret, VerifyCode: verify}, nil
}

// PhoneSession returns the session one of the phone's secrets belongs to; 401
// scan_session_ended when none does (unknown, revoked or rejected). An ended session is returned
// with StateEnded.
func (s *Service) PhoneSession(ctx context.Context, tenantID uuid.UUID, secrets []string) (Session, error) {
	now := s.clock.Now()
	var sess Session
	err := s.db.WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
		var err error
		sess, err = s.phone(ctx, q, secrets, now)
		return err
	})
	return sess, err
}

// Scan sends a code from an approved phone to its desktop and waits for the answer. The request
// is recorded under its Idempotency-Key in one short transaction first, so a retry replays the
// same scan id (or the stored answer); the wait itself holds no database connection. The
// desktop_unavailable and timeout answers are not stored. An answer stored while no waiter was
// registered (a retry the desktop answered between the commit and the wait) is read back: right
// after a retry registers its waiter, and by any scan that times out.
func (s *Service) Scan(ctx context.Context, tenantID uuid.UUID, secrets []string, key idempotency.Key, code string) (ScanResult, error) {
	code = strings.TrimSpace(code)
	if err := s.catalog.ValidateCode(code); err != nil {
		return ScanResult{}, err
	}
	now, id := s.clock.Now(), ids.New()
	var sess Session
	var row scan
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var err error
		if sess, err = s.phone(ctx, q, secrets, now); err != nil {
			return err
		}
		switch sess.State {
		case StatePending:
			return errPendingApproval()
		case StateClaimed:
		default:
			return errSessionEnded()
		}
		row, err = putScan(ctx, q, newScan{
			id: id, tenantID: tenantID, sessionID: sess.ID, key: key.Value, requestHash: key.RequestHash, code: code, now: now,
		})
		return err
	})
	if err != nil {
		return ScanResult{}, err
	}
	if !bytes.Equal(row.RequestHash, key.RequestHash) {
		return ScanResult{}, errKeyReused()
	}
	if r, ok, err := row.result(); err != nil || ok {
		return r, err
	}
	remaining := row.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return ScanResult{Outcome: OutcomeTimeout}, nil
	}
	k := Key{TenantID: tenantID, SessionID: sess.ID}
	results, cancel := s.hub.Await(k, row.ID)
	defer cancel()
	if row.ID != id {
		if r, ok, err := s.storedResult(ctx, tenantID, sess.ID, row.ID); err != nil || ok {
			return r, err
		}
	}
	ev := Event{Name: EventScan, Data: scanEvent{ScanID: row.ID, Code: row.Code, TTLMs: remaining.Milliseconds()}}
	if !s.hub.Publish(k, RoleDesktop, ev) {
		return ScanResult{Outcome: OutcomeDesktopUnavailable}, nil
	}
	timer := time.NewTimer(min(scanWait, remaining))
	defer timer.Stop()
	select {
	case r, ok := <-results:
		if !ok {
			return ScanResult{Outcome: OutcomeDesktopUnavailable}, nil
		}
		return r, nil
	case <-timer.C:
		if r, ok, err := s.storedResult(ctx, tenantID, sess.ID, row.ID); err != nil || ok {
			return r, err
		}
	case <-ctx.Done():
	}
	return ScanResult{Outcome: OutcomeTimeout}, nil
}

// storedResult reads the stored desktop answer of a scan; ok is false while there is none.
func (s *Service) storedResult(ctx context.Context, tenantID, sessionID, scanID uuid.UUID) (ScanResult, bool, error) {
	var row scan
	err := s.db.WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
		var err error
		row, err = getScan(ctx, q, sessionID, scanID)
		return err
	})
	if err != nil {
		return ScanResult{}, false, err
	}
	return row.result()
}

// RevokeByPhone ends the phone's session from the phone and tells both sides; 401
// scan_session_ended when no session matches.
func (s *Service) RevokeByPhone(ctx context.Context, tenantID uuid.UUID, secrets []string) error {
	now := s.clock.Now()
	var id uuid.UUID
	var changed bool
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		sess, err := s.phone(ctx, q, secrets, now)
		if err != nil {
			return err
		}
		id = sess.ID
		changed, err = revokeSession(ctx, q, id, RevokedByPhone, now)
		return err
	})
	if err != nil {
		return err
	}
	if changed {
		s.ended(Key{TenantID: tenantID, SessionID: id}, RevokedByPhone)
	}
	return nil
}

// ResultInput is what the desktop did with a scan. SKUID and UnitID name the line it added and
// are required (and only read) for OutcomeAdded.
type ResultInput struct {
	Outcome Outcome
	SKUID   *uuid.UUID
	UnitID  *uuid.UUID
}

// Resolve stores the desktop's answer to a scan of its session and hands it to the waiting
// phone. The first answer wins: a later one gets the stored result back. An answer after the
// scan expired, or after its session ended (no phone waits any more), is 409 scan_expired. For
// added, the product and unit names come from the catalog, never from the request.
func (s *Service) Resolve(ctx context.Context, o Owner, sessionID, scanID uuid.UUID, in ResultInput) (ScanResult, error) {
	if err := in.check(); err != nil {
		return ScanResult{}, err
	}
	now := s.clock.Now()
	// The scan must belong to a session of o before the ids of the answer are looked up, so a
	// scan of anyone else is 404 whatever the body names.
	err := s.db.WithReadOnlyTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		if _, err := s.owned(ctx, q, o, sessionID, now); err != nil {
			return err
		}
		_, err := getScan(ctx, q, sessionID, scanID)
		return err
	})
	if err != nil {
		return ScanResult{}, err
	}
	r, err := s.resultOf(ctx, o.TenantID, in)
	if err != nil {
		return ScanResult{}, err
	}
	err = s.db.WithTenantTx(ctx, o.TenantID, func(q db.Querier) error {
		sess, err := s.owned(ctx, q, o, sessionID, now)
		if err != nil {
			return err
		}
		if sess.State == StateClaimed {
			ok, err := resolveScan(ctx, q, sessionID, scanID, r, now)
			if err != nil || ok {
				return err
			}
		}
		row, err := getScan(ctx, q, sessionID, scanID)
		if err != nil {
			return err
		}
		stored, has, err := row.result()
		if err != nil {
			return err
		}
		if !has {
			return errScanExpired()
		}
		r = stored
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}
	s.hub.Resolve(scanID, r)
	return r, nil
}

// check validates the shape of the desktop's answer: an outcome the desktop may report, and for
// added both ids.
func (in ResultInput) check() error {
	if !in.Outcome.desktopReportable() {
		return invalid(map[string]string{"outcome": "oneof"})
	}
	if in.Outcome != OutcomeAdded {
		return nil
	}
	missing := map[string]string{}
	if in.SKUID == nil {
		missing["sku_id"] = "required"
	}
	if in.UnitID == nil {
		missing["unit_id"] = "required"
	}
	if len(missing) > 0 {
		return invalid(missing)
	}
	return nil
}

// resultOf builds the phone's result from the desktop's answer, which passed check.
func (s *Service) resultOf(ctx context.Context, tenantID uuid.UUID, in ResultInput) (ScanResult, error) {
	r := ScanResult{Outcome: in.Outcome}
	if in.Outcome != OutcomeAdded {
		return r, nil
	}
	product, unit, err := s.catalog.ItemNames(ctx, tenantID, *in.SKUID, *in.UnitID)
	switch {
	case errors.Is(err, ErrUnknownSKU):
		return ScanResult{}, invalid(map[string]string{"sku_id": "not_found"})
	case errors.Is(err, ErrUnknownUnit):
		return ScanResult{}, invalid(map[string]string{"unit_id": "not_found"})
	case err != nil:
		return ScanResult{}, err
	}
	r.ProductName, r.UnitName = product, unit
	return r, nil
}

// Purge deletes, in one transaction, the sessions of a tenant that were revoked or whose desktop
// went silent more than 24 hours ago, with their scans. It returns how many sessions it deleted.
func (s *Service) Purge(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	now := s.clock.Now()
	var n int64
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		var err error
		n, err = purgeSessions(ctx, q, now)
		return err
	})
	return n, err
}

// owned reads a session of o; a session of anyone else, or of another login session, is 404.
func (s *Service) owned(ctx context.Context, q db.Querier, o Owner, id uuid.UUID, now time.Time) (Session, error) {
	sess, err := getSession(ctx, q, id, now)
	if err != nil {
		return Session{}, err
	}
	if !o.owns(sess) {
		return Session{}, errNotFound()
	}
	return sess, nil
}

// maxPhoneSecrets bounds how many same-name cookies one request may make the server look up.
const maxPhoneSecrets = 4

// phone finds the session of the first of the phone's secrets that matches one; a stray cookie
// set for a parent domain cannot hide the real one.
func (s *Service) phone(ctx context.Context, q db.Querier, secrets []string, now time.Time) (Session, error) {
	tried := 0
	for _, secret := range secrets {
		if secret == "" || len(secret) > maxClaimCodeLen {
			continue
		}
		if tried++; tried > maxPhoneSecrets {
			break
		}
		sess, found, err := sessionBySecret(ctx, q, hashOf(secret), now)
		if err != nil {
			return Session{}, err
		}
		if found {
			return sess, nil
		}
	}
	return Session{}, errSessionEnded()
}

// ended tells both sides that a session was revoked and closes its streams and waiters. The
// desktop learns who revoked it; the phone learns revoked when a person did and ended when the
// system did.
func (s *Service) ended(key Key, by RevokedBy) {
	s.hub.Publish(key, RoleDesktop, Event{Name: EventRevoked, Data: revokedEvent{By: by}})
	if by == RevokedBySystem {
		s.hub.Publish(key, RolePhone, Event{Name: EventEnded, Data: struct{}{}})
	} else {
		s.hub.Publish(key, RolePhone, Event{Name: EventRevoked, Data: revokedEvent{By: by}})
	}
	s.hub.Close(key)
}

// newSecret returns a random claim code or phone secret (rand.Text, 130 bits) and its hash, the
// only form the database keeps.
func newSecret() (secret string, hash []byte) {
	secret = rand.Text()
	return secret, hashOf(secret)
}

func hashOf(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func invalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}
