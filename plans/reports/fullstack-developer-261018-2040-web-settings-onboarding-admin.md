# Báo cáo triển khai — Web: Cài đặt, Khởi tạo cửa hàng, Bảng quản trị nền tảng

- Kế hoạch: `plans/261016-0854-tally-full-product-implementation/`
- Phase: phase-18 (phần B: settings, onboarding, admin, sandbox reset, error codes, tests)
- Ngày: 2026-10-18
- Trạng thái: hoàn thành, còn lưu ý (DONE_WITH_CONCERNS)

## Kết quả cổng kiểm tra (`apps/web`)

- `corepack pnpm typecheck`: sạch.
- `corepack pnpm lint`: sạch.
- `corepack pnpm test`: sau lượt sửa theo review (gồm L4) là 130 files / 908 tests, xanh hai lần liên tiếp (xem mục "Sửa theo review"). Trước đó là 128 files / 885 tests, xanh hai lần (lần chạy gần nhất sau khi thêm gợi ý ô tìm audit) sau lượt spec store mới (Idempotency-Key và re-auth cho `subscription:change`, mua add-on, tìm audit log phía server) và spec admin mới (tên cửa hàng trong cờ, lịch sử cờ, phiên hỗ trợ; `GET /incidents?status=open` cho banner).
- Trong lượt này có một lần chạy `pnpm test` báo lỗi trước khi sửa xong phần admin; lần đó không giữ được output nên không biết test nào. Mọi lần chạy sau đó đều xanh. Nếu lead thấy một test chập chờn thì báo tôi tên test.
- Lỗi typecheck "4 lỗi ở `features/settings/testing/fixtures.ts`" không tái hiện được: `tsc -b` và `tsc -b --force` đều sạch cả trước lẫn sau khi sửa. Fixture đó nay lấy kiểu `Plan`, `SubscriptionView` từ shared.
- E2E `settings.spec.ts`, `onboarding.spec.ts`, `admin.spec.ts` mới chỉ viết, chưa chạy vì theo chỉ thị không chạy dev server.
- Không chạy bundle, không commit, không sửa `apps/api` hay `schema.d.ts`.

## Theo từng màn hình

### `/settings/account`

Các file nằm trong `features/settings/components/*`, trang `pages/account-page.tsx`.

- **Chủ cửa hàng:** tên, email và trạng thái (`OWNER_STATUS`); "Đổi mật khẩu" (`POST /auth/password`) qua re-auth.
- **2FA TOTP** (`mfa-setup-dialog`):
  - Luồng là enroll, rồi hiện QR/secret, rồi xác nhận mã, rồi hiện "Mã khôi phục".
  - Bật hoặc tắt đều phải re-auth qua `useCommit`.
- **Thiết bị đang đăng nhập:** liệt kê phiên; đăng xuất từng thiết bị (`DELETE /sessions/{id}`) hoặc "Đăng xuất các thiết bị khác" (`POST /sessions:revoke-all`, có hộp xác nhận và re-auth).
- **Thông tin cửa hàng:** PATCH hồ sơ cửa hàng.
- **Khóa sổ kỳ:**
  - Khóa tiến được ngay.
  - Mở lại cần re-auth và một exception `period_reopen`. Exception này gắn `doc_type "period_lock"`, `doc_id` là tenant id, `doc_version` là `version`.
  - Lần gọi đầu không gửi id, backend trả 409, UI hiện hộp xác nhận rồi gọi lại.
- **Chuyển quyền chủ cửa hàng:** gửi lời mời (re-auth), qua `POST /owner-transfer`. Gửi thành công thì card hiện "Đã gửi lời mời chuyển quyền tới … ". Câu này chỉ giữ trong state của màn hình, không phải trạng thái đọc từ server. Phạm vi này đúng với ORG-07 và phase 18: xem và hủy lời mời không có trong yêu cầu. Bản trước của báo cáo ghi sai rằng đã có hai việc đó; lead sẽ đưa chúng vào follow-up.
- **Xuất toàn bộ dữ liệu:** tạo job (có Idempotency-Key theo intent), theo dõi và tải về. Job id chỉ giữ trong state (xem API gap).
- **Đóng tài khoản:**
  - Có trang riêng `/settings/account-closure` (`pages/account-closure-page.tsx`).
  - Trang đi hết vòng đời: yêu cầu, chờ, hủy yêu cầu, tải bản xuất cuối.
- **Giữ nguyên:** `OfflinePolicyCard` và 7 test cũ của nó.

### `/settings/subscription`

- **Thẻ gói:**
  - Thẻ có nhãn "Đang dùng" hoặc "Gói nền". Lưới gói chính không còn hiện add-on và gói dùng thử.
  - Đổi gói qua `plan-change-dialog`.
  - Mọi số tiền đi qua `shared/lib/decimal`.
- **`POST /subscription:change`:**
  - Request mang Idempotency-Key của intent qua `useCommit` (`client.post(..., { idempotencyKey })`).
  - Khi API trả 403 `reauth_required`, `useCommit` hỏi mật khẩu (`/auth/reauth`) rồi gửi lại cùng key, cùng body.
  - Toast tùy theo hóa đơn trả về: hóa đơn `open` thì nhắc thanh toán; hóa đơn `paid` (miễn phí) thì báo đã đổi gói hoặc đã bật add-on.
  - Câu tiếng Việt cho `idempotency_key_reused`, `idempotency_key_in_progress`, `idempotency_key_invalid` đã có sẵn trong `api-error.ts`, không phải thêm.
- **Gói bổ sung (add-on):**
  - Nhóm riêng "Gói bổ sung" dưới lưới gói, lấy các gói có `kind: "addon"` từ `GET /plans`.
  - Mỗi thẻ có tên, mô tả module và "Giá: …". Add-on chưa dùng có nút "Mua add-on". Nút mở cùng dialog báo giá và điều khoản (`preview-change`), rồi gọi `subscription:change` với `plan_id` của add-on.
  - Add-on đang dùng có nhãn "Đang dùng" và "Hiệu lực {từ} – {đến}", không có nút mua. Add-on đã ngừng bán vẫn hiện khi cửa hàng còn đang dùng.
  - Add-on đang dùng đọc từ `addons` của `GET /subscription` (theo `plan_id`). Server đã chọn add-on theo ngày làm việc của cửa hàng, nên web không lọc lại và không còn suy từ hóa đơn. Add-on ngừng bán mà vẫn có trong `addons` hiện bằng `plan_name` kèm module, không có giá và nút mua.
  - `priceText` chuyển từ `current-subscription-card.tsx` sang `features/settings/labels.ts`, vì lint `react-refresh/only-export-components` không cho file component export hàm.
