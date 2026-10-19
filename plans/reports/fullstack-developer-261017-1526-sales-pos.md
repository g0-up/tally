# Báo cáo triển khai Phase 09: Sales, POS, Returns & Exchanges

Trạng thái: **đang tiến hành, chưa kiểm chứng**. Toàn bộ code dưới đây chưa được compile hay test, vì Bash vẫn lỗi E2BIG ở mọi lần gọi, kể cả lần thử mới nhất trong phiên này. Chưa tick todo nào trong phase file.

## Cách làm hiện tại

Theo chỉ đạo của team-lead, tôi tiếp tục viết code bằng Read/Write và định nghĩa đầy đủ mọi symbol được dùng. Team-lead sẽ chạy build, vet và test khi người dùng khởi động lại session, rồi gửi output cho tôi để sửa.

- Mọi file trong `internal/sales` mang `//go:build salesdraft`, nên `./...` bỏ qua package này cho tới khi nó compile được. Gỡ tag là bước cuối, sau khi build sạch.
- Tôi chỉ ghi vào `internal/sales/**`. Wiring dùng chung (app, routes, cli, openapi) được hoãn tới khi package compile được.

## Lỗi môi trường

```
Could not start /usr/bin/zsh: the command line plus environment exceed the OS exec argument limit (E2BIG).
At spawn: command line 152.1KB across 3 args (largest single arg 152KB); environment 4.8KB across 106 vars
```

Tôi không chạy Bash ngoài sandbox và không tìm cách lách.

## Tiến độ file trong `apps/api/internal/sales/` (tất cả chưa kiểm chứng)

| File | Trạng thái | Nội dung |
|------|-----------|----------|
| `model.go` | đã viết | Model dòng hàng và chứng từ |
| `ports.go` | đã viết | Port pricing, inventory, debt (có `Key` cho `PaymentInput`, `Deposits` trả `Deposit` kèm `MethodID`), phương thức thanh toán, combo, campaign, offline session |
| `date.go` | đã viết | Kiểu `Date` |
| `errors.go` | đã viết | Mã lỗi, gồm `walk_in_reverse_needs_refund` |
| `status.go` | đã viết | Trạng thái thanh toán, báo giá, giao hàng |
| `service.go` | đã viết | Service, Deps, scope/action, mặc định `docseq.NewIssuer()` |
| `lines.go` | đã viết | Dựng dòng hàng dùng chung |
| `repo.go` | đã viết | Getter có `FOR UPDATE`, `where`/`ListPage`, `cloneLines` và helper chung |
| `command.go` | đã viết | Lệnh posting chung (`ExtraDocs` cho PT/PC), khóa payment, khóa idempotency của payment, kiểm tra phương thức |
| `quotes.go` | đã viết | Báo giá: tạo, sửa, revise, gửi, chấp nhận, từ chối, copy, chuyển đơn, lịch sử, danh sách |
| `invoices.go` | đã viết | Đơn nháp: tạo, sửa header, thay dòng, xóa (trả báo giá về accepted), copy, chi tiết kèm trạng thái thanh toán, danh sách, phiên bản, `SetPaidAmount` (PT đặt cọc hoặc PC hoàn ứng) |
| `confirm.go` | đã viết | Xác nhận đơn (khóa `sku_stock`, kiểm tra màu, giá sàn, hạn mức nợ, giữ hàng), `:check` chạy thử, revise có phiên bản, giữ thêm, hủy phần còn lại |
| `stock_out.go`, `documents.go` | chưa viết | Phiếu xuất PX kèm chứng từ BH; danh sách và chi tiết chứng từ bán |
| `pos.go`, `carts.go`, `shift.go`, `offline_sync.go` | chưa viết | POS |
| `returns.go`, `exchange.go` | chưa viết | Trả và đổi hàng |
| `print.go`, `http.go`, `dto.go`, `verify.go` | chưa viết | In, HTTP, DTO, ledger verify |
| Unit test | chưa viết | |

Ngoài package:

- Migration `000050_sales_document_cash_tendered`: chưa viết.
- `internal/app/sales_ports.go`: chưa viết.
- Wiring dùng chung, OpenAPI, integration test (gồm 20 checkout đồng thời trên 10 đơn vị), job đóng ca: hoãn tới khi compile được.

## Quyết định thiết kế đã chốt

- **Đảo phiếu xuất hoặc chứng từ POS đã thu tiền:** không chặn. Tiền đã thu chuyển thành tiền ứng trước của khách; số tiền được trả trong response `:reverse` và ghi vào audit. Chỉ chặn khi debt trả 409 `obligation_has_credits`, kèm lỗi gợi ý làm phiếu trả hàng.
- **POS khách lẻ (party NULL):** chặn bằng 409 `walk_in_reverse_needs_refund`. Lý do: khách lẻ không có sổ công nợ để giữ tiền ứng trước, nên nếu đảo thì tiền đã thu không thuộc về ai. Người dùng phải làm phiếu trả hàng, phiếu này hoàn tiền mặt có chứng từ PC.
- **Khách đã thanh toán trên đơn nháp:** đi qua `ScopeInvoicePay` và ghi phiếu PT đặt cọc gắn `EarmarkedInvoiceID`. Debt không có port đảo payment trong tx, nên khi sửa số tiền, sales ghi phần chênh lệch. Tăng thì ghi thêm một phiếu đặt cọc. Giảm thì ghi phiếu PC `advance_refund` với `RefundOf` trỏ về phiếu cọc, không vượt phần chưa phân bổ của phiếu đó.
- **Khóa payment:** suy ra từ khóa idempotency của posting cộng hậu tố (`:pay0`, `:pay1`, …). Khi replay, debt trả lại payment cũ thay vì ghi mới.
- **Ca làm việc:** mở trong tx riêng trước checkout, đánh số `CA`. Nếu vi phạm `ux_shifts__one_open` thì đọc lại ca đang mở. Worker đóng ca của ngày trước.
- **Đổi hàng:** preview trước khi post để cố định số payment. Prefix chính là DH, extra gồm TH, BH và PT/PC. Apply tính lại, lệch thì trả 409 `exchange_changed`.

## Port kits/campaign

- `ComboExpander` mặc định là `NoCombos`, trả 422 `combo_not_sellable`.
- `CampaignAttributor` mặc định là `NoCampaigns`.

## Lệnh kiểm tra và kết quả thực tế

Chưa chạy được lệnh nào vì lỗi E2BIG.

## Rủi ro

- Code chưa compile nên có thể sai chữ ký so với debt, inventory, pricing và posting, dù tôi đã đọc source các package đó.
- Build tag `salesdraft` là tạm thời.
- Route `POST /pos/offline-session` là phần bổ sung so với spec.
