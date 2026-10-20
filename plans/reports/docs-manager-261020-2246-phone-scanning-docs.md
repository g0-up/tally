# Báo cáo tài liệu: quét mã vạch bằng điện thoại ghép đôi

Phạm vi: các bước 1 đến 3 (tài liệu) của phase tài liệu, triển khai và chạy thử trong kế hoạch quét bằng điện thoại.
Mọi câu được đối chiếu với code đang có trong working tree (chưa commit), không chỉ với kế hoạch. Không sửa
`docs/user-guide/danh-muc-bang-gia.md`, dòng Danh mục trong mục lục, `docs/qna.md` hay code. Không chạy `make db-docs`.

## File đã sửa

### `docs/user-guide/ban-hang-pos.md`

- Bước 1 của "Bán một đơn" nhắc cách quét bằng điện thoại (có liên kết tới mục mới) và nói mã của đơn vị lớn thêm dòng
  theo đơn vị đó.
- Mục mới **Quét bằng điện thoại**, chia bốn phần:
  - *Ghép đôi*: ô "Quét bằng điện thoại" dưới ô tìm hàng, QR tự đổi sau khoảng 2 phút, điện thoại hiện "Mã xác minh" 4
    chữ số, chỉ bấm **Cho phép** khi hai mã giống nhau, **Từ chối** cho QR mới, không ai bấm trong khoảng 2 phút thì
    kết thúc, **Bật camera** và quyền camera (Safari iOS có thể hỏi lại mỗi lần mở trang).
  - *Khi quét*: mỗi mã gửi một lần khi còn trong khung, thêm theo đúng đơn vị của mã, chữ gõ dở trong ô tìm không ảnh
    hưởng, danh sách "Kết quả gần nhất", Android rung một hoặc ba lần, iPhone bíp kèm nháy xanh hoặc đỏ sau khi chạm
    Bật camera, POS không nhận mã khi đang thanh toán hoặc mở hộp thoại, câu "Máy tính chưa phản hồi" thì nhìn máy tính
    trước khi quét lại, khóa màn hình làm camera tắt.
  - *Ngắt và kết thúc*: nút **Ngắt** / **Ngắt kết nối**, phiên theo tab nên chuyển POS, Kiểm kê, Nhập hàng không phải
    ghép lại, nút **Ghép đôi ở tab này**, phiên kết thúc khi tab vắng khoảng 1 phút, khi đăng xuất, sau 12 giờ kể từ lúc
    cho phép; tối đa 3 lần ghép đôi, lần thứ tư đóng lần cũ nhất chưa được cho phép; máy chủ khởi động lại thì tự kết
    nối lại.
  - *Sự cố*: điện thoại giữ bản cũ thì bấm **Tải lại** ở thông báo "Đã có phiên bản mới. Tải lại để cập nhật." hoặc
    đóng mọi tab của cửa hàng rồi quét lại QR; QR hết hạn; trình duyệt không hỗ trợ.
- "Bán khi mất mạng" thêm hai quy tắc: chỉ bán theo đơn vị cơ sở (mã của đơn vị khác bị từ chối kèm lời báo cần có
  mạng), và không quét bằng điện thoại được.

### `docs/user-guide/kho-kiem-ke-gio-qua.md`

Bước 2 của "Kiểm kê": khi đang đếm có thể quét vào ô quét hoặc bằng điện thoại; mỗi lần cộng theo hệ số của đơn vị mang
mã (thùng 24 hộp cộng 24); tra mã trên máy chủ nên khớp mọi mã vạch và mã hàng; hàng ngoài phiên bị báo; hết trạng thái
đang đếm thì ô quét và ô ghép đôi ẩn, điện thoại báo màn hình không nhận mã.

### `docs/user-guide/nhap-hang.md`

Mục mới **Quét mã vạch khi nhập hàng**: ô ghép đôi ở các hộp thoại Tạo đơn nhập, Nhập nhanh và Nhận hàng theo đơn; mã
đã có thêm dòng theo đơn vị của mã và quét lại cộng 1; giỏ quà/combo không thêm; mã lạ mở dòng hàng mới có ô Mã vạch
điền sẵn, con trỏ ở ô tên, quét lại cộng 1, dòng đã đổi sang thùng thì không tự cộng; ô Mã vạch gõ tay được, gắn vào
đơn vị cơ sở, hai dòng trùng mã bị chặn, mã đã thuộc hàng khác báo dưới ô; biến thể số 0 đầu của EAN/UPC coi là cùng
mã; nhận theo đơn chỉ nhận hàng trong đơn và cộng theo hệ số, mã ngoài đơn không thêm gì.

