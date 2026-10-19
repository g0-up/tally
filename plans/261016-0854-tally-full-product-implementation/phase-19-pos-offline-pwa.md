---
title: "Phase 19: POS offline & PWA"
status: in-progress
priority: P1
effort: 6d
dependsOn: [phase-16-web-sales-pos]
---

# Phase 19 — POS offline & PWA

## Context links

- UI spec §5.2.1 POS offline rows (lines 318–336), §6.2 "POS offline" rule, §7 `OFF-` prefix
- Prototype view 2216: offline callout, queue callout + "Đồng bộ ngay", exception callout (line 2229), sync loop (2314–2316)
- PRD §9.3 (offline banner with cache time and pending-sync list), §11.3 offline rules, CASH-06, AT-11, NFR-10
- Schema: `sales_documents.source = 'pos_offline'`, `offline_client_id`, `offline_created_at`, unique `idempotency_key`; `pos_sync_exceptions` (offline_client_id unique, device_session_id, payload, error_code, error_detail, status open/resolved/discarded, resolution_doc_id); `tenant_settings.offline_pos_enabled` (default false), `offline_pos_max_amount` (default 0), `offline_pos_max_hours` (default 12, 0–72)
- Architecture: `web` image serves SPA + PWA assets (design/architecture.md); §7.5 PWA per tenant host

## Overview

Makes `/sales/pos` usable without a network for cash sales: installable PWA shell, versioned offline catalog/price cache scoped to tenant and device session, an IndexedDB outbox of offline sales with stable local ids, a sync service that posts each sale exactly once, and an exception queue the owner resolves after reconnecting. Offline mode is off unless the owner enables it and sets risk caps.

## Key insights

- Offline is a controlled degraded mode, not a second engine. The browser never posts to the ledger; it records an intent (`offline_client_id`, lines, cash tendered, cache version, timestamps) and the server validates at sync. Anything the server rejects becomes a `pos_sync_exceptions` row, never a silent drop.
- Cash-only, full payment (PRD §11.3, prototype). The schema invariant mentions "cash/transfer" for `OfflineSyncService`; this phase follows PRD/prototype: transfer/QR disabled with title "QR cần xác nhận từ ngân hàng, không dùng khi ngoại tuyến". The schema note needs correcting by its owner.
- No credit, refund, return, adjustment, price override or owner exception while offline; the credit checkbox shows "(không cho phép khi ngoại tuyến)".
- Exactly-once: `offline_client_id` (UUIDv7 generated on device) is the `Idempotency-Key` and the unique column; the human label is `OFF-###` (3-digit device counter) for toasts. Re-sending after a lost response returns the original result.
- Risk caps from `tenant_settings`: per-sale and cumulative offline amount ≤ `offline_pos_max_amount`, and the cache/offline window ≤ `offline_pos_max_hours` since the last successful sync. Exceeding either blocks new offline sales with a clear error (new label).
- Every store runs on its own origin `<slug>.DOMAIN`, so the service worker, Cache Storage, IndexedDB outbox and PWA install are separated per store by the browser itself. Slugs are immutable, so an origin never changes and an unsynced outbox is never stranded. The manifest uses relative `start_url: "/sales/pos"` and `scope: "/"` so one build works on every tenant host; `name` comes from the generic app name (no per-tenant manifest in 1.1).
- Cache freshness is shown, never hidden: banner "Dùng bảng giá cache lúc dd/mm/yyyy HH:mm" uses the cache's server timestamp in Asia/Saigon.
- On reconnect the session must be re-validated (`GET /api/v1/session`); if expired or `reauth_required`, sync waits until the owner signs in again, and the outbox stays intact.
- Real connectivity replaces the prototype toggle "Mô phỏng mất mạng" / "Kết nối lại mạng": `navigator.onLine` plus a heartbeat `GET /api/v1/health` (3 s timeout, 15 s interval while degraded). The toggle stays only in dev/sandbox builds for demos and tests.

## Requirements

