# Báo cáo triển khai — POS ngoại tuyến & PWA (web)

## Kết quả

POS bán được khi mất mạng, chỉ cho phép bán thu đủ tiền mặt. Mỗi giao dịch được lưu trong IndexedDB và khi có mạng sẽ đồng bộ đúng một lần qua `POST /pos/offline-sync`. Giao dịch bị máy chủ đưa vào hàng đợi ngoại lệ được xử lý trong drawer, với hai thao tác "Ghi sổ lại" và "Bỏ qua giao dịch".

Trạng thái kiểm tra trong `apps/web` (sau khi sửa theo review, xem mục "Sửa sau review"):
- `corepack pnpm lint` sạch.
- `corepack pnpm test` xanh: 75 file, 475 test (lần nộp đầu là 62 file, 416 test).
- `corepack pnpm typecheck` sạch toàn bộ, kể cả lỗi inventory trước đây (agent khác đã sửa).

Hai việc chưa chạy: bundle build (hook chặn) và e2e (thiếu `E2E_OWNER_PASSWORD`).

## Phase

- Phase: phase-19-pos-offline-pwa
- Plan: `plans/261016-0854-tally-full-product-implementation/`
- Status: hoàn tất phần code và unit test. Frontmatter vẫn giữ `status: todo`.

## File đã sửa hoặc tạo (đường dẫn trong `apps/web/`)

**Khung ứng dụng và PWA**
- `vite.config.ts`: dùng vite-plugin-pwa ở chế độ injectManifest, prompt; khai báo manifest và icon.
- `src/sw.ts`: precache app shell, điều hướng SPA và `SKIP_WAITING`.
- `src/main.tsx`:
  - Đăng ký SW, chỉ ở PROD và trên host cửa hàng.
  - Khi API không phản hồi lúc khởi động, khởi động từ boot snapshot.
  - Gắn `watchSessionForOfflineData`.
  - Thêm `UpdatePrompt`.
- `public/icons/*`: 4 file PNG (192, 512, maskable 512, apple-touch 180).
- `package.json` và `pnpm-lock.yaml`:
  - Dependency: `idb`, `workbox-*`.
  - devDependency: `vite-plugin-pwa`, `fake-indexeddb`.

**Lõi ngoại tuyến (`src/features/pos/offline/`)**
- Lưu trữ trên máy:
  - `offline-db.ts`: schema, quy tắc xóa và `claimTenant`.
  - `outbox-repository.ts`: outbox và bộ đếm OFF-###.
  - `offline-bundle-repository.ts`: tải và làm mới catalog.
  - `boot-snapshot.ts`: snapshot khởi động và theo dõi phiên.
- Kết nối và bán hàng:
  - `connectivity.ts`: `onLine` + heartbeat, chế độ mô phỏng, gắn với `onlineManager`.
  - `offline-sale-service.ts`: hạn mức, quy tắc chỉ tiền mặt và hạn giờ.
  - `use-offline-checkout.ts`, `use-offline-line-pricing.ts`, `use-offline-pos.ts`.
  - `offline-queries.ts`, `offline-lookups.ts`.
- Đồng bộ và ngoại lệ:
  - `sync-service.ts` và `sync-errors.ts`.
  - `use-offline-sync.ts`: backoff, khóa Web Locks giữa các tab, cổng đăng nhập lại.
  - `parked-sales.ts`, `sync-exception-mutations.ts`.
- Tiện ích: `app-update.ts`, `uuidv7.ts`.
- `components/`:
  - `offline-banner`, `pending-sync-list`, `sync-queue-callout`.
  - `sync-exceptions-callout`, `sync-exception-drawer`.
  - `offline-simulation-toggle`, `update-prompt`.
- `testing/fake-offline-db.ts`, `testing/handlers.ts`: fixture và MSW handler dùng qua `server.use`. Tôi không sửa `src/test/msw/handlers.ts`.

**Màn POS**
- `containers/pos-cart-container.tsx`, `components/saved-carts-table.tsx`, `pages/pos-page.tsx`.
- Ba file này nối chế độ ngoại tuyến vào màn hình.

