---
title: "Phase 04: Identity & Organization"
status: todo
priority: P1
effort: 5d
dependsOn: [phase-03-database-schema-migrations]
---

# Phase 04 — Identity & Organization

## Context links

- [database-schema-org-partners.md](design/database-schema-org-partners.md) §1 platform & identity, §2 organization, §3 control, §5 invariants
- [architecture.md](design/architecture.md) §3.3 (`org`, `identity`), §6 security baseline, §7 tenant hosts (slug rules, host-scoped login, cookies, mail URLs)
- [ubiquitous-language.md](design/ubiquitous-language.md) §2.1, §6 exception kinds
- PRD §6.1 (ORG-01..07), §6.13 (SEC-01, SEC-03..07), §7.8 period locks, NFR-01, NFR-08

## Overview

Deliver sign-up, contact verification, login/logout, password reset, MFA (TOTP + recovery codes), session/device management, re-authentication for sensitive actions, ownership transfer, tenant creation with Go-side seed data, store/warehouse/settings, document numbering, period locks, exception confirmations, audit log reading, attachments, and demo/sandbox tenants. Implements the session and tenant resolvers behind phase 02 middleware.

## Key insights

- One owner account per tenant (SEC-01, `ux_users__one_active_owner`). There is no role matrix; authorization = authenticated session + tenant match + entitlement + (for sensitive actions) recent re-auth.
- Seed data goes through `org.TenantProvisioner` (Go) so it uses the same validation, ids, clock and audit as user actions (conventions §8): 7 colors, base units, tax rates, base attribute sets, retail/wholesale price lists, customer groups Khách lẻ/Khách sỉ, two fund accounts + payment methods, default reasons, one store, one warehouse, `tenant_settings`, onboarding steps. The provisioner calls ports owned by catalog/pricing/partner/cash so those domains keep ownership of their tables (DIP).
- Sessions are opaque random 32-byte tokens; only SHA-256 is stored. Cookie `__Host-tally_session` (dev: `tally_session`) host-only (no `Domain`), HttpOnly, Secure, SameSite=Lax, `Path=/`. CSRF uses a separate double-submit token `__Host-tally_csrf`. Each store has its own host `<slug>.DOMAIN`, so cookies never travel between stores.
- Sign-up happens on the apex host and picks the store address (slug); everything after it (verify, login, reset, MFA) happens on `<slug>.DOMAIN`, so no session ever crosses hosts. Login on a tenant host only accepts the owner of that host's tenant.
- Exception confirmation (SEC-03) replaces approval workflows: the owner ticks "Tôi xác nhận…" and the API stores an `exception_confirmations` row that the posting command references.

## Requirements

| ID | Behaviour |
|---|---|
| ORG-01 | Sign-up (apex host) with email + password + store address slug (`org.SlugPolicy`, architecture §7.2; live check `GET /signup/slug-check`), verification link to `https://<slug>.DOMAIN/verify` (single-use, 24 h), login on the tenant host scoped to that tenant, "Tìm cửa hàng của bạn" (`POST /store-lookup`, always 202, mails the store link), one account = one store (owner decision, global `UNIQUE (email_key)`): sign-up with an email that already has an account answers exactly like a new sign-up and mails that person the link to their existing store instead of creating a tenant, logout, password reset (single-use, 1 h), brute-force throttle (`login_attempts`: 5 fails / 15 min per email+IP → 15 min lock), generic error messages |
| ORG-02 | Tenant profile: legal/display name, address, tax code optional, currency VND, timezone default Asia/Ho_Chi_Minh, date format dd/MM/yyyy |
| ORG-03 | Exactly one store and one warehouse created by provisioner; no API to add more |
| ORG-05 | `docseq.Issuer` prefixes from ubiquitous-language §5; numbering unique per tenant |
| ORG-06 | Demo tenant (`is_sandbox=true`, slug `<parent-slug>-demo` when `sandbox_of` is set, `demo` for the standalone dev seed) created with sample data via `tally seed demo`; label returned in `/me`; reset endpoint re-provisions; reporting excludes sandbox tenants from platform analytics |
| ORG-07 | Ownership transfer: current owner re-auths, enters new email; new owner accepts via token; in one platform transaction the old user becomes `disabled` before the new user is activated (keeps `ux_users__one_active_owner`), old sessions revoked; documents keep `posted_by` ids; a new-owner email that already has an account in any store is rejected with `422 email_in_use` (the asker is a re-authenticated owner, the check is rate limited) |
| SEC-01 | Every request checks session ↔ tenant ↔ host tenant; tenant id only from session |
| SEC-03 | `POST /exception-confirmations` (kind, payload hash, note) returns id; posting commands verify kind + payload hash match and single use |
| SEC-04 | `GET /audit-events` (filters: entity, actor, action, date) paginated; before/after redacted |
| SEC-05 | `POST /period-locks` lock until date; `POST /period-locks/reopen` with reason + exception `period_reopen`; `periodlock.Guard` blocks backdated posting |
| SEC-06 | TOTP enrol/verify/disable, recovery codes (10, hashed), device/session list and revoke, re-auth (`POST /auth/reauth`) sets `sessions.reauthenticated_at`, valid 5 min |
| DATA-05 | `POST /attachments` presigned upload (type allow list: jpg, png, webp, pdf; ≤ 10 MB), confirm, `GET` signed URL; tenant check on every read |

