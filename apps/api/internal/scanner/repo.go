package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// endedSQL is the one definition of an ended session, for the scanner_sessions row aliased s at
// the time bound to the placeholder now: revoked, the desktop stream silent for desktopGrace, a
// phone left unapproved for approvalTTL, or approved more than sessionTTL ago. Every query that
// cares about the state goes through it or stateSQL. Every term guards its nullable column, so
// the predicate is never NULL and NOT of it means "still open".
func endedSQL(now string) string {
	return fmt.Sprintf(`(s.revoked_at IS NOT NULL`+
		` OR s.desktop_seen_at <= %[1]s::timestamptz - interval '%[2]d seconds'`+
		` OR (s.claimed_at IS NOT NULL AND s.approved_at IS NULL AND s.claimed_at <= %[1]s::timestamptz - interval '%[3]d seconds')`+
		` OR (s.approved_at IS NOT NULL AND s.approved_at <= %[1]s::timestamptz - interval '%[4]d seconds'))`,
		now, int(desktopGrace/time.Second), int(approvalTTL/time.Second), int(sessionTTL/time.Second))
}

// stateSQL derives the State of the row aliased s at now (see endedSQL).
func stateSQL(now string) string {
	return `CASE WHEN ` + endedSQL(now) + ` THEN 'ended'` +
		` WHEN s.claimed_at IS NULL THEN 'waiting'` +
		` WHEN s.approved_at IS NULL THEN 'pending'` +
		` ELSE 'claimed' END`
}

// sessionColumns selects a Session; the state is derived at the time bound to now.
func sessionColumns(now string) string {
	return `s.id, s.tenant_id, s.created_by, s.login_session_id, s.created_at, s.claim_expires_at,
		s.phone_secret_hash, s.claimed_at, s.device_label, s.approved_at, s.desktop_seen_at, s.revoked_at,
		s.revoked_by, ` + stateSQL(now) + ` AS state`
}

type newSession struct {
	id, tenantID, createdBy, loginSessionID uuid.UUID
	claimCodeHash                           []byte
	now                                     time.Time
}

func insertSession(ctx context.Context, q db.Querier, n newSession) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO scanner_sessions (id, tenant_id, created_by, login_session_id, created_at, claim_code_hash,
			claim_expires_at, desktop_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $5)`,
		n.id, n.tenantID, n.createdBy, n.loginSessionID, n.now, n.claimCodeHash, n.now.Add(claimTTL))
	if err != nil {
		return fmt.Errorf("scanner: insert session: %w", err)
	}
	return nil
}

// openSession is one session of a user that has not ended; Waiting when no phone claimed it yet.
type openSession struct {
	ID      uuid.UUID `db:"id"`
	Waiting bool      `db:"waiting"`
}

// lockOpenSessions locks the sessions of a user that have not ended, oldest first. A transaction
// lock on the user comes first: row locks only cover sessions that exist, so two concurrent opens
// would otherwise both count the same sessions and both insert.
func lockOpenSessions(ctx context.Context, q db.Querier, tenantID, userID uuid.UUID, now time.Time) ([]openSession, error) {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('scanner_sessions:' || $1::text || ':' || $2::text, 0))`,
		tenantID, userID); err != nil {
		return nil, fmt.Errorf("scanner: lock open sessions of user: %w", err)
	}
	var out []openSession
	err := sqlx.SelectContext(ctx, q, &out, `
		SELECT s.id, s.claimed_at IS NULL AS waiting FROM scanner_sessions AS s
		WHERE s.created_by = $1 AND NOT `+endedSQL("$2")+`
		ORDER BY s.created_at, s.id
		FOR UPDATE`, userID, now)
	if err != nil {
		return nil, fmt.Errorf("scanner: lock open sessions: %w", err)
	}
	return out, nil
}

func getSession(ctx context.Context, q db.Querier, id uuid.UUID, now time.Time) (Session, error) {
	var s Session
	err := sqlx.GetContext(ctx, q, &s, `SELECT `+sessionColumns("$2")+` FROM scanner_sessions AS s WHERE s.id = $1`, id, now)
	if db.IsNoRows(err) {
		return Session{}, errNotFound()
	}
	if err != nil {
		return Session{}, fmt.Errorf("scanner: read session: %w", err)
	}
	return s, nil
}

