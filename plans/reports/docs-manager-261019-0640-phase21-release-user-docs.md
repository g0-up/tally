# Báo cáo: tài liệu phát hành, hỗ trợ và hướng dẫn người dùng (phase 21, bước 12–13)

Ngày: 2026-10-19, 06:40 (Asia/Saigon). Đây là bản ghi trạng thái, không phải tài liệu chuẩn.

## File đã tạo hoặc sửa

Tạo mới:

- `docs/user-guide/` (tiếng Việt, 11 file):
  - `README.md`: mục lục và giới hạn bản 1.1 (một tài khoản, một kho, không giao nhận);
  - `bat-dau.md`, `ban-hang-pos.md`, `hoa-don-bao-gia-tra-hang.md`, `nhap-hang.md`, `danh-muc-bang-gia.md`, `kho-kiem-ke-gio-qua.md`, `cong-no-quy.md`, `chien-dich-khach-hang.md`, `bao-cao.md`, `cai-dat-goi-bao-mat.md`.
- `docs/support/support-checklist.md` (tiếng Anh, mẫu tin nhắn cho chủ cửa hàng bằng tiếng Việt). Các mục:
  - nguyên tắc;
  - phân loại theo PRD §14.3;
  - phiên hỗ trợ chỉ đọc (`tally_readonly`, audit);
  - lệch sổ (`tally ledger verify`, `tenant maintenance`);
  - đăng xuất cưỡng bức;
  - thông báo sự cố;
  - thanh toán bị từ chối và hoàn tiền tay;
  - đóng tài khoản và xuất dữ liệu;
  - khôi phục tenant.
- `docs/release/acceptance-matrix.md`:
  - mỗi AT-01..18 và NFR-01..16 có một dòng, kèm test có thật, bằng chứng, trạng thái và ghi chú;
  - không dòng nào được đánh dấu "Đạt".
- `docs/release/release-checklist.md`:
  - các ô đánh dấu theo §17, gồm DoD và 6 cổng trước khi thương mại;
  - mục triển khai;
  - bảng ký xác nhận pilot để trống cho chủ cửa hàng.

Sửa:

- `docs/operations/device-browser-matrix.md`:
  - thêm "## 6. Certified peripherals": bảng trống, mỗi loại thiết bị một dòng, ghi "not run";
  - sửa dòng "A4 printer" thành "A4/A5 printer", vì chứng từ bán in khổ A5 (`features/sales/print.css`) còn bảng kê in A4 (`features/debt/print.css`);
  - ghi rõ hai lựa chọn khổ in "Khổ A5" và "Giấy cuộn 80mm".
- `README.md`: thêm một đoạn dẫn tới user guide, support checklist, release checklist và acceptance matrix.

Không tạo `docs/release/device-matrix.md`. Không sửa `docs/runbook.md`, `apps/**`, `infra/**`, `Makefile`, `.github/**` hay `plans/**` (ngoài báo cáo này). Không commit.

## Cách kiểm chứng

- **Nhãn giao diện**:
  - Nhãn tiếng Việt trong user guide và support checklist được đối chiếu bằng grep trên `apps/web/src/features/**`: trang, dialog, nút, tên trường, trạng thái và loại thông báo.
  - Menu admin lấy từ `app/layouts/admin-shell.tsx`; route lấy từ `app/router.tsx`.
- **Lệnh CLI**: đối chiếu với `apps/api/internal/cli/*.go` (`Use`, `Flags()` và chuỗi in ra):
  - `ledger verify --tenant/--sku`. Định dạng dòng lệch là `tenant <id> <domain>: …`, dòng cuối là `verified N tenants: M mismatches`, và lệnh thoát khác 0 khi có lệch;
  - `tenant maintenance on|off --tenant`, `tenant restore-check`, `tenant copy`, `tenant purge`;
  - `maintenance revoke-sessions --tenant <id|slug> --reason`.
  - `ledger rebuild`, `seed dev` và `maintenance expire-exports` trả về `errNotImplemented`.
