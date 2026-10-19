# Review: thùng mix chưa đủ (mix-incomplete)

Ngày 2026-10-19. Chỉ review, không sửa code. `apps/` chưa được track nên không có diff. Tôi đọc trực tiếp các file mà báo cáo
`fullstack-developer-261019-0750-mix-incomplete-carton.md` liệt kê.

## Kết luận

Phần sửa server đúng với PRD §7.10 (`prd.md:541`). Thùng thiếu lưu được, tiền tính theo dây/lốc thực chọn, vẫn trả 422 khi vượt sức chứa.
Truy vấn mới trong `sales.Verify` đúng, không đếm trùng, và lỗi nó sửa là lỗi có thật từ trước. Không test nào bị nới.

Còn một lỗi Cao có từ trước, nằm ngoài diff: hộp thoại xuất kho trên web không xuất được hóa đơn có thùng mix hay combo.
Lỗi này chặn đúng luồng hóa đơn có thùng thiếu → xuất kho mà thay đổi vừa mở. Ngoài ra còn một câu hỏi sản phẩm về đơn giá trên header của thùng thiếu.

## Kiểm chứng đã chạy

| Lệnh | Kết quả |
|---|---|
| `go test -race -count=1 ./internal/sales/...` | ok |
| `go test -race -count=1 -p 1 -tags integration -run 'Sales\|Mix\|Invoice\|StockOut\|Verify' ./internal/app/...` (testcontainers) | ok (28s) |
| `go test ... -tags integration -run 'Kit\|Posting\|Combo\|Ledger\|Exchange\|Return' ./internal/app/... ./internal/cli/...` | ok |
| `golangci-lint run ./internal/sales/...`, `gofmt -l internal/sales internal/app`, `go vet -tags integration` (sales, app) | 0 issues / sạch / ok |
| `vitest run src/features/sales src/features/kits` | 20 file, 103 test pass |

Không còn tham chiếu `mix_parts_incomplete` nào trong `apps/api`, `apps/web/src`, `docs`, `plans` (trừ reports) và `prd.md`.
`schema.d.ts` khớp với `openapi.yaml` (`SalesLineInput.mix_group_id`/`parts`, `SalesMixPart`).

## Mức Cao

### H1. Web không xuất kho được hóa đơn có thùng mix hoặc combo (có từ trước, ngoài diff)

- `apps/web/src/features/sales/stock-out-lines.ts:10,20-31`: `deliverRows` chỉ bỏ dòng header (`mix_pack`, `combo`). Dòng con
  `mix_flavor`/`combo_component` vẫn thành hàng trong bảng, mặc định `qty = max`.
- `apps/web/src/features/sales/containers/stock-out-dialog.tsx:56-66`: `submit` gửi `invoice_line_id` của từng dòng con.
- `apps/api/internal/sales/stock_out.go:315-316`: `planDelivery` trả 422 `deliver_through_parent` cho mọi dòng có `ParentLineID`.
- Kịch bản: tạo hóa đơn 2 thùng mix 3+2 lốc → xác nhận → mở "Xuất kho" → bấm "Ghi sổ xuất kho". Request mang id của dòng cola/cam
  nên server từ chối cả phiếu. Người dùng chỉ ghi sổ được nếu xóa số ở các dòng vị, và khi đó thùng mix không bao giờ được xuất qua web.
  Integration test mới đi đường API đúng (`{"invoice_line_id": inv.Lines[0].ID}` là header), nên không bắt được lỗi này.
  `stock-out-lines.test.ts` không có ca mix/combo.
- Sửa: gom các dòng con theo `parent_line_id` thành một hàng của header (xuất nguyên khối, `base_qty` bỏ trống hoặc bằng `remaining` của header).
  Chỉ bật hàng đó khi mọi dòng con đã giữ đủ, khớp `ready()` ở `stock_out.go:265-281`. Thêm test web cho mix và combo.

## Mức Trung bình

### M1. Đơn giá header của thùng thiếu lệch với PRD và với editor (cần quyết định sản phẩm)

- `apps/api/internal/sales/lines.go:850-854` (`finishMixHeaders`): `UnitPrice = gross / số thùng`. Với 2 thùng 3+2 lốc (30 lon × 10.000),
  header lưu `2 thùng × 150.000 = 300.000`.
- `apps/web/src/features/sales/components/mix-pack-row.tsx:67-68`: editor hiển thị đơn giá thùng = giá bình quân × 24 = 240.000/thùng,
  đúng câu PRD "đơn giá một thùng (mặc định = giá bình quân các vị × số đơn vị lẻ/thùng)".
- `apps/api/internal/sales/print.go:385-398`: bản in, drawer và PDF lấy `unit_price` của header. Khách thấy 150.000/thùng,
  trong khi lúc soạn thấy 240.000/thùng.
- Trước thay đổi, thùng luôn đủ nên hai số này trùng nhau khi các vị cùng giá. Thay đổi này làm lộ ra chỗ lệch. Báo cáo dev ghi `finishMixHeaders` là "Đúng sẵn",
  và `TestFinishMixHeadersPricesAShortPackByItsFlavors` (`mix_pack_test.go:85-101`) khóa giá 150.000.