| PRD ID | Behaviour |
|---|---|
| §11.3, SAL-05 | Offline POS sells from cached catalog/prices; cash only, full payment; underpay error "**Chưa lưu.** Ngoại tuyến chỉ cho bán thu đủ tiền mặt." |
| §9.3 | Warn banner "**Đang ngoại tuyến.** Dùng bảng giá cache lúc dd/mm/yyyy HH:mm. Chỉ bán thu tiền ngay; không bán chịu, không hoàn tiền. Tồn hiển thị có thể chưa chính xác. Giao dịch chờ đồng bộ: **n**." + pending list |
| §11.3 | Local id + idempotency key; toast "Đã lưu OFF-00n trên máy. Sẽ ghi sổ một lần khi có mạng." |
| §11.3 | Online with queue: "Có n giao dịch ngoại tuyến chờ đồng bộ." + "Đồng bộ ngay"; result toast "Đồng bộ xong: n giao dịch ghi sổ[, m vào hàng đợi ngoại lệ]. Mỗi giao dịch chỉ ghi một lần." |
| §11.3 | Exception callout "**Hàng đợi ngoại lệ (n):** {OFF-id: lỗi} · …. Giao dịch không bị mất, quản lý cần xử lý." + resolution drawer |
| §11.3 | Caps per device/shift from tenant settings; offline disabled when `offline_pos_enabled = false` |
| §11.3 | Re-auth on reconnect before sync |
| NTF-01 | Notification kind for open sync exceptions (chip label "Lỗi đồng bộ POS", new) |
| CASH-06 | Offline sales attach to the implicit shift at sync time using `offline_created_at` |
| AT-11 | Acceptance: offline sales sync exactly once, conflicts land in the exception queue |

## Architecture

PWA tooling: `vite-plugin-pwa` in `injectManifest` mode (Workbox runtime underneath). Chosen because it integrates with the existing Vite build, generates the precache manifest from hashed assets, and lets us write our own service worker for the narrow caching rules below; plain `generateSW` would cache API responses too broadly.

```
apps/web/src/sw.ts                      precache app shell; runtime: NetworkOnly for /api/* except
                                        GET /api/v1/pos/offline-bundle (handled by app, not SW);
                                        never cache /export-jobs, downloads, /admin; SW registers only when
                                        /host-context says kind = tenant (no SW on root or admin hosts)
features/pos/offline/
  connectivity.ts                       useConnectivity(): online | offline | degraded (onLine + heartbeat)
  offline-db.ts                         idb schema: bundle, outbox, meta (per tenant + device session)
  offline-bundle-repository.ts          load/save versioned bundle {tenant_id, version, cached_at, expires_at,
                                        skus, prices, colors, combos, customers(minimal)}
  outbox-repository.ts                  add/list/markSynced/markException; OFF-### counter
  offline-sale-service.ts               validate caps + cash-only + full payment, build intent, enqueue
  sync-service.ts                       sequential POST /pos-offline-sales with Idempotency-Key, backoff
                                        (1s→60s, jitter), stop on 401/reauth, mark results
  components/offline-banner.tsx, sync-queue-callout.tsx, sync-exceptions-callout.tsx,
             sync-exception-drawer.tsx, pending-sync-list.tsx
features/settings/components/offline-policy-card.tsx   owner toggles + caps (mounted in /settings/account)
```

Selection: `PaymentMethodPanel` (phase 16) receives `mode="offline"` and disables transfer and credit strategies; `PosPage` routes checkout to `offline-sale-service` instead of the API when connectivity ≠ online.

API (phase 09 OpenAPI authoritative):

| Purpose | Request |
|---|---|
| Bundle | `GET /pos/bootstrap` (ETag; tiles, settings, price snapshot; offline section omitted and `offline_enabled=false` when disabled) |
| Policy | `GET /tenant/settings` · `PATCH /tenant/settings` (re-auth) |
| Sync | `POST /pos/offline-sync` (batch; each sale idempotent by its `offline_client_id`; item body includes `offline_created_at`, `bundle_version`, `device_session_id`) → per item `{offline_client_id, status: posted|exception, sale_doc_no?, exception_id?, error_code?}` |
| Exceptions | `GET /pos/sync-exceptions?status=open` · `POST /pos/sync-exceptions/{id}:resolve` (creates the doc online, optional exception confirmation) · `POST /pos/sync-exceptions/{id}:discard` (reason, re-auth) |

## Screens