- **Quy tắc nghiệp vụ**: đọc trực tiếp mã nguồn.
  - Phiên hỗ trợ: `support/session.go`, `support/model.go` (tối đa 8 giờ, cần nhân viên được phân công) và `middleware/support_readonly.go`.
  - Thanh toán bị từ chối: `saas/billing.go`.
  - Chế độ bảo trì: `cli/tenant_restore.go` và `docs/operations/tenant-restore.md`. Mọi thay đổi trả `423 tenant_maintenance`, job nền (kể cả xuất dữ liệu) bị bỏ qua.
  - Khổ giấy in: `print.css`. Máy quét gõ mã rồi Enter: `pos/components/scan-input.tsx`.
- **Đường dẫn test trong acceptance matrix**:
  - đều là file có thật, liệt kê bằng `ls`;
  - phạm vi từng file được đối chiếu với tên hàm `Test…` và tiêu đề `test(…)` của Playwright.
- **Liên kết**:
  - Script kiểm liên kết trong scratchpad dùng slug kiểu GitHub, có hỗ trợ chữ Việt.
  - Phạm vi: `README.md`, `docs/user-guide/*.md`, `docs/support/support-checklist.md`, `docs/release/*.md`, `docs/operations/device-browser-matrix.md`.
  - Kết quả: 167 liên kết tương đối, 0 lỗi.
  - Script đã được thử với một anchor sai và một file thiếu, và bắt được cả hai.
  - Các spec AT dự kiến (`apps/web/e2e/acceptance/at-NN-*.spec.ts`) chỉ xuất hiện trong code span và được ghi rõ "chưa viết", nên không cần ngoại lệ.
- **Hạng mục dự kiến** (kiểm lúc 06:40):
  - Chưa có: các target `acceptance`, `load-test`, `load-seed`, `soak-test`, `restore-drill`, `security-scan` trong `Makefile`; `tally seed load`; `infra/scripts/restore-drill.sh`; `apps/web/e2e/acceptance/`; `apps/api/test/isolation/`. Tài liệu ghi các mục này là *(planned)*.
  - Đã có, nên được liên kết thật: `load/k6/*.js`, `load/k6/thresholds.json`, `load/README.md`, `infra/docker-compose.backup.yml`.

## Việc cho team lead

1. **`docs/runbook.md` có một câu đã cũ.** Mục "API binary (`tally`)" ghi `ledger verify` "not implemented in this build". Thực tế lệnh này đã chạy được (`cli/ledger.go`). Câu đúng là: chỉ `seed dev`, `ledger rebuild` và `maintenance expire-exports` chưa có. Bảng lệnh cũng nên thêm `ledger verify` và `tenant maintenance/restore-check/copy/purge`. Agent sở hữu runbook cần sửa.
2. **Khi `make acceptance`, `load-test` và các target khác được thêm vào**, cần bỏ nhãn *(planned)* trong `docs/release/*.md` và trong cột ghi chú của acceptance matrix.
3. **AT-15 đang bị chặn** vì `tally ledger rebuild` chưa có. Support checklist ghi rõ là không hứa rebuild tự động, và sửa lệch sổ bằng khôi phục hoặc migration có duyệt.

## Đính chính và kiểm lại (06:41)

- Lead báo `tally ledger rebuild` đã có (`cli/ledger.go:139`). Tôi kiểm lại và thấy dòng 139 chỉ là phần khai báo lệnh (`Use`/`Short`). Ở dòng 142, `RunE` vẫn trả `errNotImplemented("ledger rebuild")`.
- Chạy thử `APP_ENV=development go run ./cmd/tally ledger rebuild --tenant <uuid> --dry-run` cho kết quả `error: ledger rebuild is not implemented in this build`, thoát với mã 1.
- Vì vậy tôi không sửa tài liệu. AT-15 vẫn ghi "Bị chặn", với hai lý do: thiếu `ledger rebuild` và thiếu `infra/scripts/restore-drill.sh`. Support checklist vẫn ghi rebuild chưa có.
- Các lệnh khác được ghi là còn thiếu, kiểm lại bằng grep `errNotImplemented(` trong `cli/*.go`:
  - chỉ có `seed dev` (`seed.go:28`), `maintenance expire-exports` (`maintenance.go:53`) và `ledger rebuild` (`ledger.go:142`);
  - `seed.go` chỉ có `dev` và `demo`, nên `seed load` chưa có.
