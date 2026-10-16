# Ubiquitous Language — Tally

One term, one meaning, everywhere: PRD (Vietnamese), UI labels, Go types, TypeScript types, tables, API resources and tests. When code and this file disagree, fix the code or update this file in the same change. Vietnamese strings below are the exact UI labels from prototype v6.14.

## 1. Naming rules

| Layer | Rule | Example |
|---|---|---|
| Go package | domain noun, singular, lowercase | `inventory`, `sales`, `debt` |
| Go type | English term from §2, PascalCase | `StockMove`, `Invoice`, `Obligation` |
| TS type | Same English term | `StockMove`, `Invoice` |
| Table | English term, plural snake_case | `stock_moves`, `invoices` |
| API resource | plural kebab-case under `/api/v1` | `/api/v1/stock-moves`, `/api/v1/invoices` |
| Web route | `/<group>/<screen>` kebab English | `/sales/pos`, `/inventory/stock-card` |
| Status code value | lowercase snake English | `partially_delivered` |
| UI label | Vietnamese from §3 via one `labels.ts` map | `Giao một phần` |

Never translate a term twice (Hóa đơn is always `Invoice`, never `SalesOrder`). Table names inherit the brainstorm DDL exactly; Go types follow the table noun inside their domain package (`sales.Invoice`, `procurement.Receipt`). Never put Vietnamese identifiers in code; Vietnamese lives only in label maps, seed data and messages.

## 2. Glossary

### 2.1 Organization, access, control

| Vietnamese | English term | Go / TS type | Table | Notes |
|---|---|---|---|---|
| Tenant / cửa hàng thuê | Tenant | `Tenant` | `tenants` | Isolation unit |
| Địa chỉ cửa hàng (tên miền con) | Store subdomain / slug | `Tenant.Slug` | `tenants.slug` | `<slug>.DOMAIN`; chosen at sign-up, immutable, never reused (architecture §7.2) |
| Cửa hàng | Store | `Store` | `stores` | Exactly one per tenant (ORG-03) |
| Kho cửa hàng | Warehouse | `Warehouse` | `warehouses` | Exactly one; `warehouse_id` kept for future |
| Chủ cửa hàng | Owner (User) | `User` | `users` | One owner account (SEC-01) |
| Phiên đăng nhập | Session | `Session` | `sessions` | Revocable, device-bound |
| Thiết bị | Device | `Device` | `sessions.device_*` | Shown in Tài khoản & bảo mật |
| Số chứng từ | Document number | `DocNumber` | `doc_sequences` | `PREFIX-0001` per tenant |
| Khóa idempotency | Idempotency key | `IdempotencyKey` | `idempotency_keys` | Header `Idempotency-Key` |
| Nhật ký | Audit event | `AuditEvent` | `audit_events` | Append-only (SEC-04) |
| Khóa kỳ | Period lock | `PeriodLock` | `period_locks` | SEC-05 |
| Xác nhận ngoại lệ | Exception confirmation | `ExceptionConfirmation` | `exception_confirmations` | "Tôi xác nhận vẫn bán" (SEC-03) |
| Đính kèm | Attachment | `Attachment` | `attachments` | Signed URL (DATA-05) |
| Ngày nghiệp vụ | Business date | `BusinessDate` | `business_date` column | Tenant timezone |
| Ghi sổ | Post | `Post()` | `posted_at`, `posted_by` | Atomic, idempotent |
| Đảo chứng từ | Reverse | `Reverse()` | `reversal_of` | Never delete posted docs |

### 2.2 Catalog and partners

