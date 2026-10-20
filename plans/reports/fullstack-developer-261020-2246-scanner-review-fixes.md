# Sửa các phát hiện review backend của tính năng quét bằng điện thoại

Ngày: 2026-10-20. Nguồn: `plans/reports/code-reviewer-261020-2246-scanner-backend.md`.

## Kết quả

Tôi đã kiểm tra lại cả 8 phát hiện được giao (H1, M2, M3, M4, M5, L3, L4, L5) với code, và cả 8 đều có thật. Không có phát hiện nào phải bác bỏ. Tất cả đã được sửa, mỗi chỗ đều có test. Với H1, M2, M4 và M5, tôi còn cố ý làm hỏng chỗ sửa để chắc rằng test mới thật sự bắt được lỗi: test đỏ khi lỗi quay lại và xanh khi có bản sửa.

Ngoài ra tôi làm thêm yêu cầu bổ sung của coordinator: sửa mô tả QR trong OpenAPI thành `/scan#c=<claim_code>`.

## Từng phát hiện

### H1: xác thực lại trên desktop làm mất phiên quét (đã sửa)

**Bằng chứng.** Luồng desktop đọc cookie `tally_session` một lần lúc mở stream. Mỗi lần tick, nó gọi `ResolveSession(token)`. Nhưng `identity.Reauth` (identity/service.go) lại thay `token_hash` mà vẫn giữ nguyên id phiên. Vì vậy sau khi xác thực lại, token cũ không còn khớp, và tick tiếp theo thu hồi phiên quét.

**Cách sửa.**
- Thêm `identity.(*Service).SessionActive(ctx, tenantID, userID, sessionID)` trong `apps/api/internal/identity/sessions.go`. Hàm tìm phiên đăng nhập theo id và áp đúng các điều kiện của `ResolveSession`: chưa bị thu hồi, chưa quá hạn tuyệt đối, còn trong thời gian rảnh 12 giờ, user active, và tenant không ở trạng thái closed hay replaced. Hàm không bao giờ cập nhật `last_seen_at`.
- Thêm port `scanner.LoginSessions` trong `scanner/http.go`. `desktopCheck` giờ kiểm tra bằng `o.LoginSessionID`, nên không còn đọc cookie phiên.
- Nối port vào app:
  - `app.Ports.LoginSessions`, mặc định là đóng: `noSessions.SessionActive` trả về false.
  - `routes.go` truyền port này cho scanner.
  - `cli/serve.go` gán `LoginSessions: accounts.identity`. Đây là một dòng ở composition root, nằm ngoài danh sách file của brief. Tôi chọn cách này thay vì để `withDefaults` tự type-assert từ `Sessions`, vì cách đó quá ngầm.

**Test.**
- Mới: `apps/api/internal/app/scanner_login_session_integration_test.go`, test `TestScannerStreamFollowsTheLoginSession`. Test chạy với identity thật và một listener thật, theo luồng sau:
  1. Mở phiên quét, phone claim, desktop approve, rồi mở stream desktop.
  2. Gọi `POST /auth/reauth`, kiểm tra cookie đã đổi. Stream im lặng qua nhiều tick, phiên vẫn `claimed`, và phone vẫn `claimed`.
  3. Gọi `POST /auth/logout`. Stream nhận `revoked {by: system}`, và phone nhận 401 scan_session_ended.
- Mới: `TestReauthenticatedDesktopKeepsTheSession` trong scanner_integration_test.go, dùng fake có `rotate`.

Kiểm tra bằng cách làm hỏng: tôi thêm `reauthenticated_at IS NULL` vào `SessionActive`, và test báo `unexpected event revoked`.

### M5: tick 10 giây làm mới `last_seen_at` (đã sửa cùng H1)

**Bằng chứng.** `ResolveSession` cập nhật `last_seen_at` mỗi phút. Vì stream gọi nó mỗi tick, một tab mở mãi sẽ giữ phiên đăng nhập không bao giờ hết hạn rảnh. `SessionActive` chỉ đọc, nên lỗi này không còn.

**Test.** Cùng test ở trên. Sau khi xác thực lại, tôi tiến đồng hồ 3 lần, mỗi lần 25 giây (tổng 75 giây, quá ngưỡng 1 phút), và desktop không gửi request nào. Kết quả: `tally.sessions.last_seen_at` giữ nguyên, còn `desktop_seen_at` của phiên quét vẫn tăng. Kiểm tra bằng cách làm hỏng: tôi thêm lệnh UPDATE `last_seen_at` vào `SessionActive`, và test báo `last_seen_at moved from 10:00:00 to 10:01:15`.

### M2: kết quả lưu khi chưa có waiter thì bị mất (đã sửa)

**Bằng chứng.** `MemoryHub.Resolve` chỉ giao kết quả cho những waiter đã đăng ký. Khi retry cùng Idempotency-Key, desktop có thể trả lời sau lúc commit nhưng trước `hub.Await`. Khi đó phone phải chờ đến hết thời gian rồi nhận `timeout`, dù kết quả đã nằm trong DB.