- **Thuê bao hiện tại:** trạng thái, kỳ hạn, các module được dùng; kèm "Hóa đơn thuê bao".
- **Truy cập hỗ trợ:** cấp quyền (có thời hạn, có phạm vi) và thu hồi.
- **Yêu cầu hỗ trợ:** tạo ticket, xem chi tiết, trả lời, đóng.

### `/settings/audit-log`

- Có các cột theo spec.
- Phân trang bằng cursor, mỗi trang 200 dòng ("Xem thêm").
- Ô tìm kiếm gửi `q` lên `GET /audit-events` sau khi ngừng gõ 250 ms. `q` nằm trong query key, nên đổi chữ tìm là đọc lại từ trang đầu (cursor reset), và trang sau vẫn giữ `q`.
- Dưới ô tìm có dòng gợi ý "Tìm theo số chứng từ, mã thao tác hoặc tên người." (lead chấp nhận giới hạn: `q` không khớp nhãn tiếng Việt của thao tác).
- Đã bỏ bộ lọc phía client và câu "Tìm trong {n} dòng đã tải; bấm Xem thêm để tìm tiếp.". Không có dòng nào khớp thì hiện "Không có dòng nào khớp.".

### Onboarding (`features/onboarding`)

- Thân bài cho đủ 7 bước, mỗi bước một file `steps/*-step.tsx`: `store_profile`, `catalog_setup`, `opening_stock`, `opening_debt`, `opening_cash`, `first_sale`, `confirm_opening`.
- Chỉ `catalog_setup` và `first_sale` được bỏ qua. Các bước số dư đầu kỳ bị chặn sau `opening-gate` cho tới khi có ngày chốt.
- Danh sách bước đọc từ `useOnboarding()` dùng chung (`shared/api/platform-queries.ts`); feature không còn schema hay query checklist riêng.
- Mutation invalidate `platformKeys.onboarding()`.

### Bảng quản trị (`/admin/*`)

- **Client và phiên:** client riêng (`schema-admin.d.ts`, `gen:api:admin`) và phiên riêng (`features/admin/api/session.ts`).
  - Phiên admin dò bằng `GET /me` (`adminSessionQuery`/`useAdminSession` trong `shared/api/platform-queries.ts`, key `adminKeys.me()`, `adminMeSchema` = `{admin: {id, full_name, email}, csrf_token, expires_at}`). 401 nghĩa là chưa đăng nhập; invalidate khi 401 vẫn nhắm `adminKeys.me()`. Probe `/plans` đã bỏ.
  - `GET /me` (`adminGetMe`) đã có trong `openapi-admin.yaml`; đã chạy `corepack pnpm gen:api:admin` để sinh lại `shared/api/schema-admin.d.ts`. `adminMeSchema` nay `satisfies` kiểu 200 của `adminGetMe`, nên spec đổi là typecheck báo ngay.
  - `csrf_token` trong câu trả lời được đặt làm CSRF cho lệnh ghi admin; test công bố sự cố khẳng định `X-CSRF-Token` là token `/me` trả về. GET `/me` không gửi CSRF. MSW trả kèm `Cache-Control: no-store`; 401 `unauthenticated` đưa về `/admin/login` (test trong `app/guards/guards.test.tsx`).
- **Header:** khác hẳn cửa hàng (nền `--surface-dark`). Nav gồm Cửa hàng, Mức sử dụng, Hóa đơn thuê bao, Sự cố, Cờ tính năng, Hỗ trợ, Đối tác giới thiệu; tên admin (email khi rê chuột) và nút "Đăng xuất".
- **Phân công ticket:** chọn người nhận trong danh bạ `GET /admins` (chỉ admin `active`, trừ người đang được giao), hoặc "Giao cho tôi", hoặc "Bỏ phân công"; cả ba gửi `PATCH /support-tickets/:id`. Cột và ô người được giao hiện tên admin, "{tên} (bạn)" cho admin đang đăng nhập, "{tên} (ngừng hoạt động)" cho admin đã nghỉ (`components/assignee.tsx`). Ô nhập UUID đã bỏ.
- **Đọc lỗi không còn giả làm danh sách rỗng:** trước đây khi GET lỗi, các trang admin vừa hiện lỗi vừa hiện câu rỗng ("Không có yêu cầu khớp bộ lọc.", "Không có sự cố đang mở; cửa hàng không thấy thông báo nào.", "Chưa có cửa hàng nào cấp phiên…"). Nay bảng chỉ vẽ khi có dữ liệu (tenants, invoices, feature-flags, lịch sử cờ, cờ của cửa hàng, incidents, support, partners, referrals, support-view).
- **Trang:** tenants, tenant chi tiết, usage, invoices, incidents, feature-flags, support, ticket, support-view, partners. `/admin` chuyển về `/admin/tenants`.
- **Phân trang cursor:** tenants, saas-invoices, incidents, partners, support-tickets, usage đọc `?cursor=&limit=50` qua `useCursorList`, nút "Xem thêm" nạp trang sau (`components/paged-rows.tsx` bọc trạng thái tải, lỗi, chân danh sách). Bỏ giới hạn 500 dòng và các câu dẫn "Tối đa 500…".
- **Chọn cửa hàng:** bộ lọc support, invoices, dialog lập hóa đơn và phạm vi đặt cờ dùng `components/tenant-picker.tsx` (Combobox tìm `GET /tenants?q=&limit=50` theo chữ gõ). Dialog lập hóa đơn ẩn cửa hàng sandbox.
- **Cờ tính năng:** ô mã cờ là danh sách `known_keys`; bảng có dòng cho mọi khóa trong `known_keys` kể cả chưa đặt. Ở cờ của một cửa hàng có nút "Gỡ giá trị riêng" (`DELETE /tenants/{id}/feature-flags/{key}`, 204, gõ mã cờ để xác nhận); 404 `flag_override_not_found` giữ dialog và hiện câu tiếng Việt, đồng thời tải lại danh sách cờ. Lịch sử cờ hiện `admin_full_name` (cột "Người đổi") và "Đã gỡ giá trị riêng" khi `enabled` là null.
- **Tên cửa hàng trên cờ và phiên hỗ trợ:** spec admin mới thêm `tenant_slug`/`tenant_name` vào `FeatureFlag`, `FeatureFlagChange`, `SupportSession`. Schema admin đã thêm các field này (typecheck báo ngay qua `satisfies`). Bảng cờ và lịch sử cờ hiện slug lấy thẳng từ dòng; phiên hỗ trợ hiện tên và slug từ dòng. Đã bỏ `useTenantRefs`, tức không còn đọc `GET /tenants/{id}` cho từng cửa hàng.
- **Banner sự cố:** thẻ "Thông báo đang hiện cho cửa hàng" đọc riêng `GET /incidents?status=open` (có "Xem thêm" nếu quá một trang), không còn lọc trên các trang đã tải của danh sách. Đã bỏ câu "Không có sự cố đang mở trong các sự cố đã tải; xem thêm để kiểm tra các sự cố cũ hơn.". Đọc lỗi thì thẻ hiện "Không tải được sự cố đang mở: …" thay cho câu rỗng. `useIncidents(status)` nhận `Filter<IncidentStatus | "open">`; lệnh ghi vẫn invalidate cả hai danh sách qua tiền tố `consoleKeys.incidents()`.
- **Usage:** tên và slug lấy thẳng từ dòng usage; không còn đọc danh sách cửa hàng để ghép tên. Khoảng mặc định 30 ngày vẫn tính theo đồng hồ máy (admin `/me` không có `server_time`), giữ nguyên theo quyết định của lead.
- **Schema admin:** mọi object schema trong `features/admin/api/schemas.ts` liệt kê đủ field và `satisfies` kiểu tương ứng trong `schema-admin.d.ts`, nên spec đổi là typecheck báo.
- **Hành động phá hủy:** đều qua `typed-confirm-dialog`, phải gõ đúng slug, số hóa đơn hoặc mã cờ. Áp dụng cho tạm ngưng tenant, hủy hóa đơn, tắt cờ, gỡ giá trị riêng của cờ, đóng sự cố.
- **Đăng nhập admin bị 429:** hiện "Thao tác quá nhanh. Thử lại sau N phút." Có test trong `features/auth/pages/admin-login-page.test.tsx`.

