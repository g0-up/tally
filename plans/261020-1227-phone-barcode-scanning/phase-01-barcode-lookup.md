---
phase: 1
title: "Tra mã vạch theo đơn vị cho POS, Kiểm kê và Nhập hàng"
status: pending
priority: P1
effort: 1.5d
dependencies: []
---

# Phase 1: Tra mã vạch theo đơn vị

## Mục tiêu và bối cảnh

Hiện chưa có chỗ nào tra từ mã vạch ra cặp (SKU, đơn vị). Phase này tạo phép tra đó ở server, rồi cho POS và Kiểm kê
dùng nó trong luồng Enter của ô quét. Phase 3 và 5 gọi lại đúng các hàm này cho mã từ điện thoại.

Bằng chứng:
- `sku_barcodes(id, tenant_id, sku_id, unit_id, code, created_at)`, unique `(tenant_id, code)` so nguyên chuỗi, FK tới
  `sku_units`. Mã hợp lệ dài tối đa 64 ký tự ASCII in được (`apps/api/internal/catalog/skus.go:98-110`, hàm
  `barcodeReason` chưa export). `insertBarcode` chỉ bắt vi phạm unique nguyên chuỗi (`skus.go:225-233`) và được
  `AddBarcode`, `QuickCreateProductIn` dùng chung (`quick_create.go:35,57`).
- `PosTile` chỉ có đơn vị cơ sở và chỉ gồm 30 hàng bán chạy (`apps/api/internal/sales/pos_bootstrap.go:16,42-51`).
- POS khớp theo từ trên `q` (chữ trong ô quét) qua `matchesAllTokens(q, sku_code, name, variant_label)`
  (`apps/web/src/features/pos/pages/pos-page.tsx:69`), và `q` rỗng thì mọi ô đều khớp (`shared/lib/textnorm.ts:25`).
  Enter lấy `matches[0]` (`pos-page.tsx:88-98`). `add()` chỉ cần SKU, nhãn, đơn vị và tồn theo màu (`:79-87`).
- `SkuListItem` có `units` (kèm `factor`) và `on_hand_by_color` (`apps/web/src/features/catalog/schemas.ts:50,85`).
- Kiểm kê khớp `sku_code` hoặc `sku.barcode` đầu tiên trên dữ liệu của `useSkusByIds`
  (`apps/web/src/features/inventory/components/count-drawer.tsx:71-90`), mà hàm này cắt danh sách ở 200 id
  (`apps/web/src/features/catalog/api/queries.ts:31,131-139`; server `maxSkuIDs` ở `apps/api/internal/catalog/sku_list.go:17`),
  trong khi phiên kiểm toàn kho không giới hạn số SKU (`apps/api/internal/inventory/count.go:80-81`). Số mới được tính từ
  `edits` của lần render rồi lưu giá trị tuyệt đối (`count-drawer.tsx:85-90`).
- Gói offline được lưu vào IndexedDB nguyên dạng, không parse qua schema (`apps/web/src/features/pos/offline/offline-bundle-repository.ts:67,72-75`;
  `offline-db.ts:17`, `OFFLINE_DB_VERSION = 2`).
- Giá trên giỏ online do server tính theo `unitId` của dòng
  (`apps/web/src/features/sales/components/line-items/use-line-pricing.ts`). Giá offline chỉ có giá đơn vị cơ sở
  (`apps/web/src/features/pos/offline/use-offline-line-pricing.ts`).

<!-- Updated: Red Team 2026-10-20 - một endpoint /sku-lookup cho mọi màn, bỏ tile-lookup và sửa search.go -->
<!-- Updated: Validation Session 1 - Kiểm kê cộng theo hệ số của đơn vị; POS offline từ chối mã đơn vị khác -->

## Files

- Tạo `apps/api/internal/catalog/code_variants.go` và `code_variants_test.go`: `CodeVariants(code string) []string`.
  Đây là chỗ duy nhất chuẩn hóa mã; web không có bản sao.
- Tạo `apps/api/internal/catalog/code_lookup.go`: `(s *Service) LookupCode` và `LookupCodeIn(ctx, q, code)`.
- Sửa `apps/api/internal/catalog/skus.go`: export `ValidateBarcode(code string) error` (bọc `barcodeReason`, phase 2 dùng
  qua cổng), và `insertBarcode` kiểm tra trùng theo biến thể.
