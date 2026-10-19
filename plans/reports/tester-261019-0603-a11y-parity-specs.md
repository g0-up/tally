# Báo cáo: bộ spec parity và accessibility (phase 20)

Ngày: 2026-10-19, 06:03 (Asia/Saigon). Phạm vi: spec Playwright theo "Architecture", "Review procedure" bước 4 và "Implementation steps" 1–4, 8 của phase 20.

## Kết quả nhanh

- `corepack pnpm typecheck` và `corepack pnpm lint` trong `apps/web`: sạch (exit 0). `tsconfig.node.json` đã bao gồm `e2e/**/*.ts`, nên spec mới được typecheck.
- `corepack pnpm exec playwright test --list`: 517 test trong 24 file. Trong đó `visual` 124, `a11y` 190, `owner-session` 1, phần còn lại thuộc các project cũ.
- Chưa chạy spec nào trên stack thật. Các spec cần `make dev`, tenant seed và `E2E_OWNER_PASSWORD`, nên chỉ mới được kiểm bằng typecheck, lint và `--list`.

## File đã tạo hoặc sửa

Tạo mới:
- `apps/web/e2e/visual/parity-states.ts`: 61 state. 40 route lấy từ `MENU_ROUTES`, 5 dialog (4 có baseline prototype, mở bằng `A.reset()`, `A.newADJ()`, `A.newCount()`, `A.newSTM()`, `A.newASM()`), và 16 addition (auth, admin login, onboarding, account closure, not-found, POS offline, hai dialog không có baseline, hai print).
- `apps/web/e2e/visual/masks.ts`: che ngày, giờ, số chứng từ và "Quá N ngày" theo text, vì prototype và app dùng class khác nhau.
- `apps/web/e2e/a11y/axe.spec.ts`: axe (WCAG 2.2 AA tags) trên 61 state, fail khi có `serious` hoặc `critical`; kiểm mọi bảng có caption hoặc tên.
- `apps/web/e2e/a11y/keyboard.spec.ts`: 3 hành trình bàn phím (sidebar, dialog điều chỉnh kho, nút xác nhận kiểm kê).
- `apps/web/e2e/a11y/focus.spec.ts`: focus trap trong dialog, trả focus về opener khi đóng, opener bị xóa thì focus về `#view` (không về body), và focus indicator.
- `apps/web/e2e/a11y/touch-targets.spec.ts`: quét bounding box mọi control của 61 state, ≥44 px ở 390 và ≥24 px ở 1440.
- `docs/operations/device-browser-matrix.md`: ma trận trình duyệt, thiết bị thật, máy quét HID, máy in 80 mm và A4; cách chạy; NVDA và VoiceOver; nơi ghi kết quả.

Sửa:
- `apps/web/e2e/visual/parity.spec.ts`: giữ nguyên test shell cũ. Thêm so sánh route và dialog ở 1440 (maxDiffPixelRatio 0.01) và 390 (0.015), threshold 0.2. Prototype được chụp lúc chạy và ghi vào đường dẫn snapshot trước khi so sánh; addition so với snapshot đã commit.
- `apps/web/playwright.config.ts`: project `visual` và `a11y` (chromium) đều dùng session chủ cửa hàng đã lưu; project `a11y-msedge` chỉ có khi `CI`. Các project tenant cũ thêm `a11y/**` vào `testIgnore` để không chạy trùng. URL host lấy từ `hostURL()` trong `parity-states.ts`, cùng biến môi trường như trước.
- `apps/web/package.json`: thêm `e2e:visual` và `e2e:a11y`. Không đổi script cũ.
- `docs/runbook.md`: thêm mục "Browser, device and peripheral checks" trỏ tới trang matrix, trong mục 1.

Không sửa `apps/web/src/**`, `apps/api/**` hoặc `.github/workflows/*`.

## Đề xuất CI job (chưa áp dụng, phase 01 sở hữu workflow)

Job `ui-gate` trên runner Linux:
1. Cài Node 22.12+, pnpm qua corepack, và trình duyệt Playwright (chromium; kênh msedge nếu chạy a11y-msedge).
2. Khởi động stack (`make dev`), tạo tenant demo seed ngày nghiệp vụ 2026-10-12.
3. Đặt `E2E_OWNER_PASSWORD` từ secret, cùng `E2E_SALES_DOC_ID` và `E2E_STATEMENT_ID` nếu có.
4. Chạy `corepack pnpm -C apps/web e2e:visual` và `corepack pnpm -C apps/web e2e:a11y` với `CI=1`.
5. Tải lên thư mục `playwright-report` khi lỗi.

