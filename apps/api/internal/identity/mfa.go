package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// TOTPIssuer names the account in authenticator apps.
const TOTPIssuer = "Tally"

const challengePurpose = "mfa-challenge"

// newChallenge signs the MFA step of a login: the tenant and user whose password was correct.
func (s *Service) newChallenge(u User) string {
	payload := make([]byte, 0, 32)
	payload = append(payload, u.TenantID[:]...)
	payload = append(payload, u.ID[:]...)
	return s.keys.Challenge.Sign(challengePurpose, payload, s.clock.Now().Add(ChallengeTTL))
}

// readChallenge returns the tenant and user of a valid, unexpired challenge.
func (s *Service) readChallenge(token string) (tenantID, userID uuid.UUID, ok bool) {
	payload, err := s.keys.Challenge.Verify(challengePurpose, token, s.clock.Now())
	if err != nil || len(payload) != 32 {
		return uuid.Nil, uuid.Nil, false
	}
	copy(tenantID[:], payload[:16])
	copy(userID[:], payload[16:])
	return tenantID, userID, true
}

// Enrolment is a TOTP secret waiting for its first code.
type Enrolment struct {
	// Secret is the base32 secret for manual entry.
	Secret string
	// URI is the otpauth:// URI for a QR code.
	URI string
}