- **Offline banner** (POS, above the layout): warn callout text above with cache time and pending count; "Xem giao dịch chờ" toggles `PendingSyncList` (OFF-id, time, total, status "Chờ đồng bộ").
- **Queue callout** (online, outbox > 0): text above + "Đồng bộ ngay" (btn-navy sm); auto-sync starts on reconnect after session check; the button stays for manual retry.
- **Exception callout** (bad): text above; "Xử lý" opens `SyncExceptionDrawer` at `/sales/pos?exceptions=open`: per row OFF-id, created time, lines, cash, error (server `error_detail`), acts "Ghi sổ lại" (resolve → may open `ExceptionConfirmDialog` for min-price/stock issues), "Bỏ qua giao dịch" (discard with Lý do, re-auth). Labels "Xử lý", "Ghi sổ lại", "Bỏ qua giao dịch", "Xem giao dịch chờ", "Chờ đồng bộ" are new.
- **Offline POS state**: tiles show cached avail with banner warning; QR disabled with title; credit disabled suffix; checkout toast offline text; cap errors "Vượt hạn mức bán ngoại tuyến. Kết nối mạng để tiếp tục." and "Dữ liệu ngoại tuyến đã quá hạn. Kết nối mạng để tải lại." (new).
- **Offline policy card** (`/settings/account`, addition): "Bán ngoại tuyến tại quầy" toggle, "Hạn mức tiền mặt ngoại tuyến", "Số giờ tối đa không đồng bộ" (0–72), hint on risks; save under re-auth.
- **Dev/sandbox only**: "Mô phỏng mất mạng" / "Kết nối lại mạng" toggle forcing `connectivity = offline`.

## Related files

Create: files listed under Architecture; tests beside each; `apps/web/public/manifest.webmanifest` (generated by plugin config), icons `apps/web/public/icons/*`; `e2e/pos-offline.spec.ts`.

Modify: `apps/web/vite.config.ts` (plugin), `apps/web/src/main.tsx` (register SW, update prompt "Đã có phiên bản mới. Tải lại để cập nhật." new label), `features/pos/pages/pos-page.tsx`, `features/pos/components/payment-method-panel.tsx`, `features/settings/pages/account-page.tsx` (mount card), `features/overview` notification chip map, `src/test/msw/handlers.ts`, nginx config for `sw.js` `Cache-Control: no-cache` (phase 01 owns infra file; coordinate).

## Implementation steps

1. Add `vite-plugin-pwa` (injectManifest), `idb`; write `sw.ts` with precache + strict NetworkOnly API rule; update prompt.
2. `offline-db.ts` with versioned stores keyed by `tenant_id` + `device_session_id` (defence in depth on top of per-origin storage); wipe on logout, on session revoke and when `/me` returns a different tenant than the stored one.
3. Bundle fetch on POS mount and every 30 min while online (ETag); store `cached_at`, `expires_at = min(server expiry, now + max_hours)`.
4. `useConnectivity` with heartbeat; expose to POS and util bar.
5. `offline-sale-service`: cash-only, full payment, caps, local price from bundle (same resolver output fields as online), enqueue with UUIDv7 + OFF counter.
6. `sync-service`: session check, sequential posting, backoff, result marking, invalidate stock/sale queries, toast summary.
7. Exception callout + drawer; resolve/discard flows.
8. Offline policy card; notification kind chip.
9. Dev toggle behind `import.meta.env.DEV || tenant.is_sandbox`.

## Todo

- [ ] PWA plugin, manifest, icons, service worker, update prompt (coded, update prompt unit-tested; bundle build that emits the manifest and service worker not run)
- [x] IndexedDB schema + wipe rules
- [x] Offline bundle repository + refresh
- [x] Connectivity hook (onLine + heartbeat)
- [x] Offline sale service with caps and cash-only rules
- [x] Outbox + sync service with backoff and re-auth gate
- [x] Banner, queue callout, pending list, exception callout + drawer
- [x] Offline policy card + notification chip (chip "Lỗi đồng bộ POS" on the notifications page, `features/notifications/pages/notifications-page.test.tsx`)
- [x] Dev/sandbox simulation toggle
- [ ] Unit + e2e tests (unit green, web vitest 156 files / 1109 tests on 2026-10-19; `e2e/pos-offline.spec.ts` written, not run)

Evidence (2026-10-18): unit tests in `apps/web/src/features/pos/offline/**` and
`apps/web/src/features/settings/components/offline-policy-card.test.tsx`; `corepack pnpm lint` and
`test` green in `apps/web` (62 files, 416 tests); `typecheck` is clean for these files (the only
errors are in `src/features/inventory/containers/color-price-dialog.test.tsx`, outside this phase).
Still open: the PWA item is coded (`vite.config.ts`, `src/sw.ts`, `public/icons/*`, update prompt
tested), but the bundle build that generates the manifest and service worker was not run. The
policy card is done; the notification chip belongs to the overview. Playwright
`e2e/pos-offline.spec.ts` is written and listed, but not run: it needs the local stack and
`E2E_OWNER_PASSWORD`. The sync endpoint is `POST /pos/offline-sync` (OpenAPI), not
`/pos-offline-sales`. Report: `plans/reports/fullstack-developer-261018-0955-pos-offline-pwa.md`.

