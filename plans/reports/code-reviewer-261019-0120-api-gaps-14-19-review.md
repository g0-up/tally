# Review api-gaps việc 14–19 và phần bổ sung phase-13

Ngày: 2026-10-19. Người review: code-reviewer. Chỉ đọc, không sửa code.

## Phạm vi

- Migration 000055–000058, `migrations/reversibility_integration_test.go` (phần 000058).
- `internal/saas/`: `billing.go`, `repo.go`, `entitlement.go`, `service.go`, `data_export.go`, `http.go`, `dto.go`, `usage_job.go`.
- `internal/admin/` (tenants, incidents, partners, feedback, auth `Me`/`Admins`, http), `internal/support/service.go` (tickets keyset), `internal/featureflag/reader.go`.
- `internal/org/audit_query.go` (lọc `q`), `internal/identity/consent.go` (đổi tên `IssueExportLink`/`ResolveExportLink`).
- `internal/platform/idempotency/guard.go`, `internal/platform/httpx/{pagination,bind}.go`.
- Test: `internal/app/{saas_control,subscription_change,data_export_idempotency}_integration_test.go`.
- Không lặp lại việc 1–13 (đã có trong `code-reviewer-261018-2100-api-gap-batch-review.md`).

Test đã chạy (testcontainer `postgres:16-alpine` của `internal/testkit`, không đụng `tally-postgres-1`):

```
go test -tags integration -count=1 -run 'TestDataExportRequestRunsOncePerKey|TestBuyingAnAddonNeedsReauthAndRunsOncePerKey|TestSubscriptionListsThePaidAddonsOfTheBusinessDay|TestAddonsSharingAModuleOnOneDayAreListedApart|TestAClosedStoreStaysClosed' ./internal/app/   -> ok (5 test khớp)
go test -tags integration -count=1 -run 'Reversib' ./migrations/                                    -> ok (TestMigrationsAreReversible)
```

## Đánh giá chung

Migration 000058, idempotency của hai endpoint, keyset admin và audit thanh toán trễ đều đúng. Có một lỗi High: lọc `q` của audit log trả 500 cho phiên support. Ba lỗi Medium nằm ở luồng mua add-on và thanh toán trùng.

## High

### H1. `GET .../support-view/settings/audit-events?q=...` trả 500 cho phiên support

- Vị trí: `apps/api/internal/org/audit_query.go:101-108` (`usersNamed` được gọi bên trong `s.read`) và `audit_query.go:129-135` (`SELECT id, full_name FROM users`).
- Kịch bản:
  1. Phiên support có `Scope.ReadOnly = true` (`org/http.go:73`), nên `s.read` chạy `WithReadOnlyTenantTx` (`org/service.go:81-83`) với role `tally_readonly`.
  2. Migration `000026_privileges_assertions.up.sql:20-26` thu hồi ALL trên `users` của `tally_readonly`.
  3. Khi có `q`, `usersNamed` chạy `SELECT ... FROM users` và gặp 42501 permission denied. Lỗi bị bọc thành `org: read users for the audit search`, nên trả 500.
  4. `support/model.go:67` có "audit-events" trong support view, nên route này người dùng truy cập được.
- Test hiện có không bắt được lỗi này:
  - `saas_control_integration_test.go:519` đọc support view `audit-events` không có `q`;
  - `org_integration_test.go:485` gọi `ReadOnly: true` với `AuditFilter{}` rỗng.
- Đề xuất: theo tiền lệ `pricing/writer.go:494-503` (`supportActorNames`).
  - Đọc tên người dùng bằng `WithTenantTx` thường, trước khi mở `s.read`, rồi truyền `[]uuid.UUID` vào truy vấn read-only.
  - Thêm test support view có `?q=` (kiểm tra 200 và khớp theo tên actor).

## Medium

### M1. Mua lại add-on đang hiệu lực: trả tiền nhưng không được thêm ngày

- Vị trí: `apps/api/internal/saas/billing.go:64-65` (add-on luôn báo giá `today .. plan.PeriodEnd(today)`) và `apps/api/internal/saas/repo.go:261-271` (`grantAddon`, `ON CONFLICT ... GREATEST(valid_to)`).
- Kịch bản 1, cùng ngày: chủ cửa hàng mua add-on X hai lần trong một ngày.
  - Có thể do không gửi `Idempotency-Key`, hoặc dùng key khác. `subscription_change_integration_test.go:80-84` cho thấy lần mua không key thứ hai vẫn phát hành hóa đơn thứ hai.
  - Cả hai hóa đơn có cùng `period_start`/`period_end`. Khi trả tiền, `grantAddon` trùng khóa `(tenant, key, valid_from, NULL, plan)`, nên `GREATEST(cùng, cùng)` không đổi gì.
  - Kết quả: trả tiền hai lần, nhận một kỳ.
- Kịch bản 2, giữa kỳ: mua lại X vào ngày 15 khi kỳ cũ còn đến ngày 30.
  - Kỳ mới bắt đầu từ ngày 15, nên các ngày 15–30 bị tính tiền hai lần.
  - `activeAddons` cũng sẽ liệt kê hai dòng chồng nhau.
