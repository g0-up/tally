# Review: Tally API platform foundation (phase 01–02)

Ngày: 2026-10-16 · Reviewer: code-reviewer · Chế độ: read-only (không sửa code, không migrate, không `go mod tidy`)

## Scope

- Code: `apps/api/internal/platform/**`, `internal/posting/**`, `internal/app/**`, `internal/cli/**`, `apps/api/api/openapi.yaml` (lướt), `.github/workflows/ci.yml`, `infra/**`, `Makefile` gốc và `apps/api/Makefile`.
- Đối chiếu spec: `plans/261016-0854-tally-full-product-implementation/phase-01-monorepo-docker-infra.md`, `phase-02-api-platform-foundation.md`, `design/architecture.md`, `design/database-conventions.md`.
- Kiểm chứng đã chạy: `go vet` trên 4 nhóm package trên (sạch), `go test -count=1` unit cho các package đó (tất cả `ok`; `periodlock` không có test unit). Không chạy integration test vì thư mục `migrations/**` và `testkit/**` đang có executor khác sửa song song.
- Ngoài phạm vi: `apps/api/migrations/**`, `internal/testkit/**` (executor khác đang sở hữu).

## Overall Assessment

Lõi giao dịch được làm cẩn thận: `db.run` rollback/re-panic đúng, `set_config(..., true)` là transaction-local, support chạy `SET LOCAL ROLE tally_readonly` cùng READ ONLY tx, idempotency `INSERT … ON CONFLICT DO NOTHING` + `SELECT … FOR UPDATE` tuần tự hoá đúng cùng-key, docseq không lỗ hổng số, Locker sắp xếp khoá theo (kind rank, UUID bytes) có dedupe, retry deadlock/serialization một lần có jitter. Không tìm thấy lỗi Critical (không rò dữ liệu chéo tenant, không bypass RLS, không lộ secret).

Rủi ro thật nằm ở **thứ tự guard so với idempotency** (ảnh hưởng trực tiếp POS offline) và **cấu hình edge production** (CSP chặn upload, middleware header gắn vào container web) – những thứ dev/CI không bao giờ bắt được vì dev không chạy qua Traefik.

## Critical Issues

Không có.

## High Priority

### H1. Guard chạy trước idempotency → retry của request ĐÃ thành công có thể nhận lỗi thay vì replay

- Bằng chứng: `apps/api/internal/posting/coordinator.go:136-147`: `Entitlements.CanPost` (dòng 137) và `Periods.Check` (dòng 141) chạy trước `Idempotency.Begin` (dòng 144). Bản ghi đã lưu chỉ được trả về ở dòng 147–150, tức là sau khi đã qua hai guard.
- Kịch bản lỗi:
  1. POS gửi bán hàng với key = local id. Server commit xong nhưng response bị rớt mạng.
  2. Trong lúc đó chủ cửa hàng khoá kỳ (hoặc tenant chuyển sang `read_only`/hết hạn gói).
  3. POS retry với cùng key và nhận 423 `period_locked` hoặc 403, thay vì 201 replay.
  4. Client đánh dấu đơn là thất bại; người dùng nhập lại nên phát sinh chứng từ trùng (doanh thu và tồn kho bị ghi hai lần), hoặc đơn bị treo trong hàng đợi offline mãi.
- Mâu thuẫn spec: `database-conventions.md` §7 ghi key "inserted first inside the posting transaction", còn phase-02 và architecture §3.5 liệt kê thứ tự period lock → idempotency. Code theo phase-02, nhưng phase-02 tự mâu thuẫn với mục tiêu "idempotent dưới retry".
- Đề xuất: tra key đã hoàn tất (gọi `Idempotency.Begin`, hoặc một lookup read-only `SELECT … WHERE completed`) **trước** `CanPost`/`Periods.Check`. Nếu có bản ghi đã lưu thì replay ngay. Nếu không thì chạy guard rồi mới làm tiếp. Không phát sinh rủi ro lock-order vì row key là riêng cho từng key. Đây là một quyết định thiết kế, cần lead/user chốt và sửa lại câu chữ trong spec cho thống nhất.

### H2. CSP production `connect-src 'self'` chặn upload qua presigned URL