Review fixes (2026-10-18, after `plans/reports/code-reviewer-261018-1030-pos-offline-pwa-review.md`):
- An online checkout without an answer is kept as an intent in IndexedDB and resent with the same key
  before the outbox, and its cart cannot be sold offline, so a lost response never posts twice.
- Only network errors switch to offline; server errors wait for the backoff.
- A sale refused for good, or failing five times, is set aside without blocking the queue.
- The cumulative cap is checked inside the outbox write.
- Sync requests time out after 15 s.
- A 5xx at boot opens the offline POS.
- Offline selling is blocked when prices exclude tax, when a named customer is chosen, or when the
  server could round the total differently.
Re-verified with phases 15–17: typecheck, lint and test green (76 files, 492 tests).

Accepted deviations (2026-10-18, to confirm with the owner):
- Logout keeps unsynced sales on the device; they sync only after signing back in to the same store.
- Discarding a parked sale asks for confirmation only, since the API takes no reason and no re-auth.
- A cleared cart whose checkout had no answer is still resent, so the sale posts at most once and its
  number is shown.


## Success criteria & verification

- `pnpm -C apps/web typecheck && pnpm -C apps/web lint && pnpm -C apps/web test` green; `pnpm -C apps/web build` emits `sw.js` and a manifest that passes Lighthouse installability.
- Unit (fake-indexeddb): `outbox-repository.test.ts` (OFF counter monotonic per device, survives reload), `offline-sale-service.test.ts` (underpay error, transfer/credit rejected, cap and age errors), `sync-service.test.ts` (resend after lost response posts once, 202 moves to exceptions, 401 pauses), `connectivity.test.ts` (heartbeat transitions).
- Playwright `pos-offline.spec.ts` (`context.setOffline`): enable policy → go offline → 3 cash sales → reload page offline → outbox still has 3 → reconnect → toast "Đồng bộ xong: 3 giao dịch ghi sổ…" → exactly 3 `HD-` with `source=pos_offline`; replay the same outbox twice → still 3 (AT-11); a sale with a SKU deactivated meanwhile → exception callout → "Ghi sổ lại" resolves; logout wipes IndexedDB.
- Manual device check on a real Android (Chrome) and iOS (Safari, added to home screen) per NFR-10: install, offline sale, reconnect sync.
- Visual parity vs view 2216 offline/queue/exception states at 1440 and 390.

## Risks

- iOS Safari evicts storage for non-installed sites after inactivity → require install for offline mode on iOS (hint in policy card); outbox sync on every foreground.
- Clock skew on devices → server stores `offline_created_at` but books business date from server receipt bounded by `max_hours`; skew > max_hours becomes an exception.
- Stale cached prices → bundle expiry and cap; cache time always visible.
- Service worker serving an old shell after deploy → update prompt + `no-cache` on `sw.js`.

## Security

- Cache holds only what selling needs: SKU names, prices, colors, avail snapshot, minimal customer list (id, name, phone suffix) — no costs, debts, credentials or tokens. CSRF token and session cookie stay in memory/cookies, never IndexedDB.
- Data is isolated per store origin by the browser, additionally scoped by tenant and device session, and wiped on logout, session revoke (`401 session_revoked` at sync) and tenant mismatch.
- Sync requires a valid session. When a device session is revoked from "Thiết bị đang đăng nhập", sync pauses and the outbox stays on that device until the owner signs in again there; the outbox is device-bound and cannot be submitted from another session. The banner shows the pending count so unsynced sales stay visible.
- Caps bound financial exposure; owner can disable offline instantly (bundle endpoint returns 403 → POS clears bundle).

## Rollback

Set `offline_pos_enabled = false` for all tenants (bundle 403, POS online-only) and ship a kill-switch service worker that unregisters itself if the PWA layer misbehaves. Outbox entries already on devices still sync once a fixed build is served.

## Next

Phase 20 audits offline states for accessibility (banner announced via `role="status"`), and phase 21 runs AT-11 in the acceptance suite plus a sync load test.