// EnrolTOTP creates (or replaces) the unconfirmed TOTP secret of the signed-in owner. MFA turns
// on only when ConfirmTOTP receives a valid code. 409 when MFA is already on.
func (s *Service) EnrolTOTP(ctx context.Context, p middleware.Principal) (Enrolment, error) {
	secret, err := authn.NewTOTPSecret()
	if err != nil {
		return Enrolment{}, err
	}
	sealed, err := s.keys.TOTP.Seal(secret, p.UserID[:])
	if err != nil {
		return Enrolment{}, err
	}
	now := s.clock.Now()
	var u User
	err = s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		var found bool
		var err error
		u, found, err = userByID(ctx, q, p.TenantID, p.UserID, true)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NewUnauthenticated()
		}
		if u.MFAEnabled {
			return apperr.NewConflict("mfa_already_enabled", "Two-step verification is already on")
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO mfa_totp_secrets (user_id, secret_ciphertext, confirmed_at, created_at) VALUES ($1, $2, NULL, $3)
			ON CONFLICT ON CONSTRAINT pk_mfa_totp_secrets
			DO UPDATE SET secret_ciphertext = EXCLUDED.secret_ciphertext, confirmed_at = NULL, last_used_step = NULL,
			  created_at = EXCLUDED.created_at`,
			u.ID, sealed, now); err != nil {
			return fmt.Errorf("identity: store TOTP secret: %w", err)
		}
		return nil
	})
	if err != nil {
		return Enrolment{}, err
	}
	return Enrolment{Secret: authn.EncodeTOTPSecret(secret), URI: authn.TOTPURI(TOTPIssuer, u.Email, secret)}, nil
}

// ConfirmTOTP turns MFA on with the first valid code of the pending secret and returns the ten
// recovery codes (shown once; only their keyed hashes are stored). The current session counts
// as MFA-verified; every other session of the owner is signed out, since it never passed the
// second step.
func (s *Service) ConfirmTOTP(ctx context.Context, p middleware.Principal, code string, c Client) ([]string, error) {
	codes, err := authn.NewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	err = s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		u, found, err := userByID(ctx, q, p.TenantID, p.UserID, true)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NewUnauthenticated()
		}
		if u.MFAEnabled {
			return apperr.NewConflict("mfa_already_enabled", "Two-step verification is already on")
		}
		secret, found, err := s.totpSecret(ctx, q, u.ID, false)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NewConflict("mfa_not_enrolled", "Start the two-step verification set-up first")
		}
		step, ok := authn.MatchTOTP(secret, code, now)
		if !ok {
			return errInvalidCode()
		}
		// The confirming code counts as used, so it cannot also complete a sign-in.
		if _, err := q.ExecContext(ctx, `UPDATE mfa_totp_secrets SET confirmed_at = $2, last_used_step = $3 WHERE user_id = $1`,
			u.ID, now, step); err != nil {
			return fmt.Errorf("identity: confirm TOTP: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE users SET mfa_enabled = true, updated_at = $3, version = version + 1 WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, u.ID, now); err != nil {
			return fmt.Errorf("identity: enable MFA: %w", err)
		}
		if err := s.replaceRecoveryCodes(ctx, q, u.ID, codes, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE sessions SET mfa_verified_at = $3 WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, p.SessionID, now); err != nil {
			return fmt.Errorf("identity: mark session verified: %w", err)
		}
		n, err := revokeSessions(ctx, q, u.TenantID, u.ID, p.SessionID, "mfa_enabled", now)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, q, ownerActor(u.TenantID, u.ID, c), audit.Event{
			Action: "user.enable_mfa", EntityType: "user", EntityID: u.ID,
			After: map[string]any{"sessions_revoked": n},
		})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP turns MFA off and deletes the secret and recovery codes.
func (s *Service) DisableTOTP(ctx context.Context, p middleware.Principal, c Client) error {
	now := s.clock.Now()
	return s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		u, found, err := userByID(ctx, q, p.TenantID, p.UserID, true)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NewUnauthenticated()
		}
		if !u.MFAEnabled {
			return apperr.NewConflict("mfa_not_enabled", "Two-step verification is off")
		}
		for _, stmt := range []string{
			`DELETE FROM mfa_recovery_codes WHERE user_id = $1`,
			`DELETE FROM mfa_totp_secrets WHERE user_id = $1`,
		} {
			if _, err := q.ExecContext(ctx, stmt, u.ID); err != nil {
				return fmt.Errorf("identity: disable MFA: %w", err)
			}
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE users SET mfa_enabled = false, updated_at = $3, version = version + 1 WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, u.ID, now); err != nil {
			return fmt.Errorf("identity: disable MFA: %w", err)
		}
		return s.audit.Record(ctx, q, ownerActor(u.TenantID, u.ID, c), audit.Event{
			Action: "user.disable_mfa", EntityType: "user", EntityID: u.ID,
		})
	})
}

// ChallengeInput answers the MFA step of a login with a TOTP code or a recovery code.
type ChallengeInput struct {
	HostTenant   uuid.UUID
	Challenge    string
	Code         string
	RecoveryCode string
	Client       Client
}

// VerifyChallenge completes a login that stopped at the MFA step. Wrong codes count towards the
// login throttle of the account's e-mail; a TOTP code is accepted once (a replayed code, or an
// older one, is a wrong code) and a recovery code works once.
func (s *Service) VerifyChallenge(ctx context.Context, in ChallengeInput) (IssuedSession, error) {
	if (in.Code == "") == (in.RecoveryCode == "") {
		return IssuedSession{}, errInvalid(map[string]string{"code": "code_or_recovery_code"})
	}
	tenantID, userID, ok := s.readChallenge(in.Challenge)
	if !ok || in.HostTenant == uuid.Nil || tenantID != in.HostTenant {
		return IssuedSession{}, errChallengeInvalid()
	}
	var (
		u     User
		found bool
	)
	if err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		u, found, err = userByID(ctx, q, tenantID, userID, false)
		return err
	}); err != nil {
		return IssuedSession{}, err
	}
	if !found || u.Status != StatusActive || !u.MFAEnabled {
		return IssuedSession{}, errChallengeInvalid()
	}
	if err := s.checkThrottle(ctx, u.EmailKey, in.Client.IP); err != nil {
		return IssuedSession{}, err
	}
	// The code is spent in the transaction that opens the session, after the tenant lock: a
	// sign-in refused because a sandbox reset replaced the tenant leaves it unused.
	issued, err := s.startSession(ctx, u, in.Client, true, func(q db.Querier, now time.Time) error {
		valid, err := s.useSecondFactor(ctx, q, u.ID, in, now)
		if err != nil {
			return err
		}
		if !valid {
			return errSecondFactorRejected
		}
		return nil
	})
	if errors.Is(err, errSecondFactorRejected) {
		return IssuedSession{}, s.loginFailed(ctx, u.EmailKey, in.Client, true, u, "mfa")
	}
	return issued, err
}

// errSecondFactorRejected rolls back the session of a challenge answered with a wrong code.
var errSecondFactorRejected = errors.New("identity: second factor rejected")

// useSecondFactor spends the TOTP step or the recovery code of a challenge answer and reports
// whether it was valid.
func (s *Service) useSecondFactor(ctx context.Context, q db.Querier, userID uuid.UUID, in ChallengeInput, now time.Time) (bool, error) {
	if in.Code != "" {
		secret, found, err := s.totpSecret(ctx, q, userID, true)
		if err != nil || !found {
			return false, err
		}
		step, ok := authn.MatchTOTP(secret, in.Code, now)
		if !ok {
			return false, nil
		}
		return useTOTPStep(ctx, q, userID, step)
	}
	res, err := q.ExecContext(ctx, `
		UPDATE mfa_recovery_codes SET used_at = $3
		WHERE id = (SELECT id FROM mfa_recovery_codes WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL LIMIT 1)`,
		userID, s.keys.Recovery.SumHex(authn.NormalizeRecoveryCode(in.RecoveryCode)), now)
	if err != nil {
		return false, fmt.Errorf("identity: use recovery code: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("identity: use recovery code: %w", err)
	}
	return n == 1, nil
}

// useTOTPStep records step as the last accepted TOTP step of the user's active secret. It
// reports false when a code of this step or a later one was already accepted; the conditional
// update makes two concurrent sign-ins with the same code race for one row, and one loses.
func useTOTPStep(ctx context.Context, q db.Querier, userID uuid.UUID, step int64) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE mfa_totp_secrets SET last_used_step = $2
		WHERE user_id = $1 AND confirmed_at IS NOT NULL AND (last_used_step IS NULL OR last_used_step < $2)`,
		userID, step)
	if err != nil {
		return false, fmt.Errorf("identity: use TOTP code: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("identity: use TOTP code: %w", err)
	}
	return n == 1, nil
}

