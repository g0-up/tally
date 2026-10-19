# Review web phase 18B: Settings, Onboarding, Admin console

Ngày: 2026-10-18 · Reviewer: code-reviewer · Chế độ: chỉ đọc

## Phạm vi

- `apps/web/src/features/{settings,onboarding,admin}/**`, `apps/web/src/app/**`
- `shared/api/{endpoints.ts,platform-queries.ts,api-error.ts}`, `shared/hooks/use-commit.ts`, `shared/lib/dates.ts`
- `features/inventory/containers/release-reservation-dialog.tsx`, `features/inventory/api/mutations.ts`
- Mọi nơi gọi `useBusinessDate`
- Đối chiếu: spec OpenAPI (tenant + admin), UI spec §5.10/§5.11, báo cáo dev `plans/reports/fullstack-developer-261018-2040-web-settings-onboarding-admin.md`

## Bằng chứng đã chạy

| Lệnh | Kết quả |
|------|---------|
| `corepack pnpm typecheck` (`tsc -b`) | Sạch |
| `corepack pnpm lint` (`eslint .`) | Sạch |
| `corepack pnpm test` | 128 file / 885 test pass |
| `test --sequence.shuffle` x3 | Pass cả 3 lần |
| 2 lượt shuffle chạy song song (tải cao) | Pass cả 2 |

Không tái hiện được lỗi flaky. Nguyên nhân khả dĩ nằm ở mục M3.

## Đánh giá chung

Kiến trúc ghi dữ liệu vững:
- `useCommit` chỉ toast sau 2xx.
- Re-auth gửi lại cùng key và cùng body.
- Admin có HttpClient và QueryClient riêng.
- Tiền đi qua `dec`.
- Nhãn khớp UI spec.

Có một lỗi luồng nghiệp vụ mức High ở onboarding gate. Có ba điểm Medium: báo cáo dev ghi quá thực tế, ngày nghiệp vụ là snapshot đứng yên, và timeout test vô hiệu.

---

## High

### H1. Onboarding gate nhốt chủ cửa hàng: các bước bắt buộc trỏ tới trang bị chính gate chặn

**Vị trí**

- `apps/web/src/app/router.tsx:88-103`: `OnboardingGate` bọc toàn bộ `AppShell`, gồm menu routes, `/catalog/*`, `/partners/*`, `/sales/pos`, `/settings/*`. Chỉ `/onboarding` (dòng 106) nằm ngoài.
- `apps/web/src/features/onboarding/steps/catalog-setup-step.tsx:27-28`: `TextLink` tới `/catalog/products` và `/catalog/suppliers`.
- `apps/web/src/features/onboarding/steps/opening-debt-step.tsx:15,124,158`: phải thu dùng `customerSource`, tức chỉ khách đã có. Chỉ có `SupplierQuickDialog`, không có dialog tạo nhanh khách hàng.

**Kịch bản**

1. Cửa hàng mới có bước bắt buộc còn `pending`, nên `hasBlockingOnboardingStep` = true.
2. Chủ bấm "Mở danh mục hàng hóa". Router vào `/catalog/products`, gate redirect ngay về `/onboarding`. Link không dùng được.
3. Ở bước công nợ đầu kỳ, chiều "Phải thu" không có khách nào để chọn và không có cách tạo khách: trang khách hàng bị gate chặn, và không có quick dialog.
4. Chủ buộc phải "Đánh dấu xong" rồi xác nhận số dư đầu kỳ. Việc này khóa kỳ đến ngày chốt, nên sau đó không nhập được phải thu đầu kỳ đúng ngày chốt nữa. Số liệu đầu kỳ sai vĩnh viễn.

Test hiện tại chỉ kiểm `href`, không kiểm điều hướng thật qua gate.

**Cách sửa** (chọn một)

- (a) Miễn gate cho `/catalog/*`, `/partners/*` (cân nhắc thêm `/settings/*`): tách nhánh router hoặc cho `OnboardingGate` nhận danh sách prefix được phép.
- (b) Giữ gate nhưng đưa thao tác vào onboarding: thêm `CustomerQuickDialog` trong `opening-debt-step` giống `SupplierQuickDialog`, và thay `TextLink` bằng dialog hoặc form nhúng.

Thêm test router: khi có bước chặn, `/catalog/products` vẫn render (với a), hoặc có thể tạo khách ngay trong bước công nợ (với b).

---

## Medium

### M1. Báo cáo dev ghi nhận chức năng mà UI không gọi

Spec có endpoint, nhưng không có code web nào gọi:

| Báo cáo dev (`fullstack-developer-261018-2040-...md`) | Endpoint trong spec | Thực tế |
|---|---|---|
| Dòng 24: đổi mật khẩu | `POST /auth/password` | `owner-card.tsx` không có form hay mutation đổi mật khẩu |
| Dòng 28: đăng xuất tất cả thiết bị | `POST /sessions:revoke-all` | `devices-card.tsx` chỉ có `DELETE /sessions/{id}` từng phiên |
| Dòng 34: xem trạng thái và hủy lời mời chuyển chủ | (luồng owner transfer) | `owner-transfer-card.tsx` chỉ `POST /owner-transfer` |