**Cài đặt**
- `src/features/settings/api/tenant-settings.ts`.
- `components/offline-policy-card.tsx`: thẻ chính sách; lưu bằng PATCH kèm `If-Match`.
- `pages/account-page.tsx`: gắn thẻ chính sách vào trang.

**Test**
- 12 file unit/component test:
  - Trong offline: `offline-db`, `outbox-repository`, `offline-bundle-repository`, `offline-sale-service`, `sync-service`, `connectivity`, `uuidv7`, `parked-sales`, `use-offline-sync`, `use-offline-checkout`.
  - Component: `offline-callouts`, `sync-exception-drawer`.
  - Cài đặt: `offline-policy-card`.
- `e2e/pos-offline.spec.ts`.

## Các mục Todo

- [x] Schema IndexedDB và quy tắc xóa
- [x] Repository gói catalog ngoại tuyến và làm mới
- [x] Hook kết nối (`onLine` + heartbeat)
- [x] Dịch vụ bán ngoại tuyến (hạn mức, chỉ tiền mặt)
- [x] Outbox và dịch vụ đồng bộ (backoff, cổng đăng nhập lại)
- [x] Banner, callout hàng chờ, danh sách chờ, callout và drawer ngoại lệ
- [x] Nút mô phỏng mất mạng cho dev/sandbox
- [ ] PWA: code đã xong, nhưng chưa chạy bundle build để sinh manifest và `sw.js` vì hook chặn lệnh có chữ "build".
- [ ] Thẻ chính sách: đã xong và có test. Chip thông báo "Lỗi đồng bộ POS" chưa làm, vì thuộc trang tổng quan của phase 18 và các file dùng chung.
- [ ] Unit test: xong. E2E: đã viết; `playwright test --list` nạp được file nhưng chưa chạy.

Phase file đã ghi dòng "Evidence (2026-10-18): …".

## Kịch bản e2e (`e2e/pos-offline.spec.ts`)

Kịch bản tự đăng nhập bằng context sạch, vì bước đăng xuất không được làm hỏng `OWNER_STATE_FILE` mà các spec khác dùng chung. Các bước:

1. Bật chính sách trên `/settings/account`.
2. Mở POS để tải catalog.
3. `context.setOffline(true)`, bán 3 giao dịch tiền mặt; outbox có 3.
4. Tải lại trang khi mất mạng; outbox vẫn còn 3.
   - Với bản production, SW phục vụ app shell.
   - Dev server không phục vụ được trình duyệt offline. Khi đó spec chặn mọi `/api/v1/**` để app thấy đúng tình trạng mất API.
5. Kết nối lại; toast "Đồng bộ xong: 3 giao dịch ghi sổ."; ba item `posted`.
6. Gửi lại đúng batch đó; ba item `replayed`, cùng `sales_document_id`.
7. Mất mạng lại, bán một SKU thứ hai, rồi ngừng SKU đó qua API. Kết nối lại thì callout "Hàng đợi ngoại lệ (1)" hiện ra.
8. Bật lại SKU, nhấn "Ghi sổ lại"; toast "Đã ghi sổ OFF-… thành …"; outbox còn 0.
9. Khôi phục chính sách ban đầu.
10. Đăng xuất; IndexedDB không còn bundle, không còn boot snapshot, outbox rỗng.

Nếu kịch bản dừng giữa chừng, khối `finally` vẫn khôi phục chính sách và trạng thái SKU. Lỗi khi khôi phục được ghi vào annotation, không bị nuốt.

## Khác biệt so với spec

- **Endpoint đồng bộ:** dùng `POST /pos/offline-sync` theo OpenAPI, không phải `/pos-offline-sales`.
- **Heartbeat** gọi `/me` vì không có `/health`. Cửa sổ ngoại tuyến lấy từ `/pos/offline-session`.
- **Hạn giờ** = min(cửa sổ máy chủ, số giờ của chính sách).
- **"Bỏ qua giao dịch"**: API không nhận lý do và không đòi xác thực lại, nên UI dùng hộp thoại xác nhận.
- **Số giờ tối đa:** OpenAPI quy định tối thiểu 1, còn Go service chấp nhận 0–72. UI kiểm 1–72.
- **Đăng xuất "xóa IndexedDB":** chỉ xóa catalog và boot snapshot. Giao dịch chưa đồng bộ được giữ lại để gửi sau lần đăng nhập kế tiếp, vì xóa chúng là mất tiền đã thu. E2E kiểm outbox rỗng vì lúc đăng xuất mọi giao dịch đã ghi sổ.
- **Giới hạn khi bán ngoại tuyến:**
  - Chỉ dùng giá bán lẻ và đơn vị của tile.
  - Không tính thuế, khuyến mãi hay giá tối thiểu trên máy; máy chủ kiểm khi đồng bộ và đưa vào hàng đợi ngoại lệ nếu sai.
  - Không tìm được khách hàng.
  - Giỏ hàng đã lưu bị khóa.