- Đề xuất:
  - Khi báo giá add-on, đọc dòng entitlement add-on còn hiệu lực của cùng `source_plan_id`. Nếu có, cho kỳ mới bắt đầu từ `valid_to + 1` (gia hạn nối tiếp). Nếu không, từ chối bằng 409 `addon_already_active`.
  - Thêm test mua lại cùng ngày và giữa kỳ.
- Ghi chú: logic báo giá có từ trước batch này, nhưng giờ mới lộ ra vì luồng mua qua `subscription:change` có idempotency tùy chọn.

### M2. Phát hành hóa đơn add-on B hủy cả hóa đơn add-on A đang mở

- Vị trí: `apps/api/internal/saas/billing.go:195-199` (`issueIn`):
  ```sql
  UPDATE saas_invoices SET status = 'void'
  WHERE tenant_id = $1 AND status = 'open' AND (purpose = 'addon') = $2
  ```
- Kịch bản:
  1. Chủ cửa hàng mua add-on A, nhận hóa đơn mở, và chuyển khoản.
  2. Trước khi tiền về, họ mua add-on B, và hóa đơn A bị chuyển sang `void`.
  3. Webhook hoặc MarkPaid của A trả `invoice_not_open`. Đi vào `reportRefusedPayment`, tiền đã nhận phải hoàn tay, và A không được bật.
  - Với hóa đơn gói chính, hủy hóa đơn mở cũ là hợp lý vì chỉ có một gói. Add-on thì độc lập với nhau.
- Đề xuất: khi `qt.Purpose == PurposeAddon`, chỉ hủy hóa đơn mở có cùng `plan_id` (`AND plan_id = $3`). Thêm test mua A rồi B, sau đó trả A thì A vẫn được bật.

### M3. Tiền thật trả cho hóa đơn đã `paid` bị coi là trùng lặp và bị bỏ qua im lặng

- Vị trí: `apps/api/internal/saas/billing.go:559-561` (`applyPayment`):
  ```go
  if inv.Status == InvoicePaid {
      return false, s.markEventProcessed(ctx, q, providerName, pay.EventID, now)
  }
  ```
- Kịch bản:
  1. Admin `MarkPaid` hóa đơn theo sao kê ngân hàng.
  2. Sau đó cổng thanh toán gửi một sự kiện khác (event_id khác) cho cùng hóa đơn, vì khách đã trả lần nữa qua cổng.
  3. Nhánh trên đánh dấu event là đã xử lý và trả `duplicate: true`. Không có log error, không có audit `saas_invoice.payment_refused`, nhưng tiền đã bị thu hai lần.
  - Phần "thanh toán trễ bị từ chối" mới chỉ phủ hóa đơn `void` (`CodeInvoiceNotOpen`), không phủ hóa đơn đã `paid` bởi nguồn khác.
- Đề xuất:
  - Khi `inv.Status == InvoicePaid` và `(provider, event_id)` khác lần đã trả (so `provider`/`provider_ref` của hóa đơn, hoặc tra `saas_payment_events`), gọi cùng đường `reportRefusedPayment`: log error cộng audit một lần mỗi event.
  - Giữ `duplicate: true` cho đúng event đã trả. Thêm test.

## Low

### L1. `featureflag.Changes` vẫn hiện override của sandbox đã `replaced`

- Vị trí: `apps/api/internal/featureflag/reader.go:268-283`. `List` (`reader.go:251-258`) loại `t.status = 'replaced'`, còn `Changes` thì không.
- Kịch bản: lịch sử flag trên admin UI hiện slug và tên của sandbox đã bị thay, không khớp danh sách flag.
- Đề xuất: thêm `WHERE t.id IS NULL OR t.status <> 'replaced'` như `List`. Hoặc ghi rõ trong spec rằng lịch sử cố ý giữ lại.

### L2. Không có CHECK loại trừ giữa `source_subscription_id` và `source_plan_id`

- Vị trí: `apps/api/migrations/000058_entitlement_source_plan.up.sql`.
- Kịch bản: một lỗi code tương lai ghi dòng có cả hai cột. Dòng đó vừa bị `syncPlanEntitlements` (lọc `source_subscription_id IS NOT NULL`) xóa hoặc sửa, vừa được `activeAddons` bỏ qua, nên add-on mất mà không ai báo.
- Đề xuất (tùy chọn): `CHECK (source_subscription_id IS NULL OR source_plan_id IS NULL)`.
- Bảng nhỏ, nhưng thêm CHECK không có `NOT VALID` sẽ quét toàn bảng. Chấp nhận được.

### L3. Down migration 000058 đóng dòng plan đang mở vô hạn

- Vị trí: `apps/api/migrations/000058_entitlement_source_plan.down.sql`.
- `CASE WHEN p.valid_to >= a.valid_to ...` cho `valid_to` NULL thành ngày hết hạn của add-on, đúng với hành vi GREATEST cũ (test reversibility khẳng định dòng pos kết thúc 2026-04-15).
- Quyền hiệu lực không mất, vì `Resolve` đọc module của gói trực tiếp. Chỉ mất thông tin `source_plan_id` và các dòng add-on cũ hơn cùng ngày.
- Đây là thông tin, không cần sửa.