- Bằng chứng: `infra/docker-compose.homelab.yml:61`: `connect-src 'self'`, trong khi `img-src` đã có `${OBJECT_STORE_ENDPOINT}`. `objectstore/store.go:42`: "PresignPut returns a URL the browser PUTs the file to". `httpx/bind.go` ghi rõ upload đi thẳng tới object store.
- Kịch bản: trên production, `fetch`/XHR `PUT` của trình duyệt tới origin của object store bị CSP chặn, nên mọi upload (logo, chứng từ đính kèm, import) đều hỏng. Dev không có CSP (Traefik không chạy ở dev), nên dev, CI và e2e đều xanh. Architecture §7.6 cũng chép cùng CSP này, tức lỗi nằm ở cả design.
- Đề xuất: `connect-src 'self' ${OBJECT_STORE_ENDPOINT}` (chỉ dùng origin, không có path), đồng thời sửa architecture §7.6.

### H3. Middleware `tally-headers` được khai báo trên container `web` nhưng router `tally-api` dùng nó (outage API bị buộc vào web)

- Bằng chứng: `infra/docker-compose.homelab.yml:54-62` khai báo middleware trong labels của service `web`, còn `:36` thì router `tally-api` tham chiếu `tally-headers`.
- Kịch bản: docker provider của Traefik bỏ cấu hình của container đang dừng hoặc unhealthy. Khi `web` restart, crash, unhealthy, hoặc chưa start (web `depends_on` api healthy nên luôn lên sau api), Traefik báo router `tally-api` lỗi "middleware does not exist" và trả 404 cho toàn bộ `/api/`. Như vậy một sự cố chỉ ở nginx kéo sập cả API, kể cả POS sync. Khi deploy `up -d --build`, có cửa sổ thời gian API bị 404.
- Đề xuất: khai báo middleware trên `api` (service luôn lên trước), hoặc trên cả hai với định nghĩa giống hệt, hoặc chuyển sang file provider của Traefik.

## Medium Priority

### M1. Dải trusted proxy quá rộng; spoof `CF-Connecting-IP` từ cùng network `homelab`

- `infra/.env.example:19`: `TRUSTED_PROXY_CIDRS=172.21.0.0/16` tin toàn bộ network `homelab` dùng chung với các stack khác. Mọi container khác trên network đó có thể gọi thẳng `api:8080` với `CF-Connecting-IP` giả, từ đó bypass rate limit theo IP và làm sai IP trong audit/session.
- Việc giả mạo qua Traefik LAN :80 là trade-off đã được chấp nhận (architecture §7.6), nhưng dải tin cậy rộng hơn mức "chỉ Traefik". Đề xuất: pin IP của container Traefik (gán IP tĩnh trên `homelab`) và dùng /32.
- Comment `.env.example:18` nói "X-Forwarded-For are trusted", nhưng code chỉ tin `CF-Connecting-IP` (`httpx/engine.go:31`). Comment sai lệch với code.
- `config` không bắt buộc `TRUSTED_PROXY_CIDRS` khi `APP_ENV=production` (compose bắt buộc qua `:?`, binary chạy tay thì không). Nếu thiếu, mọi client sẽ mang IP của Traefik và dùng chung một bucket 20 rps / burst 100 (`app.go`), nên một client có thể làm nghẽn tất cả. Đề xuất: fail-fast trong config khi production.

### M2. Tra DB theo slug xảy ra trước rate limiter

- `apps/api/internal/app/app.go:93-95`: `HostContext` (cache miss thì query DB) đứng trước `RateLimitByIP`. Negative cache ở `middleware/host_context.go:75-84` dừng ghi khi đạt 10000 entry.
- Kịch bản: kẻ tấn công gửi `<random>.sidecup.app/api/...` (wildcard DNS và HostRegexp khớp). Sau 10000 slug ngẫu nhiên, mỗi request đều là một query DB không bị throttle, đánh thẳng vào pool kết nối. Đề xuất: đặt `RateLimitByIP` trước `HostContext`, và cho negative cache dùng LRU/TTL thay vì "đầy thì ngừng".

### M3. Rate limiter: map không giới hạn và IPv6 theo từng địa chỉ

- `middleware/ratelimit.go:56-60`: bucket map không có trần, key là IP chính xác. Một client IPv6 xoay địa chỉ trong /64 vừa né được limit vừa làm phình bộ nhớ giữa các chu kỳ dọn. Đề xuất: key IPv6 theo /64 và đặt trần số bucket.

### M4. Outbox dispatcher poll từng tenant (N+1) và giữ transaction dài quanh side effect

