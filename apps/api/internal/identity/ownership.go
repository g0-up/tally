package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// transferPayload is stored with an ownership transfer token.
type transferPayload struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
}

// TransferInput asks to hand the store over to someone else.
type TransferInput struct {
	Email    string
	FullName string
	Client   Client
}

// RequestOwnerTransfer mails the new owner a link to accept the store. The caller passed the
// re-authentication guard. An address that already has an account in any store is 422
// email_in_use; requests are rate limited per owner, since the answer reveals whether an
// address is registered.
func (s *Service) RequestOwnerTransfer(ctx context.Context, p middleware.Principal, in TransferInput) error {
	email, err := checkOwner(in.Email, in.FullName)
	if err != nil {
		return err
	}
	if allowed, _ := s.transferLimiter.Allow(p.UserID.String()); !allowed {
		return apperr.New(apperr.RateLimited, "rate_limited", "Too many requests; try again later")
	}
	payload, err := marshalPayload(transferPayload{Email: email, FullName: strings.TrimSpace(in.FullName)})
	if err != nil {
		return err
	}
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
		var found bool
		var err error
		owner, found, err = userByID(ctx, q, p.TenantID, p.UserID, true)
		if err != nil {
			return err
		}
		if !found || owner.Status != StatusActive {
			return apperr.NewUnauthenticated()
		}
		taken, err := emailTaken(ctx, q, textnorm.EmailKey(email))
		if err != nil {
			return err
		}
		if taken {
			return errEmailInUse()
		}
		if store, err = readTenantInfo(ctx, q, p.TenantID); err != nil {
			return err
		}
		if err := retireTokens(ctx, q, owner.TenantID, owner.ID, PurposeOwnerTransfer, now); err != nil {
			return err
		}
		if err := insertToken(ctx, q, owner.TenantID, owner.ID, PurposeOwnerTransfer, s.keys.Tokens.Sum(token), payload,
			now.Add(OwnerTransferTTL), now); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, ownerActor(owner.TenantID, owner.ID, in.Client), audit.Event{
			Action: "tenant.request_owner_transfer", EntityType: "tenant", EntityID: owner.TenantID,
		})
	})
	if err != nil {
		return err
	}
	s.send(mailOwnerTransfer, email, mailData{
		Name: strings.TrimSpace(in.FullName), StoreName: store.DisplayName, StoreURL: s.urls.Tenant(store.Slug, "/"),
		Link: s.urls.Tenant(store.Slug, "/owner-transfer/accept?token="+token),
	})
	return nil
}

// AcceptInput accepts a store with the password of the new owner account.
type AcceptInput struct {
	HostTenant uuid.UUID
	Token      string
	Password   string
	Client     Client
}

// AcceptOwnerTransfer makes the invited person the owner, in one transaction: the old owner is
// disabled before the new one is inserted (only one non-disabled owner may exist), the old
// owner's sessions and pending transfer links are revoked. Existing records keep the old
// owner's id as their actor.
func (s *Service) AcceptOwnerTransfer(ctx context.Context, in AcceptInput) error {
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
		tok, found, err := consumeToken(ctx, q, in.HostTenant, PurposeOwnerTransfer, s.keys.Tokens.Sum(in.Token), now)
		if err != nil {
			return err
		}
		if !found {
			return errTokenInvalid()
		}
		var payload transferPayload
		if err := json.Unmarshal(tok.Payload, &payload); err != nil || payload.Email == "" {
			return errTokenInvalid()
		}
		old, found, err := userByID(ctx, q, tok.TenantID, tok.UserID, true)
		if err != nil {
			return err
		}
		if !found || old.Status == StatusDisabled {
			return errTokenInvalid()
		}
		newOwner := User{
			ID: ids.New(), TenantID: old.TenantID, Email: payload.Email, EmailKey: textnorm.EmailKey(payload.Email),
			FullName: payload.FullName, PasswordHash: hash, EmailVerifiedAt: &now, Status: StatusActive,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		taken, err := emailTaken(ctx, q, newOwner.EmailKey)
		if err != nil {
			return err
		}
		if taken {
			return errEmailInUse()
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE users SET status = $3, updated_at = $4, version = version + 1 WHERE tenant_id = $1 AND id = $2`,
			old.TenantID, old.ID, StatusDisabled, now); err != nil {
			return fmt.Errorf("identity: disable previous owner: %w", err)
		}
		if err := insertUser(ctx, q, newOwner); err != nil {
			if db.IsUniqueViolation(err, "ux_users__email_key") {
				return errEmailInUse()
			}
			return err
		}
		if _, err := revokeSessions(ctx, q, old.TenantID, old.ID, uuid.Nil, "owner_transferred", now); err != nil {
			return err
		}
		if err := retireTokens(ctx, q, old.TenantID, old.ID, PurposeOwnerTransfer, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, ownerActor(newOwner.TenantID, newOwner.ID, in.Client), audit.Event{
			Action: "tenant.transfer_owner", EntityType: "tenant", EntityID: old.TenantID,
			Before: map[string]any{"owner_id": old.ID}, After: map[string]any{"owner_id": newOwner.ID},
		})
	})
}
