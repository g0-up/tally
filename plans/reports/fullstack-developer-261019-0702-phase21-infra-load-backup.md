# Phase 21 — hạ tầng tải, sao lưu, CI và vận hành

Ngày 2026-10-19, chế độ --auto (không có --yagni). Phạm vi: phần infra/load/backup/CI của
`phase-21-acceptance-load-security-release.md`. Không commit, không đẩy, không chạy tải thật, không
dựng image.

## Kết quả

Đã giao đủ 8 hạng mục. Mọi kiểm tra tĩnh trong tiêu chí nghiệm thu đều qua. Còn mối lo ở những phần
chỉ chạy được khi có image hoặc stack thật (xem "Chưa chạy").

## Đã làm

| File | Nội dung |
|---|---|
| `load/k6/lib/tally.js`, `load/k6/{lookup,posting,reports,pos-devices,soak}.js`, `load/k6/thresholds.json`, `load/README.md` | Kịch bản k6 theo `openapi.yaml`, gồm: đăng nhập qua host-context/CSRF; `Idempotency-Key` cho mọi lệnh ghi sổ; `If-Match` khi xác nhận hóa đơn; tiền và số lượng dạng chuỗi thập phân. Ngưỡng: tra cứu p95<1s, ghi sổ p95<2s, báo cáo p95<5s, lỗi <0.5%. Chỉ 409 `insufficient_stock`, `sale_changed` và `shift_changed` được tính là mong đợi. `pos-devices` cho 3 máy mỗi tenant tranh nhau bán `POS_LAST_UNITS` đơn vị cuối, rồi kiểm tra tồn kho cuối bằng 0. Giá trị mặc định khớp `tally seed load`: slug `load-42-N`, owner `owner@<slug>.load.tally.test`. |
| `infra/docker-compose.backup.yml`, `infra/postgres/postgresql.prod.conf`, `infra/backup/Dockerfile`, `infra/backup/walg-sidecar.sh`, `infra/.env.example` | Sidecar wal-g lo lưu trữ WAL liên tục với `archive_timeout=300s` (RPO ≤ 15 phút). Base backup chạy hằng ngày lúc `BACKUP_BASE_TIME` và giữ `BACKUP_RETAIN_FULL` bản. Mã hóa bằng libsodium. Healthcheck báo lỗi khi WAL chờ quá 15 phút, archiver lỗi, hoặc base backup cũ hơn 26 giờ. Image chạy non-root (`USER postgres`). |
| `infra/scripts/restore-drill.sh` | Diễn tập khôi phục có đo lường. Các tùy chọn: `--target-time`, `--tenant`, `--keep`, `--db-only`, `--primary`. Dữ liệu được khôi phục vào volume và mạng nội bộ riêng (không có egress), replay WAL, rồi promote. Sau đó đo RPO/RTO, chạy `ledger verify` và `tenant restore-check`, rồi ghi báo cáo vào `test-results/restore-drill/`. Script có `--help` và dùng `set -euo pipefail`. |
| `Makefile` | Thêm các target `load-seed`, `load-test`, `soak-test`, `acceptance`, `restore-drill` và `security-scan`, kèm dòng help. Thêm `BACKUP_COMPOSE`, mặc định gộp overlay backup vào mọi lệnh prod. |
| `.github/workflows/ci.yml` | Job docker có thêm: compose với overlay backup, image sidecar, shellcheck, `restore-drill --help`, promtool check/test, `k6 inspect`. Thêm job `security-scan`. Thêm job `e2e-nightly` (lịch 02:30 giờ VN, hoặc `workflow_dispatch`) chạy 3 bộ: acceptance, visual (ui-gate) và a11y. Bộ nào chưa có thư mục sẽ được bỏ qua kèm cảnh báo. |
| `.github/workflows/release.yml` (mới) | Khi có tag `v*`: dựng image api/web/walg, quét trivy (lỗi HIGH/CRITICAL có bản sửa sẽ chặn release), đẩy lên GHCR, ký bằng cosign keyless, rồi chạy `cosign verify`. |
| `infra/monitoring/alerts.yml`, `infra/monitoring/alerts.test.yml` (mới) | 13 luật, chỉ dựa trên metric có thật trong `apps/api/internal/platform/metrics`. Kèm unit test promtool. |
| `docs/runbook.md` | Viết lại §3 Backup and restore: lưu trữ liên tục, dump logic, diễn tập có đo lường, khôi phục prod về một thời điểm. Trong §4: thêm lệnh make mới vào Routine operations, thêm mục "Upgrade and upgrade rehearsal" (NFR-14) và "Alerts and observability" (anchor được `alerts.yml` trỏ tới). Không đụng §1 (gồm "Browser, device and peripheral checks") và §2 (đã so diff dòng 1–151, không đổi). |