| Vietnamese | English term | Go / TS type | Table | Notes |
|---|---|---|---|---|
| Mặt hàng / hàng hóa | Product | `Product` | `products` | Not in ledger |
| Mã hàng / biến thể | SKU | `SKU` | `skus` | Ledger key |
| Loại hàng | Product type | `ProductType` | `products.type` | `goods`, `material`, `kit`, `combo` |
| Nhóm hàng | Category | `Category` | `categories` | |
| Thuộc tính | Attribute | `Attribute` | `attributes` | Dynamic, code immutable |
| Lựa chọn thuộc tính | Attribute option | `AttributeOption` | `attribute_options` | |
| Mẫu ngành | Attribute set | `AttributeSet` | `attribute_sets` | Template per category |
| Trục biến thể | Variant axis | `VariantAxis` | `product_variant_axes` | `select` attributes only |
| Đơn vị tính (ĐVT) | Unit | `Unit` | `units` | thùng, hộp, dây, lốc, chai, kg |
| Đơn vị cơ sở | Base unit | `BaseUnit` | `skus.base_unit_id` | Stock stored in base unit |
| Quy đổi đơn vị | Unit conversion | `SKUUnit` | `sku_units` | `factor` > 0 |
| Mã vạch | Barcode | `Barcode` | `sku_barcodes` | Unique per tenant, per unit |
| Màu | Color | `Color` | `colors` | 7 fixed: xanh, đỏ, tím, vàng, nâu, đen, trắng |
| Chưa gán màu | Uncolored | `ColorID = nil` | `color_id IS NULL` | A separate state, not a color |
| Nhóm mix vị | Mix group | `MixGroup` | `mix_groups` | pack/part units |
| Vị | Flavor | `MixGroupItem` | `mix_group_items` | `flavor_label` |
| Thùng mix vị | Mix pack line | `MixLine` | `mix_key` on lines | Split into flavor lines on posting |
| Quy cách | Spec text | `SpecText` | `spec_text` | Free text on lines |
| Đối tác | Party | `Party` | `parties` | One party, two ledgers |
| Khách hàng | Customer | `CustomerProfile` | `customer_profiles` | |
| Nhà cung cấp (NCC) | Supplier | `SupplierProfile` | `supplier_profiles` | Đang giao dịch / Ngừng giao dịch |
| Nhóm khách | Customer group | `CustomerGroup` | `customer_groups` | Khách lẻ / Khách sỉ … |
| Hạn mức công nợ | Credit limit | `CreditLimit` | `customer_profiles.credit_limit` | DEBT-06 |
| Lý do | Reason | `Reason` | `reasons` | Return, damage, adjustment, write-off (CAT-07) |
| Phương thức thanh toán | Payment method | `PaymentMethod` | `payment_methods` | Tiền mặt, Chuyển khoản |

### 2.3 Pricing

| Vietnamese | English term | Go / TS type | Table | Notes |
|---|---|---|---|---|
| Bảng giá | Price list | `PriceList` | `price_lists` | retail, wholesale, group, customer, season |
| Giá lẻ / giá sỉ | Retail / wholesale price | `PriceListKind` | `price_lists.kind` | Seeded per tenant |
| Giá theo màu | Color price | `PriceListItem{ColorID}` | `price_list_items.color_id` | PRC-07 |
| Giá bậc thang | Tier price | `PriceListItem{MinQty}` | `price_list_items.min_qty` | PRC-02 |
| Nguồn giá | Price source | `PriceSource` | `price_source jsonb` | Returned by resolver |
| Lịch sử giá | Price change | `PriceChange` | `price_changes` | Append-only |
| Giá tối thiểu | Minimum price | `MinPrice` | `skus.min_price` | PRC-04 |
| Giá mua tham khảo | Reference cost | `RefCost` | `skus.ref_cost` | Never becomes cost |
| Khuyến mại | Promotion | `Promotion` | `promotions` | PRC-03 |
| Chiết khấu (CK) | Discount | `Discount` | `discount` columns | Line and document |
| Thuế | Tax rate | `TaxRate` | `tax_rates` | PRC-06, snapshot |

### 2.4 Procurement

| Vietnamese | English term | Go / TS type | Table | Prefix |
|---|---|---|---|---|
| Đơn nhập | Purchase order | `PurchaseOrder` | `purchase_orders` | `PO` |
| Phiếu nhận / nhận hàng | Receipt (goods) | `procurement.Receipt` | `receipts` | `PN` |
| Hóa đơn NCC | Supplier document | `SupplierDocument` | `supplier_documents` | — |
| Trả NCC | Purchase return | `PurchaseReturn` | `purchase_returns` | `XT` |
| Chi phí mua phân bổ | Landed cost allocation | `LandedCostAllocation` | `landed_cost_allocations` | PUR-06 |
| Điều chỉnh giá nhập | Purchase price adjustment | `PurchasePriceAdjustment` | `purchase_price_adjustments` | PUR-10 |
| Danh sách hàng hóa | Product list (procurement view) | `ProductListView` | read model | Menu Nhập hàng |

