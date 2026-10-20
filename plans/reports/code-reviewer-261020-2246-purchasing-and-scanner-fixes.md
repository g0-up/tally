# Review: luồng quét khi nhập hàng và hai thay đổi backend của scanner

Ngày: 2026-10-21. Chỉ review, không sửa mã nguồn.

## Phạm vi

- Backend nhập hàng: `apps/api/internal/procurement/{dto.go,new_products.go,new_products_test.go,ports.go}`,
  `apps/api/internal/app/procurement_ports.go`, `apps/api/internal/app/purchasing_screens_integration_test.go`,
  `apps/api/internal/catalog/skus.go` (phần `ValidateBarcode` và `insertBarcode`), `apps/api/api/openapi.yaml`
  (`NewProductRequest.barcode`).
- Web nhập hàng (thư mục thật là `features/procurement`, không phải `features/purchasing`):
  `buy-lines.ts`, `api/adapters.ts`, `components/buy-preset.tsx`, `containers/new-po-dialog.tsx`,
  `containers/new-receipt-dialog.tsx` và các test đi kèm, gồm `new-po-dialog.test.tsx` mới.
- Scanner: `sessionToClose` và cờ `waiting` của `lockOpenSessions` (`scanner/repo.go`, `service.go`, `model.go`),
  `phoneCheck` trong `scanner/http.go`, cùng `service_test.go` và `scanner_integration_test.go`.
- Khoảng 760 dòng thêm ở phần nhập hàng, cộng phần scanner liên quan.

## Kiểm tra đã chạy

- `go vet` cho procurement, catalog, scanner, app, cả có và không có tag `integration`: sạch.
- `go test -race` cho procurement, catalog, scanner: pass.
- `go test -race -tags integration ./internal/scanner/...`: pass (29,5 s).
- `go test -race -tags integration -run TestPurchasingScreensOverHTTP ./internal/app/`: pass.
- `vitest run src/features/procurement`: 11 file, 61 test pass. `tsc -b` và eslint cho `features/procurement`: sạch.
- Container testcontainers đã dừng (chỉ còn ryuk của lần chạy này, đã stop). Không đụng container prod.

## Đánh giá chung

Backend phần mã vạch của hàng mới đúng về giao dịch và tenant: mã vạch được tạo trong cùng transaction của chứng từ,
`sku_barcodes` bật và FORCE RLS, khóa advisory có `tenant_id` trong khóa, và lỗi `barcode_taken` về đúng
`lines.{i}.new_product.barcode` (đã có test tích hợp cho 409, 422 và va chạm biến thể GTIN giữa hai dòng). Hai thay
đổi của scanner cũng đúng: khóa advisory theo user rồi mới `FOR UPDATE`, nên việc chọn phiên để đóng không bị đua với
lượt claim của điện thoại; `phoneCheck` dùng `created_by` và `login_session_id` đọc từ DB, không tin dữ liệu từ client.

Vấn đề nằm ở phía web nhập hàng. Có hai lỗi làm hỏng dữ liệu hoặc làm luồng quét không dùng được, trong khi điện thoại
vẫn báo thành công.

## Critical

Không có.

## High

### 1. Nhận hàng theo đơn: mã quét cộng chồng lên số lượng đã điền sẵn, nên phiếu vượt dung sai và không ghi sổ được

- Vị trí: `apps/web/src/features/procurement/buy-lines.ts:149` (dòng theo đơn khởi tạo `qty: ol.pending_base_qty`),
  `buy-lines.ts:250-253` (nhánh `fixed` cộng `found.factor` vào `qty` hiện có), `buy-lines.ts:196-198` và `:429`
  (`overTolerance` chặn khi vượt `pending × 1.05`).
- Kịch bản lỗi: đơn đặt 24 hộp, hộp thoại mở ra với dòng đã ghi 24. Thủ kho quét thùng 12 hộp, dòng thành 36 và điện
  thoại hiện `added`. Bấm Ghi sổ thì bị chặn với thông báo vượt dung sai. Nếu thủ kho quét đủ 24 hộp để đếm, dòng thành
  48. Nghĩa là quét để đếm hàng khi nhận theo đơn luôn cho ra số sai. Test
  `containers/new-receipt-dialog.test.tsx:252` khẳng định chính trạng thái 24 thành 36 này là đúng, nên đây là test
  chứng minh hành vi sai chứ không bảo vệ hành vi đúng.
- Nguyên nhân: plan (bước 6.4) chỉ nói "cộng số lượng theo đơn vị cơ sở" mà không tính tới việc dòng đã điền sẵn số còn
  chờ nhận. Đây là chỗ hở của plan, không phải lỗi gõ.