- Sửa `apps/api/internal/catalog/http.go` (route `GET /sku-lookup`), `errors.go` (`code_not_found`).
- Sửa `apps/api/internal/sales/pos_bootstrap.go` (thêm `Barcodes` vào `PosTile`).
- Sửa `apps/api/api/openapi.yaml`: `PosTile.barcodes`, `SkuBarcodeRef`, `SkuCodeMatch`, đường dẫn `/sku-lookup`.
- Tạo `apps/web/src/shared/lib/barcode.ts` và `barcode.test.ts`: `matchCode` (khớp chính xác), kiểu `ScanOutcome`,
  `ScanSource = "keyboard" | "phone"`.
- Tạo `apps/web/src/features/catalog/api/sku-lookup.ts` (hàm `lookupSkuCode(code)` gọi `/sku-lookup`, trả `null` khi
  404 `code_not_found`).
- Sửa `apps/web/src/features/sales/schemas.ts` (`posTileSchema.barcodes` có `.default([])`),
  `features/pos/pages/pos-page.tsx` (hoặc tách phần khớp ra `features/pos/lib/scan-match.ts`),
  `features/inventory/components/count-drawer.tsx`.
- Sửa fixture có `PosTile` hoặc gói offline: handler offline của POS, `product-tile.test.tsx`,
  `offline-sale-service.test.ts`, `offline-bundle-repository.test.ts`, `procurement/testing/fixtures.ts:19`,
  `catalog/testing/fixtures.ts:18`, `kits/testing/fixtures.ts:178`, fixture của onboarding và inventory,
  `assembly-complete-dialog.test.tsx` (tìm bằng `rg -l "PosTile|pos_tile|tiles:" apps/web/src`).
- Tạo `apps/api/migrations/000059_catalog_sku_barcodes_sku_index.up.sql` và `.down.sql`: index
  `ix_sku_barcodes__sku (tenant_id, sku_id)`, vì `sku_barcodes` hiện chỉ có `ux_sku_barcodes__code (tenant_id, code)`
  (`apps/api/migrations/000007_catalog_products_skus.up.sql:108-114`) mà phase này đọc mã vạch theo `sku_id`.
- Test: `apps/api/internal/catalog/*_integration_test.go` cho tra mã và trùng biến thể, test bootstrap POS,
  `count-drawer.test.tsx`, `features/pos/lib/scan-match.test.ts` (hoặc `pos-scan.test.tsx`), test gói offline cũ.

## Các bước

1. **Biến thể mã (chỉ ở Go).** `CodeVariants` trả mã như được gõ trước tiên, rồi với mã chỉ gồm chữ số:
   - Dài 12, 13 hoặc 14: thêm các bản bỏ số 0 đầu rồi đệm lại thành 12, 13, 14 chữ số (không trùng lặp, không dài hơn 14).
   - Dài 8 và là UPC-E hợp lệ (số đầu 0 hoặc 1): thêm bản mở rộng UPC-A 12 chữ số và các bản đệm 13, 14 của nó.
   - Dài 12–14 mà dạng UPC-A nén được thành UPC-E: thêm bản UPC-E 8 chữ số.
   Bảng test: `036000291452` khớp `0036000291452` và `00036000291452`; `4006381333931` không sinh bản 12 chữ số;
   `01234565` khớp `012345000065` và `0012345000065` (dạng WASM trả); mã chữ giữ nguyên.
2. **`LookupCode`.** Trong giao dịch của tenant:
   - Tìm `sku_barcodes` có `code = ANY(variants)` thuộc SKU đang hoạt động của sản phẩm đang hoạt động. Mã khớp đúng như
     gõ thắng, rồi tới thứ tự biến thể.
   - Không có mã vạch thì tìm `skus.code` bằng mã gõ hoặc `upper(mã)`, đơn vị là đơn vị cơ sở.
   - Trả `CodeMatch{SkuID, UnitID, UnitName, Factor, MatchedBy: "barcode"|"sku_code"}`. Không thấy thì trả lỗi
     `code_not_found` (404).