**Cách sửa** (trong `scanner/service.go` Scan):
- Id của scan mới được sinh trước transaction. Nếu `row.ID` khác id đó, tức đây là retry, thì sau khi `Await` đã đăng ký, code đọc lại hàng scan (`storedResult`, một tx read-only ngắn) và trả kết quả đã lưu nếu có, trước khi Publish.
- Khi timer hết hạn, code cũng đọc lại một lần trước khi trả `timeout`.
- Scan mới (lần đầu) không tốn thêm lần đọc nào, vì `Await` luôn đăng ký trước `Publish`.

**Test.** File mới `apps/api/internal/scanner/scan_result_integration_test.go`. Hai test dùng `scanner.NewService` với một hub bọc `MemoryHub`:
- `TestRetryGetsAnAnswerStoredBeforeItsWaiter`: desktop resolve ngay trong hook trước `Await`. Retry phải trả `not_found` đã lưu, và trong dưới 2 giây.
- `TestTimedOutScanReturnsAnAnswerStoredMeanwhile`: hub bỏ việc giao kết quả. Scan phải trả kết quả đã lưu sau 4 giây chờ.

Khi tắt `storedResult`, cả hai test đều trả `timeout` và đỏ.

### M3: route phone để lộ trạng thái maintenance hoặc read-only cho người ẩn danh (đã sửa)

**Bằng chứng.** claim, phoneRevoke và scan đều gọi `writable()` trước khi xác thực. Người ẩn danh vì thế phân biệt được 423 với 403, và mỗi request tốn 2 lần tra cứu platform. Riêng `DELETE /scan/session` không có limiter.

**Cách sửa.**
- claim: sau khi bind JSON, gọi `Service.CheckClaimCode`. Hàm này dùng tx read-only, áp cùng các luật như Claim (`claimableSession`: code hash khớp, chưa claim, chưa hết hạn, phiên chưa kết thúc) và trả 410 claim_invalid. Chỉ sau đó mới gọi `writable()`.
- scan: gọi `PhoneSession` trước, trả 401 nếu không có phiên hoặc phiên đã kết thúc, rồi mới gọi `writable()`.
- phoneRevoke: bỏ hẳn `writable()` (xem L3).
- `DELETE /scan/session` có bucket riêng `byIP(h.limits.Claim)`, cùng mức 10/phút, burst 5. Tôi không đổi con số nào.

**Test.**
- `TestMaintenance` có thêm các trường hợp người lạ: code giả trả 410, scan không có cookie trả 401, còn người giữ code thật vẫn nhận 423.
- `TestPhoneLeavesAHeldOrReadOnlyStore` chạy cả maintenance và read-only. Ở read-only, người giữ code thật và phone có cookie nhận 403 subscription_read_only.
- `TestRateLimits` có subtest mới "phone sign-outs have their own bucket". Unit test `TestRateLimitsAnswer429WithRetryAfter` có thêm DELETE.

### M4: race với giới hạn 3 phiên mở (đã sửa)

**Bằng chứng.** `lockOpenSessions` chỉ dùng `FOR UPDATE` trên các hàng đã tồn tại. Hai lần mở đồng thời đều đếm cùng một tập phiên rồi cùng insert.

**Cách sửa.** Trong `lockOpenSessions` (repo.go), code lấy `pg_advisory_xact_lock(hashtextextended('scanner_sessions:' || tenant || ':' || user, 0))` trước khi đếm. Cách này theo mẫu `catalog/skus.go:241`.

**Test.** `TestConcurrentOpensKeepThreeSessions`: 8 request mở song song của cùng một user, sau đó đúng 3 phiên còn mở. Khi bỏ khóa, test đỏ với "7 open sessions after 8 concurrent opens".

### L3: phone phải tự kết thúc phiên được cả khi maintenance hoặc read-only (đã sửa)

phoneRevoke không còn gọi `writable()`, và cookie luôn được xoá. `RevokeByPhone` dùng tenant tx thường. Maintenance và read-only chỉ được chặn ở tầng app, nên DB vẫn cho ghi. Test là `TestPhoneLeavesAHeldOrReadOnlyStore`: request trả 204, cookie bị xoá, stream phone nhận `revoked`, và phiên chuyển sang `ended`.

### L4: event thu hồi bị mất khi ctx của tick bị huỷ (đã sửa)

**Bằng chứng.** `Publish` không nhận ctx. Rủi ro thật nằm ở chỗ khác: nếu ctx bị huỷ trong lúc commit, việc thu hồi vẫn được commit nhưng hàm trả lỗi, nên `ended()` không chạy và phone không được báo.

**Cách sửa.** `desktopCheck` gọi `RevokeForSignOut` với `context.WithTimeout(context.WithoutCancel(ctx), tickTimeout)`, theo mẫu `context.WithoutCancel` ở `cli/serve.go`. Việc này khó tái hiện bằng test một cách tất định. Các test sign-out (`TestSignedOutDesktopEndsTheSession` và test của H1) xác nhận đường đi này vẫn đúng.