- Hai lựa chọn: (a) header giữ giá thùng đầy và thành tiền bằng tổng các vị (qty × giá ≠ thành tiền trên header), có ghi chú "thùng thiếu N dây";
  (b) giữ như hiện tại và đổi editor/bản in để hiện "giá bình quân/thùng thực chọn". Cần lead hoặc người dùng chọn.

### M2. Nhánh mới của Verify thiếu test cho các đường nó thêm vào

- `verify.go:42-46` thêm nhánh cho `combo` và loại BH `reversed`. Test duy nhất chạm tới nhánh này là subtest mix
  (`sales_integration_test.go:1432-1474`, kèm `verifySalesLedgers` ở dòng 1846). Chưa test nào đảo một phiếu xuất có header mix/combo
  (`POST /stock-outs/{id}:reverse`) rồi verify. Cũng chưa test nào xuất kho combo qua hóa đơn rồi verify.
  Các test xuất kho khác (`screen_reads_integration_test.go:694-702`, `campaign_reporting_integration_test.go:509`) chỉ dùng dòng hàng thường.
- `TestPlanDeliveryDeliversAShortPackWhole` (`mix_pack_test.go:103-126`) không phân biệt thùng thiếu với thùng đủ. `planDelivery` không đọc
  `units_per_pack`, nên test này pass cả trước lẫn sau thay đổi và không chứng minh hành vi mới.
- Đề xuất: thêm subtest "đảo phiếu xuất thùng mix thiếu". Sau khi đảo, `delivered_base_qty` của header về 0, tồn quay lại, Verify sạch.
  Thêm một ca combo qua hóa đơn.

## Mức Thấp

### L1. Bỏ chặn dưới làm tổng tràn int64 lọt qua kiểm sức chứa

- `lines.go:722-745`: `total += int64(p.Parts)` không giới hạn từng phần. Với `parts = [9223372036854775807, 1]`, `total` tràn thành
  `-2^63` nên qua được `total > packs*perPack`. Số lon cola là `(2^63-1)*6`, tràn thành `-6`.
- Trước thay đổi không thể xảy ra. Điều kiện cũ cần `total == want`, mà a + b ≤ 2^64 − 2 < want + 2^64 nên tổng tràn không bao giờ bằng `want`.
- Hậu quả bị chặn ở tầng sau. Pricer trả 422 `lines.N.qty: must_be_positive` (`pricing/resolver.go:256-257`), và DB có `CHECK (qty > 0)`.
  Không hỏng dữ liệu, nhưng mã lỗi sai chỗ (`qty` thay vì `parts`). `validateDraft` (`lines.go:301-307`) chỉ chặn `≤ 0`.
- Sửa: trong `expandMix`, trả `mix_parts_exceed_pack` ngay khi một phần vượt `packs*perPack`, trước khi cộng dồn. Cũng có thể đặt trần trong `validateDraft`.

### L2. CTE `delivered` dùng UNION ALL mà không gộp lại

- `verify.go:36-46`: mỗi nhánh tự `GROUP BY`, sau đó `LEFT JOIN` theo id. Hiện tại không đếm trùng: `stocked()` loại `mix_pack`/`combo`
  (`model.go:30`), nên header không bao giờ có trong `stock_out_lines` (`stock_out.go:144-147`). Mỗi PX có đúng một BH
  (`stock_out.go:160-167`). Tuy vậy, nếu sau này một loại header giữ tồn, JOIN sẽ nhân đôi dòng thay vì cộng.
  Nên bọc ngoài bằng `SELECT id, SUM(qty) FROM (...) GROUP BY id`.
- `d.reversal_of IS NULL` thừa với `source = 'stock_out'`. `ReverseStockOut` chỉ đánh dấu BH `reversed` (`stock_out.go:731`),
  không tạo BH đảo. `checkReversible` cấm đảo BH của phiếu xuất (`pos.go:719-720`). Điều kiện này vô hại.

### L3. Web gửi số dây lẻ cho trường kiểu int

- `draft.ts` (`draftToInput`): `parts: Number(p)`. `QtyInput` cho nhập số thập phân (`qty-input.tsx:4`), còn `draftProblem` không chặn số lẻ.
  Nhập "1,5 dây" thì server không decode được JSON vào `int` (`MixPart.Parts`, `lines.go:62`) và trả lỗi body chung. Lỗi có từ trước.

### L4. Câu chữ

- `verify.go:16`: dòng comment dài 120 ký tự, câu bị nối vào giữa đoạn.
- `docs/user-guide/danh-muc-bang-gia.md:37`: "tổng không vượt số thùng" nên là "không vượt số thùng × số dây/lốc mỗi thùng". Dòng 42-43 đã đúng.

## Trả lời các câu hỏi của lead