// sessionBySecret finds the session a phone secret hash belongs to; found is false for none.
func sessionBySecret(ctx context.Context, q db.Querier, secretHash []byte, now time.Time) (Session, bool, error) {
	var s Session
	err := sqlx.GetContext(ctx, q, &s,
		`SELECT `+sessionColumns("$2")+` FROM scanner_sessions AS s WHERE s.phone_secret_hash = $1`, secretHash, now)
	if db.IsNoRows(err) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("scanner: read session by secret: %w", err)
	}
	return s, true, nil
}

// revokeSession ends a session that is not revoked yet and forgets its code and phone secret;
// it reports whether it did.
func revokeSession(ctx context.Context, q db.Querier, id uuid.UUID, by RevokedBy, now time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE scanner_sessions AS s
		SET revoked_at = $2, revoked_by = $3, claim_code_hash = NULL, claim_expires_at = NULL, phone_secret_hash = NULL
		WHERE s.id = $1 AND s.revoked_at IS NULL`, id, now, string(by))
	if err != nil {
		return false, fmt.Errorf("scanner: revoke session: %w", err)
	}
	return affected(res)
}

// replaceClaimCode gives a waiting session a new claim code.
func replaceClaimCode(ctx context.Context, q db.Querier, id uuid.UUID, codeHash []byte, now time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE scanner_sessions AS s SET claim_code_hash = $2, claim_expires_at = $3
		WHERE s.id = $1 AND `+stateSQL("$4")+` = 'waiting'`, id, codeHash, now.Add(claimTTL), now)
	if err != nil {
		return false, fmt.Errorf("scanner: replace claim code: %w", err)
	}
	return affected(res)
}

// claimableSession reports whether a claim code hash belongs to a waiting session whose code is
// still live; claimSession applies the same conditions.
func claimableSession(ctx context.Context, q db.Querier, codeHash []byte, now time.Time) (bool, error) {
	var found bool
	err := q.QueryRowxContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM scanner_sessions AS s
			WHERE s.claim_code_hash = $1 AND s.claimed_at IS NULL AND s.claim_expires_at > $2
				AND NOT `+endedSQL("$2")+`)`, codeHash, now).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("scanner: check claim code: %w", err)
	}
	return found, nil
}

// claimSession hands the waiting session of a live claim code to a phone; found is false when
// no session matches.
func claimSession(ctx context.Context, q db.Querier, codeHash, secretHash []byte, label *string, now time.Time) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := sqlx.GetContext(ctx, q, &id, `
		UPDATE scanner_sessions AS s
		SET claim_code_hash = NULL, claim_expires_at = NULL, phone_secret_hash = $2, claimed_at = $4, device_label = $3
		WHERE s.claim_code_hash = $1 AND s.claimed_at IS NULL AND s.claim_expires_at > $4
			AND NOT `+endedSQL("$4")+`
		RETURNING s.id`, codeHash, secretHash, label, now)
	if db.IsNoRows(err) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("scanner: claim session: %w", err)
	}
	return id, true, nil
}

func approveSession(ctx context.Context, q db.Querier, id uuid.UUID, now time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE scanner_sessions AS s SET approved_at = $2
		WHERE s.id = $1 AND `+stateSQL("$2")+` = 'pending'`, id, now)
	if err != nil {
		return false, fmt.Errorf("scanner: approve session: %w", err)
	}
	return affected(res)
}

