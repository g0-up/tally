# Rút gọn mã: tính năng quét mã vạch bằng điện thoại

Phạm vi: toàn bộ file được giao (backend `internal/scanner/**`, `catalog/code_variants.go`, `catalog/code_lookup.go`; web `features/scanner/**`, `shared/lib/barcode.ts`, `catalog/api/sku-lookup.ts`, `pos/lib/scan-match.ts`, `catalog/containers/barcode-units-drawer.tsx`, `procurement/buy-lines.ts`). Nguyên tắc: chỉ sửa khi lợi ích rõ ràng và không đổi hành vi, hợp đồng công khai, thời gian, giới hạn, câu chữ hay assertion của test.

## Thay đổi

1. `apps/web/src/features/scanner/phone/use-phone-session.ts`
   - Bỏ schema cục bộ `revokedSchema` và dùng lại `revokedEventSchema` trong `features/scanner/api/schemas.ts`. Hai schema giống hệt nhau (`z.object({ by: z.string().optional() })`), và phía desktop vốn đã dùng bản trong `api/schemas.ts`, nên giờ sự kiện `revoked` chỉ có một định nghĩa.
   - Đưa `live()` lên trước `emit` và dùng `reconnecting && live()` thay cho biểu thức `view.kind === "pending" || view.kind === "claimed"` viết lại trong `emit`. Logic như cũ, nhưng điều kiện "phiên còn sống" giờ chỉ nằm ở một chỗ.

2. `apps/web/src/features/procurement/buy-lines.ts`
   - Thêm hàm nội bộ `changeRow(lines, i, change)` (không export) thay cho ba lần lặp `lines.map((l, j) => (j === i ? ... : l))` trong `scanNewProduct` và `addScan`. Kết quả giống hệt, các dòng return ngắn hơn và dễ đọc hơn.

## Đã xem nhưng giữ nguyên

- Backend Go (`scanner/*.go`, `catalog/code_variants.go`, `catalog/code_lookup.go`): mã đã gọn, chú thích bám sát các bất biến. Đoạn kiểm tra claim code lặp lại trong `CheckClaimCode` và `Claim` chỉ có hai dòng và đọc rõ ràng, nên tách helper ra không đáng.
- `scanner-controller.ts`: cặp `forget(); reconcile();` lặp lại vài lần, và `create`/`reject` cùng đặt lại trạng thái "waiting". Gộp lại chỉ bớt được vài dòng mà lại che mất thứ tự gọi có chủ đích (trong `resume` có `emit()` chen giữa), nên giữ nguyên.
- Các file còn lại (`event-stream.ts`, `reconnect.ts`, `session-store.ts`, `scan-loop.ts`, `use-camera.ts`, `send-scan.ts`, `barcode-reader.ts`, `feedback.ts`, `outcome-copy.ts`, `scan-page.tsx`, `phone-pairing-card.tsx`, `pairing-qr.tsx`, `barcode.ts`, `sku-lookup.ts`, `scan-match.ts`, `barcode-units-drawer.tsx`): không có mã chết hay chỗ trùng lặp đáng sửa.

## Kết quả kiểm tra

- `apps/web`: `corepack pnpm lint` sạch; `corepack pnpm typecheck` sạch; `corepack pnpm test` qua 174 file, 1320 test.
- `apps/api`: `go vet ./...` sạch; `go test -race ./internal/scanner/... ./internal/catalog/...` qua; `go test -race -tags integration ./internal/scanner/...` qua (29 giây). Đã dừng container reaper của testcontainers do lần chạy này tạo ra. Không đụng tới container prod nào.
- Không có thao tác git nào, cũng không để lại tiến trình nền.