- **Test của các màn dùng chung:** `TEST_ME` không có `session`, nên test của app/guards dùng chung không bật tính năng ngoại tuyến.

## Lỗi thật đã sửa trong lúc viết test

Khi một lệnh ghi trong transaction idb thất bại (ví dụ thêm trùng `offline_client_id`), `tx.done` bị reject mà không ai chờ, sinh ra unhandled rejection (AbortError). Đã thêm `inTransaction(writes, done)` để chờ cả hai cùng lúc, và dùng nó trong `addToOutbox`, `markOutboxException`, `pruneSettledExceptions`, `claimTenant`.

## Vấn đề ngoài phạm vi

- **Typecheck:** 3 lỗi Qty/Money brand trong `src/features/inventory/containers/color-price-dialog.test.tsx` (agent phase17-web). Tôi không sửa.
- **Lint:** lỗi lint của kits trong `recipe-editor.tsx`, thấy ở lần chạy trước, nay đã hết.
- **Test chập chờn:** `src/features/procurement/pages/orders-page.test.tsx` fail 1 lần khi chạy cả bộ, vì `findByRole` hết 1 s lúc máy tải nặng. Chạy riêng thì xanh; 2 lần chạy cả bộ sau đó đều xanh. Không thuộc phase này.
- **Chưa làm:** chip "Lỗi đồng bộ POS" trên tổng quan và chỉ báo trên util bar. Cả hai cần sửa file dùng chung hoặc thuộc phase 18.
- **Tiến trình nền:** không khởi động vite hay watcher nào; đã kiểm `ps`, không còn tiến trình.
- **Commit:** chưa commit.

## Bước tiếp theo

- Chạy bundle build để xác nhận manifest và `sw.js` được sinh ra, rồi kiểm SW trên host cửa hàng.
- Chạy `corepack pnpm exec playwright test e2e/pos-offline.spec.ts` khi có `make dev` và `E2E_OWNER_PASSWORD`.
- Phase 18 thêm chip "Lỗi đồng bộ POS" (đếm từ `GET /pos/sync-exceptions?status=open`).

## Sửa sau review

Đã sửa đủ các finding trong `plans/reports/code-reviewer-261018-1030-pos-offline-pwa-review.md`. Không sửa `apps/api`, `src/test/msw/handlers.ts`, router/sidebar dùng chung, hay features inventory/kits/sales/procurement. Handler MSW của offline nằm ở `features/pos/offline/testing/handlers.ts`.

### Critical và High

- **C1, ghi sổ trùng.** Checkout online không rõ kết quả (lỗi mạng, timeout, 502, 504) được lưu vào store IDB mới `intents` (DB lên v2): Idempotency-Key, path, body, If-Match, số tiền khách đã trả. Giỏ đó bị chặn bán ngoại tuyến. Callout "Chưa nhận được kết quả thanh toán giỏ này…" hiện lên, và F9 lúc mất mạng báo "Chưa lưu. Giao dịch trước của giỏ này có thể đã ghi sổ. Chờ có mạng để kiểm tra trước khi bán lại."
  - Khi có mạng, mỗi lượt sync gửi lại các intent trước outbox, cùng key và cùng body. Intent chỉ bị xóa khi nhận 2xx (đã ghi, hoặc server phát lại kết quả cũ) hay một lỗi 4xx dứt khoát. Lỗi mạng, 5xx và `idempotency_key_in_progress` thì giữ intent và dừng lượt sync.
  - Kết quả được báo cho mọi tab qua sự kiện `intent-settled`. Đã ghi thì giỏ được xóa và toast hiện số HD; bị từ chối thì giỏ bán lại được.
  - Lưu hay xóa intent đều phát sự kiện `changed`. Lỗi này lộ ra nhờ test mới của container: thiếu sự kiện thì sync không biết có intent cần gửi.
  - Test `containers/pos-cart-container.test.tsx`: mất response, F9 lúc offline không tạo bản ghi outbox nào, có mạng lại thì `/pos/checkout` được gửi lại đúng key và body cũ, giỏ được xóa.