**Kịch bản**: lead đánh dấu xong phase dựa trên báo cáo. Chủ cửa hàng nghi lộ mật khẩu nhưng không đổi được mật khẩu và không đăng xuất hàng loạt được trên web. Đây là khoảng trống về bảo mật tài khoản.

**Cách sửa**: cài ba luồng qua `useCommit` (re-auth cho đổi mật khẩu và revoke-all), hoặc sửa báo cáo và bằng chứng trong plan cho đúng thực tế.

### M2. Ngày nghiệp vụ là snapshot của `server_time`, không trôi theo thời gian

**Vị trí**: `apps/web/src/shared/api/platform-queries.ts:115-125`. `useServerBusinessDate` lấy `server_time` lúc fetch `/me` (stale 60s). Giá trị chỉ đổi khi `/me` refetch, tức khi mount, focus hoặc invalidate. Không có `refetchInterval`.

**Kịch bản**: máy POS để mở qua nửa đêm, không ai chuyển tab, `/me` không refetch. Hóa đơn và phiếu tạo lúc 00:30 vẫn lấy ngày hôm qua làm ngày nghiệp vụ (ví dụ `invoice-mutations.ts:156`). Hệ quả: chứng từ ghi nhầm ngày, có thể rơi vào kỳ sắp khóa.

**Cách sửa**:
- Lưu `offset = Date.parse(server_time) - Date.now()` lúc nhận `/me`, rồi tính "bây giờ" bằng `Date.now() + offset`.
- Thêm timer re-render ở mốc đổi ngày theo múi giờ cửa hàng, hoặc `refetchInterval` cho `/me`.

Thêm test với fake timers vượt qua nửa đêm.

### M3. Timeout 10s trong test vô hiệu vì `testTimeout` mặc định là 5s (nguyên nhân flaky khả dĩ)

**Vị trí**

- `apps/web/vitest.config.ts`: không đặt `testTimeout`, nên dùng mặc định 5000ms.
- `apps/web/src/app/router.test.tsx:11`: `waitFor(..., { timeout: 10_000 })`.
- `apps/web/src/app/layouts/app-shell.test.tsx:117`: `findByRole(..., { timeout: 10_000 })`.
- Các chờ 3000-4000ms cho retry delay của React Query, sát ngưỡng 5s khi máy tải cao:
  - `src/app/guards/guards.test.tsx:38,62`
  - `src/features/settings/components/offline-policy-card.test.tsx:113`
  - các test trang admin: usage, incidents, support, support-ticket, partners

**Kịch bản**: lần chạy lạnh hoặc CI tải cao, transform lazy chunk mất hơn 5s. Vitest giết test ở 5s trước khi `waitFor` 10s kịp hết hạn, nên test fail ngẫu nhiên. Ở 6 lượt chạy local không tái hiện, nhưng ngưỡng rõ ràng không nhất quán.

**Cách sửa**:
- Đặt timeout tường minh cho các test đó, ví dụ `it("...", fn, 15_000)`.
- Đặt `retry: false` cho QueryClient trong test helper để bỏ chờ backoff.
- Hạ timeout `waitFor` về dưới `testTimeout`.

---

## Low

**L1. Ba cache cho `GET /tenant`**: `platformKeys.tenant`, `settingsKeys.storeProfile`, `onboardingKeys.tenant`.
- `apps/web/src/features/onboarding/api/mutations.ts:77` (PATCH hồ sơ trong onboarding) không invalidate `settingsKeys.storeProfile`, nên Cài đặt có thể hiện hồ sơ cũ tới 30s.
- `/me` cũng bị nhân đôi qua `settingsKeys.owner`.
- Sửa: dùng chung một query `platformKeys.tenant` / `platformKeys.me`.

**L2. Invalidate thiếu sau khóa kỳ.**
- `apps/web/src/features/settings/components/period-lock-card.tsx:49,92` chỉ invalidate `settingsKeys.periodLocks()`, không invalidate `platformKeys.onboarding()` (chứa `locked_through`).
- `confirm-opening-step` không invalidate cache khóa kỳ và tenant.
- Sửa: thêm các key đó vào `invalidate`.

**L3. Plans.**
- `plan-cards.tsx:72`: nhánh "add-on không còn hoạt động" không bao giờ chạy, vì `GET /plans` chỉ trả gói đang hoạt động.
- Mở lại dialog đổi gói sau khi mất response sẽ sinh key mới, có thể tạo yêu cầu đổi gói thứ hai.
- Suy add-on từ hóa đơn là khoảng trống API đã biết.

**L4. Xuất dữ liệu.** Job id chỉ nằm trong state, request không có Idempotency-Key, nên retry mạng có thể xếp hàng job export thứ hai. Sửa: gửi key ổn định theo intent.

**L5. Thiếu `maxLength` 100 cho ô tìm kiếm**, trong khi spec giới hạn `q` ≤ 100. Nhập dài hơn sẽ nhận 422 thay vì bị chặn ở UI. Sửa: truyền `maxLength={100}` hoặc cắt chuỗi trước khi gửi.
- `apps/web/src/features/admin/pages/tenants-page.tsx:62`
- `apps/web/src/features/admin/components/tenant-picker.tsx`
- `apps/web/src/features/settings/pages/audit-log-page.tsx:82`