- Khi rebuild được cài thật, cần sửa ở hai nơi:
  - dòng AT-15 trong `docs/release/acceptance-matrix.md`;
  - mục "3. Ledger drift" bước 3 trong `docs/support/support-checklist.md`.

## Cập nhật sau p21-infra (07:10)

Đối chiếu với `Makefile`, `apps/api/internal/cli/seed_load.go`, `apps/api/internal/seedload/options.go`, `infra/scripts/restore-drill.sh`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `infra/monitoring/alerts.yml`, `apps/web/Dockerfile` và `apps/web/nginx.conf`.

- `release-checklist.md`: bỏ mọi nhãn *(planned)* và dòng giải thích nhãn. Ghi điều kiện chạy lấy từ Makefile: `make acceptance` cần `make dev` và `E2E_OWNER_PASSWORD`; load/soak chạy trên stack giống production, không chạy trên production, và kết thúc bằng `ledger verify`. Cách cấu hình trỏ về `load/README.md`, không chép lại. Restore drill phải ra báo cáo PASS; chạy `--db-only` chỉ ra PARTIAL. CI chạy `make security-scan` không kèm image; workflow release quét từng image bằng trivy trước khi push.
- `acceptance-matrix.md`:
  - Mục Evidence ghi thư mục output thật: `load/test-results/` và `test-results/restore-drill/`.
  - `apps/web/e2e/acceptance/` vẫn chưa có. Các spec vẫn để "chưa viết"; CI nightly bỏ qua suite này và in cảnh báo.
  - AT-03 bỏ "not shipped yet".
  - NFR-04 và NFR-07 đổi từ "Bị chặn" sang "Chưa chạy" vì đã có công cụ.
  - Sửa ghi chú của NFR-03 và NFR-08.
  - NFR-09: alert rule đã có, nhưng stack chưa chạy Prometheus hay Alertmanager.
- Cổng 80: các tài liệu của tôi không nhắc cổng của container web, nên không phải sửa. Đã xác nhận `USER nginx`, `EXPOSE 8080` và `listen 8080`.
- Giữ nguyên theo chỉ đạo của lead: `seed dev`, `ledger rebuild`, AT-15 và mục "3. Ledger drift". Ghi chú AT-15 vẫn nói restore drill là "planned", câu này nay đã sai. Sẽ sửa cùng lúc cập nhật `ledger rebuild`.
- Hai dòng ở mục khoảng trống dưới đây nay đã cũ: alert rule đã có (NFR-09), và CI đã có scan bảo mật (NFR-08). Uptime probe và SLO dashboard (NFR-06) vẫn chưa có.
- Kiểm link: 173 liên kết, không lỗi.

## Cập nhật sau p21-go (09:16)

Đã đối chiếu với `cli/ledger_rebuild.go`, `cli/seed_dev.go`, `cli/seed_load.go`, `cli/maintenance.go`, `cli/worker.go`, `reporting/export.go`, `saas/data_export.go`, `platform/db/tenants.go`, `apps/api/go.mod`, `infra/backup/Dockerfile` và `infra/docker-compose.homelab.yml`. Đã build binary và chạy `--help` của `ledger rebuild`, `seed` và `maintenance`. Với `APP_ENV=production`, `seed dev` bị từ chối. Truyền cả `--dry-run` lẫn `--confirm` thì cũng bị từ chối.