### Sandbox reset

- Đã có sẵn trong `app/layouts/util-bar.tsx` (`SandboxResetDialog`, `POST /tenant/sandbox/reset`, invalidate toàn bộ).
- Đã có test "resets the sandbox store through the API after confirmation" trong `app/layouts/app-shell.test.tsx`, nên không cần sửa gì.

### Mã lỗi tiếng Việt (`shared/api/api-error.ts`)

- Thêm bản dịch cho khoảng 45 mã:
  - đóng tài khoản và bảo trì
  - thuê bao và hóa đơn
  - hỗ trợ
  - đăng nhập và MFA
  - sandbox
  - khóa sổ và exception
  - xuất dữ liệu
  - onboarding và số dư đầu kỳ
- Thêm các field reason: `not_after_current`, `not_before_current`, `immutable`, `range`, `unique`, `addon`.
- Test mới trong `api-error.test.ts`: `it.each` qua 11 mã, thời gian chờ của 429, và một field reason.

## Nhãn mới (không có trong prototype, cần duyệt)

- **Cài đặt** (`features/settings/labels.ts` và hằng trong component):
  - Thẻ: "Thông tin cửa hàng", "Khóa sổ kỳ", "Chuyển quyền chủ cửa hàng", "Xuất toàn bộ dữ liệu", "Đóng tài khoản", "Mã khôi phục", "Hóa đơn thuê bao".
  - Nhãn trạng thái chủ, thuê bao, hóa đơn và ticket (ví dụ "Chờ xác thực email", "Quá hạn, đang ân hạn", "Chờ cửa hàng trả lời").
  - Tên module (ví dụ "Hỏi đáp AI", "Báo cáo nâng cao").
- **Onboarding:** "Thiết lập cửa hàng", tiêu đề và mô tả của 7 bước, thông điệp của cổng ngày chốt.
- **Admin** (`features/admin/lib/labels.ts`, `app/layouts/admin-shell.tsx`):
  - "Quản trị nền tảng" và toàn bộ nav.
  - Thẻ: "Chi tiết cửa hàng", "Mức sử dụng", "Công bố sự cố", "Lập hóa đơn thuê bao", "Phân công", "Trao đổi", "Trả lời", "Xử lý", "Xem dữ liệu cửa hàng", "Thêm đối tác giới thiệu".
  - Câu nhắc gõ xác nhận.
  - Lượt trước: nút "Giao cho tôi", hậu tố "(bạn)" sau tên admin, câu "Chưa phân công".
  - Lượt nối spec admin mới:
    - Chọn cửa hàng: "Gõ tên hoặc địa chỉ cửa hàng để tìm…", lựa chọn "Mọi cửa hàng" và "Toàn hệ thống" (phạm vi cờ).
    - Phân công: "Người nhận việc", "Chọn quản trị viên", "Chọn người nhận việc.", hậu tố "(ngừng hoạt động)".
    - Cờ: "Chọn mã cờ", "Chọn mã cờ.", nút và tiêu đề "Gỡ giá trị riêng", câu xác nhận "{slug} sẽ theo giá trị chung của cờ này: Bật/Tắt.", toast "Đã gỡ giá trị riêng của cờ {key} cho {slug}.", giá trị lịch sử "Đã gỡ giá trị riêng", cột "Người đổi".
    - Mã lỗi `flag_override_not_found`: "Cửa hàng không còn giá trị riêng của cờ này, có thể đã được gỡ trước đó."
    - Sự cố, khi đọc sự cố đang mở lỗi: "Không tải được sự cố đang mở: …" (câu "…trong các sự cố đã tải…" đã bỏ).
    - Đã bỏ: các câu dẫn "Tối đa 500…" ở đầu trang danh sách.
- **Lượt spec store mới** (`features/settings`):
  - Nhóm add-on: tiêu đề "Gói bổ sung" (trùng tên loại hóa đơn `addon` đã có), nút "Mua add-on" (aria-label "Mua add-on {tên}"), dòng "Giá: {giá}", dòng "Hiệu lực {từ} – {đến}".
  - Dialog add-on: tiêu đề "Mua add-on {tên}", câu phụ "Giá và kỳ do máy chủ tính; add-on được bật khi hóa đơn được thanh toán.", nút gửi "Mua add-on".
  - Toast: "Đã tạo hóa đơn {số}. Thanh toán để bật add-on.", "Đã bật add-on. Hóa đơn {số} miễn phí nên đã được thanh toán.", "Đã đổi gói. Hóa đơn {số} miễn phí nên đã được thanh toán."
