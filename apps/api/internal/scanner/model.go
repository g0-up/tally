// Package scanner pairs a signed-in desktop tab with a phone that scans barcodes for it. The
// desktop opens a session and shows its claim code as a QR code; a phone claims it, the desktop
// approves the phone after comparing the verify code, and from then on every code the phone scans
// reaches the desktop over a server-sent event stream. The phone waits for the desktop's answer in
// the same request and only ever learns the outcome, the product name and the unit name.
package scanner

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// Lifetimes of a session and its scans. The session state derives from them (see stateSQL).
const (
	// claimTTL is how long a claim code stays valid.
	claimTTL = 2 * time.Minute
	// approvalTTL is how long a claimed phone may wait for the desktop to approve it.
	approvalTTL = 2 * time.Minute
	// desktopGrace is how long the session survives without a live desktop event stream.
	desktopGrace = 60 * time.Second
	// seenRefresh is how stale desktop_seen_at may get before a stream tick refreshes it.
	seenRefresh = 30 * time.Second
	// sessionTTL bounds an approved session.
	sessionTTL = 12 * time.Hour
	// scanTTL is how long the desktop may answer one scan.
	scanTTL = 10 * time.Second
	// scanWait is how long one scan request waits for the desktop before answering timeout.
	scanWait = 4 * time.Second
	// maxOpenSessions is how many sessions one user may keep open; opening one more revokes the
	// oldest one no phone claimed, or the oldest when phones claimed them all.
	maxOpenSessions = 3
	// purgeAfter is how long an ended session is kept before the worker deletes it.
	purgeAfter = 24 * time.Hour
)

// State is the derived state of a session.
type State string

// Session states.
const (
	// StateWaiting waits for a phone to claim the code.
	StateWaiting State = "waiting"
	// StatePending has a phone that waits for the desktop to approve it.
	StatePending State = "pending"
	// StateClaimed has an approved phone that may send codes.
	StateClaimed State = "claimed"
	// StateEnded was revoked or timed out.
	StateEnded State = "ended"
)

// RevokedBy names who revoked a session.
type RevokedBy string

// Revokers.
const (
	RevokedByDesktop RevokedBy = "desktop"
	RevokedByPhone   RevokedBy = "phone"
	RevokedBySystem  RevokedBy = "system"
)

// Outcome is what the desktop did with a scanned code, as the phone sees it.
type Outcome string

// Outcomes. The desktop reports the first six; the server answers the last two itself.
const (
	OutcomeAdded            Outcome = "added"
	OutcomeNotFound         Outcome = "not_found"
	OutcomeNotAccepting     Outcome = "not_accepting"
	OutcomeNeedsDesktopEdit Outcome = "needs_desktop_edit"
	OutcomeNewProduct       Outcome = "new_product"
	OutcomeNotInOrder       Outcome = "not_in_order"
	// OutcomeDesktopUnavailable means no desktop stream took the scan.
	OutcomeDesktopUnavailable Outcome = "desktop_unavailable"
	// OutcomeTimeout means the desktop did not answer in time.
	OutcomeTimeout Outcome = "timeout"
)

// desktopReportable reports whether the desktop may report o.
func (o Outcome) desktopReportable() bool {
	switch o {
	case OutcomeAdded, OutcomeNotFound, OutcomeNotAccepting, OutcomeNeedsDesktopEdit, OutcomeNewProduct, OutcomeNotInOrder:
		return true
	}
	return false
}

// ScanResult is everything the phone learns about a scan. It is built by the server from the
// outcome and the catalog names only; nothing the desktop sends reaches the phone verbatim.
type ScanResult struct {
	Outcome     Outcome `json:"outcome"`
	ProductName string  `json:"product_name,omitempty"`
	UnitName    string  `json:"unit_name,omitempty"`
}

// Session is a scanner_sessions row with its derived state.
type Session struct {
	ID              uuid.UUID  `db:"id"`
	TenantID        uuid.UUID  `db:"tenant_id"`
	CreatedBy       uuid.UUID  `db:"created_by"`
	LoginSessionID  uuid.UUID  `db:"login_session_id"`
	CreatedAt       time.Time  `db:"created_at"`
	ClaimExpiresAt  *time.Time `db:"claim_expires_at"`
	PhoneSecretHash []byte     `db:"phone_secret_hash"`
	ClaimedAt       *time.Time `db:"claimed_at"`
	DeviceLabel     *string    `db:"device_label"`
	ApprovedAt      *time.Time `db:"approved_at"`
	DesktopSeenAt   time.Time  `db:"desktop_seen_at"`
	RevokedAt       *time.Time `db:"revoked_at"`
	RevokedBy       *string    `db:"revoked_by"`
	State           State      `db:"state"`
}