- **`docs/runbook.md`**
  - Thêm vào bảng CLI các lệnh `ledger rebuild` (mặc định dry-run, `--confirm` mới ghi, ghi chú phải backup trước), `maintenance expire-exports` (link tải hết hạn sau 24 giờ với export báo cáo và 7 ngày với export toàn bộ dữ liệu) và `seed dev`.
  - `seed load` có thêm câu về việc chặn production và tốc độ khoảng 95 moves/s.
  - Bỏ đoạn "not implemented in this build", thay bằng một câu dẫn sang quy trình lệch sổ.
  - Thêm `make seed` vào Everyday commands.
  - Dòng `tenant maintenance` ghi chính xác: mọi thay đổi bị từ chối với 423, các job nền bị bỏ qua, vẫn đọc được.
- **`docs/support/support-checklist.md`, mục "3. Ledger drift"**: bước 3 nay theo trình tự verify → dry-run → nếu K = 0 thì `make db-backup STACK=prod` rồi chạy `--confirm` → verify lại → mới tắt maintenance. Các chuỗi output trích nguyên văn từ code.
- **`docs/release/acceptance-matrix.md`**
  - AT-09 và NFR-01 dẫn tới bộ cô lập mới cùng test worker và vai trò; bằng chứng là báo cáo p21-go (chạy trên máy dev).
  - AT-15 đổi từ "Bị chặn" sang "Chưa chạy". Đã có rebuild cùng test tích hợp, đã có script diễn tập. Diễn tập mới chạy phần CSDL một lần, trước khi image wal-g chuyển sang non-root, và chưa chạy lại từ đó. Diễn tập đầy đủ chưa chạy lần nào. Script diễn tập không gọi `rebuild`.
  - NFR-03 có thêm search gate (p95 202 ms trên máy dev).
  - NFR-07 ghi rằng diễn tập chưa chạy lại sau khi chuyển non-root.
  - NFR-08 chuyển sang "Có test tự động một phần". Lần quét đầu có phát hiện, các lỗi đã được sửa, nhưng chưa có lần quét đầy đủ nào sạch. Bốn điểm yếu còn mở được liệt kê.
  - NFR-12 ghi rằng export hết hạn đã bị xóa.
- **`docs/release/release-checklist.md`**: gate 4 nói rõ OPEN-11 nay chỉ còn là chuyện file trên object store của cửa hàng đã bị purge.
- Không dòng nào được ghi "Đạt". Kiểm link: 194 liên kết (thêm runbook và `docs/operations/*`), không lỗi.
- **Lệch trạng thái cần lead quyết**: `apps/web/e2e/acceptance/` nay có đủ `at-01`..`at-18` và `fixtures/`. Theo chỉ đạo trước, tài liệu vẫn ghi "chưa viết", chờ lead xác nhận các spec đã xong.

## Cập nhật sau acceptance specs (09:21)

Lead đã xác nhận các spec đã viết xong, nên mục lệch trạng thái ở phần trên đã được giải quyết. Đã đối chiếu với `apps/web/e2e/acceptance/at-*.spec.ts`, `fixtures/acceptance.ts`, `playwright.config.ts` (project `acceptance`, reporter), `package.json` (`e2e:acceptance`), target `acceptance` của `Makefile`, `.gitignore`, job `e2e-nightly` trong `.github/workflows/ci.yml`, `cli/ledger.go` (cờ `--tenant`, dòng `N mismatches`), `cli/seed.go` (`seed demo --parent`), và báo cáo `plans/reports/tester-261019-0704-acceptance-specs.md`.