- Các nhãn đã có trong spec được giữ nguyên văn: "Chủ cửa hàng", "Thiết bị đang đăng nhập", "Đang dùng", "Gói nền", "Thuê bao hiện tại", "Truy cập hỗ trợ", "Yêu cầu hỗ trợ".

## API gap

- **Cài đặt:**
  - Không có gói "Nhiều điểm" trong danh mục gói.
  - `q` của audit log tìm trong số chứng từ, mã hành động, mã loại đối tượng và tên người làm, không tìm trong nhãn tiếng Việt mà màn hình hiện. Lead chấp nhận giới hạn này; màn hình ghi gợi ý "Tìm theo số chứng từ, mã thao tác hoặc tên người.".
  - Không có endpoint liệt kê các job xuất toàn bộ dữ liệu, nên job id mất khi tải lại trang.
  - Không có số liệu sử dụng (counter) phía cửa hàng.
  - Ngoài `subscription:change` và `POST /data-exports` (thêm ở việc 19), không endpoint settings nào khai báo Idempotency-Key.
  - Mỗi mục nhạy cảm tự xin re-auth; chưa có cơ chế phiên re-auth dùng chung.
  - Chưa có trang nhận chuyển quyền chủ trong `features/auth`.
  - `exportJobSchema` của reports không nhận `report_key` rỗng, nên settings có schema riêng.
- **Onboarding:**
  - `fundSchema` của debt thiếu `version` và các trường đầu kỳ, nên onboarding có schema riêng.
- **Admin:**
  - API admin không khai báo Idempotency-Key cho lệnh ghi nào, và backend admin không có idempotency. Lead đã quyết giữ nguyên spec: lệnh ghi admin **không** gửi Idempotency-Key; test ghi thành công khẳng định header vắng mặt.
  - Ô chọn cửa hàng chỉ hiện 50 kết quả đầu của `GET /tenants?q=`; muốn thấy cửa hàng khác thì gõ thêm.
  - Danh bạ `GET /admins` được đọc hết các trang (limit 200) để đặt tên người được giao; ổn với danh bạ nhỏ.
  - Support view trả JSON thô.

## Chưa làm và lý do

- **Admin, cố ý để ngoài:**
  - tạo hoặc sửa gói;
  - chuyển thuê bao sang grace hoặc closed từ UI;
  - gửi lại link tải khi đóng tài khoản;
  - trang feedback.
  - Trang incidents chỉ hiện bản xem trước banner, vì app cửa hàng chưa có banner trạng thái tenant.
- **E2E chưa chạy:**
  - `settings.spec.ts`: test khóa sổ ghi sổ đúng vào ngày đã khóa vì UI không có trường lùi ngày.
  - `onboarding.spec.ts`: `@root`, serial.
  - `admin.spec.ts`: `@admin`, cần `E2E_ADMIN_EMAIL`, `E2E_ADMIN_PASSWORD`, `E2E_ADMIN_TOTP_SECRET`, `E2E_ADMIN_TENANT_SLUG`.

## Sửa hợp đồng shared (theo quyết định "cho sửa endpoints")

Một nguồn hợp đồng duy nhất; feature không còn schema/query trùng.

- `shared/api/endpoints.ts`:
  - Onboarding: `step.status` = `pending|completed|skipped`, thêm `skippable`, `completed_at`, `skipped_at`, `totals`; checklist có `done`, `total`, `complete`, `locked_through`, `totals`.
  - Subscription: `subscriptionViewSchema` = `{subscription, plan, entitlements}`; thêm `planSchema`, `PLAN_MODULES`, `SUBSCRIPTION_STATUSES`.
  - Entitlements: `{status, modules: map | null, limits: map | null, can_post, can_export}`; helper `hasModule()` (`modules === null` mở mọi module). Bỏ `READ_ONLY_STATUSES`.
  - `adminMeSchema` theo shape `/me` ở trên.
- `shared/api/platform-queries.ts`: `subscriptionQuery` dùng `subscriptionViewSchema`; `adminSessionQuery`/`useAdminSession` dùng chung.
- `shared/hooks/use-read-only.ts`: chỉ sửa chú thích.
- `app/providers/tenant-provider.tsx`: chỉ-xem = `entitlements.can_post === false` (theo spec: grace vẫn ghi sổ; read_only và closed thì không).
- `app/guards/onboarding-steps.ts`: chặn khi có bước `!skippable && status === "pending"`.
- `app/guards/require-entitlement.tsx`: dùng `hasModule()`.
- `app/guards/require-admin-auth.tsx`, `app/layouts/admin-shell.tsx`: phiên admin từ shared, hiện tên admin.
- `app/guards/guards.test.tsx`: test theo shape mới, thêm ca `modules: null`, ca grace không có banner, ca read_only/closed có banner.
- `shared/api/schema-admin.d.ts`: sinh lại bằng `gen:api:admin` (không sửa tay).
- `test/msw/handlers.ts`: `TEST_ENTITLEMENTS`, `TEST_SUBSCRIPTION`, `testOnboarding()`, `TEST_ADMIN_ME`, handler admin `GET /me`, helper `withEntitlements`/`withSubscription`/`withOnboarding`.

## Test admin mới (vitest + MSW)

