# Báo cáo sửa lỗi: đổi đơn vị của mã vạch (theo review 261020-2246)

- Ngày: 2026-10-21 (Asia/Saigon).
- Đầu vào: `plans/reports/code-reviewer-261020-2246-catalog-barcode-units.md`.
- Trạng thái: hoàn thành. Mọi cổng kiểm tra đều đạt. Không sửa tài liệu hay plan, không thao tác git.

## Hành vi người dùng thấy (dùng cho `docs/user-guide/danh-muc-bang-gia.md`)

Drawer "Mã vạch" (nút "Mã vạch" trên mỗi dòng của trang Hàng hóa):

- Câu phụ dưới tiêu đề: "{mã SKU} · Chọn đơn vị rồi bấm Lưu. POS đang mở ở nơi khác có thể tính theo đơn vị cũ tới
  khi tải lại hàng bán."
- Mỗi mã vạch có một ô chọn "Đơn vị khi quét". Ô chọn chỉ đưa ra các đơn vị đang dùng của hàng, sắp từ nhỏ tới lớn theo
  hệ số (ví dụ "hộp", "lốc (4 hộp)", "thùng (12 hộp)"). Đơn vị đã ngừng bị ẩn, trừ khi chính mã đó đang dùng nó.
- Chọn đơn vị khác chỉ đổi dòng đó trên màn hình, chưa lưu gì. Nút "Lưu" hiện cạnh ô chọn khi đơn vị đang chọn khác
  đơn vị đã lưu. Chọn lại đơn vị cũ thì nút "Lưu" biến mất. Dùng phím mũi tên trên ô chọn đang đóng không lưu các đơn
  vị đi qua.
- Bấm "Lưu" (chuột, Enter hoặc Space): nút đổi thành "Đang lưu…", nút và ô chọn bị khóa nhưng vẫn giữ focus. Bấm thêm
  hay chọn đơn vị khác lúc này không gửi gì.
- Lưu xong: toast "Mã {mã vạch} nay là mã của {đơn vị}.". Nếu không tìm được tên đơn vị thì toast là "Đã đổi đơn vị
  của mã {mã vạch}.". Nút "Lưu" biến mất và focus trở về ô chọn.