- **`docs/release/acceptance-matrix.md`**
  - Bảng trạng thái có thêm giá trị "Đã viết spec, chưa chạy".
  - Đoạn "not written yet" được thay bằng mục mới "Running the acceptance specs". Mục này ghi: lệnh chạy (`make acceptance` hoặc `corepack pnpm e2e:acceptance` trong `apps/web`); stack cần `make dev` và `make seed`; biến `E2E_OWNER_*` và `E2E_LEDGER_VERIFY_CMD`; nên chạy tay trên sandbox `cua-hang-a-demo`; nhóm AT chỉ chạy với `APP_ENV=test`.
  - Mục này cũng ghi quy tắc tính Đạt: phải không có annotation `skipped-step` và output phải có `0 mismatches`. Job nightly không đặt `E2E_LEDGER_VERIFY_CMD`, nên lần chạy nightly xanh không đủ để tính Đạt.
  - Mục Evidence: trace và screenshot luôn bật, lưu ở `apps/web/e2e/acceptance/evidence/<ngày>/` (ngày theo giờ Việt Nam, đã git-ignore). HTML report chỉ sinh khi có biến `CI`.
  - AT-01..14 và AT-16..18: cột test có link tới spec, trạng thái là "Đã viết spec, chưa chạy". Ghi chú nêu biến cần đặt, các nhãn và body API chưa được xác nhận, và phần kịch bản chưa phủ, theo mục Concerns của báo cáo tester.
  - AT-15: có link tới spec, nhưng trạng thái vẫn là "Chưa chạy", vì spec luôn skip và bằng chứng là báo cáo diễn tập, mà diễn tập chưa chạy.
- **`docs/release/release-checklist.md`**: lệnh ở gate 2 dẫn tới mục "Running the acceptance specs". Bỏ câu "not written yet".
- Không dòng nào được ghi "Đạt". Kiểm link: 216 liên kết, không lỗi.
- **`docs/runbook.md`, dòng `seed dev`**: đối chiếu với `apps/api/internal/cli/seed_dev.go` (`reportExistingDevStore`) và output của `go run ./cmd/tally seed dev --help`. Khi `cua-hang-a` đã có, đủ ít nhất 40 SKU và 400 stock move, lệnh không đổi gì và in ra số SKU và số stock move. Nếu cửa hàng có ít hơn, do `tenant create` tạo ra hoặc do seed dev trước đó hỏng giữa chừng, lệnh báo lỗi: phải xóa cửa hàng đó hoặc reset DB rồi chạy lại.
- **Cách dựng binary `tally` ở lượt p21-go (09:16)**: chạy `go build -o <scratchpad>/tally ./cmd/tally` trong `apps/api`, xuất file ra thư mục scratchpad của phiên, ngoài repo. Sau đó chạy `--help` của `ledger rebuild`, `seed`, `seed load` và `maintenance` bằng binary đó. Binary đã được xóa. Từ lượt này trở đi chỉ dùng `go run ./cmd/tally ... --help`.
- **Điểm lệch so với chỉ đạo**
  - Spec acceptance không đọc `E2E_ADMIN_*`. Biến này chỉ có trong `e2e/admin.spec.ts`, nên tài liệu không liệt kê nó là biến cần cho acceptance.
  - Comment của `EVIDENCE_DIR` trong `fixtures/acceptance.ts` nói thư mục này chứa cả HTML report. Thực tế reporter `html` chỉ bật khi có `CI` và ghi vào `playwright-report/`.
  - Job nightly chỉ upload artifact khi fail. Danh sách artifact không gồm thư mục `evidence/`.

## Cập nhật sau khi lead sửa CI (09:24)

Đã đối chiếu với `.github/workflows/ci.yml` (job `e2e-nightly`: biến `E2E_LEDGER_VERIFY_CMD` và bước "Upload acceptance evidence" với `if: always()`, giữ 90 ngày) và với comment mới của `EVIDENCE_DIR`.

- **`docs/release/acceptance-matrix.md`**
  - Bỏ câu "nightly không đặt `E2E_LEDGER_VERIFY_CMD`". Thay bằng một gạch đầu dòng "Nightly CI": job chạy `make acceptance` có đặt verify cmd, và lưu bằng chứng của mọi lần chạy, kể cả khi pass, thành artifact `acceptance-evidence`.
  - Gạch đầu dòng này ghi rõ cấu hình trên chưa từng chạy trên CI. Job không dựng stack với `APP_ENV=test`, nên AT-10, AT-11 (fault hook), AT-13 và AT-18 sẽ bị skip trên nightly và cần một lần chạy riêng.
  - Mục Evidence: khi lưu trữ theo lần chạy CI, dẫn tới artifact `acceptance-evidence`.