Hai lệnh này cần được đặt làm required check sau khi chạy ổn định.

## Cần stack thật để chạy

- `make dev` với tenant `cua-hang-a` đã seed, dữ liệu khớp prototype (ngày nghiệp vụ 2026-10-12).
- Chủ cửa hàng có mật khẩu trong `E2E_OWNER_PASSWORD`.
- Tenant demo (sandbox) để nút "Đặt lại mẫu" hiện ra.
- `E2E_SALES_DOC_ID` và `E2E_STATEMENT_ID` để chạy hai state print.
- Linux để chụp baseline parity.

## Các điểm cần lưu ý (DONE_WITH_CONCERNS)

1. **Chưa có spec nào được chạy thật.** Selector dựa trên nhãn tiếng Việt hiện có trong `src` (đã đối chiếu bằng grep) và trên `shell.spec.ts`. Các nhãn chưa đối chiếu được khi chạy: "Mở rộng Kho" / "Thu gọn Kho" (sidebar chưa có trong `src`), tên dialog, và nút "Thu tiền khách".
2. **`shell.spec.ts` không nằm trong phạm vi được giao** nhưng vẫn tìm nhóm sidebar bằng `role="button"`. Khi nhóm trở thành link theo quyết định của chủ (2026-10-19), test này sẽ hỏng. Cần cập nhật bởi người sở hữu file.
3. **Thư mục snapshot prototype cần đưa vào `.gitignore`.** Các ảnh prototype được ghi vào `apps/web/e2e/visual/parity.spec.ts-snapshots/prototype/` mỗi lần chạy. Tôi chưa sửa `.gitignore` (ngoài danh sách file được giao). Đề xuất thêm dòng `apps/web/e2e/visual/parity.spec.ts-snapshots/prototype/`. Lần chạy đầu tiên cần kiểm tra `testInfo.snapshotPath` có khớp đường dẫn mà `toMatchSnapshot` đọc không.
4. **Addition chưa có baseline sẽ fail ở lần chạy đầu** (đây là hành vi có chủ đích). Cần chụp, duyệt, rồi commit.
5. **Chưa được phủ:**
   - 10 trang admin (cần session admin), chỉ có trang đăng nhập admin.
   - State tenant rỗng và state lỗi (cần tenant thứ hai chưa seed).
   - Drawer (cần id dòng cụ thể).
   - Hành trình bàn phím: chỉ 3 trên 9 bước. Các bước POS, giữ giỏ, hóa đơn có ngoại lệ, giao hàng, biên lai, xuất báo cáo và đăng xuất thiết bị chưa được viết vì selector chưa đối chiếu được.
   - Mục 6 và 7 của Review procedure (contrast token, trình duyệt ngoài Chromium trên CI) là phần việc riêng.
6. **Test "opener bị xóa"** xóa phần tử React quản lý. Nếu React ném lỗi khi render lại, test sẽ báo qua pageerror của trang; cần xem khi chạy thật.
7. **Touch target** đo bằng bounding box, không đo vùng bấm thực. Link inline trong đoạn văn có thể không đạt 44 px ở 390. Theo quyết định của chủ, đó là lỗi thật cần sửa, không được miễn trừ tự động.
8. **Thông số đã chọn theo yêu cầu**, không tự đổi: 1440 (0.01), 390 (0.015), threshold 0.2, WCAG tags wcag2a/aa, 21a/aa, 22aa.
9. **Tên báo cáo:** hook yêu cầu mẫu `a11y-e2e-261019-0555-{slug}.md`, còn lead yêu cầu `tester-261019-<HHMM>-a11y-parity-specs.md`. Tôi dùng tên của lead.
10. **Git:** `apps/web/` và `docs/runbook.md` đang là file chưa được theo dõi (untracked) trong git, nên không thể xem diff so với git. Cần commit hoặc kiểm tra lại trạng thái repo trước khi review.

## Câu hỏi chưa giải quyết

- Tenant `cua-hang-a` seed có phải sandbox không? Nếu không, state "Đặt lại mẫu" sẽ không tìm thấy nút.
- Dữ liệu seed có khớp prototype từng số không? Mask chỉ che ngày, giờ, số chứng từ và nhãn tương đối. Số tiền hoặc tồn lệch sẽ làm diff vượt ngưỡng.
- Có nên mở rộng `apps/web/e2e/a11y/` sang firefox và webkit cho axe không? Hiện chỉ chromium và msedge (CI), theo yêu cầu.
- Nên dùng tenant rỗng thứ hai để phủ state rỗng và lỗi, hay chấp nhận để các state này cho phase 21?