### 2.5 Inventory

| Vietnamese | English term | Go / TS type | Table | Prefix |
|---|---|---|---|---|
| Lô | Lot | `Lot` | `lots` | Unique per (sku, lot_no, expiry, color, quarantine) |
| Hạn dùng | Expiry date | `ExpiryDate` | `lots.expiry_date` | |
| Chuyển động kho | Stock move | `StockMove` | `stock_moves` | Append-only, signed qty |
| Tồn lô | Lot balance | `LotBalance` | `lot_balances` | Projection |
| Tồn kho (mặt hàng) | SKU stock | `SKUStock` | `sku_stock` | Projection, lock row |
| Tồn thực | On hand | `OnHand` | `sku_stock.on_hand` | §7.4 |
| Tồn bán được | Sellable | `Sellable` | `sku_stock.sellable` | On hand − quarantine/expired/blocked |
| Tồn có thể bán | Available | `Available` | computed | Sellable − reserved |
| Giữ hàng | Reservation | `Reservation` | `reservations` | Per SKU, not per color |
| Cách ly | Quarantine | `Quarantine` | `lots.is_quarantine` | INV-11 |
| Kiểm kê | Stock count | `StockCount` | `stock_counts` | `KK` |
| Điều chỉnh kho | Stock adjustment | `StockAdjustment` | `stock_adjustments` | `DC` |
| Thẻ kho | Stock card | `StockCard` | read model | INV-10 |
| Giá vốn bình quân | Moving average cost | `AvgCost` | `sku_stock.avg_cost` | Per SKU, not per color |
| FEFO | FEFO pick | `FEFOPicker` | — | Expiry-first lot order |
| Quy đổi tự động | Unit breakdown | `UnitBreakdown` | — | "17 bó 10 cái" |

### 2.6 Sales

| Vietnamese | English term | Go / TS type | Table | Prefix |
|---|---|---|---|---|
| Báo giá | Quote | `Quote` | `quotes` | `BG` |
| Hóa đơn (chứng từ nội bộ, không phải HĐĐT) | Invoice | `sales.Invoice` | `invoices` | `SO` |
| Phiên bản hóa đơn | Invoice version | `sales.InvoiceVersion` | `invoice_versions` | SAL-10 |
| Phiếu xuất kho | Stock out | `StockOut` | `stock_outs` | `PX` |
| Chứng từ bán | Sales document | `SalesDocument` | `sales_documents` | `BH` (from stock out), `HD` (from POS) |
| POS & giỏ hàng | POS cart | `Cart` | `pos_carts` | `GH` |
| Ca bán hàng | Shift | `Shift` | `shifts` | `CA`, opened implicitly |
| Trả hàng | Sales return | `SalesReturn` | `sales_returns` | `TH` |
| Đổi hàng | Exchange | `Exchange` | `exchanges` | `DH` |
| Bên trả tiền | Payer | `PayerID` | `invoices.payer_party_id` | |
| Khách đã thanh toán | Paid amount | `PaidAmount` | `obligation_settlements` | |
| Còn lại | Remaining | `Remaining` | computed | |

### 2.7 Kits

| Vietnamese | English term | Go / TS type | Table | Prefix |
|---|---|---|---|---|
| Giỏ quà (đóng sẵn) | Kit | `ProductType=kit` | `products` | Has stock |
| Combo (ghép khi bán) | Combo | `ProductType=combo` | `products` | No stock |
| Công thức giỏ | Kit recipe | `KitRecipe` | `kit_recipes` | Versioned |
| Thành phần | Recipe component | `KitRecipeItem` | `kit_recipe_items` | Fixed color optional |
| Lệnh đóng giỏ | Assembly order | `AssemblyOrder` | `assembly_orders` | `LD` |
| Lần đóng | Assembly posting | `AssemblyPosting` | `assembly_postings` | |
| Tháo giỏ | Disassembly | `Disassembly` | `disassemblies` | `TG` |
| Truy xuất lô | Lot genealogy | `LotGenealogy` | `lot_genealogy` | KIT-08 |

### 2.8 Debt and cash