### `docs/user-guide/README.md`

Ba dòng mục lục POS, Nhập hàng và Kho/Kiểm kê nhắc quét bằng điện thoại. Dòng Danh mục giữ nguyên.

### `docs/operations/device-browser-matrix.md` (tiếng Anh, giữ ngôn ngữ của file)

- Bảng §1 thêm hàng "Paired phone as scanner" cho Android Chrome và iOS Safari.
- §3 thêm mục kiểm tra tay: Cho phép, Từ chối, EAN-13, UPC-A, mã thùng, mã lạ, ngắt từ hai bên, khóa màn hình, chuyển
  POS sang Kiểm kê, khởi động lại `api`, PWA bản trước, đăng xuất trên máy tính. Thêm câu không ghi QR, mã xác minh hay
  cookie.
- §6 sửa câu về máy quét: máy quét cầm tay vẫn phải chạy như bàn phím kết thúc bằng Enter, điện thoại ghép đôi là cách
  quét thứ hai, không cần driver.

### `docs/runbook.md` (tiếng Anh)

- Mục kiểm tra trình duyệt và thiết bị nhắc điện thoại ghép đôi và việc phải chạy trên tenant thử của stack thật.
- Bước 7 của first deploy checklist: `/scan` có `camera=(self)`, CSP `script-src 'self' 'wasm-unsafe-eval'`,
  `referrer-policy: no-referrer`; `/` và `/sales/pos` giữ `camera=()` và CSP không có `script-src`; `.wasm` trả
  `application/wasm` và gzip; câu ưu tiên router đổi thành 4, 3 và 2 (`tally-web-scan`, `tally-api`, `tally-web`),
  kèm kiểm tra `tiendouong.sidecup.app/scan`.