3. **`GET /sku-lookup?code=`** trong nhóm tenant đã đăng nhập, không thuộc module nào (POS, Kiểm kê và Nhập hàng cùng
   dùng). Trả `SkuCodeMatch{sku: SkuListItem, unit_id, unit_name, factor, matched_by}`. Mã rỗng hoặc sai định dạng theo
   `ValidateBarcode` trả 422. Đây là phép tra server duy nhất của tính năng: không có `/pos/tile-lookup`, và tìm kiếm
   `SearchSkus` (`search.go`) giữ nguyên.
4. **Chặn trùng theo biến thể.** Trong `insertBarcode`, trước khi INSERT: lấy khóa giao dịch
   `pg_advisory_xact_lock` theo tenant và dạng chuẩn của mã (GTIN-14 với mã số, nguyên chuỗi với mã chữ), rồi
   `SELECT EXISTS (… code = ANY(CodeVariants(code)))`. Có thì trả `barcode_taken`. Nhờ vậy `AddBarcode`, tạo nhanh và
   hàng mới từ Nhập hàng (phase 5) đều không gắn được `0036000291452` khi `036000291452` đã có, kể cả trên cùng SKU.
5. **`PosTile.barcodes`.** Sau khi đọc 30 ô, đọc mọi mã vạch của các SKU đó bằng một truy vấn và gắn
   `barcodes: [{code, unit_id}]`. Gói offline nhận trường này vì dùng chung `tiles`. `posTileSchema.barcodes` có
   `.default([])`, và mọi chỗ đọc dùng `tile.barcodes ?? []`, vì gói đã lưu trước deploy không có trường này.
6. **Web `matchCode`.** `matchCode(code, items)` nhận danh sách `{skuCode, barcodes}` và trả mục khớp cùng `unitId`. Khớp
   chính xác, không phân biệt hoa thường; mã vạch thắng `sku_code`. Biến thể GTIN không làm ở web: mã không khớp chính
   xác thì đi tiếp tới `/sku-lookup`.
7. **POS.** Viết lại `addFirst` thành `scanCode(code, source): Promise<ScanOutcome>`. Hàm **không đọc `q`**. Thứ tự:
   1. `matchCode` trên các ô hiện có. Mã vạch của đơn vị cơ sở (hoặc `sku_code`) thì thêm như bấm ô. Mã vạch của đơn vị
      khác thì thêm dòng với `unitId` đó khi online.
   2. Online mà chưa khớp thì gọi `lookupSkuCode(code)`. Thấy thì thêm dòng với SKU, nhãn, tồn theo màu lấy từ
      `SkuListItem` và đơn vị của mã. Giá do server tính theo `unitId` như mọi dòng online.
   3. Chỉ với `source === "keyboard"`: chưa khớp thì dùng khớp theo từ trên chính `code` như cũ. Nguồn điện thoại bỏ
      bước này.
   4. Không có gì thì báo “Không tìm thấy mã “…”.” như hiện nay và trả `not_found`.
   Khi offline mà mã khớp mã vạch của đơn vị khác đơn vị cơ sở, không thêm dòng, báo “Mã này là của {đơn vị}, cần có
   mạng để bán theo đơn vị này.” và trả `not_found`, vì giá offline chỉ có giá đơn vị cơ sở. Enter từ bàn phím gọi
   `scanCode(q.trim(), "keyboard")`, và ô quét chỉ xóa nội dung khi đã thêm dòng. Một lần gọi thêm tối đa một dòng.
8. **Kiểm kê.** Tách `countScan(code): Promise<ScanOutcome>` dùng cho cả Enter và điện thoại:
   - Gọi `lookupSkuCode(code)`, rồi tìm dòng kiểm có cùng `sku_id` trong `lines` của phiên. Không phụ thuộc
     `useSkusByIds`, nên phiên hơn 200 SKU vẫn khớp.
   - Không có dòng nào (404 hoặc SKU ngoài phiên) thì toast “Không có mã … trong phiên kiểm kê.” và trả `not_found`.
   - Có thì cộng `factor` của đơn vị mã vào số đếm theo đơn vị cơ sở (mã thùng 24 hộp cộng 24). Số mới tính từ một ref
     giữ `edits` mới nhất (cập nhật đồng bộ trước khi lưu) và đặt bằng `setEdits(cur => …)`, nên hai mã đến liền nhau
     cộng dồn đúng. Dòng thông báo ghi “Đã cộng {factor} – {tên} ({đơn vị}), thực đếm {n}”.