- `features/admin/pages/usage-page.test.tsx`: hiển thị (tên và slug từ dòng usage, không đọc `/tenants`, khoảng 30 ngày gửi lên), lỗi.
- `tenants-page.test.tsx`: thêm "Xem thêm" nạp trang sau bằng `cursor`, mỗi trang `limit=50`, nút biến mất ở trang cuối.
- `invoices-page.test.tsx`: thêm lọc theo cửa hàng chọn trong picker (gửi `tenant_id`) và dialog lập hóa đơn ẩn sandbox, body `{tenant_id, plan_id}`.
- `feature-flags-page.test.tsx`: tắt cờ chung, lịch sử hiện tên người đổi và "Đã gỡ giá trị riêng"; khóa trong `known_keys` có dòng, dialog đặt cờ chọn mã cờ từ `known_keys` và cửa hàng trong picker.
- `tenant-page.test.tsx`: gỡ giá trị riêng (gõ mã cờ, DELETE 204, không Idempotency-Key, toast, bảng về giá trị chung), 404 `flag_override_not_found` giữ dialog và hiện câu tiếng Việt.
- `incidents-page.test.tsx`: banner gửi `status=open` cùng lúc với danh sách không lọc; banner hiện sự cố đang mở dù trang đầu của danh sách chỉ có sự cố đã khắc phục; câu "Không có sự cố đang mở…" khi API không trả sự cố nào; hiển thị banner, lỗi (không hiện câu rỗng), công bố thành công (toast chỉ sau 2xx, không có Idempotency-Key), cập nhật bị từ chối (dialog giữ, "Chưa ghi sổ.").
- `support-page.test.tsx`: hiển thị (người được giao theo danh bạ, gồm "(bạn)" và "(ngừng hoạt động)"; phiên hỗ trợ có tên cửa hàng lấy từ dòng, không đọc `/tenants/{id}`), bộ lọc gửi `status`/`tenant_id`/`limit` với cửa hàng tìm theo slug, lỗi.
- `support-ticket-page.test.tsx`: hiển thị, "(bạn)", lỗi, chọn người nhận trong danh bạ (bỏ trống bị chặn, admin ngừng hoạt động không có trong danh sách, PATCH `{assigned_admin_id}`), "Giao cho tôi" thành công (toast chỉ sau 2xx, không Idempotency-Key), gửi trả lời thành công, trả lời bị `ticket_closed`.
- `partners-page.test.tsx`: hiển thị và mở danh sách giới thiệu, lỗi, thêm đối tác thành công (mã viết hoa, "12,5" thành "12.5", toast sau 2xx), `referral_code_taken` (báo tại ô và toast).
- `console-shell.test.tsx`: thêm ca tên admin trên header.
- "Toast chỉ sau 2xx" được kiểm bằng cách giữ câu trả lời của MSW (`heldAnswer()` trong `testing/fixtures.ts`), khẳng định chưa có toast, rồi mới thả.

## Test settings của lượt spec store mới

- `features/settings/pages/subscription-page.test.tsx`:
  - Đổi gói: request mang header `Idempotency-Key` khác rỗng.
  - 403 `reauth_required`: nhập mật khẩu, request thứ hai cùng body và cùng key, nhận 201, toast "Đã tạo hóa đơn…".
  - Nhóm "Gói bổ sung" có nút "Mua add-on Hỏi đáp AI" và "Giá: 49.000 ₫ / tháng"; lưới gói chính không có add-on.
  - Mua add-on miễn phí: preview gửi `{plan_id}`, dialog "Mua add-on Hỏi đáp AI" hiện loại hóa đơn "Gói bổ sung", nút gửi bị khóa đến khi tích điều khoản, change gửi `{plan_id, accept_terms: true}` kèm key, toast "Đã bật add-on…", rồi thẻ add-on hiện "Đang dùng" và "Hiệu lực 12/10/2026 – 11/11/2026", hết nút mua.
  - Mua add-on có phí: toast "Đã tạo hóa đơn HD-2026-0008. Thanh toán để bật add-on.".
  - Add-on có trong `addons` hiện "Đang dùng" và kỳ của nó, kể cả add-on `GET /plans` không trả (ngừng bán). Add-on có hóa đơn đã trả nhưng không có trong `addons` vẫn hiện nút mua.
- `features/settings/pages/audit-log-page.test.tsx`: có dòng gợi ý nguyên văn dưới ô tìm; chữ gõ (đã cắt khoảng trắng) đi thành `q`, bảng chỉ hiện dòng API trả về, "Không có dòng nào khớp." khi rỗng; "Xem thêm" của một lần tìm gửi `{q, cursor}`.

## Sửa ngoài thư mục feature (ghi chú theo yêu cầu)

- `features/debt/index.ts`: thêm một dòng `export { customerSource } from "./api/queries";` để onboarding dùng.
- `features/auth/pages/admin-login-page.test.tsx`: thêm test 429.
- `app/**`:
  - `router.tsx` (route account-closure và các trang admin)
  - `route-elements.tsx` (bỏ `AdminHome`)
  - `router.test.tsx`
  - `layouts/admin-shell.tsx`
  - `guards/require-admin-auth.tsx`
  - `guards/guards.test.tsx`
- `package.json`: chỉ thêm script `gen:api:admin`.

## Lượt sửa theo quyết định của lead (lý do giải phóng, ngày làm việc, GET /tenant, admin `/me`)

- **Giải phóng hàng giữ cần lý do** (`POST /reservations/{id}:release {reason}`):
  - `features/inventory/containers/release-reservation-dialog.tsx`: ô "Lý do" bắt buộc, cắt khoảng trắng, tối đa `RELEASE_REASON_MAX` = 500 ký tự (`maxLength` và kiểm trước khi gửi).
  - `features/inventory/api/mutations.ts`: gửi `{reason}` thay cho `{}`; body có kiểu `satisfies ReleaseBody` lấy từ spec. Không gửi `qty`, nên cả phần giữ được giải phóng như trước.
  - `features/inventory/api/adapters.ts`: `RELEASE_REASON_MAX`, câu "Lý do tối đa 500 ký tự." (nhãn mới); "Cần lý do." đã có.
  - `shared/hooks/use-commit.ts`: `setFieldError(field, message, reason)` nay nhận thêm mã reason của API, để dialog tự diễn đạt `required`/`too_long`. Chỗ gọi cũ chỉ dùng hai tham số đầu nên không đổi.
  - Test `features/inventory/pages/reservations-page.test.tsx`: gửi thành công với body `{reason: "Khách hủy"}`, chặn khi trống, và 422 `fields.reason` = `required`/`too_long` giữ dialog và hiện đúng câu.