- Cách sửa (cần chủ sản phẩm chọn một):
  1. Thêm cờ `scanned` vào `BuyLine`. Lần quét đầu vào một dòng theo đơn thì đặt `qty = factor`, các lần sau mới cộng.
     Ít thay đổi nhất và giữ được hành vi gõ tay hiện tại.
  2. Khi hộp thoại có điện thoại đã ghép, khởi tạo các dòng theo đơn với số lượng 0.
  3. Giữ cách cộng, nhưng nếu kết quả vượt `pending × RECEIVE_TOLERANCE` thì không cộng và trả `needs_desktop_edit`
     kèm toast. Cách này chỉ chặn sai lệch, không làm việc đếm bằng máy quét đúng.
  Dù chọn cách nào, cần sửa test ở `new-receipt-dialog.test.tsx` để khẳng định số lượng sau khi quét là số đã đếm.

### 2. BuyEditor vẫn nhận mã khi đang lưu đơn hoặc ghi sổ phiếu: dòng quét bị mất mà điện thoại báo thành công

- Vị trí: `apps/web/src/features/procurement/components/buy-preset.tsx:110` (`useScanTarget(scanCode)` không có điều
  kiện), `containers/new-po-dialog.tsx:52-53`, `containers/new-receipt-dialog.tsx:105-106`.
- Kịch bản lỗi: người dùng bấm "Ghi sổ nhập kho" trong khi vẫn đang quét. Body đã được dựng và gửi đi. Một mã đến
  trong lúc request đang chạy, `scanCode` thêm dòng hoặc cộng số lượng, điện thoại nhận `added`. Request thành công,
  `after()` đóng hộp thoại, và dòng vừa quét không có trong phiếu. Với phiếu nhận, hàng đã vào kho thực tế nhưng không
  được ghi sổ, nên tồn kho lệch mà không ai được báo.
- Bằng chứng về quy ước sẵn có: POS chặn đúng trường hợp này
  (`features/pos/containers/pos-cart-container.tsx:190`, target là `null` khi `saving` và trả `NOT_ACCEPTING` khi
  `hasBlockingLayer()`), và count drawer cũng vậy (`features/inventory/components/count-drawer.tsx:112`). Luồng nhập
  hàng là chỗ duy nhất bỏ qua quy ước này.
- Cách sửa: thêm prop `busy` cho `BuyEditor` (truyền `save.pending` và `post.pending`), rồi gọi
  `useScanTarget(busy ? null : (code) => (hasBlockingLayer() ? NOT_ACCEPTING : scanCode(code)))`. `hasBlockingLayer`
  còn chặn luôn trường hợp hộp thoại `SupplierQuickDialog` đang mở phía trên bảng dòng. Thêm một test: lúc request lưu
  đang treo thì mã quét nhận `not_accepting` và không thay đổi dòng nào.

## Medium

### 3. Nhận theo đơn: khi một SKU nằm ở nhiều dòng của đơn, mã quét luôn cộng vào dòng đầu tiên

- Vị trí: `apps/web/src/features/procurement/buy-lines.ts:251`
  (`lines.findIndex((l) => l.order?.skuId === found.sku.id)`).
- Kịch bản lỗi: đơn có "Áo thun" màu đỏ 10 cái và màu xanh 10 cái, cùng `sku_id`, khác `color_id`. Mọi lần quét đều cộng
  vào dòng đỏ. Dòng đỏ vượt dung sai còn dòng xanh vẫn ở số cũ. Cùng một SKU cũng có thể nằm ở hai dòng với hai đơn giá.
  Nhánh thường (`buy-lines.ts:263`) cũng bỏ qua màu, nhưng ở đó người dùng tự chọn dòng nên rủi ro thấp hơn.
- Cách sửa: chọn dòng đầu tiên còn thiếu (`qty < pending`), nếu không còn dòng nào thiếu thì chọn dòng cuối. Nếu có
  hơn một dòng khớp mà không phân định được thì trả `needs_desktop_edit` để người dùng chọn trên máy tính. Mục này nên
  sửa cùng mục 1 vì cả hai cùng nằm trong nhánh `fixed`.

## Low

### 4. Kiểm tra định dạng mã vạch ở procurement trùng với kiểm tra mà quick-create của catalog đã làm

- Vị trí: `apps/api/internal/procurement/new_products.go:104-127` (`checkBarcode`), `procurement/ports.go`
  (`Catalog.ValidateBarcode`), `app/procurement_ports.go` (dòng cuối).