## Lệnh kiểm tra và kết quả (chạy lại trên trạng thái cuối)

| Lệnh | Kết quả |
|---|---|
| `docker run grafana/k6:2.3.0 inspect k6/<script>.js` cho cả 5 script | rc 0 |
| `make help` | liệt kê đủ 6 target mới |
| `make -n <target> ENV_FILE=infra/.env.example` cho 6 target | rc 0 |
| `infra/scripts/restore-drill.sh --help` | rc 0 |
| `koalaman/shellcheck:v0.11.0 restore-drill.sh walg-sidecar.sh` | rc 0 |
| `docker compose -f prod -f homelab -f backup --env-file infra/.env.example config -q` | rc 0 |
| `rhysd/actionlint:1.7.12 -no-color ci.yml release.yml` | rc 0 |
| `promtool check rules alerts.yml` / `test rules alerts.test.yml` (prom/prometheus:v3.15.0) | SUCCESS, 13 luật / SUCCESS |
| `make security-scan` (đã chạy thật một lần) | exit 2, có phát hiện; xem "Phát hiện bảo mật" |
| summary của k6 ghi bằng uid của người gọi (script noop) | ghi được |

Đã đối chiếu đường dẫn và body trong k6 với `apps/api/api/openapi.yaml`. Đã đối chiếu các tham số
`seed load` với `apps/api/internal/cli/seed_load.go` và `internal/seedload/options.go`, và chúng
không đổi ở lần kiểm tra cuối.

## Chưa chạy và lý do

- **Chưa dựng image nào** (`tally-walg`, cũng như `tally-api`/`tally-web` mà diễn tập cần): hook chặn
  lệnh có chữ "build", và brief yêu cầu không né. Bước "Backup sidecar image" trong CI và
  `release.yml` sẽ là lần dựng đầu tiên.
- **Diễn tập khôi phục đầy đủ** (phần API, `ledger verify`, `restore-check`): cần image api. Ở phiên
  trước, phần CSDL đã chạy với image dựng tạm và đã tìm ra hai lỗi trong Dockerfile/sidecar (mục
  passwd của wal-g, quyền sở hữu spool), cả hai đã sửa. Sau đó image chuyển sang non-root (`USER
  postgres`, sửa lỗi trivy DS-0002) và script dùng `--user 0`, nhưng **chưa chạy lại diễn tập sau
  thay đổi này**.
- **Tải thật và soak**: brief cấm.
- **Workflow nightly và release**: không đẩy nên không chạy. Chỉ actionlint đã qua.
- **Quy trình đổi volume khi khôi phục prod về một thời điểm** (runbook §3) mới chỉ viết, chưa thử.

## Khoảng trống metric (đã ghi trong runbook "Alerts and observability")

- Stack chưa có Prometheus hay Alertmanager. Cả hai listener metric bind `127.0.0.1` trong container,
  nên muốn scrape thì cần `METRICS_ADDR=0.0.0.0:<port>` trên một mạng không publish.