| Vietnamese | English term | Go / TS type | Table | Prefix |
|---|---|---|---|---|
| Nghĩa vụ (phải thu / phải trả) | Obligation | `Obligation` | `obligations` | direction `receivable`/`payable` |
| Khách còn nợ | Receivable balance | `ReceivableBalance` | read model | |
| Cửa hàng còn nợ NCC | Payable balance | `PayableBalance` | read model | |
| Phiếu thu | Payment in | `debt.Payment{Direction: In}` | `payments` | `PT` |
| Phiếu chi | Payment out | `debt.Payment{Direction: Out}` | `payments` | `PC` |
| Phân bổ / tất toán | Settlement | `ObligationSettlement` | `obligation_settlements` | Payment, credit note or offset → obligation |
| Giảm trừ / ghi có | Credit note | `CreditNote` | `credit_notes` | Returns, post-sale discount |
| Tiền khách ứng trước / cọc | Customer advance | `Advance` | unallocated `payments` | |
| Ứng trước NCC | Supplier advance | `Advance` | unallocated `payments` | |
| Điều chỉnh công nợ / xóa nợ | Debt adjustment | `DebtAdjustment` | `debt_adjustments` | DEBT-09 |
| Bù trừ | Offset | `DebtOffset` | `debt_offsets` | DEBT-10 |
| Bảng kê đối soát | Statement | `Statement` | `statements` | `DS` |
| Lịch thu/trả | Payment schedule | `ObligationSchedule` | `obligation_schedules` | |
| Hẹn thanh toán / ghi chú liên hệ | Collection note | `CollectionNote` | `collection_notes` | DEBT-08 |
| Quỹ | Fund account | `FundAccount` | `fund_accounts` | Tiền mặt tại cửa hàng, Tài khoản ngân hàng |
| Dòng tiền quỹ | Cash entry | `CashEntry` | `cash_entries` | Append-only |

### 2.9 Campaign, CRM, notifications, SaaS

| Vietnamese | English term | Go / TS type | Table |
|---|---|---|---|
| Chiến dịch mùa vụ | Campaign | `Campaign` | `campaigns` |
| Gắn chiến dịch | Campaign attribution | `CampaignAttribution` | `campaign_attributions` |
| Checklist mùa | Campaign checklist item | `CampaignChecklistItem` | `campaign_checklist_items` |
| Hoạt động khách | Customer activity | `CustomerActivity` | `customer_activities` |
| Đồng ý liên hệ | Consent | `Consent` | `consents` |
| Điểm thưởng | Loyalty entry | `LoyaltyEntry` | `loyalty_entries` |
| Thông báo & việc cần làm | Notification | `Notification` | `notifications` |
| Gói | Plan | `Plan` | `plans` |
| Thuê bao | Subscription | `Subscription` | `subscriptions` |
| Quyền module | Entitlement | `Entitlement` | `entitlements` |
| Hóa đơn dịch vụ | SaaS invoice | `SaaSInvoice` | `saas_invoices` |
| Ticket hỗ trợ | Support ticket | `SupportTicket` | `support_tickets` |
| Phiên hỗ trợ | Support session | `SupportSession` | `support_sessions` |

## 3. Status vocabularies (code value → UI label)

| Aggregate | Code values → Vietnamese label |
|---|---|
| Quote | `draft` Nháp · `sent` Đã gửi · `accepted` Khách chấp nhận · `rejected` Khách từ chối · `converted` Đã chuyển hóa đơn · derived `expired` Hết hạn |
| Purchase order | `draft` Nháp · `confirmed` Đã chốt · `partially_received` Nhận một phần · `completed` Hoàn tất · `closed_short` Đóng – thiếu |
| Sales invoice | `draft` Nháp · `confirmed` Đã xác nhận · `partially_delivered` Giao một phần · `completed` Hoàn tất · `cancelled_remaining` Đã hủy phần còn lại |
| Invoice payment status (derived) | `unpaid` Chưa thanh toán · `partially_paid` Thanh toán một phần · `paid` Đã thanh toán đủ · `overpaid` Thừa tiền |
| Stock out | `posted` Đã xuất kho |
| POS cart | `open` Đang soạn · `sold` Đã bán |
| Kit / combo product | `draft` Nháp · `active` Đang bán · `inactive` Ngừng bán |
| Stock count | `counting` Đang đếm · `pending_post` Chờ ghi sổ · `posted` Đã ghi sổ |
| Assembly order | `draft` Nháp · `reserved` Giữ nguyên liệu · `in_progress` Đang đóng · `partially_completed` Hoàn thành một phần · `completed` Hoàn thành · `closed` Đóng |
| Statement | `draft` Nháp · `sent` Đã gửi · `confirmed` Đã xác nhận · `disputed` Tranh chấp · `superseded` Đã thay bằng phiên bản mới |
| Mix group | `active` Đang dùng · `inactive` Ngừng dùng |
| Supplier | `active` Đang giao dịch · `inactive` Ngừng giao dịch |
| Period | `open` Mở · `locked` Đã khóa (reopen is an event with reason) |
| Posted document | `posted` Đã ghi sổ · `reversed` Đã đảo · `partially_reversed` Đảo một phần |
| Payment (bank transfer) | `pending_verification` Chờ xác minh · `confirmed` Đã nhận |
| Subscription | `trialing` Dùng thử · `active` Đang hoạt động · `grace` Gia hạn chờ · `read_only` Chỉ xem · `closed` Đã đóng |