- **H1, vòng lặp request.** Chỉ lỗi mạng mới gọi `reportFailure`; hàm này không còn probe ngay mà hẹn kiểm lại sau 5 s. 5xx, 408 và 429 dừng lượt sync và chờ backoff; lượt tự động không chạy khi timer backoff đang chờ. Test: 5xx liên tục trong khi `/me` trả 200 thì trong 5 s có 2–3 POST, trạng thái vẫn online.
- **H2, thuế.** Không tính thuế trên máy: khi `prices_include_tax = false` thì không bán ngoại tuyến, banner và toast báo `OFFLINE_MESSAGES.taxExcluded` ("Cửa hàng đang tính giá chưa gồm thuế; máy không có thuế suất để tính đúng tổng tiền nên không bán ngoại tuyến được. Kết nối mạng để bán."). Cần quyết định sau này nếu muốn hỗ trợ: đưa thuế suất vào `/pos/bootstrap` (phạm vi API).

### Medium

- **M1.** Bản ghi bị từ chối dứt khoát (4xx của request, `idempotency_key_reused`) hoặc lỗi lặp lại 5 lần (`MAX_SALE_FAILURES`) chuyển sang trạng thái `blocked`, không chặn hàng đợi và không retry mãi. Callout `BlockedSalesCallout` liệt kê từng giao dịch, lý do, nút "Gửi lại" và "Xóa khỏi máy" (có hộp thoại xác nhận, nhắc đối chiếu tiền). `/pos/offline-session` chỉ gọi một lần mỗi lần có kết nối.
- **M2.** Hạn mức cộng dồn được kiểm trong chính transaction IDB của `addToOutbox`, tính cả giao dịch đang chờ, đang trong hàng đợi ngoại lệ và bị chặn. Transaction readwrite trên outbox chạy tuần tự giữa các tab, nên hai tab không cùng vượt hạn mức (có test hai lần ghi song song).
- **M3.** Đang offline mà giỏ có khách thì hiện callout cảnh báo, và lúc thanh toán bị chặn với `OFFLINE_MESSAGES.customer` ("Ngoại tuyến chỉ bán giá lẻ cho khách lẻ…"). Sale ngoại tuyến không bao giờ gửi `customer_party_id`. Ngoài ra, tổng mà server có thể làm tròn khác (làm tròn từng dòng so với cả chứng từ) bị chặn với `OFFLINE_MESSAGES.rounding`.
- **M4.** Khi boot, nếu API trả 5xx hoặc một trang HTML của proxy (context lấy từ hostname) thì vẫn vào chế độ offline từ snapshot đã lưu. Lỗi mạng mà không có snapshot vẫn hiện màn lỗi boot như trước.
- **M5.** Mọi request sync có timeout 15 s (`SYNC_TIMEOUT_MS`); hết hạn được tính như không có phản hồi.

### Low

- **L1.** `main.tsx` gỡ `link[rel="manifest"]` khi host không phải cửa hàng. Plugin vẫn chèn link lúc build; việc gỡ diễn ra lúc chạy.
- **L2.** `refreshBundle` kiểm `cancelled` trước khi ghi IDB (có test).
- **L3.** Outbox của tenant khác trên máy (sau sandbox reset hoặc đổi cửa hàng) được đếm và báo bằng callout: "Máy còn n giao dịch ngoại tuyến của cửa hàng khác, chưa đồng bộ…".
- **L4.** Thẻ chính sách bắt hạn mức lớn hơn 0 đ khi bật bán ngoại tuyến: "Bật bán ngoại tuyến cần hạn mức lớn hơn 0 đ."
- **L5.** Callout ngoại lệ và giao dịch bị chặn dùng `role="alert"`; callout hàng chờ dùng `role="status"`.
- **L6.** `offline-events.ts` phát sự kiện qua BroadcastChannel (và trong chính tab đó). Outbox, intent và số giao dịch của tenant khác được đọc lại ở mọi tab.
- **L7.** E2E đếm chứng từ `HD-` có `source=pos_offline` cho mỗi `offline_client_id` đã đồng bộ, yêu cầu đúng 1 chứng từ.