- Probe edge: chưa có. Hiện chỉ có bước kiểm tra thủ công (bước 7 của checklist).
- Lệch sổ: chưa có metric. Đề xuất cron chạy `tally ledger verify` hằng đêm.
- WAL chờ quá 15 phút: chỉ thể hiện qua docker health của sidecar, chưa có metric hay exporter, và
  chưa có gì cảnh báo khi container unhealthy.
- Job lỗi: mới phủ outbox (`dead` và `retry`). Export báo cáo, gửi chiến dịch và job theo lịch chưa
  có metric lỗi.
- Webhook thanh toán: chỉ phân theo mã trạng thái, chưa tách theo nguyên nhân (chữ ký, payload, xử lý).

## Phát hiện bảo mật (ngoài phạm vi sửa của tôi)

- `apps/api`: govulncheck báo GO-2026-6617/6612/6611/6603 ở `golang.org/x/net` v0.58.0 (cần nâng lên
  v0.60.0) và GO-2026-5676 ở `quic-go` v0.59.0 (cần v0.59.1). Trivy báo CVE-2026-17106 ở
  `moby/go-archive` v0.2.0 (cần 0.3.0).
- `apps/web/Dockerfile`: trivy DS-0002 (image chạy bằng root). Đã sửa theo yêu cầu bổ sung, xem
  mục dưới.
- gosec chạy qua golangci-lint (tôn trọng `//nolint:gosec`): 0 vấn đề. pnpm audit --prod: sạch.
  gitleaks: không có rò rỉ.

## Quyết định cần chủ dự án

1. Overlay backup bật mặc định trong prod (`BACKUP_COMPOSE`), vì vậy `make prod-up` cần đủ biến
   `WALG_*`, gồm khóa libsodium và một bucket riêng. Mất khóa thì mọi bản backup đều vô dụng, và
   đổi khóa thì phải chụp base backup mới.
2. Sidecar kết nối bằng superuser postgres để gọi `pg_backup_start`/`pg_backup_stop`. Có thể tạo
   role hẹp hơn nếu muốn.
3. Giờ chạy: base backup lúc 02:30, nightly CI cũng lúc 02:30 giờ VN.
4. Có chấp nhận quy trình khôi phục prod về một thời điểm hay không, khi nó chưa được thử.

## Mối lo khác

- `seed dev` vẫn `errNotImplemented`, nên bước `make seed` của nightly e2e sẽ lỗi cho tới khi có. Cũng
  cần tạo secret `E2E_OWNER_PASSWORD` và các biến `E2E_SALES_DOC_ID`/`E2E_STATEMENT_ID`.
  `apps/web/e2e/acceptance` chưa tồn tại, nên bộ acceptance bị bỏ qua kèm cảnh báo.
- `load-seed` gửi mail xác minh qua SMTP của stack: owner ở `.load.tally.test`, nên cần dùng Mailpit.
  Chạy tải cần `TRUSTED_PROXY_CIDRS` cùng Traefik cục bộ. Nếu không, mọi máy dùng chung một IP và
  bị 429.
- `pos-devices` làm thay đổi tồn kho, nên mỗi lần chạy lại cần seed với `LOAD_SEED` mới. Seed phải
  không có hạn mức công nợ hay giá tối thiểu, nếu không sẽ sinh 409 `exception_required`.
- Các file docs ngoài phạm vi chưa khớp với trạng thái mới:
  - `docs/release/release-checklist.md` và `acceptance-matrix.md` vẫn ghi các lệnh make mới là
    "*(planned)*".
  - `acceptance-matrix.md` còn ghi `tally seed load` "not shipped yet".

  Cần docs-manager cập nhật.
- Đã tạo docker volume `tally-trivy-cache` (chỉ là cache, xóa được). Không còn container diễn tập
  hay tiến trình nền nào.

## Bổ sung theo yêu cầu lead: mục "API binary (`tally`)" trong runbook