// totpSecret decrypts the TOTP secret of a user; confirmed selects the active secret rather
// than a pending enrolment.
func (s *Service) totpSecret(ctx context.Context, q db.Querier, userID uuid.UUID, confirmed bool) ([]byte, bool, error) {
	cond := `confirmed_at IS NULL`
	if confirmed {
		cond = `confirmed_at IS NOT NULL`
	}
	var sealed []byte
	err := q.QueryRowxContext(ctx, `SELECT secret_ciphertext FROM mfa_totp_secrets WHERE user_id = $1 AND `+cond, userID).Scan(&sealed)
	if db.IsNoRows(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("identity: read TOTP secret: %w", err)
	}
	secret, err := s.keys.TOTP.Open(sealed, userID[:])
	if err != nil {
		return nil, false, fmt.Errorf("identity: decrypt TOTP secret: %w", err)
	}
	return secret, true, nil
}

func (s *Service) replaceRecoveryCodes(ctx context.Context, q db.Querier, userID uuid.UUID, codes []string, now time.Time) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("identity: replace recovery codes: %w", err)
	}
	for _, code := range codes {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO mfa_recovery_codes (id, user_id, code_hash, created_at) VALUES ($1, $2, $3, $4)`,
			ids.New(), userID, s.keys.Recovery.SumHex(authn.NormalizeRecoveryCode(code)), now); err != nil {
			return fmt.Errorf("identity: store recovery code: %w", err)
		}
	}
	return nil
}
