package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// activeOwner returns the principal's user when it is still an active account of its tenant.
func activeOwner(ctx context.Context, q db.Querier, p middleware.Principal) (User, error) {
	u, found, err := userByID(ctx, q, p.TenantID, p.UserID, true)
	if err != nil {
		return User{}, err
	}
	if !found || u.Status != StatusActive {
		return User{}, apperr.NewUnauthenticated()
	}
	return u, nil
}

// GrantSupportConsent records the owner's consent to a support session as a support_consent
// token expiring at expires, and runs within in the same transaction with the token id (the
// caller inserts the session). The caller passed the re-authentication guard.
func (s *Service) GrantSupportConsent(ctx context.Context, p middleware.Principal, expires time.Time,
	within func(q db.Querier, tokenID uuid.UUID) error,
) (uuid.UUID, error) {
	token, err := authn.NewToken()
	if err != nil {
		return uuid.Nil, err
	}
	now := s.clock.Now()
	var tokenID uuid.UUID
	err = s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		u, err := activeOwner(ctx, q, p)
		if err != nil {
			return err
		}
		if tokenID, err = insertTokenID(ctx, q, u.TenantID, u.ID, PurposeSupportConsent, s.keys.Tokens.Sum(token), nil,
			expires, now); err != nil {
			return err
		}
		return within(q, tokenID)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return tokenID, nil
}