- Bị từ chối: ô chọn quay về đơn vị đã lưu, lý do hiện ngay dưới ô (ví dụ "Đơn vị này không còn thuộc hàng hóa. Tải
  lại để xem bản mới nhất."), nút "Lưu" biến mất và focus trở về ô chọn.
- Drawer luôn đọc SKU từ danh sách Hàng hóa hiện tại. Đơn vị mới được thêm ở nơi khác sẽ xuất hiện trong ô chọn sau khi
  danh sách tải lại. Nếu SKU không còn trong danh sách thì drawer tự đóng và không tự mở lại.

Thời điểm các màn hình nhận đơn vị mới (đã đối chiếu với mã nguồn):

- Kiểm kê, Nhập hàng và POS với hàng không nằm trên lưới ô hàng: nhận ngay ở lần quét sau, vì các màn này tra
  `/sku-lookup` trực tiếp, không có cache.
- POS với hàng trên lưới ô hàng (tối đa 30 hàng bán chạy): POS khớp mã theo dữ liệu bootstrap trước khi tra
  `/sku-lookup` (`features/pos/lib/scan-match.ts`).
  - Cùng tab trình duyệt với trang Hàng hóa: sau khi lưu, bootstrap POS của tab này bị đánh dấu cũ, nên lần mở POS kế
    tiếp trong tab đó luôn tải lại. Lưu ý: cache React Query là riêng từng tab, nên "cùng trình duyệt" chỉ đúng với
    cùng một tab. Trong lúc tải lại, POS vẫn hiện dữ liệu cũ một nhịp ngắn cho tới khi bản mới về.
  - Tab khác (kể cả trong cùng trình duyệt) hay máy khác: vẫn dùng đơn vị cũ cho hàng trên lưới tới khi bootstrap của
    nó tải lại. Bootstrap tải lại khi POS đó ghi sổ một đơn bán (`useCheckout` làm mới `POSTING_KEYS`, gồm `["pos"]`),
    khi lưu, sao chép hoặc xóa giỏ (`posKeys.all`). Ngoài ra, khi bootstrap đã cũ hơn 15 giây (`staleTime: 15_000`),
    nó cũng tải lại lúc cửa sổ lấy lại focus, lúc có mạng trở lại (onlineManager theo heartbeat) và lúc mở lại màn
    POS. Không có tải lại định kỳ.
- Gói offline của POS: làm mới theo lịch riêng, khi mở POS lúc có mạng, khi có mạng trở lại, và mỗi 30 phút
  (`BUNDLE_REFRESH_MS`). Việc invalidate bootstrap không làm mới gói offline.

## Đã sửa

1. POS dùng đơn vị cũ cho mã thuộc ô hàng: giữ nguyên thứ tự tra của POS. `useUpdateBarcodeUnit` nay invalidate thêm
   `posKeys.bootstrap()` (`["pos", "bootstrap"]`). Key được export qua `features/pos/index.ts`, theo cách các feature
   khác import `skuKeys` hay `pricingKeys` từ index của feature. Không có vòng import (pos chỉ import kiểu và
   `lookupSkuCode` từ catalog, còn `pos/api/queries` chỉ phụ thuộc `sales/schemas`). Docstring của drawer và của
   mutation đã viết lại theo đúng các thời điểm ở trên. Câu phụ của drawer cũng đã sửa vì câu cũ ("lần quét sau tính
   theo đơn vị mới") không đúng với POS ở nơi khác.
2. Ô chọn lưu ở mỗi lần đổi: ô chọn nay sửa một bản nháp riêng của từng dòng, và nút "Lưu" (`Button` có `pending`,
   `pendingLabel="Đang lưu…"`, nhãn truy cập "Lưu đơn vị của mã {mã}") mới gửi PATCH. Giữ khóa khi đang gửi
   (`aria-disabled`, focus giữ nguyên), lỗi dưới ô kèm quay về đơn vị cũ, toast khi thành công. Có thêm test bàn phím.
3. Drawer dùng bản chụp SKU: `products-page.tsx` chỉ giữ `skuId` trong state và tìm SKU trong `list.items` ở mỗi lần
   render. Nếu SKU biến mất thì drawer đóng và state được xóa, nên drawer không bật lại khi SKU quay lại danh sách.
   Toast không bao giờ hiện tên đơn vị rỗng. Thêm một option dự phòng "Đơn vị khác · tải lại để xem" khi đơn vị của mã
   chưa có trong danh sách đơn vị của SKU (danh sách đơn vị cũ hơn danh sách mã), để ô chọn không hiện nhầm đơn vị đầu
   tiên. Phần này nhỏ và nằm ngoài danh sách việc được giao, xem mục Câu hỏi.
4. Các mục Low:
   - Chú thích trong `skus.go`: nói đúng rằng 404 thắng với mọi `unit_id` mà body nêu, còn body thiếu `unit_id` thì
     bị trả 422 trước khi tra cứu, nên câu trả lời đó không lộ mã nào tồn tại.
   - `catalog_partner_integration_test.go`: mục PATCH dùng `own.units["thùng"]` theo quy ước của test. Bước "Nothing
     of A changed" kiểm thêm mã vạch của A vẫn còn và vẫn ở `hộp`. `TestTenantIsolationAcrossEveryRoute` không đổi và
     vẫn đạt.
   - `sku_lookup_integration_test.go`: chuyển mã về `cái` bằng `t.Cleanup` đăng ký ngay sau khi PATCH thành công.
   - Kiểu chờ inline: giữ nguyên cho ô chọn, vì trong `globals.css` chỉ có `.btn[aria-disabled="true"]`, không có quy
     tắc nào áp cho select (và `globals.css` nằm ngoài phạm vi được sửa). Nút "Lưu" dùng `Button`, nên tự nhận quy tắc
     `.btn` sẵn có. Chú thích của hằng `PENDING_STYLE` đã ghi rõ lý do.
   - Test mới: `unitChoices` (ẩn đơn vị ngừng trừ đơn vị hiện tại, sắp theo hệ số) và invalidate (danh sách SKU và
     bootstrap POS bị đánh dấu cũ sau khi lưu).
5. API nhận đơn vị đã ngừng: giữ nguyên theo quyết định, nhất quán với `AddBarcode`.

## Tệp đã sửa

- `apps/web/src/features/catalog/containers/barcode-units-drawer.tsx`: bản nháp và nút "Lưu", trả focus, toast dự
  phòng, option dự phòng, docstring và câu phụ.
- `apps/web/src/features/catalog/containers/barcode-units-drawer.test.tsx`: 10 test (trước là 5).
- `apps/web/src/features/catalog/api/mutations.ts`: invalidate `posKeys.bootstrap()` và cập nhật docstring.
- `apps/web/src/features/catalog/pages/products-page.tsx`: chỉ giữ id của SKU.
- `apps/web/src/features/pos/index.ts`: thêm một dòng `export { posKeys } from "./api/queries";`.
- `apps/api/internal/catalog/skus.go`: chỉ sửa chú thích.
- `apps/api/internal/app/catalog_partner_integration_test.go`, `apps/api/internal/app/sku_lookup_integration_test.go`.
- `apps/web/src/test/msw/barcodes.ts`: không cần sửa.

## Kiểm tra

- Web: `corepack pnpm lint`, `typecheck`, `test` (174 file, 1320 test) và `build` đều đạt. Không có lỗi nào từ phần
  procurement đang được sửa song song.
- Thử đột biến: bỏ dòng invalidate bootstrap thì test invalidate fail. Cho ô chọn lưu ngay ở `onChange` thì 3 test
  fail (gồm test bàn phím). Bỏ dòng xóa state khi SKU biến mất thì test của trang Hàng hóa fail. Đã khôi phục mã sau
  mỗi lần thử.
- Test bàn phím: jsdom không đổi giá trị của select đang đóng khi bấm mũi tên (đã thử với user-event). Vì vậy test bấm
  phím thật rồi phát sự kiện `change` mà Chrome và Firefox trên Windows hoặc Linux sẽ phát, và kiểm rằng không có PATCH
  nào cho tới khi Tab sang "Lưu" rồi bấm Enter, và PATCH duy nhất mang đơn vị cuối.
- Go: `gofmt -l`, `go vet ./...`, `go vet -tags integration ./...` sạch.
  `go test -race -tags integration ./internal/app/ -run 'TestCatalogAndPartnersOverHTTP|TestScanLookupAndPosTileBarcodes|TestTenantIsolationAcrossEveryRoute'`
  đạt. Reaper testcontainers đã tự thoát, không còn container test nào. Không động tới container prod, không để lại
  tiến trình nền.

## Câu hỏi còn mở

1. Option dự phòng "Đơn vị khác · tải lại để xem" là phần thêm nhỏ để ô chọn không hiện sai đơn vị trong khoảnh khắc
   danh sách đơn vị cũ hơn danh sách mã. Nếu không muốn, có thể bỏ mà không ảnh hưởng các phần khác.
2. Tài liệu người dùng cần đổi câu "Từ lần quét sau, POS (khi có mạng) ... tính mã đó theo đơn vị mới" theo mục "Thời
   điểm các màn hình nhận đơn vị mới" ở trên, và mô tả bước bấm "Lưu".

Status: DONE
Summary: Đã sửa toàn bộ các mục theo quyết định của lead (invalidate bootstrap POS, lưu bằng nút "Lưu" với bản nháp từng dòng, drawer đọc SKU hiện tại, các mục Low và test mới); lint, typecheck, test, build của web và go vet cùng ba test tích hợp đều đạt.