9. **Kiểu kết quả chung** ở `apps/web/src/shared/lib/barcode.ts`:
   `{ outcome: "added", skuId, unitId } | { outcome: "not_found" } | { outcome: "needs_desktop_edit" } |
   { outcome: "new_product" } | { outcome: "not_in_order" }`. Hai giá trị cuối chỉ Nhập hàng ở phase 5 trả.
10. Viết migration `000059` (bắt đầu bằng `SET search_path = tally;`), chạy `make test` cho catalog và sales, sinh lại
    `schema.d.ts` bằng `corepack pnpm gen:api`.

## Validation

- `go test ./internal/catalog/...` cho `CodeVariants` (bảng ví dụ ở bước 1, gồm UPC-E).
- `make test-integration` cho tra mã: mã vạch đơn vị thùng trả đơn vị thùng và `factor`; mã vạch thắng `sku_code` của
  SKU khác; UPC 12 chữ số khớp mã lưu 13 chữ số; UPC-E 8 chữ số khớp mã lưu dạng UPC-A; SKU ngừng bán không khớp; mã của
  tenant khác không khớp. Chặn trùng: mã 12 chữ số đã có trên SKU A thì gắn bản 13 chữ số vào SKU B trả `barcode_taken`.
- Vitest:
  - `barcode.test.ts`: khớp chính xác, mã vạch thắng `sku_code`, không phân biệt hoa thường.
  - POS: khớp ô; tra server bằng MSW; khớp theo từ chỉ với nguồn bàn phím; nguồn điện thoại gửi “a” khi ô quét trống trả
    `not_found` và giỏ không đổi; offline với mã đơn vị khác; gói offline không có `barcodes` vẫn khớp theo `sku_code` và
    không ném lỗi.
  - `count-drawer.test.tsx`: khớp mã vạch thứ hai của SKU; phiên có SKU thứ 201 vẫn khớp; mã thùng cộng theo hệ số; hai
    lần quét liền nhau không có render xen giữa ra số đếm 2.
- `openapi_contract_test.go` qua.

## Rủi ro

- Biến thể có thể khớp sai khi cửa hàng in mã nội bộ toàn chữ số dài 8 hoặc 12–14 ký tự. Khớp đúng như gõ luôn thắng,
  và bước 4 chặn hai mã biến thể của nhau cùng tồn tại, nên kết quả tra luôn duy nhất.
- Catalog có thể đã chứa sẵn hai mã là biến thể của nhau trên hai SKU. `LookupCode` khi đó trả mã khớp đúng như gõ, rồi
  theo thứ tự biến thể, nên vẫn ổn định. Phase 7 chạy một truy vấn đếm các cặp như vậy trên prod trước khi triển khai.
- Kiểm kê chuyển từ khớp cục bộ sang gọi server cho mỗi lần quét. Kiểm kê vốn cần mạng để lưu, nên không mất khả năng
  nào; độ trễ đo ở phase 7.

## Bảo mật

Endpoint mới nằm trong nhóm tenant đã đăng nhập, đọc dưới RLS. Không route công khai nào ở phase này.

## Rollback

Hoàn tác các file trên. Migration `000059` chỉ thêm một index nên an toàn khi giữ lại; bản `down` xóa index. Trường mới
trong JSON là trường thêm vào nên client cũ bỏ qua được.

## Việc cần làm

- [x] `CodeVariants` kèm bảng test (GTIN và UPC-E)
- [x] `LookupCode` và `GET /sku-lookup`
- [x] `ValidateBarcode` export và chặn trùng theo biến thể trong `insertBarcode`
- [x] `PosTile.barcodes`, schema `.default([])`, cập nhật fixture
- [x] `matchCode`, `ScanOutcome`, `lookupSkuCode` ở web
- [x] POS `scanCode(code, source)`
- [x] Kiểm kê `countScan` qua `/sku-lookup`, cộng theo hệ số
- [x] Migration `000059`, OpenAPI, `gen:api`
- [x] Test Go, test tích hợp và Vitest ở mục Validation