Đã đối chiếu với `apps/api/internal/cli/*.go` (root.go đăng ký serve, worker, migrate, seed, tenant,
ledger, maintenance, admin, healthcheck; control.go và identity.go chỉ là helper, không có lệnh).
Bảng lệnh được viết lại như sau:

- Thêm các lệnh đang có: `ledger verify` (`--tenant`, `--sku`); `tenant maintenance on|off`,
  `restore-check`, `copy`, `purge`; `seed load`; `admin create`; cờ `healthcheck`.
- Cập nhật dòng `worker`: 8 cờ interval và các job theo lịch.
- 3 lệnh đã kiểm thấy còn trả `errNotImplemented`: `seed dev` và `ledger rebuild`
  (`--tenant`/`--dry-run`) ghi "in progress"; `maintenance expire-exports` ghi "not available"
  (worker đã tự xử lý hết hạn export). Cờ xác nhận khi ghi của `rebuild` chưa có trong code nên chưa ghi.
- `restore-drill.sh` chỉ gọi `ledger verify` và `tenant restore-check`, không gọi `rebuild` (đã grep).
- Mục "Browser, device and peripheral checks" không đổi (đã diff).

## Bổ sung: container web chạy non-root (DS-0002)

- `apps/web/Dockerfile`:
  - Thay base `nginx:1.27-alpine` bằng `nginxinc/nginx-unprivileged:1.27.5-alpine`, cùng phiên bản
    nginx 1.27.5. Tag `1.27-alpine` hiện trỏ cùng digest.
  - Thêm `USER nginx` (uid 101) và đổi sang `EXPOSE 8080`.
- `apps/web/nginx.conf`: `listen 8080`.
- `infra/docker-compose.prod.yml`: healthcheck web đổi thành `http://127.0.0.1:8080/`.
- `infra/docker-compose.homelab.yml`: nhãn Traefik `tally-web.loadbalancer.server.port: "8080"`.
- `docs/runbook.md`: sơ đồ topology §4 đổi thành `web:8080`.
- Không có chỗ tham chiếu nào khác tới cổng web: không có cổng nào được publish ra host, và
  Makefile/CI chỉ tham chiếu tên image.

Kiểm tra (không dựng image):

| Kiểm tra | Kết quả |
|---|---|
| `docker compose config` với `.env.example`: prod; prod + homelab + backup | rc 0. Bản merge có `server.port "8080"` và healthcheck `:8080` |
| `nginx -t` chạy dưới `--user nginx`, mount conf của repo | syntax ok, rc 0 |
| Chạy image unprivileged với `--read-only --tmpfs /tmp`, mount conf và một webroot giả | Healthcheck wget rc 0. `/some/route` trả 200 `no-cache`. `/assets/a.js` trả 200 `immutable`. `/api/x` trả 404. Master và worker đều chạy bằng user `nginx`. Container tạm đã xóa |
| trivy config `apps/web` (0.76.0, HIGH/CRITICAL) | 0 misconfiguration |

Rủi ro thấp. Image thật sẽ được dựng lần đầu ở CI và release. Docs ngoài phạm vi vẫn ghi `web:80`:
`plans/.../design/architecture.md` (dòng 51 và 319) và `phase-01-monorepo-docker-infra.md` (dòng 47).
Việc nâng nginx lên dòng mới hơn 1.27 là quyết định riêng, chưa làm.

Status: DONE_WITH_CONCERNS
Summary: Đã giao đủ k6, backup wal-g, restore-drill, Makefile, CI (security-scan, nightly e2e, release ký bằng cosign), alerts và runbook; mọi kiểm tra tĩnh đều qua.
Concerns/Blockers: DS-0002 web đã sửa. Chưa dựng image nào và chưa chạy lại diễn tập sau khi image chuyển sang non-root. Còn khoảng trống metric (edge, lệch sổ, tuổi WAL, job ngoài outbox) và lỗ hổng dependency trong apps/api. `seed dev` chưa có nên nightly e2e chưa chạy được. Docs release còn ghi "planned".