- Bằng chứng: `QuickCreateProduct` gọi `sku.normaliseFields` (`catalog/skus.go:86-95`). Hàm này đã trim mã, coi mã rỗng
  là không có, và đặt lỗi lên trường `barcode`. Sau đó `newProductError` đổi trường này thành
  `lines.{i}.new_product.barcode`. Kết quả HTTP vì vậy giống hệt khi không có `checkBarcode`. Báo cáo triển khai cũng
  thừa nhận catalog kiểm lại lần nữa.
- Tác động: không có lỗi hành vi. Cái giá là interface của port rộng thêm một method, có một fake trong test viết lại
  luật định dạng (`new_products_test.go`), và vòng lặp `for _, v := range e.Fields` (`:120`) chỉ đúng khi catalog trả
  đúng một trường.
- Đề xuất: plan yêu cầu "gọi ValidateBarcode qua cổng" nên tôi không coi đây là lỗi. Nếu lead đồng ý, có thể bỏ
  `checkBarcode` và method của port, giữ test `newProductError` cho trường `barcode`. Nếu giữ, nên đọc đúng
  `e.Fields["code"]` thay vì lặp qua map.

### 5. Lỗi mã vạch của lần lưu trước có thể hiện thoáng qua lên dòng của lần lưu mới

- Vị trí: `containers/new-po-dialog.tsx:52-53,90`, `containers/new-receipt-dialog.tsx:105-106,141`.
- Kịch bản: lần lưu trước trả 409 ở `lines.0`. Người dùng thêm một dòng hàng mới lên đầu bảng rồi bấm lưu lại.
  `setSent(built)` (React state) và việc mutation chuyển sang pending (cập nhật qua `notifyManager` của TanStack) có thể
  rơi vào hai lần render khác nhau. Ở lần render giữa, lỗi cũ được ghép với `keys` mới, và nếu dòng mới ở chỉ số 0 có mã
  vạch thì nó hiện lỗi của dòng khác cho tới khi mutation reset lỗi.
- Cách sửa: lưu cặp `{ sent, error }` trong `onError` của lần gửi đó, hoặc xóa `sent` ngay khi bắt đầu lần gửi mới và
  chỉ đặt lại khi lỗi về.

### 6. Hai stream cùng thu hồi một phiên có thể làm mất sự kiện kết thúc

- Vị trí: `apps/api/internal/scanner/service.go:236-253` (`RevokeForSignOut`, nhánh `changed == false` gọi
  `s.hub.Close(key)`), nơi gọi là `http.go:343-345` và `http.go:547-549`.
- Kịch bản: giờ cả `desktopCheck` lẫn `phoneCheck` đều kiểm tra login session. Nếu hai tick rơi gần nhau, UPDATE của bên
  thua chờ row lock, thấy 0 dòng rồi gọi `hub.Close`. Bước này có thể chạy trước khi bên thắng gọi `s.ended`. Khi đó
  stream bị đóng mà không có `revoked`/`ended`. Client chỉ biết phiên đã hết khi kết nối lại và gọi GET, nên desktop thấy
  trạng thái `ended` chứ không thấy `revoked{by:"system"}`. Hậu quả nhỏ vì login session đã mất, mọi lần tạo phiên mới
  đều trả 401.
- Cách sửa (tùy chọn): ở nhánh `changed == false`, gửi lại `s.ended(key, RevokedBySystem)` thay vì `Close`. Việc gửi lại
  sự kiện kết thúc là idempotent ở phía client.

### 7. `maxLength: 64` trong OpenAPI tính trước khi trim, còn server trim rồi mới đếm

- Vị trí: `apps/api/api/openapi.yaml:13697`, `procurement/new_products.go:108`, `catalog/skus.go:113`.
- Mã 64 ký tự có khoảng trắng hai đầu được server nhận nhưng vi phạm schema. Hiện chưa gây lỗi vì không có middleware
  nào validate theo OpenAPI. Có thể thêm vào mô tả rằng giới hạn tính sau khi trim, hoặc bỏ qua.

### 8. Có thể deadlock khi hai chứng từ cùng tạo hàng mới với hai mã vạch theo thứ tự ngược nhau

- Vị trí: `catalog/skus.go:241` (khóa advisory theo dạng chuẩn của mã, giữ tới hết transaction của chứng từ).
- Hai phiếu nhận chạy song song, phiếu A có mã X rồi Y, phiếu B có Y rồi X, sẽ bị Postgres báo 40P01 và trả 500. Rủi
  ro này đã có từ trước với unique index và thực tế gần như không xảy ra. Ghi lại để biết; nếu muốn chặn thì đổi 40P01
  thành 409 để client thử lại.

### 9. Còn thiếu test

- Chưa có test tích hợp cho trường hợp phiếu nhận nhanh có hàng mới kèm mã vạch thành công. Test hiện có chỉ phủ đơn
  nhập thành công và phiếu nhận thất bại.