- `outbox/dispatcher.go:361-388` cùng `cli/worker.go` (`defaultDispatchInterval = 2s`): mỗi tick mở một tx cho mỗi tenant, mỗi tx 2 query, kể cả khi không có event. Với 1000 tenant là khoảng 1000 tx mỗi 2 giây khi rảnh.
- Handler chạy trong một tx dài cho cả batch (tối đa 100 event, giữ `FOR UPDATE`). Nếu commit lỗi thì side effect bên ngoài (SMTP) bị lặp cho cả batch. Đề xuất: tìm tenant có event sẵn sàng bằng một query toàn cục qua owner/worker role (hoặc `LISTEN/NOTIFY`), và commit theo từng event hoặc batch nhỏ.
- Low: metric lag dùng `min(available_at)` có tính cả event bị hoãn trong tương lai, nên lag có thể âm.
- Low: `purgeIdempotency` (`cli/worker.go:274-282`) dừng ở tenant lỗi đầu tiên và bỏ qua các tenant còn lại. Nó còn cộng `total += n` trước khi kiểm tra lỗi.

### M5. Backup guard khi migrate production là kiểm tra yếu

- `cli/migrate.go:172-192` chỉ kiểm tra file tồn tại, không rỗng và mtime dưới 1 giờ. Nó không xác minh dump có phải `pg_dump -Fc` hợp lệ (magic `PGDMP`) hay thuộc đúng DB đích không. Bất kỳ file mới nào cũng qua được.
- Guard phụ thuộc `APP_ENV=production`. Service `migrate` trong compose mặc định production (tốt), nhưng operator chạy binary trực tiếp mà thiếu `APP_ENV` thì guard bị bỏ qua (mặc định là development) trong khi `MIGRATE_DATABASE_URL` vẫn trỏ prod.
- `make prod-migrate` (Makefile:154–156) được nối đúng: backup trước, `umask 077`, ghi `.partial` rồi `mv`, mount read-only. Tốt.
- Low: `db-backup` không dump globals/roles, nên restore lên máy mới cần chạy `roles.sql` trước (nên ghi vào runbook). `prod-up` deploy code mới mà không migrate, nên thứ tự phụ thuộc vào runbook.
- Đề xuất: kiểm tra magic header `PGDMP`. Cân nhắc yêu cầu `--backup-file` bất cứ khi nào host DSN không phải localhost, thay vì dựa vào `APP_ENV`.

### M6. CSRF chỉ là double-submit không gắn session; group `API()` (root host) không có CSRF

- `middleware/csrf.go:108-121`: token double-submit thuần. Ở production chấp nhận được nhờ cookie `__Host-`. Ở dev, tên cookie bỏ tiền tố `__Host-` (`middleware/session.go:265-274`), chỉ ảnh hưởng dev.
- `app/routes.go:196-198`: group `API()` (login, sign-up, store-lookup trên root host) resolve session nhưng không kiểm CSRF. Hiện chưa khai thác được, nhưng khi phase auth thêm login/logout thì cần quyết định có chống login-CSRF không (SameSite=Lax có giảm thiểu một phần).

## Low Priority

- **Slug cache dương không hết hạn và chỉ trong một process** (`middleware/host_context.go:30-94`). Slug là bất biến và không tái sử dụng, nên không phải lỗ hổng. Nhưng `Purge` chỉ tác động process hiện tại; khi chạy nhiều replica (hoặc api cùng worker) thì tenant đã purge vẫn còn trong cache. Guard fail-closed ở tầng DB nên không rò dữ liệu.
- **Logger ghi `URL.Path`** (`middleware/logger.go:38`). Query string không được log (tốt). Nếu sau này có token nằm trong path (verify/reset), token sẽ vào log. Quy ước nên là token chỉ đi qua query hoặc body.
- **`migrationsFS` vẫn nil** (`cli/migrate.go:20`). Khi executor song song thêm file `.sql` mà chưa `//go:embed`, `migrate up` trong job CI `migrations` sẽ fail với `ErrNoMigrations` (hoặc job vẫn bị skip). Cần xác nhận với executor phase 03.
- **CI actions pin theo tag, không theo SHA** (`.github/workflows/ci.yml`). Đây là rủi ro supply-chain, ở mức thấp cho repo cá nhân.
- **Comment cũ** `Makefile:63` ("Schema migrations join this target once the migrate command exists"): lệnh migrate đã tồn tại nhưng `dev` vẫn chưa migrate.
- **HSTS `includeSubDomains`** trên zone dùng chung `sidecup.app` cũng ép HTTPS cho các host khác. Đây là quyết định thiết kế đã chấp nhận, chỉ ghi lại để nhận biết.
- **`period_locks` chọn bản ghi "mới nhất"** theo `created_at DESC, id DESC` lấy từ clock ứng dụng. Clock lệch giữa các instance có thể chọn sai sự kiện lock/unlock. Nên dùng `now()` của DB hoặc một sequence.
- **`periodlock` dùng `SELECT … FOR KEY SHARE` trên `tenants`**, việc này cần quyền UPDATE trên `tenants` cho `tally_app`. Default privileges đã cấp, nhưng cần xác minh trong migrations của phase 03.