// rejectSession sends the pending phone away and puts the session back to waiting on a new code.
func rejectSession(ctx context.Context, q db.Querier, id uuid.UUID, codeHash []byte, now time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE scanner_sessions AS s
		SET phone_secret_hash = NULL, claimed_at = NULL, device_label = NULL, claim_code_hash = $2, claim_expires_at = $3
		WHERE s.id = $1 AND `+stateSQL("$4")+` = 'pending'`, id, codeHash, now.Add(claimTTL), now)
	if err != nil {
		return false, fmt.Errorf("scanner: reject phone: %w", err)
	}
	return affected(res)
}

// touchDesktop records that the desktop stream is alive, at most once per seenRefresh.
func touchDesktop(ctx context.Context, q db.Querier, id uuid.UUID, now time.Time) error {
	_, err := q.ExecContext(ctx, `
		UPDATE scanner_sessions AS s SET desktop_seen_at = $2
		WHERE s.id = $1 AND NOT `+endedSQL("$2")+` AND s.desktop_seen_at < $3`, id, now, now.Add(-seenRefresh))
	if err != nil {
		return fmt.Errorf("scanner: touch desktop: %w", err)
	}
	return nil
}

// scan is a scanner_scans row.
type scan struct {
	ID             uuid.UUID `db:"id"`
	SessionID      uuid.UUID `db:"session_id"`
	IdempotencyKey string    `db:"idempotency_key"`
	RequestHash    []byte    `db:"request_hash"`
	Code           string    `db:"code"`
	ExpiresAt      time.Time `db:"expires_at"`
	Result         []byte    `db:"result"` // NULL until the desktop answers
}

const scanColumns = `id, session_id, idempotency_key, request_hash, code, expires_at, result`

// result decodes the stored desktop answer; ok is false while there is none.
func (s scan) result() (ScanResult, bool, error) {
	if s.Result == nil {
		return ScanResult{}, false, nil
	}
	var r ScanResult
	if err := json.Unmarshal(s.Result, &r); err != nil {
		return ScanResult{}, false, fmt.Errorf("scanner: decode scan result: %w", err)
	}
	return r, true, nil
}

type newScan struct {
	id, tenantID, sessionID uuid.UUID
	key                     string
	requestHash             []byte
	code                    string
	now                     time.Time
}

// putScan records a scan under its Idempotency-Key unless the key was seen, then returns the row
// the key names (the new one or the earlier one).
func putScan(ctx context.Context, q db.Querier, n newScan) (scan, error) {
	_, err := q.ExecContext(ctx, `
		INSERT INTO scanner_scans (id, tenant_id, session_id, idempotency_key, request_hash, code, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, session_id, idempotency_key) DO NOTHING`,
		n.id, n.tenantID, n.sessionID, n.key, n.requestHash, n.code, n.now, n.now.Add(scanTTL))
	if err != nil {
		return scan{}, fmt.Errorf("scanner: insert scan: %w", err)
	}
	var s scan
	err = sqlx.GetContext(ctx, q, &s, `SELECT `+scanColumns+` FROM scanner_scans WHERE session_id = $1 AND idempotency_key = $2`,
		n.sessionID, n.key)
	if err != nil {
		return scan{}, fmt.Errorf("scanner: read scan: %w", err)
	}
	return s, nil
}

func getScan(ctx context.Context, q db.Querier, sessionID, id uuid.UUID) (scan, error) {
	var s scan
	err := sqlx.GetContext(ctx, q, &s, `SELECT `+scanColumns+` FROM scanner_scans WHERE id = $1 AND session_id = $2`, id, sessionID)
	if db.IsNoRows(err) {
		return scan{}, errNotFound()
	}
	if err != nil {
		return scan{}, fmt.Errorf("scanner: read scan: %w", err)
	}
	return s, nil
}

// resolveScan stores the first desktop answer of a scan that has not expired; it reports whether
// it did.
func resolveScan(ctx context.Context, q db.Querier, sessionID, id uuid.UUID, r ScanResult, now time.Time) (bool, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return false, fmt.Errorf("scanner: encode scan result: %w", err)
	}
	res, err := q.ExecContext(ctx, `
		UPDATE scanner_scans SET result = $3, resolved_at = $4
		WHERE id = $1 AND session_id = $2 AND result IS NULL AND expires_at > $4`, id, sessionID, string(body), now)
	if err != nil {
		return false, fmt.Errorf("scanner: resolve scan: %w", err)
	}
	return affected(res)
}

// purgeSessions deletes the sessions of the transaction's tenant that were revoked, or whose
// desktop went silent, more than purgeAfter before now; their scans go with them (ON DELETE
// CASCADE).
func purgeSessions(ctx context.Context, q db.Querier, now time.Time) (int64, error) {
	cutoff := now.Add(-purgeAfter)
	res, err := q.ExecContext(ctx, `DELETE FROM scanner_sessions WHERE revoked_at < $1 OR desktop_seen_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("scanner: purge sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("scanner: purge sessions: %w", err)
	}
	return n, nil
}

type rowsResult interface{ RowsAffected() (int64, error) }

func affected(res rowsResult) (bool, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("scanner: rows affected: %w", err)
	}
	return n > 0, nil
}