- Routine operations thêm lệnh kiểm tra compose bằng `infra/.env.example` như CI.
- Mục mới **Phone scanner pairing**: một replica `api` vì hub SSE trong bộ nhớ; khởi động lại gửi `restarting`, hai
  bên tự nối lại, phiên chết nếu luồng máy tính vắng quá hạn (60 giây sau lần làm mới `desktop_seen_at`, làm mới mỗi 30
  giây); phiên theo phiên đăng nhập (kiểm tra mỗi khoảng 10 giây khi luồng mở; đăng xuất, "Đăng xuất các thiết bị
  khác", `tally maintenance revoke-sessions`, hết hạn; xác thực lại không làm mất phiên); worker xóa phiên cũ hơn 24 giờ
  trong vòng purge sẵn có.

## Đối chiếu nguồn

| Nội dung | Nguồn |
|---|---|
| Nhãn ô ghép đôi, Cho phép, Từ chối, Ngắt, Ghép đôi ở tab này, Đang kết nối lại… | `apps/web/src/features/scanner/components/phone-pairing-card.tsx` |
| Trang điện thoại: Mã xác minh, Bật camera, Ngắt kết nối, Kết quả gần nhất, QR hết hạn | `apps/web/src/features/scanner/pages/scan-page.tsx` |
| Câu kết quả trên điện thoại (không nhận mã, chưa phản hồi, không có trong đơn nhập, nhập theo thùng) | `apps/web/src/features/scanner/phone/outcome-copy.ts` |
| Rung một/ba lần, bíp cao/thấp, nháy, mở khóa âm thanh khi chạm | `apps/web/src/features/scanner/phone/feedback.ts` |
| Camera tắt khi rời trang, hướng dẫn mở lại quyền | `apps/web/src/features/scanner/phone/use-camera.ts` |
| Phiên theo tab (`sessionStorage`), provider trong AppShell | `features/scanner/session-store.ts`, `app/layouts/app-shell.tsx` |
| Thông báo cập nhật và nút Tải lại | `features/pos/offline/app-update.ts`, `features/pos/offline/components/update-prompt.tsx` |
| POS: khớp mã, offline chỉ đơn vị cơ sở | `apps/web/src/features/pos/lib/scan-match.ts` |
| Kiểm kê: cộng theo `factor`, chỉ khi `counting`, báo hàng ngoài phiên | `apps/web/src/features/inventory/components/count-drawer.tsx` |
| Nhập hàng: `addScan`, `scanNewProduct`, ô Mã vạch, câu báo | `features/procurement/buy-lines.ts`, `components/buy-preset.tsx`, `api/adapters.ts`, tiêu đề hộp thoại ở `containers/` |
| Mã vạch hàng mới gắn đơn vị cơ sở; câu `barcode_taken` | `apps/api/internal/procurement/new_products.go`, `apps/api/internal/catalog/errors.go` |
| Thời hạn 2 phút, 60 giây, 30 giây, 12 giờ, 3 phiên, 24 giờ | `apps/api/internal/scanner/model.go`, `repo.go` (`endedSQL`) |
| Nhịp kiểm tra 10 giây, ping 15 giây | `apps/api/internal/scanner/sse.go` |
| Kiểm tra phiên đăng nhập theo id, thu hồi khi mất | `apps/api/internal/scanner/http.go` (`desktopCheck`), `identity/sessions.go` |
| `restarting` khi tắt máy | `apps/api/internal/scanner/hub.go` (`CloseAll`), `apps/api/internal/cli/serve.go` |
| Worker xóa phiên | `apps/api/internal/cli/worker.go` (`purgeScannerSessions`) |
| `revoke-sessions` | `apps/api/internal/cli/maintenance.go` |
| Header `/scan`, ưu tiên router 4/3/2 | `infra/docker-compose.homelab.yml` |
| `.wasm` gzip | `apps/web/nginx.conf` (`gzip_types`) |
| Lệnh compose với `.env.example` | `.github/workflows/ci.yml` |
| Route `/scan`, `/sales/pos` | `apps/web/src/app/router.tsx`, `features/onboarding/steps/first-sale-step.tsx` |

## Kiểm tra

- Script kiểm tra mọi liên kết tương đối và anchor trong sáu file đã sửa: 0 lỗi (gồm `#quét-bằng-điện-thoại`,
  `#kiểm-kê`, `#quét-mã-vạch-khi-nhập-hàng` và ba liên kết tới file Go trong runbook).
- Độ dài: lớn nhất là `docs/runbook.md` với 521 dòng, dưới 800.
- Không có mã ghép đôi, mã xác minh, cookie hay bí mật trong ví dụ; không thêm mã kế hoạch hay số phase.

## Lưu ý

- Hai hành vi được ghi theo thông báo của coordinator, chưa thấy trong code lúc viết: POS bỏ qua mã từ điện thoại khi
  đang thanh toán hoặc mở hộp thoại (`pos-page.tsx` hiện chỉ gọi `useScanTarget` không điều kiện), và phiên thứ tư đóng
  phiên chưa ghép đôi cũ nhất trước, kèm nút ghép đôi lại trên tab bị đóng. Cần đối chiếu lại khi code đó vào.
- Câu "đăng xuất trên máy tính thì điện thoại báo trong khoảng 15 giây" chỉ có ở ma trận thiết bị như một mục cần đo.
  Code chỉ thu hồi ngay khi luồng máy tính còn mở; nếu đăng xuất làm đóng luồng thì phiên chết theo thời gian vắng của
  máy tính (30 đến 60 giây). Runbook ghi cả hai đường; chạy thử trên máy thật sẽ cho biết đường nào xảy ra.
- Câu "Safari iOS hỏi lại quyền camera mỗi lần mở trang" lấy từ nghiên cứu, ghi là "có thể" cho tới khi chạy thử xác nhận.

Status: DONE_WITH_CONCERNS
Summary: Đã cập nhật hướng dẫn POS, Kiểm kê, Nhập hàng, mục lục, ma trận thiết bị và runbook theo code hiện có; mọi liên kết và anchor đều đúng.
Concerns/Blockers: Hai hành vi (POS bỏ qua mã khi thanh toán hoặc mở hộp thoại; quy tắc phiên thứ tư) được ghi theo coordinator và chưa có trong code lúc viết.
