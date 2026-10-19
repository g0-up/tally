# Code review: phase 19, POS offline và PWA

Ngày: 2026-10-18 · Reviewer: code-reviewer · Chỉ review, không sửa source.

## Phạm vi

- Web: `apps/web/src/sw.ts`, `apps/web/src/main.tsx`, `apps/web/vite.config.ts`, `apps/web/nginx.conf`
- `apps/web/src/features/pos/offline/**`: offline-db, outbox-repository, offline-bundle-repository, boot-snapshot, connectivity, sync-service, use-offline-sync, use-offline-pos, offline-sale-service, use-offline-checkout, use-offline-line-pricing, parked-sales, sync-exception-mutations, components/*
- `apps/web/src/features/pos/containers/pos-cart-container.tsx`, `apps/web/src/features/pos/pages/pos-page.tsx`
- `apps/web/src/features/settings/components/offline-policy-card.tsx`, `apps/web/src/features/settings/api/tenant-settings.ts`
- Đối chiếu server: `apps/api/internal/sales/offline_sync.go`, `apps/api/internal/sales/pos.go`, OpenAPI và migration settings
- `apps/web/e2e/pos-offline.spec.ts`

Lưu ý: số dòng lấy theo bản file lúc 11:08. Các file trong `offline/` có mtime từ 10:14 đến 10:57.

## Kết quả kiểm tra

| Kiểm tra | Kết quả |
|---|---|
| `corepack pnpm exec vitest run src/features/pos src/features/settings` | 15 file, 109 test pass |
| `tsc -p tsconfig.app.json --noEmit` | Còn 3 lỗi, đều nằm trong `src/features/inventory/containers/color-price-dialog.test.tsx` (thuộc agent khác, bỏ qua) |
| eslint trên các file phase 19 | Sạch |
| Bundle production, Playwright e2e | **Chưa chạy.** Hook chặn lệnh có chữ build; e2e cần stack đang chạy |

## Đánh giá chung

Phần lõi offline khá chắc:
- Outbox ghi nguyên tử, bộ đếm OFF tăng trong cùng transaction.
- Body gửi lại giữ nguyên từng byte, có `offline_client_id` UUIDv7 ổn định.
- Trường hợp mất response được xử lý qua `replayed`, có test.
- 401 dừng sync và giữ nguyên outbox.
- SW không cache `/api`, chỉ đăng ký trên tenant host.
- Tiền tính bằng Decimal.

Rủi ro thật nằm ở **chỗ chuyển giữa online và offline**: một sale có thể bị ghi sổ hai lần, và có một vòng lặp request không có backoff. Ngoài ra, giá offline không khớp resolver khi cửa hàng để giá chưa gồm thuế hoặc khi giỏ có khách.

---

## Critical

### C1. Ghi sổ trùng: checkout online mất response, giỏ được bán lại ở chế độ offline với ID mới

- **File:**
  - `apps/web/src/features/pos/containers/pos-cart-container.tsx:79` (`useCheckout(() => cart.reset())`, chỉ reset giỏ khi thành công)
  - `:92-94` (lỗi mạng gọi `connectivity.reportFailure()`)
  - `:104-113` (`pay()` khi `isOffline` gọi `offlineCheckout.run(...)`)
  - `apps/web/src/features/pos/offline/offline-sale-service.ts:142` (`offline_client_id: uuidv7(now.getTime())` được tạo mới mỗi lần bán)
- **Kịch bản:**
  1. Thu ngân bấm F9 khi đang online. POST checkout đến được server và được ghi sổ, nhưng response bị mất (Wi-Fi rớt, LB reset).
  2. `checkout.error.isNetwork` dẫn tới `reportFailure()`. Probe `/me` cũng lỗi, trạng thái chuyển sang `degraded`, và `isOffline` thành true.
  3. Giỏ vẫn còn trên màn hình vì `cart.reset()` chỉ chạy khi thành công. Thu ngân thấy banner offline và bấm F9 lại.
  4. `offlineCheckout.run` tạo `offline_client_id` mới. Khi có mạng lại, `/pos/offline-sync` ghi sổ thêm một HD- nữa.
  
  Idempotency-Key của lần checkout online và `offline:<uuid>` thuộc hai namespace khác nhau, nên server không có cách nào khử trùng. Hậu quả: doanh thu, xuất kho và quỹ tiền mặt đều bị ghi đôi. Đây đúng là vi phạm mục tiêu "exactly-once" của phase.
- **Cách sửa:**
  - Khi checkout online kết thúc bằng lỗi mạng (không biết đã ghi sổ hay chưa), lưu "intent chưa rõ kết quả" (Idempotency-Key và body) vào IDB, gắn với giỏ hiện tại.
  - Trong lúc intent đó còn tồn tại, chặn bán offline giỏ này. Hiển thị thông điệp rõ ràng, ví dụ "Giao dịch trước có thể đã ghi sổ, chờ có mạng để kiểm tra".
  - Khi có mạng, gửi lại intent online **với cùng key** trước rồi mới sync outbox.
  - Một phương án khác: chuyển giỏ đó sang outbox, dùng chính idempotency key của lần online làm `offline_client_id`. Cách này cần server tra chéo hai namespace.
  - Thêm test: checkout online trả `isNetwork`, sau đó `pay()` ở offline không được tạo entry outbox mới cho cùng giỏ.

## High

### H1. Vòng lặp request không có backoff khi API trả 5xx còn `/me` vẫn trả 200

- **File:**
  - `apps/web/src/features/pos/offline/sync-service.ts:53` (5xx được coi là `"network"`)
  - `apps/web/src/features/pos/offline/use-offline-sync.ts:124-127` (`reportFailure(); scheduleRetry()`)
  - `use-offline-sync.ts:90` (`run()` gọi `clearRetry()`)
  - `use-offline-sync.ts:141-143` (effect `[state, hasPending]` chạy sync ngay khi state về online)
  - `apps/web/src/features/pos/offline/connectivity.ts:92-96`, `:137-140` (`reportFailure` chuyển sang `degrade()` rồi gọi `check()` ngay lập tức)
  - `apps/web/src/features/pos/offline/use-offline-pos.ts:50`, `:59` (refresh bundle lỗi 5xx cũng gọi `reportFailure`; effect có `online` trong deps)
- **Kịch bản:** Có một sale mà handler `SyncOffline` panic hoặc trả 500 (dữ liệu xấu, lỗi DB cục bộ), trong khi `/me` vẫn 200.
  1. `stopped="network"`, gọi `reportFailure()`, state chuyển `degraded`, và `check()` chạy ngay.
  2. `/me` trả 200, state về `online`, effect dòng 141 chạy `run(false)`.
  3. `clearRetry()` hủy timer backoff vừa đặt, rồi gửi lại ngay. Quay lại bước 1.
  
  Vòng lặp chạy với tốc độ một RTT, mỗi vòng gồm GET `/me`, POST `/pos/offline-session` (ghi session và audit) và POST `/pos/offline-sync`. Backoff 1s đến 60s không bao giờ có tác dụng. Refresh bundle (`/pos/offline-bundle` trả 5xx) cũng tạo vòng lặp tương tự qua effect `online` ở `use-offline-pos.ts:59`.
  
  Tác dụng phụ trên UI:
  - POS nhấp nháy giữa offline và online.
  - Mỗi lần sang offline, effect `pos-cart-container.tsx:84-89` xóa luôn số chuyển khoản và cờ bán chịu thu ngân đang nhập.
  - Query toàn app bị pause rồi refetch liên tục qua `bindOnlineManager`.
- **Cách sửa:**
  - Chỉ gọi `reportFailure()` khi `isNetwork` (request không có phản hồi). 5xx là server có phản hồi, nên retry bằng backoff riêng và không đổi trạng thái kết nối.
  - Effect tự chạy (dòng 141) và `visibilitychange` (dòng 147) phải tôn trọng backoff: bỏ qua nếu `retry.current !== null`, hoặc chỉ reset `attempt` khi một pass thành công.
  - `reportFailure()` không nên probe ngay. Nên chờ `intervalMs`, hoặc có ít nhất một khoảng tối thiểu.
  - Thêm test: `offline-sync` trả 500 và `/me` trả 200 thì số POST trong 5 giây phải nhỏ hơn hoặc bằng 3.

### H2. Bỏ qua thuế: cửa hàng có `prices_include_tax = false` thì mọi sale offline đều vào ngoại lệ, và khách bị thu thiếu tiền

- **File:**
  - `apps/web/src/features/pos/offline/offline-sale-service.ts:61-85` (`priceOffline`, có comment "no tax")
  - `apps/web/src/features/pos/offline/use-offline-line-pricing.ts:33` (`tax_amount: toMoney(0)`)
  - Server: `apps/api/internal/sales/pos.go:320` gọi `checkOfflineTotal(built.Totals.Total, t.Paid, ...)`, với `offline_sync.go:137` yêu cầu `paid == total`
- **Kịch bản:**
  1. Cửa hàng tắt "giá đã gồm thuế". Client thu đúng bằng giá lẻ.
  2. Server dựng sale với `unit_price` client gửi lên rồi cộng thuế, nên `total > paid`. Kết quả là `offline_payment_not_allowed` và sale bị đưa vào hàng ngoại lệ.
  3. Với khách lẻ, "Ghi sổ lại" gửi lại đúng payments cũ nên lại thất bại. Lựa chọn duy nhất còn lại là "Bỏ qua", tức hàng đã giao mà không có chứng từ.
  
  Nhánh `rounding_mode = 'document'` cũng có khả năng làm `total ≠ paid` (xem câu hỏi mở). Migration mặc định là `prices_include_tax = true`, nên lỗi chỉ xảy ra với cửa hàng đã đổi cấu hình. Tuy vậy, khi xảy ra thì 100% sale offline bị hỏng.
- **Cách sửa:**
  - Cách 1: đưa `prices_include_tax`, thuế suất và rounding vào bundle, rồi tính thuế ở client theo đúng resolver online.
  - Cách 2 (nhanh hơn): khi `prices_include_tax = false`, chặn bán offline bằng thông điệp rõ ràng, giống cách đang làm với `offline_pos_enabled`.
  - Thêm test hợp đồng: cùng giỏ hàng thì total offline phải bằng total của `/pos/price` online.

## Medium

### M1. Một sale lỗi vĩnh viễn chặn cả hàng đợi và bị retry mãi mãi

- **File:**
  - `apps/web/src/features/pos/offline/sync-service.ts:54-55` (4xx khác 401/403 là `"failed"`)
  - `sync-service.ts:83-107` (dừng pass ngay ở sale đầu tiên bị lỗi)
  - `apps/web/src/features/pos/offline/use-offline-sync.ts:128-131` (`scheduleRetry()` không có giới hạn)
- **Kịch bản:** Một sale bị 422 cho cả batch (body không qua validate, ví dụ một SKU đã bị xóa khiến schema từ chối), hoặc item trả `failed` vì lý do không tự hết.
  - Pass dừng lại ở đúng sale đó, nên mọi sale xếp sau không bao giờ được gửi.
  - Retry chạy mỗi khoảng 60s mãi mãi, và mỗi lần đều gọi `/pos/offline-session` trước, sinh audit rác.
  - Không có UI nào cho quản lý xử lý entry này. Callout chỉ hiện "Chờ đồng bộ".
- **Cách sửa:**
  - Đếm số lần thử cho từng entry. Với 4xx không tạm thời, hoặc khi vượt N lần, đánh dấu entry là `blocked` (hiển thị cùng hàng ngoại lệ, có nút "Bỏ qua" hoặc "Thử lại") và tiếp tục với các sale sau. Riêng lỗi do chính sale đang được gửi ở upload khác thì giữ nguyên thứ tự.
  - Chỉ grant `/pos/offline-session` một lần mỗi phiên kết nối, không gọi ở mỗi pass.

### M2. Hạn mức cộng dồn bị vượt khi mở nhiều tab và không tính sale đã vào ngoại lệ

- **File:**
  - `apps/web/src/features/pos/offline/use-offline-checkout.ts:37` (`pendingTotal` lấy từ `pending` trong cache query của tab hiện tại)
  - `apps/web/src/features/pos/offline/use-offline-sync.ts:56-64` (`staleTime: Infinity`, chỉ lọc `status === "pending"`)
  - `apps/web/src/features/pos/offline/offline-sale-service.ts:125`
  - Server chỉ kiểm theo từng giao dịch (`offline_sync.go:137`)
- **Kịch bản:**
  - Hai tab POS cùng cửa hàng, mỗi tab chỉ thấy outbox của chính nó, nên mỗi tab bán được tới đủ cap. Tổng chưa đồng bộ lên tới 2× cap.
  - Sale đã bị park (exception) vẫn là tiền chưa ghi sổ nhưng không được cộng vào. Sau một pass có sale bị park, cap cộng dồn coi như được "nạp lại".
  - Server không có lớp chặn thứ hai.
- **Cách sửa:** Trong `addToOutbox`, đọc tổng outbox của tenant (pending và exception) ngay trong cùng IDB transaction rồi kiểm cap tại đó. Transaction readwrite trên store `outbox` được tuần tự hóa giữa các tab.

### M3. Giá offline khác resolver online khi giỏ có khách đã chọn

- **File:**
  - `apps/web/src/features/pos/pages/pos-page.tsx:74` (`if (partyId === null || offline.offline) return t.retail_price;`)
  - `apps/web/src/features/pos/offline/offline-sale-service.ts:127-131`, `:144` (gửi `unit_price` giá lẻ kèm `customer_party_id`)
- **Kịch bản:** Khách sỉ có bảng giá riêng. Thu ngân chọn khách rồi mất mạng, giỏ chuyển sang giá lẻ mà không có thông báo nào. Server nhận `unit_price` như giá override nên bảng giá của khách bị bỏ qua. Khách có thể bị thu cao hơn, và chứng từ ghi giá khác với chính sách.
- **Cách sửa:** Khi chuyển sang offline mà giỏ có khách, hiển thị cảnh báo kiểu "Giá ngoại tuyến là giá lẻ", hoặc bỏ khách khỏi giỏ. Phương án lâu dài: đưa giá theo khách vào bundle.

### M4. Reload khi API trả 5xx (Traefik 502) không mở được POS offline

- **File:** `apps/web/src/main.tsx:30-32` (`offlineStart` chỉ xử lý `TypeError`)
- **Kịch bản:** Container API chết nhưng Traefik vẫn sống và trả 502/503. Thu ngân reload thì app vào BootError thay vì POS ngoại tuyến. Hành vi này không nhất quán với heartbeat (`heartbeatProbe` coi 5xx là không với tới được). Đây đúng là tình huống phase này cần phục vụ.
- **Cách sửa:** Ở bước host-context, coi 502/503/504 (và lỗi phân tích response khi gặp trang lỗi HTML của proxy) như mất kết nối.

### M5. Request sync không có timeout

- **File:**
  - `apps/web/src/shared/api/http.ts:21`, `:105` (`signal` là tùy chọn)
  - `apps/web/src/features/pos/offline/sync-service.ts:70`, `:81` (không truyền `signal`)
- **Kịch bản:** Một POST bị treo (TCP half-open trên Wi-Fi yếu) giữ `running.current` và Web Lock vô thời hạn. Mọi tab đều không sync được cho tới khi trình duyệt tự hủy kết nối, có thể mất nhiều phút.
- **Cách sửa:** Truyền `AbortSignal.timeout(15_000)` (hoặc giá trị tương đương) vào các request sync. Lỗi abort được coi là `network`.

## Low

- **L1.** Manifest PWA với `start_url: "/sales/pos"` được chèn vào `index.html` trên mọi host, kể cả root và admin (`apps/web/vite.config.ts`). Trên host admin, người dùng vẫn được mời "cài app" và mở tới một route không tồn tại. SW thì đã được chặn đúng. Đề xuất: chỉ inject link manifest trên tenant host, hoặc chấp nhận và ghi chú lại.
- **L2.** `refreshBundle` lưu bundle vào IDB trước khi kiểm tra `cancelled` (`offline-bundle-repository.ts:88-90`; `use-offline-pos.ts:45-46`). Nếu logout trong lúc refresh đang chạy, `wipeSessionData` xóa xong rồi bundle mới vẫn được ghi lại. Ảnh hưởng thấp vì bundle được khóa theo `sessionId` cũ. Đề xuất: truyền tín hiệu hủy vào và kiểm tra trước `saveBundle`.
- **L3.** Sandbox reset đổi tenant id trên cùng origin. `claimTenant` (`offline-db.ts:156`) khiến outbox của tenant cũ bị bỏ rơi trong IDB mà không có thông báo. Chỉ ảnh hưởng sandbox.
- **L4.** `offline-policy-card.tsx:58-61` cho phép bật offline với hạn mức 0. Khi đó mọi sale bị từ chối với "vượt hạn mức", không có gợi ý nào. Đề xuất: validate `amount > 0` khi `enabled`.
- **L5.** `sync-exceptions-callout` và `sync-queue-callout` không có `role`. Hàng ngoại lệ mới xuất hiện sẽ không được trình đọc màn hình thông báo. Đề xuất: dùng `role="alert"` cho ngoại lệ và `role="status"` cho hàng chờ. `offline-banner` và `update-prompt` đã có `role="status"`.
- **L6.** Outbox và banner ở các tab khác bị cũ (`staleTime: Infinity`, không có BroadcastChannel). Số "Chờ đồng bộ" ở tab B không cập nhật sau khi tab A sync. Liên quan trực tiếp tới M2.
- **L7.** E2E `apps/web/e2e/pos-offline.spec.ts` không kiểm số chứng từ HD- có `source=pos_offline` sau sync, trong khi spec yêu cầu kiểm "đúng một chứng từ mỗi sale". Đề xuất: thêm assertion đếm qua API.
- **L8. Lệch spec, cần lead/user chấp nhận:** logout chỉ xóa bundle và boot snapshot, outbox được giữ lại (`offline-db.ts:144`). Spec ghi "wiped on logout". Giữ outbox là lựa chọn an toàn hơn cho dữ liệu tiền, nhưng cần được ghi nhận là một quyết định chính thức.
- **L9. Lệch spec đã nêu trong báo cáo dev:** spec yêu cầu discard phải có lý do và xác thực lại, nhưng API `discard` không nhận lý do. Cần lead quyết định có bổ sung ở phase API hay không.

## Đạt (đã kiểm chứng, không phải finding)

- `sw.ts`: `NavigationRoute` có denylist `/api`, `NetworkOnly` cho `/api` ở mọi method, không cache export hoặc download. `nginx.conf` đặt `location = /sw.js` với `Cache-Control: no-cache`.
- SW chỉ đăng ký ở PROD (`main.tsx:41`). Các dịch vụ store chỉ khởi động khi `context.kind === "tenant"` (`main.tsx:79`).
- IndexedDB khóa bundle theo tenant và device session. Watcher phiên xóa dữ liệu khi gặp 401 hoặc khi `me` bị removed, và gọi `claimTenant` khi đổi tenant.
- Outbox: số OFF tăng nguyên tử trong cùng transaction. Body gửi lại giữ nguyên. `posted` và `replayed` thì xóa entry, `exception` thì đánh dấu (`sync-service.ts:93-102`). 401 dừng sync và giữ outbox. Mất response được xử lý bằng `replayed` (có test).
- Chỉ nhận tiền mặt, thu đủ (`offline-sale-service.ts:123`). Cap kiểm cả theo từng giao dịch lẫn cộng dồn ở client (`:125`, xem thêm M2). Hạn giờ: `expires = min(offline_until, now + max_hours)`.
- Tiền tính bằng `dec`/`Money`, không dùng float.
- Khóa giữa các tab bằng Web Lock `ifAvailable`. Backoff 1s đến 60s có jitter (nhưng bị H1 vô hiệu hóa).
- Chuỗi tiếng Việt khớp nguyên văn spec: banner, toast lưu offline, "Chưa lưu.", callout, lỗi hạn mức, update prompt, toast đồng bộ xong.
- CSRF bị bỏ khỏi snapshot vẫn được khôi phục qua `refreshCsrf` khi gặp 403 `csrf_failed`.

## Chất lượng test

- Unit test bao phủ tốt: outbox, connectivity, sync-service (posted, replayed, exception, failed, 401), uuidv7, sale service.
- Thiếu test cho:
  - Kịch bản C1 (checkout online lỗi mạng rồi bán offline cùng giỏ).
  - Vòng lặp H1 (5xx trong khi `/me` 200).
  - Thuế (H2).
  - Cap khi mở nhiều tab (M2).
- E2E chưa chạy trong lần review này.

## Đề xuất ưu tiên

1. Sửa C1. Chặn release nếu chưa sửa.
2. Sửa H1: tách 5xx khỏi `reportFailure`, cho effect tôn trọng backoff, không probe ngay.
3. Sửa H2: tối thiểu chặn bán offline khi `prices_include_tax = false`.
4. M1 và M2 (cap kiểm trong transaction IDB, entry bị blocked), sau đó M4 và M5.
5. Lead quyết định L8 và L9.

## Câu hỏi mở

- Khuyến mãi tự áp, hoặc `rounding_mode = 'document'`, có làm `built.Totals.Total` khác tổng các `unit_price` client gửi lên không? Nếu có thì đây là một nguồn park hàng loạt khác, cùng loại với H2.
- Server có yêu cầu xác thực lại khi PATCH `/tenant/settings` cho chính sách offline không? Client hiện không yêu cầu.

---

Status: DONE_WITH_CONCERNS
Summary: Lõi offline của phase 19 vững (outbox nguyên tử, xử lý replay, cách ly dữ liệu, SW đúng phạm vi, 109 test pass, typecheck sạch ngoài phần inventory). Còn 1 Critical: ghi sổ trùng khi checkout online mất response rồi bán lại offline. Còn 2 High: vòng lặp request không backoff khi gặp 5xx; bỏ qua thuế khi giá chưa gồm thuế.
Concerns/Blockers: Chưa chạy bundle production (hook chặn) và Playwright e2e. L8 (logout giữ outbox) và L9 (discard không có lý do) là lệch spec, cần lead/user quyết định.