- **Ngày làm việc lấy từ `GET /me` `server_time`**, không từ `business_date` (không có trong spec `Tenant`):
  - `shared/api/endpoints.ts`: `meSchema` thêm `server_time` (RFC 3339 có offset); bỏ `business_date` khỏi `tenantSchema`.
  - `shared/lib/dates.ts`: `todayBusinessDate(now, timeZone)` và `businessDateOf(instant, timeZone)` nhận múi giờ (mặc định `Asia/Ho_Chi_Minh`), thêm `isTimeZone()`.
  - `shared/api/platform-queries.ts`: `useServerBusinessDate()` trả ngày của `server_time` theo `timezone` của GET /tenant (múi giờ lạ thì về `Asia/Ho_Chi_Minh`), `null` khi `/me` chưa tải; `useBusinessDate()` chỉ rơi về đồng hồ máy khi `/me` chưa tải.
  - Đã thay ở: util bar (`app/layouts/app-shell.tsx`), procurement (orders, new-po), debt (party-drawer, statements, debt-note, schedule, receivables, payables), campaign seasons, reports filter, dashboard, crm customers. Chỗ trước đây chờ tenant (`?? null`, `enabled`) nay chờ `/me`.
  - Cũng thay những chỗ màn cửa hàng đọc thẳng đồng hồ máy: thẻ kho, báo giá (mặc định và kiểm "không trước hôm nay"), ngày hóa đơn mới, kiểm ngày chốt ở bước hồ sơ onboarding, toast phiên bản kit. Ngày hóa đơn mới nay cố định theo intent (`InvoiceProgress.date`), nên lần gửi lại cùng key luôn cùng body kể cả khi ngày đổi giữa chừng. Còn giữ đồng hồ máy: `admin/usage-page` (admin `/me` không có `server_time`) và giá trị mặc định của `shared/ui/date-picker` khi không truyền `today`.
  - Test: `app/layouts/app-shell.test.tsx` thêm ba ca, gồm máy đã sang 13/10 mà server còn 12/10 (`vi.setSystemTime`), `server_time` dạng UTC đọc theo múi giờ cửa hàng, và cửa hàng đặt `Asia/Tokyo`. Test mới `shared/lib/dates.test.ts`.
  - Fixture: `TEST_ME.server_time = "2026-10-12T09:00:00+07:00"`. `features/sales/api/create-documents.test.tsx` trước đây đặt `invoice_date` theo ngày máy (18/10), chỉ xanh đúng hôm nay; nay theo ngày làm việc của cửa hàng test (12/10).
- **Một schema GET /tenant dùng chung:** `tenantSchema` trong `shared/api/endpoints.ts` theo đủ `Tenant` của spec và `satisfies components["schemas"]["Tenant"]`. Đã bỏ `storeProfileSchema`/`StoreProfile` (settings) và `tenantProfileSchema`/`TenantProfile` (onboarding); shell, `/settings/account` và bước hồ sơ onboarding cùng đọc và PATCH bằng schema này. `TEST_TENANT` nay là một `Tenant` đầy đủ; fixture settings và onboarding dựng từ nó.
- **Admin `/me`:** đối chiếu lại với `schema-admin.d.ts` mới: `adminMeSchema` khớp `{admin: AdminRef{id, full_name, email}, csrf_token, expires_at}` (typecheck kiểm qua `satisfies`), `TEST_ADMIN_ME` có kiểu `AdminMe`, handler MSW trả `Cache-Control: no-store`, 401 `unauthenticated` ở `signedOut`. Không phải sửa gì.

## Sửa theo review

Review: `plans/reports/code-reviewer-261018-2300-web-settings-onboarding-admin-review.md`. Đã xử lý H1, M1, M2, M3 và L1–L9. L4 làm sau cùng, khi spec đã có Idempotency-Key cho `POST /data-exports` (việc 19).

### Cổng kiểm tra sau lượt sửa (`apps/web`)

- `corepack pnpm typecheck`: sạch. `corepack pnpm lint`: sạch.
- `corepack pnpm test`: 130 files / 906 tests, xanh hai lần liên tiếp.
- Output nằm trong scratchpad của phiên: `gate-review-static.txt`, `gate-review-run1.txt`, `gate-review-run2.txt`.

### High và Medium

- **H1, gate onboarding:**
  - `app/guards/onboarding-gate.tsx` để `/catalog` và các trang con mở khi bước bắt buộc chưa xong; sản phẩm, nhà cung cấp và bảng giá đều nằm ở đó.
  - App không có route `/partners`: nhà cung cấp sửa ở `/catalog/suppliers`, nên không cần mở thêm route nào.
  - Bước công nợ đầu kỳ không còn đưa chủ cửa hàng sang `/crm`. Nút "+ Khách hàng mới" mở dialog tạo nhanh ngay trong bước, và khách mới được chọn sẵn.
  - Dialog là `CustomerQuickDialog` cùng `useQuickCustomer`, viết mới trong `features/crm` (`containers/customer-quick-dialog.tsx`, `api/mutations.ts`, export qua `index.ts`). Đây là sửa ngoài thư mục onboarding.
  - Test: `guards.test.tsx` kiểm `/catalog` mở, `/sales` vẫn bị chuyển; `opening-balances.test.tsx` kiểm tạo nhanh khách rồi ghi nợ đầu kỳ.
- **M1, chức năng báo cáo ghi mà UI chưa có:**
  - Đã làm "Đổi mật khẩu" (`change-password-dialog.tsx`, `POST /auth/password`, re-auth). Toast báo số thiết bị khác bị đăng xuất.
  - Đã làm "Đăng xuất các thiết bị khác" (`devices-card.tsx`, `POST /sessions:revoke-all`), có hộp xác nhận và re-auth.
  - Chuyển quyền chủ: theo quyết định của lead, phạm vi chỉ là gửi lời mời (re-auth), không thêm API xem hay hủy lời mời. Đã sửa mục `/settings/account` ở trên cho đúng. Sau khi gửi thành công, card hiện câu "đã gửi tới email đó", chỉ giữ trong state. Câu nằm ngoài vùng re-auth, nên hết hạn re-auth thì câu vẫn còn. Body lấy kiểu từ spec (`requestOwnerTransfer`).
  - Hai test chuyển quyền chủ: gửi sau re-auth thì body đúng, câu đã gửi hiện và form được xóa; 422 `email_in_use` thì không có câu đã gửi và form giữ nguyên email.
  - `account-page.test.tsx` có thêm 4 test. Đổi mật khẩu: qua re-auth thì gửi lại đúng request; mật khẩu mới được kiểm trước khi gửi, và lỗi 422 của server hiện trên ô. Đăng xuất các thiết bị khác: thành công sau hộp xác nhận; bị từ chối thì hộp vẫn mở và không có toast thành công.
- **M2, ngày làm việc trôi theo thời gian:**
  - `shared/api/platform-queries.ts`: `useServerBusinessDate()` và `useBusinessDate()` giữ độ lệch giữa `server_time` của `/me` và lúc câu trả lời về (`dataUpdatedAt`).
  - Ngày được tính từ đồng hồ máy cộng độ lệch, đọc lúc mount và đọc lại đúng nửa đêm theo múi giờ cửa hàng (setTimeout), nên màn hình mở qua đêm tự sang ngày mới mà không gọi lại `/me`.
  - Nếu độ lệch hoặc múi giờ đổi làm ngày khác đi thì đọc lại ngay.
  - `shared/lib/dates.ts` thêm `msUntilNextBusinessDay(now, timeZone)`. Hàm tính một ngày đúng 24 giờ, nên ở múi giờ có giờ mùa hè có thể chạy sớm một giờ; lúc đó hook đọc lại ngày.
  - Chứng từ vẫn giữ ngày theo intent, ví dụ `InvoiceProgress.date` của hóa đơn mới.
  - Test mới `shared/api/platform-queries.test.tsx` (fake timers) kiểm: theo đồng hồ server, sang ngày đúng nửa đêm, múi giờ UTC, `/me` chưa tải, và giá trị của `msUntilNextBusinessDay`.