**L6. Support view.**
- `apps/web/src/features/admin/lib/support-view.ts:33-38` chặn `.`/`..` nhưng không chặn segment percent-encoded `%2e%2e`. Rủi ro thấp: chỉ GET, dưới phiên admin, server vẫn kiểm quyền. Nên decode trước khi kiểm, hoặc từ chối `%`.
- `tenantId` không qua `encodeURIComponent`.
- `activeGrant` (dòng 42) dùng `Date.now()` một lần, nên grant hết hạn khi đang mở màn hình thì view không tự ẩn.

**L7. Đồng hồ thiết bị còn sót.**
- `shared/ui/date-picker.tsx:34` (`today ?? todayBusinessDate()`): highlight "hôm nay" theo máy, vì `period-lock-card.tsx:68,143` và `store-profile-step.tsx` không truyền `today`.
- `admin/pages/usage-page.tsx:32` dùng `todayBusinessDate()`, chỉ ở phía admin.

**L8. Schema settings không có `satisfies`** đối chiếu kiểu spec như admin (42 chỗ). Lệch contract sẽ không bị typecheck bắt.

**L9. Thông điệp lỗi.**
- Ticket hỗ trợ bị từ chối hiện toast "Chưa ghi sổ.", câu này dành cho chứng từ.
- Cảnh báo `possible_password` vẫn hiển thị sau khi người dùng đã sửa nội dung.
- Ở `store-profile-step`, nếu bước complete chạy ngầm lỗi ngay sau "Đã lưu" thì người dùng thấy cả "Đã lưu" lẫn "Chưa ghi sổ.", dễ hiểu nhầm.

---

## Đã kiểm, không có lỗi

- **Contract**: 42 `satisfies` ở `admin/api/schemas.ts` khớp schema admin. Tiền dùng `moneySchema` và `dec`. `Number()`/`parseInt` chỉ dùng cho số ngày và số giờ nguyên.
- **Bảo mật admin**:
  - client và QueryClient tách hẳn (`app/platform.ts`);
  - CSRF lấy từ `/me`;
  - 401 chuyển `/admin/login?next=` qua `safeNextPath`;
  - logout xóa `adminKeys.all`;
  - typed confirm (khớp chính xác sau trim) cho Suspend, Gỡ legal hold, Void invoice;
  - support view chỉ GET, hiện read-only.
- **Ghi dữ liệu**:
  - `useCommit` toast sau 2xx, rồi await invalidate, rồi mới chạy `after`, nên `navigate(HOME_PATH)` sau confirm opening an toàn;
  - re-auth gửi lại cùng biến và key;
  - `inFlight` chặn double submit;
  - closure chỉ cho hủy ở requested/verified/waiting;
  - release reservation có body `satisfies ReleaseBody`.
- **Onboarding gate**: lỗi tải checklist không khóa app. Complete lặp lại sau khi post là an toàn, vì spec nói complete bước đã xong không thay đổi gì.
- **Phân trang và debounce**:
  - `useCursorList` giữ `q` trong key và mọi trang;
  - Combobox debounce nội bộ;
  - `useAdmins` phân trang tuần tự, không có N+1 theo dòng.
- **Nhãn**: §5.10/§5.11 khớp (Họ tên, Cửa hàng, Đăng nhập gần nhất, Xác thực 2 lớp, Thiết bị đang đăng nhập, Thiết bị này, Đang dùng/Gói nền, Thuê bao hiện tại, Truy cập hỗ trợ, Cấp quyền hỗ trợ 2 giờ (chỉ đọc), Thu hồi).
- **a11y**: `Field` bọc control trong `<label>`, nên các control có tên truy cập.

## Việc nên làm theo thứ tự

1. Sửa H1 (miễn gate hoặc thêm quick dialog khách hàng) kèm test điều hướng thật.
2. M1: cài đổi mật khẩu, revoke-all, trạng thái và hủy chuyển chủ; hoặc đính chính báo cáo.
3. M2: offset đồng hồ server và cập nhật ngày ở nửa đêm.
4. M3: timeout test và `retry: false` trong test QueryClient.
5. Các mục Low theo tiện.

## Theo dõi plan

Phần settings, onboarding và admin đã có code, test và chạy sạch. Chưa nên đánh dấu xong phase 18B khi H1 và M1 chưa được xử lý hoặc đính chính.

Status: DONE_WITH_CONCERNS
Summary: Typecheck, lint và 885/885 test sạch qua 6 lượt chạy, không tái hiện flaky. Có 1 High (onboarding gate chặn chính các trang mà bước bắt buộc trỏ tới, và không tạo được khách cho phải thu đầu kỳ), 3 Medium, 9 Low.
Concerns/Blockers: H1 gây sai số liệu đầu kỳ không sửa được sau khi khóa kỳ. Báo cáo dev ghi ba chức năng mà UI chưa có.