1. **`expandMix`**: vẫn chặn vượt sức chứa (`lines.go:745-748`). Thùng thiếu tính đúng tiền: mỗi vị được định giá theo số lon thực chọn,
   header là tổng các vị. `qty` của header vẫn là số thùng (`Qty = BaseQty = packs`, `UnitFactor = 1`), nhất quán với `ready`,
   `addWhole` và ràng buộc `delivered + cancelled ≤ base_qty`. Chiết khấu dòng được chia theo trọng số số dây (`money.Allocate`), tổng đúng bằng chiết khấu nhập.
   Thuế và chiết khấu đơn được tính trên từng vị rồi cộng lên header. Làm tròn `gross/packs` có thể làm qty × đơn giá ≠ thành tiền trên header.
   Chỗ lệch này đã có từ trước khi các vị khác giá. Riêng đơn giá header là M1.
2. **Verify**:
   - Không đếm trùng (xem L2).
   - Điều kiện `status <> 'reversed'` khớp vòng đời. Chỉ hai nơi ghi `delivered_base_qty`: cộng khi xuất (`stock_out.go:205` → `confirm.go:905`)
     và trừ khi đảo (`stock_out.go:787`, theo `dl.BaseQty` của từng dòng BH, kể cả header). Đảo phiếu xuất đánh dấu BH `reversed`
     (`stock_out.go:731`). Trả hàng chỉ đặt `partially_reversed` (`returns.go:590`), không đụng `delivered_base_qty`, và `<> 'reversed'`
     vẫn tính BH đó, nên khớp. Hủy phần còn lại chỉ đổi `cancelled`.
   - Lỗi có thật từ trước. `stocked(KindMixPack/KindCombo) = false` (`model.go:30`) nên `PostStockOut` bỏ qua `stock_out_lines` cho header
     (`stock_out.go:144-147`), nhưng vẫn cộng `Delivered` cho header (`stock_out.go:200-209`, vì `addWhole` đặt `out[h.ID]`).
     Truy vấn cũ chỉ đọc `stock_out_lines`, nên ra expected 0 trong khi stored bằng số thùng. Trước đây không test nào xuất header qua hóa đơn
     rồi chạy verify, nên lỗi không lộ. Lệnh `ledger verify` (`cli/ledger.go:35`) trên production sẽ báo lệch giả cho mọi hóa đơn mix/combo đã giao.
     Tôi chứng minh bằng code. Không có diff để chạy lại truy vấn cũ.
3. **Các đường khác**: giữ hàng, `ready`/`addWhole`/`remaining`, trả và đổi hàng, POS, báo cáo đều tính theo dòng vị. Thùng mix không bị gộp
   (`mergeLines` bỏ qua dòng mix, `lines.go:372`). Kits web gửi `qty` theo đơn vị dây bằng tổng dây thực chọn (`kits/view-model.ts:78`),
   và server so khớp theo dây (`kits/recipe_build.go:357-379`), nên đúng như báo cáo dev nói. Riêng web xuất kho hỏng (H1).
4. **Test**: ca POS cũ đổi từ 422 `mix_parts_incomplete` sang bán thành công. Đây là đổi theo PRD, không phải nới test. Ca vượt vẫn khẳng định
   422 `mix_parts_exceed_pack`. Integration kiểm tiền, tồn từng vị, trạng thái `completed` và `delivered_amount`. Còn thiếu ca đảo phiếu và ca combo (M2).
5. **Hợp đồng và tài liệu**: `openapi.yaml:14254-14285` khớp code, nhưng không ghi trần 100 vị (`maxFlavorParts`). `schema.d.ts` đã sinh lại đúng.
   `mutations.ts` bỏ thông điệp cũ. Phase-16 dòng 28 đúng PRD. User guide có một câu chưa chính xác (L4).

## Việc nên làm, theo thứ tự

1. H1: sửa hộp thoại xuất kho để xuất mix/combo qua header, kèm test web.
2. M1: chốt với người dùng đơn giá header của thùng thiếu, rồi chỉnh server hoặc bản in cho thống nhất.
3. M2: thêm integration test đảo phiếu xuất mix và ca combo qua hóa đơn, có verify.
4. L1: chặn từng phần vượt sức chứa trước khi cộng.
5. L2–L4: dọn khi tiện.

## Câu hỏi còn mở

- Với thùng thiếu, bản in và chứng từ nên hiện đơn giá nào: giá thùng đầy (theo PRD) hay thành tiền chia số thùng (như code hiện tại)?

Status: DONE_WITH_CONCERNS
Summary: Sửa server cho thùng mix chưa đủ đúng PRD, và sửa Verify đúng, không đếm trùng (lỗi Verify có thật từ trước). Test sales, kits và posting integration pass, lint sạch.
Concerns/Blockers: H1 (có từ trước): web không xuất kho được hóa đơn có mix hoặc combo vì gửi id dòng con. M1: đơn giá header của thùng thiếu lệch giữa editor và chứng từ, cần quyết định sản phẩm. M2: thiếu test đảo phiếu xuất mix và ca combo.
