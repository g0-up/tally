# Thùng mix chưa đủ: server khớp PRD §7.10

## Kết quả

Server đã cho lưu thùng mix có tổng số dây/lốc nhỏ hơn số thùng × số dây/thùng, và tính tiền theo số dây/lốc thực chọn. Trường hợp vượt vẫn trả 422 `mix_parts_exceed_pack`. Mã `mix_parts_incomplete` đã được bỏ khỏi server và web.

## Nguyên nhân

`expandMix` (`apps/api/internal/sales/lines.go`) so sánh tổng phần với `packs * perPack` và trả 422 `mix_parts_incomplete` khi tổng nhỏ hơn. Nhánh này đi ngược PRD dòng 541 và ngược với UI.

Trường hợp tổng = 0 không thể xảy ra. `validateDraft` đã chặn `parts` rỗng (`lines.N.parts: required`) và phần ≤ 0 (`must_be_positive`). Tôi giữ nguyên các lỗi này.

## Rà các chỗ dùng thùng mix

| Chỗ | Kết luận |
|---|---|
| `finishMixHeaders` | Đúng sẵn. Thành tiền header bằng tổng các vị. Đơn giá header = gross / số thùng. Thùng thiếu cho đơn giá bằng tiền các vị thực chọn chia cho số thùng. |
| `planDelivery` (`ready`/`addWhole`/`remaining`) | Đúng sẵn. Code tính theo số lượng thực của từng dòng vị, không suy từ `units_per_pack`. |
| Trả hàng / đổi hàng | Đúng sẵn. Code trả theo từng dòng vị (`returnable`); header không trả được. |
| Báo giá → hóa đơn, sao chép | Đúng sẵn. `draftOf`/`flavorParts` dựng lại số phần từ số hộp / `units_per_part`. |
| POS, giỏ POS, đồng bộ offline | Cùng đi qua `buildLines` → `expandMix`, nên được sửa theo. |
| Kits (giỏ quà/combo) | Không đổi. `buildMixItem` chỉ đối chiếu số lượng khai báo với tổng vị (`parts_mismatch`). Web gửi `qty` bằng tổng dây thực chọn theo đơn vị dây, nên thùng thiếu đã lưu được. |
| Báo cáo, CRM, campaign | Không đổi. Các chỗ này tính trên dòng vị, không dùng `units_per_pack`. |
| `sales.Verify` | **Có lỗi từ trước.** Lỗi lộ ra khi test xuất kho thùng mix qua hóa đơn. Check "invoice line delivered" chỉ đếm `stock_out_lines`, mà header mix/combo không giữ tồn nên không có dòng nào ở đó. Kết quả là mọi header đã xuất đều bị báo lệch. Đã sửa: header lấy số đã giao từ các dòng chứng từ BH còn hiệu lực. |

## File đã sửa

- `apps/api/internal/sales/lines.go`: bỏ nhánh `total < want` và cập nhật comment của `expandMix`.
- `apps/api/internal/sales/errors.go`: bỏ `CodeMixPartsIncomplete`.
- `apps/api/internal/sales/verify.go`: header mix/combo tính "delivered" từ `sales_document_lines` của BH xuất kho còn hiệu lực.
- `apps/api/internal/sales/mix_pack_test.go` (mới): unit test cho thùng thiếu, vượt, phần rỗng/0, giá header, xuất nguyên header và sao chép.
- `apps/api/internal/app/sales_integration_test.go`: đưa nhóm mix lên mức test. Test `mix_parts_incomplete` cũ đổi thành bán POS thùng thiếu (180000, cola −12, cam −6). Ca vượt khẳng định 422 `mix_parts_exceed_pack`. Thêm subtest báo giá 2 thùng 3+2 lốc = 300000 → chuyển hóa đơn → xác nhận → xuất nguyên header (cola −18, cam −12, hóa đơn completed 300000).
- `apps/api/api/openapi.yaml`: thêm mô tả cho `SalesLineInput.mix_group_id`/`parts` và `SalesMixPart.parts` `minimum: 1`.
- `apps/web/src/shared/api/schema.d.ts`: sinh lại bằng `corepack pnpm gen:api`.
- `apps/web/src/features/sales/api/mutations.ts`: bỏ thông điệp `mix_parts_incomplete`.
- `docs/user-guide/danh-muc-bang-gia.md`: thùng chưa đủ vẫn lưu và tính theo dây/lốc thực chọn.
- `plans/261016-0854-tally-full-product-implementation/phase-16-web-sales-pos.md`: câu "sum of flavor parts must equal one pack" đổi theo PRD.

Không có migration. Không sửa file nào trong vùng của p21-go.

## Gate

- `go-gates.sh`: gofmt sạch, vet ok, vet integration ok, `test -race` ok.
- `golangci-lint`: 1 lỗi revive, nằm ở `internal/app/tenant_isolation_integration_test.go:1318` (`real` che builtin).
- Test integration: mọi package pass, chỉ `TestTenantIsolationAcrossEveryRoute` fail. Cả hai lỗi đều ở file test cách ly mà p21-go đang sửa, không dính tới thùng mix (export-jobs, host-context, rate limit, support session).
- `TestSalesOverHTTP` và `TestSalesDocumentLifecycleOverHTTP` pass.
- Web: `typecheck` ok, `lint` ok, `test` 1090/1090 pass.