- **M3, timeout test:**
  - `vitest.config.ts`: `testTimeout: 15_000`.
  - `test/render.tsx`: QueryClient của test đặt `retry: false`, để query lỗi trả về ngay thay vì retry kèm backoff.
  - Đã bỏ các timeout 3000/4000 tự đặt trong test guards, admin (usage, incidents, partners, support, support ticket) và `offline-policy-card`.
  - Hai chỗ chờ 10_000 ở `router.test.tsx` và `app-shell.test.tsx` giữ nguyên, vì nay đã nhỏ hơn `testTimeout`.
  - Sửa ngoài feature: sau khi tắt retry, `features/dashboard/pages/dashboard-page.test.tsx` cần trả response trễ có kiểm soát (deferred) thay vì dựa vào retry, nên đã đổi test đó.

### Low

- **L1, một cache cho `GET /tenant`:**
  - Đã bỏ `onboardingKeys.tenant`/`useTenantProfile` và `settingsKeys.storeProfile`/`useStoreProfile`.
  - Bước hồ sơ onboarding và "Thông tin cửa hàng" dùng `useTenant()` (`platformKeys.tenant`).
  - If-Match lấy từ `version`, vì ETag của API là version đặt trong ngoặc kép.
  - PATCH hồ sơ ở cả hai nơi invalidate `platformKeys.tenant()`, `platformKeys.me()` và `platformKeys.onboarding()`.
  - `/me` của settings (`settingsKeys.owner`) vẫn để riêng: `meSchema` của platform chỉ đọc các trường shell cần, nếu mở rộng nó thì phải sửa toàn bộ fixture `TEST_ME`.
- **L2, invalidate thiếu:**
  - Khóa sổ và mở lại kỳ invalidate thêm `platformKeys.onboarding()`.
  - Xác nhận số đầu kỳ invalidate thêm `settingsKeys.periodLocks()` và `platformKeys.tenant()`; `features/settings/index.ts` export `settingsKeys` cho việc này.
- **L3, gói:**
  - `plan-cards.tsx` bỏ nhánh "không còn bán", vì `GET /plans` chỉ trả gói đang bán.
  - `plan-change-dialog.tsx` chỉ tạo key mới khi đổi sang gói khác. Mở lại cùng gói sau khi mất response thì gửi lại đúng key cũ.
  - Test mới kiểm cả hai trường hợp, dùng `HttpResponse.error()`. Test này đỏ với cách remount key cũ.
- **L4, xuất dữ liệu:** đã làm sau khi spec có `Idempotency-Key` (tùy chọn) cho `POST /data-exports`.
  - `data-export-card.tsx`: lệnh xuất đi qua `useCommit` và gửi `idempotencyKey` của intent.
  - Re-auth gửi lại cùng key, cùng body.
  - Bấm lại sau khi mất response thì giữ key cũ. Key chỉ bỏ sau 2xx, theo hợp đồng chung của `useCommit`; API chỉ lưu câu trả lời 2xx, nên bấm lại sau một lần bị từ chối cũng an toàn.
  - `useCommit` và job id nằm ngoài `RequireRecentAuth`. Vì vậy khi vùng re-auth đóng rồi mở lại, key và job đang theo dõi vẫn còn.
  - 409 `idempotency_key_reused` và `idempotency_key_in_progress` dùng câu tiếng Việt đã có.
  - `features/settings/api/account.ts` export thêm kiểu `DataExport`.
  - Test (`account-page.test.tsx`):
    - Test xuất dữ liệu cũ nay khẳng định có header.
    - Test mới: re-auth gửi lại đúng key và body.
    - Test mới: `HttpResponse.error()` rồi bấm lại thì cùng key; lần xuất sau khi thành công thì key mới.
  - Cổng kiểm tra:
    - typecheck và lint sạch.
    - `corepack pnpm test` xanh hai lần liên tiếp, 130 files / 908 tests.
    - Output trong scratchpad: `gate-l4-static.txt`, `gate-l4-run1.txt`, `gate-l4-run2.txt`.
- **L5, độ dài ô tìm:**
  - `shared/ui/filter-bar.tsx`: `SearchInput` nhận `maxLength`.
  - `TENANT_SEARCH_MAX_LENGTH` và `AUDIT_SEARCH_MAX_LENGTH` = 100, theo `maxLength` của `q` trong spec.
  - Trang cửa hàng (admin) và audit log truyền `maxLength`. Ô chọn cửa hàng cắt chuỗi trước khi gửi, để không lọt khi dán.
  - Test kiểm thuộc tính maxlength, và kiểm dán 120 ký tự vào ô chọn cửa hàng chỉ gửi 100.
- **L6, support view:**
  - `supportPathError` từ chối mọi `%`.
  - `supportViewUrl()` mã hóa `tenantId` và từng segment của route.
  - Màn hình đặt hẹn giờ tới `expires_at` (`msUntilExpiry`, giới hạn theo mức tối đa của setTimeout), nên phiên hết hạn thì dữ liệu cửa hàng rời màn hình đúng lúc.
  - Test mới `features/admin/lib/support-view.test.ts`, cùng test trang dùng fake timers. Test trang đỏ khi không có hẹn giờ.
- **L7, đồng hồ máy còn sót:**
  - `period-lock-card` (khóa và mở lại), `store-profile-step` và `store-profile-card` truyền `today` từ `useBusinessDate()` cho DatePicker.
  - `admin/pages/usage-page.tsx` cố ý giữ đồng hồ máy, có ghi chú trong code: `/me` của admin không có `server_time`, và ngày ở đó chỉ dùng làm khoảng báo cáo mặc định.