Every status shown in UI uses `StatusBadge` with text plus symbol, never color alone (PRD §9.3).

## 4. Stock move types

| `move_type` | Vietnamese | Sign | Source doc |
|---|---|---|---|
| `receipt` | Nhập hàng | + | receipt |
| `purchase_return` | Trả NCC | − | purchase_return |
| `sale` | Xuất bán | − | stock_out, pos sale |
| `sales_return` | Khách trả hàng | + | sales_return |
| `adjustment_in` / `adjustment_out` | Điều chỉnh tăng / giảm | ± | stock_adjustment |
| `count_gain` / `count_loss` | Kiểm kê thừa / thiếu | ± | stock_count |
| `assembly_consume` | Tiêu hao đóng giỏ | − | assembly_posting |
| `assembly_produce` | Nhập giỏ thành phẩm | + | assembly_posting |
| `disassembly_consume` | Tháo giỏ (xuất giỏ) | − | disassembly |
| `disassembly_return` | Tháo giỏ (nhập thành phần) | + | disassembly |
| `quarantine_out` / `quarantine_in` | Chuyển vào/ra cách ly | ± | stock_adjustment |
| `opening` | Tồn đầu kỳ | + | stock_adjustment (kind `opening`) |
| `cost_adjustment` | Điều chỉnh giá vốn (qty 0, value only) | ± value | landed_cost_allocation, purchase_price_adjustment |
| `reversal` | Đảo | opposite | any |

## 5. Document prefixes

Format `PREFIX-NNNN` (zero-padded to 4, grows past 9999), unique per tenant and prefix, issued by `doc_sequences`.

| Prefix | Document | Prefix | Document |
|---|---|---|---|
| `BG` | Báo giá | `SO` | Hóa đơn |
| `PX` | Phiếu xuất kho | `BH` | Chứng từ bán (from PX) |
| `HD` | Chứng từ bán POS | `GH` | Giỏ hàng POS |
| `TH` | Trả hàng | `DH` | Đổi hàng |
| `PO` | Đơn nhập | `PN` | Phiếu nhận |
| `XT` | Trả NCC | `KK` | Kiểm kê |
| `DC` | Điều chỉnh kho | `LD` | Lệnh đóng giỏ |
| `TG` | Tháo giỏ | `PT` | Phiếu thu |
| `PC` | Phiếu chi | `DS` | Bảng kê đối soát |
| `CA` | Ca bán hàng | `DN` | Điều chỉnh công nợ |
| `BT` | Biên bản bù trừ | `DG` | Điều chỉnh giá nhập |

## 6. Exception kinds (SEC-03)

`credit_limit_exceeded` Vượt hạn mức · `below_min_price` Dưới giá tối thiểu · `stock_disposal` Tiêu hủy · `count_variance` Chênh lệch kiểm kê · `debt_write_off` Xóa nợ · `expired_lot_sale` Bán lô hết hạn · `quarantine_release` Khôi phục hàng cách ly · `period_reopen` Mở lại kỳ.

## 7. Price sources (`price_source.kind`)

`customer_list`, `group_list`, `season_list`, `base_list`, `manual`. `price_changes.source`: `catalog`, `purchase_order`, `product_list`, `inventory`, `price_list`.