### Lệch spec cần lead/user quyết định

- **Đăng xuất giữ outbox (L8).** Spec ghi "wiped on logout". Theo chỉ đạo của lead, outbox và intent được giữ vì xóa là mất tiền đã thu. Chúng gắn theo tenant id và chỉ được liệt kê và đồng bộ khi đăng nhập lại đúng tenant đó (có test cách ly). Catalog và boot snapshot vẫn bị xóa.
- **Bỏ qua giao dịch (L9).** API `:discard` không nhận lý do và không đòi xác thực lại, nên UI chỉ có hộp thoại xác nhận. Nếu cần, phải bổ sung ở phase API.
- **Chặn bán ngoại tuyến** khi giỏ có khách, khi giá chưa gồm thuế, và khi tổng có thể làm tròn khác server.
- **Rủi ro còn lại:** khuyến mãi tự áp của server vẫn có thể làm tổng lệch, và giao dịch sẽ vào hàng đợi ngoại lệ chứ không mất. Máy không có dữ liệu khuyến mãi.
- **Giỏ bị xóa khi chưa rõ kết quả:** intent vẫn được gửi lại khi có mạng. Nếu lần đầu chưa ghi sổ thì lần gửi lại sẽ ghi sổ giao dịch đó, và toast báo số HD để thu ngân đối chiếu. Server dùng key để bảo đảm chỉ ghi một lần.

### File sửa hoặc thêm trong lần này (trong `apps/web/`)

- `src/main.tsx`
- `src/features/pos/api/mutations.ts`
- `src/features/pos/containers/pos-cart-container.tsx`, `pos-cart-container.test.tsx` (mới)
- `src/features/pos/pages/pos-page.tsx`
- `src/features/pos/offline/`:
  - mới: `checkout-intents.ts`, `offline-events.ts`
  - sửa: `offline-db.ts`, `outbox-repository.ts`, `sync-service.ts`, `connectivity.ts`, `offline-queries.ts`, `offline-bundle-repository.ts`, `use-offline-pos.ts`, `use-offline-sync.ts`, `offline-sale-service.ts`, `use-offline-checkout.ts`, `use-offline-line-pricing.ts`
  - các file test tương ứng
  - `testing/handlers.ts`
- `src/features/pos/offline/components/`:
  - mới: `blocked-sales-callout.tsx`
  - sửa: `offline-banner.tsx`, `sync-queue-callout.tsx`, `sync-exceptions-callout.tsx`, `offline-callouts.test.tsx`
- `src/features/settings/components/offline-policy-card.tsx` và test
- `e2e/pos-offline.spec.ts`

### Chưa chạy

- Bundle build (hook chặn lệnh có chữ "build"), nên chưa xác nhận manifest và `sw.js` sau build.
- E2E Playwright (cần API đang chạy và `E2E_OWNER_PASSWORD`).

Không khởi động tiến trình nền nào. Chưa commit.

Status: DONE_WITH_CONCERNS
Summary: Đã sửa C1, H1, H2, M1–M5 và L1–L7. L8 và L9 được ghi nhận là lệch spec. Lint và test xanh (75 file, 475 test), typecheck sạch.
Concerns/Blockers: Chưa chạy bundle build (hook chặn) và e2e (cần API và E2E_OWNER_PASSWORD). Cần lead/user chấp nhận các lệch spec: giữ outbox khi đăng xuất, discard không có lý do hay xác thực lại, chặn bán ngoại tuyến khi có khách, khi giá chưa gồm thuế hoặc khi tổng có thể làm tròn khác server. Khuyến mãi tự áp vẫn có thể đẩy giao dịch vào hàng đợi ngoại lệ.