### L4. Test thanh toán trễ chưa khẳng định đủ bất biến

- Vị trí: `apps/api/internal/app/saas_control_integration_test.go:~752-800` (`TestAClosedStoreStaysClosed`, đếm audit tại dòng 776).
- Test chỉ đếm một audit qua hai lần retry. Nó chưa kiểm tra:
  - `actor_type = 'system'`;
  - hóa đơn vẫn `void` và `paid_at` NULL;
  - kỳ của subscription không đổi.
- Đề xuất: thêm ba assertion trên.

## Thông tin (không phải lỗi)

- Cursor hỏng hoặc giả mạo trả 422 `invalid_cursor` theo quy ước `httpx` của dự án, không phải 400 như brief. Mọi endpoint mới đều nhất quán.
- `DELETE /tenants/{id}/feature-flags/{key}`:
  - key sai định dạng trả `not_found`;
  - key đúng định dạng nhưng không có override trả `flag_override_not_found`.
  
  Vì key là tập mở (`ValidKey` theo pattern), điều này khớp spec "no such store or key".
- Không có endpoint idempotent nào trả ETag (`subscription:change` trả 201 + InvoiceDTO, `data-exports` trả 202). Replay trả cùng status và body. Không có ETag để so.

## Đã kiểm tra và đúng

- 000058:
  - `UNIQUE NULLS NOT DISTINCT` chạy được trên PG16;
  - backfill `source_plan_id` đúng, vì chỉ `grantAddon` ghi dòng nguồn NULL và `period_start = valid_from`; ca mơ hồ (hai add-on khác plan cùng ngày) được để NULL qua `o.id IS NULL`;
  - `entitlements` là bảng platform không RLS, `tally_readonly` bị thu hồi ALL, nên cột mới kế thừa quyền đó;
  - khóa và thời gian chạy không đáng kể trên bảng nhỏ;
  - FK `source_plan_id` không có index, nhưng gói không bao giờ bị xóa.
- Entitlement:
  - `Resolve` khử trùng module bằng map;
  - `syncPlanEntitlements` chỉ chạm dòng `source_subscription_id IS NOT NULL`, nên đổi gói hoặc hết kỳ không làm mất add-on;
  - `activeAddons` theo ngày làm việc của cửa hàng (`t.today`);
  - không có đường hủy hoặc hoàn tiền hóa đơn đã `paid` (`VoidInvoice` chỉ áp dụng cho `open`).
- Idempotency:
  - scope riêng (`data_export.request`, change);
  - hash method + path + JSON chuẩn hóa; key dùng lại với body khác trả 409;
  - replay trả cùng status và body, audit một lần;
  - quota 5 job export đếm trong `create`, nên replay không bị tính;
  - re-auth chạy trước replay, đã ghi tài liệu và có test.
- Admin:
  - mọi keyset có id tie-breaker (tenants, incidents, partners, feedback, admins, tickets NULLS LAST, usage);
  - `q` của tenants và audit được escape `\ % _` và truyền tham số;
  - `known_keys` lấy từ `saas.FlagKeys()`;
  - `status=open` và giá trị lạ trả 422;
  - join tenant chỉ lấy slug và display_name;
  - `/me` trả `Cache-Control: no-store`.
- Thanh toán trễ bị từ chối:
  - không đổi tiền hay kỳ, vì giao dịch `applyPayment` rollback;
  - actor là `systemActor` (system);
  - khử trùng theo (provider, event_id) dưới `lockBilling`.
- Đổi tên `IssueExportLink`/`ResolveExportLink` nhất quán giữa `saas/service.go:28-35` và `identity/consent.go`.
- Test dùng `s.clk.Advance`, không phụ thuộc giờ máy.

## Việc nên làm theo thứ tự

1. H1: tách đọc `users` khỏi giao dịch read-only, và thêm test support view có `q`.
2. M2: chỉ hủy hóa đơn add-on mở cùng `plan_id`.
3. M1: gia hạn nối tiếp hoặc từ chối mua lại add-on đang hiệu lực.
4. M3: báo thanh toán thừa trên hóa đơn đã `paid`.
5. L1, L4; L2 là tùy chọn.

## Câu hỏi còn mở

- M1: sản phẩm muốn mua lại add-on đang hiệu lực là gia hạn nối tiếp hay bị từ chối? Đây là quyết định sản phẩm.

Status: DONE_WITH_CONCERNS
Summary: Migration 000058, idempotency, admin keyset/filter và audit thanh toán trễ đúng; test hẹp liên quan và TestMigrationsAreReversible đều pass. Có 1 lỗi High (lọc `q` của audit log trả 500 cho phiên support) và 3 lỗi Medium ở luồng add-on/thanh toán trùng.
Concerns/Blockers: H1 cần sửa trước khi bàn giao; M1 cần lead/chủ sản phẩm chọn giữa gia hạn và từ chối.