// RevokeSupportConsent uses up the consent token tokenID and runs within in the same
// transaction. The token is found by its id alone: a sandbox reset moves the owner's tokens to
// the new tenant while the support session stays with the replaced one.
func (s *Service) RevokeSupportConsent(ctx context.Context, p middleware.Principal, tokenID uuid.UUID,
	within func(q db.Querier) error,
) error {
	now := s.clock.Now()
	return s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		if _, err := activeOwner(ctx, q, p); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE auth_tokens SET used_at = $3
			WHERE id = $1 AND purpose = $2 AND used_at IS NULL`, tokenID, PurposeSupportConsent, now); err != nil {
			return fmt.Errorf("identity: revoke support consent: %w", err)
		}
		return within(q)
	})
}

// closurePayload is stored with a closure verification token.
type closurePayload struct {
	RequestID uuid.UUID `json:"request_id"`
}

// IssueClosureToken mails the owner a one-time link confirming an account closure request.
// within runs first in the same transaction and returns the request id (the caller records or
// re-opens the request); earlier closure links of the owner stop working. The caller passed the
// re-authentication guard.
func (s *Service) IssueClosureToken(ctx context.Context, p middleware.Principal,
	within func(q db.Querier) (uuid.UUID, error),
) error {
	token, err := authn.NewToken()
	if err != nil {
		return err
	}
	now := s.clock.Now()
	var (
		owner User
		store TenantInfo
	)
	err = s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		var err error
		if owner, err = activeOwner(ctx, q, p); err != nil {
			return err
		}
		if store, err = readTenantInfo(ctx, q, p.TenantID); err != nil {
			return err
		}
		requestID, err := within(q)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(closurePayload{RequestID: requestID})
		if err != nil {
			return fmt.Errorf("identity: encode closure token: %w", err)
		}
		if err := retireTokens(ctx, q, owner.TenantID, owner.ID, PurposeClosureVerify, now); err != nil {
			return err
		}
		return insertToken(ctx, q, owner.TenantID, owner.ID, PurposeClosureVerify, s.keys.Tokens.Sum(token), payload,
			now.Add(ClosureTokenTTL), now)
	})
	if err != nil {
		return err
	}
	s.send(mailClosureVerify, owner.Email, mailData{
		Name: owner.FullName, StoreName: store.DisplayName, StoreURL: s.urls.Tenant(store.Slug, "/"),
		Link: s.urls.Tenant(store.Slug, "/settings/account-closure?token="+token),
	})
	return nil
}

// ConsumeClosureToken spends the owner's closure link token and runs within with the request it
// confirms, in one transaction; a token that is unknown, used, expired or another user's is 422
// token_invalid. The caller passed the re-authentication guard.
func (s *Service) ConsumeClosureToken(ctx context.Context, p middleware.Principal, token string,
	within func(q db.Querier, requestID uuid.UUID) error,
) error {
	if token == "" {
		return errTokenInvalid()
	}
	now := s.clock.Now()
	return s.withAccountTx(ctx, p.TenantID, func(q db.Querier) error {
		if _, err := activeOwner(ctx, q, p); err != nil {
			return err
		}
		tok, found, err := consumeToken(ctx, q, p.TenantID, PurposeClosureVerify, s.keys.Tokens.Sum(token), now)
		if err != nil {
			return err
		}
		var payload closurePayload
		if !found || tok.UserID != p.UserID || json.Unmarshal(tok.Payload, &payload) != nil || payload.RequestID == uuid.Nil {
			return errTokenInvalid()
		}
		return within(q, payload.RequestID)
	})
}

// closureExportPayload is stored with a closure export link token.
type closureExportPayload struct {
	ExportJobID uuid.UUID `json:"export_job_id"`
}

// closureExportPath is the API route a closure export link opens on the store's host. It needs no
// session: the owner of a closed store can no longer sign in.
const closureExportPath = "/api/v1/account-closure/export?token="

// IssueExportLink runs within in a transaction of the tenant. When within returns an
// export (its id and the end of its link), the store's active owner gets a reusable link to it
// that works until then, mailed after commit; the store's earlier closure export links stop
// working. It reports whether a link was issued: none when within returned no export or the
// store has no active owner.
func (s *Service) IssueExportLink(ctx context.Context, tenantID uuid.UUID,
	within func(q db.Querier) (uuid.UUID, time.Time, error),
) (bool, error) {
	token, err := authn.NewToken()
	if err != nil {
		return false, err
	}
	now := s.clock.Now()
	var (
		owner   User
		store   TenantInfo
		expires time.Time
		issued  bool
	)
	err = s.withAccountTx(ctx, tenantID, func(q db.Querier) error {
		var exportID uuid.UUID
		var err error
		if exportID, expires, err = within(q); err != nil || exportID == uuid.Nil {
			return err
		}
		err = sqlx.GetContext(ctx, q, &owner, `SELECT `+userColumns+` FROM users WHERE tenant_id = $1 AND status = $2
			ORDER BY created_at DESC, id DESC LIMIT 1`, tenantID, StatusActive)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: read store owner: %w", err)
		}
		if store, err = readTenantInfo(ctx, q, tenantID); err != nil {
			return err
		}
		payload, err := json.Marshal(closureExportPayload{ExportJobID: exportID})
		if err != nil {
			return fmt.Errorf("identity: encode closure export token: %w", err)
		}
		if _, err := q.ExecContext(ctx, `UPDATE auth_tokens SET used_at = $3
			WHERE tenant_id = $1 AND purpose = $2 AND used_at IS NULL`, tenantID, PurposeClosureExport, now); err != nil {
			return fmt.Errorf("identity: retire closure export tokens: %w", err)
		}
		issued = true
		return insertToken(ctx, q, tenantID, owner.ID, PurposeClosureExport, s.keys.Tokens.Sum(token), payload, expires, now)
	})
	if err != nil || !issued {
		return false, err
	}
	s.send(mailClosureExport, owner.Email, mailData{
		Name: owner.FullName, StoreName: store.DisplayName, StoreURL: s.urls.Tenant(store.Slug, "/"),
		Link: s.urls.Tenant(store.Slug, closureExportPath+token), Time: s.localTime(expires),
	})
	return true, nil
}

// ResolveExportLink returns the export a closure export link of the tenant opens. The
// link stays usable until it expires or a newer one replaces it; any other token is 404
// closure_link_not_found.
func (s *Service) ResolveExportLink(ctx context.Context, tenantID uuid.UUID, token string) (uuid.UUID, error) {
	if token == "" || tenantID == uuid.Nil {
		return uuid.Nil, errClosureLinkNotFound()
	}
	var raw []byte
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		err := q.QueryRowxContext(ctx, `
			SELECT payload FROM auth_tokens
			WHERE token_hash = $1 AND purpose = $2 AND tenant_id = $3 AND used_at IS NULL AND expires_at > $4`,
			s.keys.Tokens.Sum(token), PurposeClosureExport, tenantID, s.clock.Now()).Scan(&raw)
		if db.IsNoRows(err) {
			return errClosureLinkNotFound()
		}
		if err != nil {
			return fmt.Errorf("identity: read closure export token: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	var payload closureExportPayload
	if json.Unmarshal(raw, &payload) != nil || payload.ExportJobID == uuid.Nil {
		return uuid.Nil, errClosureLinkNotFound()
	}
	return payload.ExportJobID, nil
}