## Architecture

```
identity/
  model.go     User, Session, AuthToken(purpose: verify_email|reset_password|owner_transfer|support_consent), TotpSecret, RecoveryCode
  ports.go     UserRepo, SessionRepo, TokenRepo, MfaRepo, AttemptRepo, Mailer, Hasher, Clock
  service.go   AuthService (SignUp, Verify, Login, Logout, RequestReset, Reset, Reauth),
               MfaService, SessionService (List, Revoke, RevokeAll), OwnershipService
  resolver.go  implements middleware.SessionResolver (token → Principal{UserID, TenantID, ReauthenticatedAt, MfaVerifiedAt, IsSandbox})
               and middleware.TenantHostResolver (slug → tenant id, via WithPlatformTx on tenants.slug)
org/
  model.go     Tenant (incl. Slug), Store, Warehouse, TenantSettings, PeriodLock, ExceptionConfirmation
  slug.go      SlugPolicy.Validate(slug) (format, reserved list, -demo suffix), Suggest(displayName) via textnorm.Slug, availability incl. retired_tenant_slugs
  provisioner.go TenantProvisioner.Provision(ctx, q, TenantSpec) — Facade over seeding ports
  ports.go     ColorSeeder, UnitSeeder, TaxSeeder, AttributeSetSeeder, PriceListSeeder, PartnerSeeder, FundSeeder, ReasonSeeder
  service.go   SettingsService, PeriodLockService, ExceptionService, AuditQueryService, AttachmentService
```

Patterns: Facade (`TenantProvisioner`), Strategy (`Hasher` = argon2id params m=64 MiB, t=3, p=2), Factory (`AuthToken.New(purpose, ttl)`), Template (all mails through `mailer` templates). Seeder ports are implemented in the owning domain phases; until those land, the provisioner wires the implementations as each phase completes (phase 05 colors/units/attributes, phase 06 price lists, phase 10 funds).