- Kiểm link: 217 liên kết, 0 lỗi. Không dòng nào được ghi "Đạt".
- **Trả lời lead: dựng binary `tally` bằng lệnh nào.** Ở lượt p21-go (09:16) tôi chạy `go build -o <scratchpad của phiên>/tally ./cmd/tally` trong `apps/api`. Binary nằm ngoài repo, dùng để chạy `--help` của `ledger rebuild`, `seed`, `seed load` và `maintenance`. Binary đã bị xóa. Từ lượt `seed dev` trở đi tôi chỉ dùng `go run ./cmd/tally ... --help`.
- **Điểm cần lead kiểm (chưa sửa, ngoài phạm vi docs):** khối `env` của job `e2e-nightly` không có `DATABASE_URL`. Repo cũng không có `apps/api/.env`, vì file này bị git-ignore. Trong khi đó `platform/config/config.go` bắt buộc phải có `DATABASE_URL`. Nếu stack của job không tự đặt biến này, các bước `make migrate-up`, `make seed`, `make dev-api` và cả `E2E_LEDGER_VERIFY_CMD` có thể đều không kết nối được DB.

## Cập nhật sau các bản sửa review của p21-go (10:14)

Đã đối chiếu với các file sau:
- `cli/seed_load.go` (`requireDevelopmentOrTest`) và `cli/seed_dev.go`;
- `cli/ledger.go` (`requireAppRole` và thông báo từ chối);
- check trạng thái trong `procurement/verify.go`, `sales/verify.go`, `kits/verify.go`;
- `Makefile` và `apps/api/Makefile` (giá trị mặc định `APP_ENV ?= development`);
- `infra/docker-compose.prod.yml` (`APP_ENV: ${APP_ENV:-production}`);
- job `e2e-nightly` trong `.github/workflows/ci.yml`;
- spec AT-11.

Đã xem `--help` của `ledger rebuild` và `ledger verify` bằng `go run ./cmd/tally ... --help`, không dùng `go build`.

- **`docs/runbook.md`**
  - `ledger verify`: kiểm thêm việc trạng thái PO, hóa đơn và lệnh lắp ráp khớp với số lượng trên các dòng. Lệnh chỉ chạy bằng role ứng dụng (`DATABASE_URL` của API); superuser, role có BYPASSRLS và `MIGRATE_DATABASE_URL` đều bị từ chối.
  - `ledger rebuild`: thêm `[--statement-timeout 10m]` (0 là không giới hạn, thay cho `DB_STATEMENT_TIMEOUT` của API). Lệnh sửa cả trạng thái suy ra từ số lượng và cũng chỉ chạy bằng role ứng dụng.
  - `seed dev` và `seed load`: bỏ câu "Refused with `APP_ENV=production`". Nay hai lệnh chỉ chạy khi `APP_ENV` là `development` hoặc `test`; nếu `APP_ENV` không được đặt thì bị từ chối. `make seed` mặc định đặt `development`.
  - Dòng `seed load` có thêm ghi chú: `make load-seed` chạy trong stack giống production, nơi `APP_ENV` mặc định là `production`, nên file env của stack đó phải đặt `development` hoặc `test`.
- **`docs/release/acceptance-matrix.md`**
  - `E2E_LEDGER_VERIFY_CMD` phải chạy bằng role ứng dụng.
  - Gạch đầu dòng "Nightly CI":
    - job đặt `APP_ENV=test` và `DATABASE_URL` (role ứng dụng), nên AT-10, AT-13 và AT-18 chạy được;
    - AT-11 vẫn bị skip, vì spec cần cả `TALLY_FAULT_INJECT` lúc khởi động API lẫn `E2E_FAULT_INJECT_ARMED=1`, mà job không đặt hai biến này (`requireEnv` ở dòng 23 của spec);
    - vẫn ghi rõ cấu hình này chưa từng chạy trên CI.