## Bổ sung sau review (`code-reviewer-261019-0800-mix-incomplete-review.md`)

Chỉ sửa phía Go và tài liệu. H1 (hộp thoại xuất kho trên web) đã giao cho uifix-b.

- **M2.** Thêm subtest "reversing the stock out of a short mix pack books its flavors back and undelivers the pack" trong
  `TestSalesDocumentLifecycleOverHTTP`. Hóa đơn 1 thùng 1+1 lốc → xác nhận → xuất qua header → `POST /stock-outs/{id}:reverse`.
  Sau khi đảo, `delivered_base_qty` của mọi dòng (kể cả header) về 0, `delivered_amount` về 0, tồn cola/cam quay lại như trước khi xuất, và verify sạch.
  Thêm `TestComboInvoiceIsDeliveredThroughItsHeader` (dùng `newKitsStack` vì stack sales không có route `/kits`). Combo 3 bộ = 42000
  → xác nhận → gửi id dòng thành phần thì 422 `deliver_through_parent` → xuất qua header (túi 10→7, thiệp 10→4, hóa đơn completed) → verify
  → đảo → tồn về 10/10, header về 0 → verify.
  Tôi đã thử đột biến: bỏ `'combo'` khỏi `line_kind IN (...)` thì test combo đỏ, bỏ `d.status <> 'reversed'` thì cả hai test đảo đỏ. Hai nhánh mới của Verify đều có test bắt.
  Đổi tên `TestPlanDeliveryDeliversAShortPackWhole` thành `TestPlanDeliveryDeliversAMixPackWithEachFlavorsOwnPieces`, kèm comment.
  `planDelivery` không đọc quy cách thùng nên không thể phân biệt thùng thiếu với thùng đủ. Test giờ chỉ khẳng định đúng điều nó chứng minh.
- **L1.** `expandMix` tính `capacity = packs × parts per pack`, rồi trả `mix_parts_exceed_pack` ngay khi một phần vượt `capacity - total`, trước khi cộng.
  Tổng chạy vì vậy không vượt `capacity` và không tràn. Bỏ phép kiểm sau vòng lặp. Thêm `TestExpandMixRejectsPartsTooLargeToAdd`
  với `{MaxInt, 1}`, `{1, MaxInt}` và `{MaxInt, MaxInt}`. Với code cũ, ca đầu tràn và lọt qua.
- **L2.** CTE `delivered` giờ là `SELECT id, SUM(qty) FROM (… UNION ALL …) u GROUP BY id`. Comment của `Verify` viết lại thành danh sách, xuống dòng dưới 100 cột.
- **M1.** Server giữ nguyên như lead chốt. `openapi.yaml` thêm mô tả `unit_price` ở `SalesLine`, `QuoteLine`, `InvoiceLine`, `SalesDocumentLine`
  và `SalesPrintLine`. Trên dòng mix_pack, đơn giá = tiền các vị trước chiết khấu ÷ số thùng (làm tròn), nên qty × đơn giá khớp, kể cả thùng thiếu.
  Thêm `maxItems: 100` cho `SalesLineInput.parts` (`maxFlavorParts`). Đã chạy `corepack pnpm gen:api`, và `schema.d.ts` đã có mô tả mới.
  `docs/user-guide/danh-muc-bang-gia.md` có thêm một gạch đầu dòng mô tả đơn giá thùng mix trên chứng từ và bản in.
- **L4.** Dòng 37-38 của user guide sửa thành "tổng không vượt số thùng × số dây/lốc mỗi thùng".

Gate lần này:
- `gofmt -l internal/sales internal/app` sạch, `go vet -tags integration ./...` ok, `go test -race ./internal/sales/...` ok.
- `golangci-lint run ./internal/sales/... ./internal/app/...` còn 1 lỗi revive ở `tenant_isolation_integration_test.go:1419` (`real`), là file của p21-go.
- Integration `-race -run 'Sales|Mix|Invoice|StockOut|Verify|Combo' ./internal/app/...` ok. `-run 'Kit|Posting|Combo|Ledger|Exchange|Return'` trên `./internal/app/...` và `-run '…|Verify' ./internal/cli/...` đều ok.

Status: DONE_WITH_CONCERNS
Summary: Đã làm xong M2, L1, L2, M1 và L4 phía Go và tài liệu. Có test đảo phiếu xuất thùng mix thiếu và test combo qua hóa đơn kèm verify; đột biến xác nhận cả hai nhánh mới của Verify đều có test bắt. OpenAPI, `schema.d.ts` và user guide đã cập nhật.
Concerns/Blockers: Lint `./internal/app/...` còn 1 lỗi revive ở `tenant_isolation_integration_test.go:1419`, file của p21-go. H1 (web xuất kho mix/combo) thuộc uifix-b.