Routes (`/api/v1`) — root host only: `POST /auth/signup`, `GET /signup/slug-check?slug=` → `{available, reason: invalid|reserved|taken, suggestion}`, `POST /store-lookup`; any host: `GET /host-context`; tenant host only: everything else: `/auth/verify`, `/auth/login`, `/auth/logout`, `/auth/password-reset`, `/auth/password-reset/confirm`, `/auth/reauth`, `/auth/mfa/totp` (enrol), `/auth/mfa/totp/verify`, `DELETE /auth/mfa/totp`, `GET /me`, `GET/DELETE /sessions/{id}`, `POST /owner-transfer`, `/owner-transfer/accept`, `GET/PATCH /tenant`, `GET/PATCH /tenant/settings`, `GET /store`, `GET/POST /period-locks`, `POST /period-locks/reopen`, `POST /exception-confirmations`, `GET /audit-events`, `POST /attachments`, `POST /attachments/{id}:complete`, `GET /attachments/{id}/url`.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/identity/{model.go,ports.go,repo.go,service.go,resolver.go,http.go,dto.go,errors.go}` + tests | create |
| `apps/api/internal/org/{model.go,ports.go,repo.go,service.go,provisioner.go,http.go,dto.go,errors.go}` + tests | create |
| `apps/api/internal/platform/authn/{argon2.go,token.go,totp.go,recovery.go}` | create (TOTP RFC 6238, secrets AES-GCM encrypted with `APP_ENCRYPTION_KEY`) |
| `apps/api/internal/platform/mailer/templates/{verify_email,reset_password,owner_transfer,new_device,store_lookup,account_exists}.{txt,html}.tmpl` | create (Vietnamese copy; every link built with `urls.Builder.Tenant(slug, …)`) |
| `apps/api/internal/platform/textnorm/slug.go` + test | create — `Slug(s)` (`Cửa hàng Đường Phèn` → `cua-hang-duong-phen`, cut to 30, trimmed hyphens) |
| `apps/api/internal/cli/tenant.go`, `seed.go` | modify — `tenant create`, `seed demo` |
| `apps/api/api/openapi.yaml` | modify — auth, me, tenant, settings, period locks, exceptions, audit, attachments |
| `apps/api/internal/app/routes.go` | modify |

## Implementation steps

1. Hasher + token + TOTP primitives with tests (RFC 6238 vectors, constant-time compares).
2. Identity repos (platform tables via `WithPlatformTx`, explicit `tenant_id` filters); AuthService with sign-up creating tenant (with validated slug; a `tenants_slug_key` unique violation maps to `422 slug_taken`; an existing `email_key` sends the `account_exists` mail and returns the same status and body as a successful sign-up, after a dummy argon2id hash so timing matches) + user in one platform tx, then `WithTenantTx` to run `TenantProvisioner`. `slug-check` and `store-lookup` are rate limited per IP (and per email for lookup); slug-check revealing that a slug is taken is accepted, it is what a free-address check is.
3. Login flow (tenant host): throttle check → find user by `email_key` **and** host tenant (no match → verify a dummy argon2id hash, return generic `invalid_credentials`) → verify hash → MFA challenge if enrolled (`mfa_required` response with short-lived challenge token) → create session → set cookies; logout revokes.
4. Password reset + verification + ownership transfer tokens (single use: `used_at` set in same tx; reuse → `token_invalid`).
5. SessionResolver + TenantHostResolver + Tenant middleware (host guard from phase 02); Re-auth middleware `RequireRecentAuth(5*time.Minute)` used by sensitive routes across later phases.
6. Org services: settings (version-checked PATCH), period locks (append-only events; current lock = latest event), exception confirmations (payload hash = SHA-256 of canonical command payload, single use, expires 30 min), audit query, attachments (presign → complete with size/type check via `HEAD` → signed GET).
7. Provisioner skeleton + `tally tenant create --name --slug --owner-email` (`--slug` optional, defaults to the suggestion; prints the one-time `https://<slug>.DOMAIN/verify?…` link in dev only).
8. Demo tenant: `tally seed demo` creates a sandbox tenant with realistic Tết catalog sample (taken from prototype sample data, labelled sandbox); `POST /tenant/sandbox/reset` for sandbox only.
9. Tests: auth unit + integration (throttle, token single-use, session revoke takes effect on next request, MFA; slug policy table incl. built-in reserved and `RESERVED_SLUGS` entries, `xn--`, `--`, `-demo`, length 2/31, Vietnamese names; owner of A logging in on host B gets the same response as a wrong password; store lookup answers 202 for unknown emails and sends no mail; sign-up with an existing email returns the success response, creates no tenant and sends `account_exists`; ownership transfer to an email used in another store → `422 email_in_use`), isolation (session of tenant A requesting B's attachment id → 404), period-lock guard blocks backdated posting via a fake posting command.

## Todo

- [ ] authn primitives (argon2id, opaque tokens, TOTP, recovery codes)
- [ ] identity repos + AuthService + MFA + sessions + re-auth
- [ ] slug policy + suggestion + slug-check + store lookup; host-scoped login; host-only cookies
- [ ] ownership transfer
- [ ] SessionResolver/Tenant middleware wired
- [ ] org settings, store/warehouse read, doc prefixes
- [ ] period locks + guard integration
- [ ] exception confirmations (hash-bound, single use)
- [ ] audit query endpoint
- [ ] attachments presign/complete/signed GET
- [ ] TenantProvisioner + CLI `tenant create`
- [ ] demo/sandbox tenant + reset
- [ ] OpenAPI + contract test updated

## Success criteria & verification

- `make -C apps/api test test-integration` green; tests cover throttle lockout, expired/reused tokens, MFA required path, revoke-all, ownership transfer keeps audit ids, period-lock rejection (`423 period_locked`), exception confirmation reuse rejected.
- `tally tenant create --name "Cửa hàng A" --owner-email a@example.test` creates tenant `cua-hang-a` with one store, one warehouse, settings, 7 colors (after phase 05 seeders are wired), audit entry.
- Cross-tenant attachment/audit access returns 404, verified in isolation tests.
- Mail visible in Mailpit for verify/reset in `make dev`.

## Risks

| Risk | Mitigation |
|---|---|
| Email enumeration | identical responses/timings for unknown emails on login/reset, for emails of another tenant on a tenant host, for sign-up with an existing email, and for store lookup (always 202) |
| Store enumeration through subdomains | unknown slugs behave like real ones on every route except slug-check (rate limited); DNS is a wildcard so it reveals nothing |
| Session fixation | new token on login and on re-auth elevation; old token revoked |
| Provisioner partial failure | whole provisioning in one tenant transaction; retry-safe by tenant id |

## Security

- argon2id, TOTP secrets encrypted at rest, recovery codes hashed, tokens hashed, cookies host-only `__Host-` HttpOnly/Secure/SameSite, CSRF double submit, rate limits, generic auth errors, re-auth for sensitive actions, audit for every auth event (`login_succeeded`, `login_failed`, `mfa_enabled`, `session_revoked`, `owner_transferred`).

## Rollback

Revert code; no destructive data migration in this phase. Sessions can be mass-revoked by `tally maintenance revoke-sessions --tenant` if a defect is found.

## Next

Phase 05 implements catalog/partner seeders used by the provisioner; phase 14 builds the auth screens.