- Chưa có test quét trong lúc đang lưu (mục 2), và chưa có test cho đơn có cùng SKU ở nhiều dòng (mục 3).

## Edge case tìm được khi scout

- Mã được gửi khi handler quá ttl: controller bỏ qua lượt đó và toast "Kiểm tra lại xem dòng hàng có được thêm không",
  còn `scanCode` vẫn áp dụng khi lookup trả về muộn. Đây là thiết kế đã chấp nhận và đã có toast, nên không nêu thành
  lỗi. Việc `scanCode` cập nhật `latest.current` ngay rồi mới `onChange` làm hai lượt chồng nhau không đọc cùng một bản
  cũ: đã kiểm.
- Các truy vấn mới đều dựa vào RLS mà không lọc `tenant_id` trong câu SQL (`sku_barcodes`, `scanner_sessions`). Cả hai
  bảng đều `FORCE ROW LEVEL SECURITY` (migration 000007 và 000060), đúng quy ước của package catalog và scanner.
  `SessionActive` chạy trong platform tx nhưng có `s.tenant_id = $1` tường minh.
- Lượt claim của điện thoại đua với `Create` khi `Create` đang thu hồi đúng phiên đó: UPDATE của claim chờ row lock,
  được đánh giá lại sau khi commit, lúc này `claim_code_hash` đã là NULL nên claim trả `claim_invalid`. Hành vi đúng.
- `phoneCheck`: goroutine của tick có tối đa một cái mỗi stream, kênh kết quả có buffer 1, và
  `WithoutCancel + tickTimeout` giới hạn việc thu hồi trong 2 s. Không rò goroutine.
- `idempotency.Once` không lưu response lỗi, nên sau khi bị 409 `barcode_taken`, người dùng sửa mã rồi gửi lại với
  cùng key thì không bị replay lỗi cũ.

## Đối chiếu plan Phase 5

| Việc | Thực tế |
|---|---|
| `Barcode` qua `newProductRequest`, `NewProduct`, `CreateProduct`; `ValidateBarcode` qua port | Đã làm (xem mục 4) |
| `barcode_taken` về `lines.i.new_product.barcode`; OpenAPI | Đã làm, có test tích hợp |
| `BuyLine.barcode`, `scannedNewLine`, chặn trùng chính xác, ô Mã vạch | Đã làm |
| `scanCode` (cộng dồn, hàng mới, theo đơn cộng `factor`, ref dòng mới nhất) | Đã làm, nhưng nhánh theo đơn sai (mục 1 và 3) |
| `useScanTarget` và `PhonePairingCard` trong `BuyEditor` | Đã làm, nhưng thiếu điều kiện chặn khi đang lưu (mục 2) |
| Test ở mục Validation | Có đủ, nhưng test nhận theo đơn khẳng định hành vi sai |

Đề xuất: chưa nên đánh dấu Phase 5 là hoàn thành cho tới khi mục 1 (cần quyết định sản phẩm) và mục 2 được xử lý.

## Hành động đề xuất theo thứ tự

1. Chọn hành vi cho việc quét khi nhận theo đơn (mục 1), sửa nhánh `fixed` cùng mục 3, và viết lại test của
   `new-receipt-dialog`.
2. Chặn scan target khi đang lưu hoặc khi có lớp chặn (mục 2), kèm test.
3. Tùy chọn: dọn `checkBarcode` (mục 4), ghép lỗi theo từng lần gửi (mục 5), gửi lại sự kiện kết thúc ở nhánh thua
   (mục 6).

## Câu hỏi còn mở

- Khi nhận theo đơn bằng máy quét, số trên dòng nên là số đã đếm (bắt đầu từ 0) hay số còn chờ cộng thêm? Mục 1 phụ
  thuộc vào câu trả lời này.
- Thực tế cửa hàng có đặt cùng một SKU ở nhiều dòng (theo màu hoặc theo giá) trong một đơn không? Câu trả lời quyết định
  mức ưu tiên của mục 3.

Status: DONE_WITH_CONCERNS
Summary: Backend mã vạch của hàng mới và hai thay đổi của scanner đúng về transaction, khóa, RLS và cách map lỗi; mọi test Go, test tích hợp, Vitest, tsc và eslint đều pass. Phía web có hai lỗi High: khi nhận theo đơn, mã quét cộng chồng lên số đã điền sẵn nên phiếu không ghi sổ được; và mã đến trong lúc đang lưu bị mất trong khi điện thoại báo `added`.
Concerns/Blockers: Mục 1 cần chủ sản phẩm quyết định hành vi đếm khi nhận theo đơn trước khi sửa.