- Kiểm link: 217 liên kết, 0 lỗi. Không dòng nào được ghi "Đạt".
- **Ngoài quyền sửa, chuyển cho lead**
  - **`ci.yml`, job `e2e-nightly`:** khối `env` có hai lần `DATABASE_URL:` với cùng giá trị, một lần cạnh `MIGRATE_DATABASE_URL` và một lần sau `E2E_LEDGER_VERIFY_CMD`. Trùng key trong YAML có thể làm GitHub Actions từ chối workflow. Nên xóa một dòng.
  - **`make load-seed`:** chạy `seed load` trong project `tally-prod`, với `--env-file` là `infra/.env`. `infra/.env.example` đặt `APP_ENV=production`, nên `make load-seed` sẽ bị từ chối trừ khi file env của stack load đặt `development` hoặc `test`. `load/README.md` (ngoài `docs/`) chưa nhắc điều này.
  - **`infra/.env.example`:** comment trên `APP_ENV` đã khớp với quy tắc mới, không cần sửa.
  - Concern trước của tôi rằng nightly "thiếu `DATABASE_URL`" đã được giải quyết, vì job nay đặt biến này.

## Khoảng trống sản phẩm phát hiện khi viết (chưa sửa, ngoài phạm vi docs)

- Chưa có:
  - nút tạo lại mã khôi phục MFA (phải tắt rồi bật lại MFA);
  - màn hình tạo chiến dịch;
  - màn hình lập biên bản bù trừ công nợ;
  - màn hình chuyển quỹ hay ghi chi phí (OPEN-13);
  - màn hình quản lý phương thức thanh toán;
  - giao diện CRM cho loyalty, timeline và việc chăm sóc (mới có API).
- Nội dung giao diện trỏ tới trang không tồn tại: lỗi POS nhắc "Cài đặt › Thanh toán", dialog đóng PO nhắc "Cài đặt → Lý do".
- `GET /status` có dữ liệu sự cố, nhưng chưa màn hình nào của app cửa hàng hiển thị. Dialog "Công bố sự cố" lại ghi "hiện trên trang trạng thái của mọi cửa hàng". Câu này sai cho tới khi có giao diện.
- Admin console không có nút gửi lại link tải bản xuất cuối của closure đã hoàn tất (chỉ có API `account-closure:retry-export`).
- Chưa có alert rule nào (NFR-09), uptime probe hay SLO dashboard (NFR-06).
- CI chưa có scan bảo mật (NFR-08).

## Quyết định cần chủ sản phẩm chốt

- Khung giờ hỗ trợ (PRD §14.3), cần công bố trước khi bán. Support checklist đang để trống.
- Bảng giá các gói (đang "Chưa chốt"). Mục này chặn cổng §17.4.
- Retention của file trên object store (OPEN-11). Hiện purge không xóa các file này.
- OPEN-14 (cutover) và OPEN-15 (xuất dữ liệu): AT-12 và AT-14 đang theo mặc định.
- OPEN-18 (đổi hàng khi khách đang nợ): nửa bán sỉ của AT-07.
- Số tenant mục tiêu cho load test (OPEN-07, mặc định 50).
- Danh sách máy in và máy quét cụ thể sẽ chứng nhận (mục §6 đang trống).

Status: DONE_WITH_CONCERNS
Summary: Đã viết user guide tiếng Việt (11 file), support checklist, acceptance matrix (AT-01..18, NFR-01..16, chưa dòng nào "Đạt"), release checklist kèm bảng ký pilot, thêm §6 vào device matrix và link trong README. 167 liên kết tương đối đã kiểm, không lỗi; mọi lệnh CLI và nhãn giao diện đã đối chiếu với mã nguồn.
Concerns/Blockers: Câu về `ledger verify` trong runbook đã cũ (agent sở hữu runbook cần sửa). (07:10) Các target make đã có, docs đã cập nhật. AT-15 bị chặn vì `ledger rebuild` chưa có. Còn chờ chủ sản phẩm chốt giờ hỗ trợ, bảng giá, OPEN-11/14/15/18 và danh sách thiết bị chứng nhận.