- **L8, `satisfies`:** `features/settings/api/contract.ts` gom kiểu từ spec. Schema của account, billing, support, audit và tenant-settings đều `satisfies` kiểu tương ứng.
- **L9, thông điệp lỗi:**
  - `shared/hooks/use-commit.ts` có tùy chọn `failedLead`. `toast.commitError(message, lead?)` và `commitErrorMessage(message, lead)` trong `shared/ui/toast-store.ts` nhận câu mở đầu, mặc định vẫn là "Chưa ghi sổ.".
  - Ticket hỗ trợ (tạo và trả lời) bị từ chối thì báo "Chưa gửi.".
  - Cảnh báo `possible_password` và nút "Vẫn gửi" ẩn khi nội dung đã sửa khác bản vừa gửi.
  - Bước onboarding bị từ chối thì báo "Chưa đánh dấu xong bước “…”." hoặc "Chưa bỏ qua bước “…”.", nên sau "Đã lưu" người dùng biết là hồ sơ đã lưu nhưng bước chưa xong.
  - Có thêm test cho từng câu.

### Sửa file dùng chung trong lượt này

- `shared/hooks/use-commit.ts`: thêm `failedLead`.
- `shared/ui/toast-store.ts`: thêm tham số `lead`.
- `shared/ui/filter-bar.tsx`: thêm `SearchInput.maxLength`.
- `shared/lib/dates.ts`: thêm `msUntilNextBusinessDay`.
- `shared/api/platform-queries.ts`: các hook ngày làm việc như mô tả ở M2.
- `test/render.tsx`: `retry: false`.
- `vitest.config.ts`: `testTimeout`.
- Ngoài thư mục được giao: `features/crm` (dialog tạo nhanh khách) và `features/dashboard/pages/dashboard-page.test.tsx`.

### Nhãn mới (cần duyệt)

- Tạo nhanh khách ở bước công nợ đầu kỳ:
  - "+ Khách hàng mới", "Thêm khách hàng mới", "Quay lại", "Thêm & chọn";
  - sub "Lưu xong sẽ quay lại màn đang làm, khách mới được chọn sẵn. Thông tin chi tiết bổ sung sau ở Khách hàng.";
  - toast "Đã thêm … và chọn sẵn.".
- Đổi mật khẩu:
  - "Đổi mật khẩu";
  - sub "Các thiết bị khác sẽ bị đăng xuất; thiết bị này vẫn giữ đăng nhập.";
  - toast "Đã đổi mật khẩu." và "Đã đổi mật khẩu. N thiết bị khác đã bị đăng xuất.".
- Đăng xuất các thiết bị khác:
  - "Đăng xuất các thiết bị khác", hộp xác nhận "Đăng xuất các thiết bị khác?";
  - sub "Các thiết bị đó phải đăng nhập lại ở lần dùng tiếp theo. Thiết bị này vẫn giữ đăng nhập.";
  - nội dung "Đăng xuất N thiết bị khác đang đăng nhập vào tài khoản này?";
  - toast "Đã đăng xuất N thiết bị khác." và "Không còn thiết bị khác đang đăng nhập.".
- Chuyển quyền chủ: câu trên card sau khi gửi, "Đã gửi lời mời chuyển quyền tới …. Người nhận cần mở liên kết trong email trong 72 giờ để chấp nhận.".
- Lỗi:
  - "Chưa gửi." cho ticket hỗ trợ;
  - "Chưa đánh dấu xong bước “…”." và "Chưa bỏ qua bước “…”." cho onboarding.

### Ghi chú

- Add-on đang dùng nay đọc từ `addons` của `GET /subscription` (xem mục "Add-on đọc từ `addons`" dưới đây).

## Add-on đọc từ `addons`, và invalidate `/me`

- **Schema:** `shared/api/endpoints.ts` thêm `activeAddonSchema` (`plan_id`, `plan_name`, `modules`, `period_start`, `period_end`). Schema này `satisfies` `ActiveAddon` của spec, trừ `modules`: trường này giữ dạng chuỗi như `planSchema`, để module mới vẫn parse được. `subscriptionViewSchema` có `addons: z.array(activeAddonSchema)` (luôn có, không null).
- **`plan-cards.tsx`:** "Đang dùng" và "Hiệu lực {từ} – {đến}" đọc theo `plan_id` trong `addons`, không lọc lại theo ngày. Add-on ngừng bán mà vẫn có trong `addons` có thẻ riêng, đặt tên theo `plan_name`, ghi module và kỳ, không có nút mua. Thẻ add-on không còn tải `/saas-invoices`.
- **Đã bỏ:** `addonsInForce` và `AddonTerm` trong `features/settings/api/billing.ts`, cùng mọi chỗ suy add-on từ hóa đơn.
- **Sau khi mua:** `subscription:change` vẫn invalidate `platformKeys.subscription()`, `platformKeys.entitlements()` và hóa đơn.
- **Fixture:**
  - `TEST_SUBSCRIPTION` và `testSubscriptionView()` có `addons: []`.
  - `testActiveAddon()` mới trong `features/settings/testing/fixtures.ts`.
- **Test (`subscription-page.test.tsx`):**
  - Mua add-on miễn phí: thẻ chuyển sang "Đang dùng" khi `GET /subscription` trả add-on đó.
  - Test mới: add-on trong `addons` hiện "Đang dùng" kèm kỳ, kể cả add-on ngừng bán (đặt tên theo `plan_name`, không có nút). Add-on có hóa đơn đã trả mà không có trong `addons` vẫn hiện nút mua.
- **Invalidate `/me` theo quyết định của lead:** lệnh ghi làm đổi `/me` invalidate cả `settingsKeys.owner()` và `platformKeys.me()`.
  - Đổi mật khẩu: trước chỉ invalidate phiên, nay thêm hai key này.
  - Bật 2FA (`mfa-setup-dialog`), tắt 2FA (`owner-card`) và PATCH hồ sơ ở settings: đã có đủ hai key.
  - PATCH hồ sơ ở onboarding: thêm `settingsKeys.owner()`.
- **Test invalidate:**
  - Test đổi mật khẩu khẳng định `GET /me` được đọc lại hai lần, cho shell và cho thẻ chủ cửa hàng; test đỏ khi bỏ invalidate.
  - Test khóa sổ khẳng định `GET /onboarding` được đọc lại; test đỏ khi bỏ invalidate.
- **Cổng kiểm tra:**
  - typecheck và lint sạch.
  - `corepack pnpm test` xanh hai lần liên tiếp, 130 files / 906 tests.
  - Output trong scratchpad: `gate-addons-static.txt`, `gate-addons-run1.txt`, `gate-addons-run2.txt`.

## Câu hỏi còn mở

- Chuyển quyền chủ: xem và hủy lời mời là follow-up; lead quyết không thêm API trong phase này.