## Edge Cases Found by Scout

- Retry cùng key sau khi trạng thái tenant hoặc kỳ thay đổi (H1).
- Request đồng thời cùng key: request thứ hai chờ `FOR UPDATE` trên row key, sau khi request đầu commit thì replay. Đúng. Nếu request đầu rollback (lỗi nghiệp vụ) thì row key cũng rollback, nên retry chạy lại. Đúng.
- `Complete` chỉ lưu response 2xx. Lỗi 4xx không bị "đóng băng", retry sẽ chạy lại. Đúng ý đồ.
- Host lạ hoặc wildcard ngẫu nhiên vượt 10000 entry negative cache (M2).
- Container web down kéo theo router API (H3).
- Upload trên production qua CSP (H2).
- Binary chạy thiếu `TRUSTED_PROXY_CIDRS` hoặc `APP_ENV` (M1, M5).
- Command router: id không phải UUID hoặc verb lạ trả 404. Item route `/:id` và collection route `\:verb` không xung đột. Đúng.

## Đã xác minh đúng (để hiệu chỉnh mức rủi ro)

- `db/tx.go`: rollback khi lỗi hoặc panic; tenant GUC là transaction-local; tx support là READ ONLY cộng `SET LOCAL ROLE tally_readonly`. `db.Open` không in DSN ra lỗi.
- `infra/postgres/roles.sql`: `tally_app` và `tally_readonly` là `NOSUPERUSER NOBYPASSRLS`.
- `RenderError` không lộ lỗi nội bộ. Recover re-raise `http.ErrAbortHandler`. Engine không dùng `TrustedPlatform`. `ClassifyHost` xử lý port, IP literal và label sâu. Tenant guard trả về 401 giống hệt nhau (không có oracle để dò tenant).
- Audit redaction áp dụng cả key lồng nhau. SMTP từ chối gửi credential khi không có STARTTLS. Subject được Q-encode (không có CRLF injection).
- Dockerfile dùng distroless nonroot với `-trimpath`. Postgres không nằm trên `homelab` và không publish port ở production. Port dev bind 127.0.0.1. `.gitignore` bao `.env` và `infra/backups`. Lỗi config không in giá trị.

## Recommended Actions

1. **(H1)** Chốt quyết định: idempotency lookup/replay trước entitlement và period guard; sửa `coordinator.go:136-147`; thêm test "retry sau khi khoá kỳ vẫn replay 201"; thống nhất câu chữ giữa phase-02, architecture §3.5 và database-conventions §7.
2. **(H2)** Thêm `${OBJECT_STORE_ENDPOINT}` vào `connect-src` trong `docker-compose.homelab.yml:61` và architecture §7.6.
3. **(H3)** Chuyển định nghĩa `tally-headers` sang service `api` (hoặc khai báo ở cả hai).
4. **(M2, M1)** Đặt `RateLimitByIP` trước `HostContext`. Bắt buộc `TRUSTED_PROXY_CIDRS` khi production và thu hẹp xuống IP của Traefik. Sửa comment `.env.example`.
5. **(M5)** Kiểm magic `PGDMP` trong backup guard.
6. **(M3, M4)** Đặt trần cho map rate limiter, gộp IPv6 theo /64. Giảm polling theo tenant của outbox trước khi số tenant tăng.
7. Xác nhận với executor phase 03 về `migrationsFS` / `//go:embed` và quyền của `tally_app` trên `tenants`.

## Metrics

- `go vet`: 0 vấn đề (platform, posting, app, cli).
- Unit test: tất cả package `ok`; `periodlock` không có test unit (chỉ có integration).
- Type/test coverage: không đo (không chạy `-cover`, integration bị loại khỏi phạm vi).
- Lint (golangci-lint): không chạy.

## Unresolved Questions

1. Thứ tự idempotency so với guard (H1): ưu tiên replay hay ưu tiên guard? Spec đang mâu thuẫn, cần lead/user quyết định.
2. Có thể gán IP tĩnh cho Traefik trên network `homelab` để thu hẹp `TRUSTED_PROXY_CIDRS` không, hay network đó do stack khác quản lý?
3. Phase 03 sẽ nối `migrationsFS` (`//go:embed`) ở đâu, và job CI `migrations` sẽ được bật như thế nào?
4. Có dự kiến chạy nhiều replica api không? Nếu có thì cần cơ chế invalidation slug cache dùng chung (M6/Low).