### L5: `in.check()` gọi hai lần, và Resolve không kiểm tra trạng thái phiên (đã sửa)

**Cách sửa.**
- Bỏ lệnh `check()` trong `resultOf`, chỉ giữ ở đầu `Resolve`. Unit test `TestResultValidation` giờ gọi `ResultInput.check()` trực tiếp.
- Trong write tx, `resolveScan` chỉ chạy khi phiên đang `claimed`. Nếu không, Resolve trả kết quả đã lưu nếu có, còn không thì trả 409 scan_expired. Tôi không dùng 401 scan_session_ended vì desktop vẫn đăng nhập bình thường.
- Mô tả 409 trong OpenAPI giờ ghi thêm "or its session ended".

**Test.** `TestAnswerAfterTheSessionEndedIsRefused`: desktop thu hồi phiên trong khi một scan đang chờ, sau đó resolve trả 409 scan_expired và không có kết quả nào được lưu.

## Không đổi (theo brief)

- Các con số rate-limit.
- Giới hạn của hub (20 mỗi tenant, 2000 tổng).
- Hub trong bộ nhớ, chạy một instance.
- Migration 000059.
- L1 (verify code là bước so sánh của người dùng; approve không đòi nó).
- L2.

## Thay đổi hợp đồng OpenAPI và web

`apps/api/api/openapi.yaml` có thay đổi hợp đồng thật, nên tôi đã chạy `cd apps/web && corepack pnpm gen:api`, và lệnh này sinh lại `apps/web/src/shared/api/schema.d.ts`. Các thay đổi trong OpenAPI:
- `DELETE /scan/session`: bỏ 423; 403 chỉ còn csrf_failed; thêm 429 rate_limited kèm Retry-After; mô tả ghi rõ route hoạt động cả khi maintenance hoặc read-only, giới hạn 10/phút.
- `POST /scan/claim` và `POST /scan/scans`: mô tả ghi thứ tự kiểm tra, xác thực trước rồi mới đến 423/403.
- `POST .../scans/{scanId}/result`: mô tả 409 có thêm "or its session ended".
- Mô tả QR đổi từ `/scan#<claim_code>` thành `/scan#c=<claim_code>` ở 2 chỗ. Không có comment Go nào ghi dạng cũ.

Không có code web nào tham chiếu `revokePhoneScannerSession` hay `/scan/session`, nên bỏ 423 không làm hỏng web. Ghi chú theo coordinator: khi stream đã kết thúc trả 204, web luôn GET session trước mỗi lần kết nối lại, nên phía web không cần đổi gì.

## File đã sửa

- Đã có sẵn, được sửa:
  - `apps/api/internal/identity/sessions.go`
  - `apps/api/internal/scanner/http.go`
  - `apps/api/internal/scanner/service.go`
  - `apps/api/internal/scanner/repo.go`
  - `apps/api/internal/app/app.go`
  - `apps/api/internal/app/routes.go`
  - `apps/api/internal/cli/serve.go` (một dòng wiring)
  - `apps/api/api/openapi.yaml`
  - `apps/web/src/shared/api/schema.d.ts` (sinh tự động)
- Test đã có, được sửa:
  - `apps/api/internal/scanner/http_test.go`
  - `apps/api/internal/scanner/service_test.go`
  - `apps/api/internal/scanner/scanner_integration_test.go`
  - `apps/api/internal/app/saas_control_integration_test.go`
- Test mới:
  - `apps/api/internal/scanner/scan_result_integration_test.go`
  - `apps/api/internal/app/scanner_login_session_integration_test.go`

## Kiểm chứng

- `go build ./... && go vet ./... && go vet -tags integration ./...`: đạt.
- `go test -race ./internal/scanner/... ./internal/identity/... ./internal/app/... ./internal/cli/...`: đạt.
- `make lint`: 0 issues.
- `make test-integration` (toàn bộ): đạt: exit 0, 51 package ok, không package nào FAIL (app 209 giây, scanner 28 giây). Sau khi sửa lint, tôi chạy lại riêng scanner integration với `-race`, và vẫn đạt.

Status: DONE_WITH_CONCERNS
Summary: Cả 8 phát hiện được giao đều có thật và đã được sửa, mỗi chỗ có test. Riêng H1, M2, M4 và M5 đã được xác nhận bằng cách làm hỏng bản sửa và thấy test đỏ. OpenAPI đã cập nhật hợp đồng của `DELETE /scan/session` và mô tả QR `#c=`, và `schema.d.ts` đã được sinh lại.
Concerns/Blockers: `cli/serve.go` nằm ngoài danh sách file của brief (một dòng wiring `LoginSessions`). `schema.d.ts` được sinh lại trong khi một agent khác đang sửa apps/web, dù không có code web nào dùng phần đã đổi.