// hasPhone reports whether a phone holds the session right now (pending or claimed).
func (s Session) hasPhone() bool {
	return (s.State == StatePending || s.State == StateClaimed) && len(s.PhoneSecretHash) > 0
}

// Status is the desktop view of a session (GET /scanner-sessions/{id} and the stream's state
// event).
type Status struct {
	State       State      `json:"state"`
	DeviceLabel string     `json:"device_label,omitempty"`
	VerifyCode  string     `json:"verify_code,omitempty"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
}

// status is the desktop view of s; the phone details only show while a phone holds it.
func (s Session) status() Status {
	out := Status{State: s.State}
	if s.hasPhone() {
		out.VerifyCode = VerifyCode(s.PhoneSecretHash)
		out.ClaimedAt = s.ClaimedAt
		out.ApprovedAt = s.ApprovedAt
		if s.DeviceLabel != nil {
			out.DeviceLabel = *s.DeviceLabel
		}
	}
	return out
}

// PhoneStatus is the phone view of a session (GET /scan/session and the stream's state event).
type PhoneStatus struct {
	State      State  `json:"state"`
	VerifyCode string `json:"verify_code,omitempty"`
}

func (s Session) phoneStatus() PhoneStatus {
	if !s.hasPhone() {
		return PhoneStatus{State: StateEnded}
	}
	return PhoneStatus{State: s.State, VerifyCode: VerifyCode(s.PhoneSecretHash)}
}

// Created answers the desktop that opened a session. The claim code is shown only once.
type Created struct {
	ID uuid.UUID `json:"id"`
	ClaimCode
}

// ClaimCode is a fresh claim code and its expiry.
type ClaimCode struct {
	Code      string    `json:"claim_code"`
	ExpiresAt time.Time `json:"claim_expires_at"`
}

// Claimed answers the phone that claimed a session; Secret goes into the phone cookie.
type Claimed struct {
	SessionID  uuid.UUID
	Secret     string
	VerifyCode string
}

// Owner identifies the desktop side of a session: the user and the login session that opened it.
type Owner struct {
	TenantID       uuid.UUID
	UserID         uuid.UUID
	LoginSessionID uuid.UUID
}

func (o Owner) owns(s Session) bool {
	return s.TenantID == o.TenantID && s.CreatedBy == o.UserID && s.LoginSessionID == o.LoginSessionID
}

// VerifyCode is the four-digit code the desktop and the phone both show for the operator to
// match: the first two bytes of the phone secret hash, big-endian, modulo 10000.
func VerifyCode(secretHash []byte) string {
	if len(secretHash) < 2 {
		return ""
	}
	return fmt.Sprintf("%04d", int(binary.BigEndian.Uint16(secretHash[:2]))%10000)
}

// Error codes of the scanner routes.
const (
	CodeClaimInvalid         = "claim_invalid"
	CodeSessionEnded         = "scan_session_ended"
	CodeAlreadyClaimed       = "already_claimed"
	CodeNotPending           = "not_pending"
	CodePendingApproval      = "scan_pending_approval"
	CodeIdempotencyKeyReused = "idempotency_key_reused"
	CodeScanExpired          = "scan_expired"
	CodeStreamLimit          = "stream_limit"
	CodeServerRestarting     = "server_restarting"
)

func errNotFound() error { return apperr.NewNotFound("not_found", "Not found") }

func errClaimInvalid() error {
	return apperr.NewGone(CodeClaimInvalid, "This code is not valid any more; scan the QR code on the computer again")
}

func errSessionEnded() error {
	return apperr.New(apperr.Unauthenticated, CodeSessionEnded, "This scanner session has ended")
}

func errAlreadyClaimed() error {
	return apperr.NewConflict(CodeAlreadyClaimed, "A phone already claimed this session")
}

func errNotPending() error {
	return apperr.NewConflict(CodeNotPending, "No phone is waiting for approval")
}

func errPendingApproval() error {
	return apperr.NewConflict(CodePendingApproval, "The computer has not approved this phone yet")
}

func errKeyReused() error {
	return apperr.NewConflict(CodeIdempotencyKeyReused, "This Idempotency-Key was used for a different code")
}

func errScanExpired() error {
	return apperr.NewConflict(CodeScanExpired, "The phone stopped waiting for this scan")
}
