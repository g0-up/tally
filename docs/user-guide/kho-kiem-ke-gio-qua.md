# Kho, kiểm kê và giỏ quà

[Về mục lục](README.md)

Bản 1.1 chỉ có **một kho**. Mọi số tồn đều được tính từ sổ kho chỉ ghi thêm. Không có thao tác nào sửa thẳng số tồn.

## Tồn kho

- **Kho › Tồn kho** hiện tồn của từng hàng **theo màu**.
- Dưới tên hàng có mã, đơn vị cơ sở và các đơn vị lớn đang dùng, ví dụ "ĐV cơ sở: cái · 1 dây = 50 cái · 1 thùng = 500
  cái".
- Số tồn luôn tính theo đơn vị cơ sở. Với hàng có đơn vị lớn, bên dưới số tồn hiện số đó quy ra các đơn vị lớn. Ví dụ
  tồn 1.250 cái hiện thêm "= 2 thùng 5 dây". Tồn nhỏ hơn đơn vị lớn nhỏ nhất thì không hiện dòng quy đổi.
- Sửa đơn vị và giá theo đơn vị ở **Danh mục › Hàng hóa** (xem [Danh mục và bảng giá](danh-muc-bang-gia.md)). Màn hình này
  không có nút sửa đơn vị.
- **Có thể bán** = tồn thực tế − hàng cách ly/hết hạn − hàng đang giữ cho hóa đơn.
- **Sửa giá** → **Lưu giá màu** đặt giá riêng cho một màu.

## Giữ hàng

**Kho › Giữ hàng** liệt kê hàng đang được giữ cho hóa đơn đã xác nhận hoặc lệnh đóng giỏ đã chốt. Bấm **Giải phóng** để trả
phần giữ về tồn có thể bán.

Hai máy cùng bán một mặt hàng sẽ không bán vượt số có thể bán. Hệ thống từ chối giao dịch đến sau nếu không còn đủ hàng.

## Lô & hạn dùng

- Hàng có lô được xuất theo **FEFO**: lô hết hạn trước xuất trước. Lô đã hết hạn bị chặn, không bán được.
- **Lô cách ly** (ví dụ hàng khách trả) cần bạn quyết định: **Đạt – cho bán** hoặc **Không đạt – hủy**. Cho bán lại là một
  ngoại lệ cần xác nhận.
- **Lô hết hạn**: **Tiêu hủy** → **Tiêu hủy lô hết hạn** → **Ghi sổ tiêu hủy**. Thao tác này cần xác nhận ngoại lệ.
- **Lô sắp hết hạn**: **Xả hàng** mở [Chiến dịch mùa vụ](chien-dich-khach-hang.md) để lên kế hoạch xả hàng.

## Kiểm kê

1. **Bắt đầu phiên kiểm kê** → **Bắt đầu kiểm kê** cho cả cửa hàng hoặc một nhóm hàng. Hệ thống chụp số tồn tại thời điểm
   bắt đầu để đối chiếu.
2. **Nhập số đếm** theo từng hàng và từng màu. Đây là kiểm kê mù: số trên sổ hiện "Ẩn khi đếm mù" cho tới khi đếm xong.
   Trong lúc đang đếm, bạn cũng có thể quét mã vạch vào ô quét, hoặc quét bằng điện thoại đã ghép đôi qua ô
   **Quét bằng điện thoại** của phiên (cách ghép đôi ở [Quét bằng điện thoại](ban-hang-pos.md#quét-bằng-điện-thoại)).
   - Mỗi lần quét cộng vào số thực đếm theo hệ số của đơn vị mang mã đó: mã của hộp cộng 1, mã của thùng 24 hộp cộng 24.
   - Tally tra mã trên máy chủ, nên khớp được mọi mã vạch của hàng (mã của hộp, mã của thùng) và cả mã hàng.
   - Đổi hệ số của một đơn vị thì lần quét sau cộng theo hệ số mới. Số đã cộng trước đó giữ nguyên.
   - Mã vạch của đơn vị đã **Ngừng dùng** không quét được nữa.
   - Hàng không thuộc phiên kiểm kê thì không được cộng, và màn hình báo mã đó không có trong phiên.
   - Khi phiên đã hoàn tất đếm, ô quét và ô ghép đôi ẩn đi. Điện thoại đã ghép đôi báo màn hình trên máy tính không
     nhận mã quét.
3. **Hoàn tất đếm**. Dòng nào lệch mà cần đếm lại thì bấm **Đếm lại**.
4. **Xem & ghi sổ** → **Xác nhận & ghi sổ chênh lệch**. Chênh lệch được ghi tại thời điểm chụp số, và cần bạn xác nhận
   ngoại lệ.

## Điều chỉnh kho

**Tạo điều chỉnh** → **Điều chỉnh giảm kho** dùng cho các loại **Hỏng**, **Hao hụt**, **Biếu tặng** và **Dùng nội bộ**. Bạn chọn **Loại**, **Hàng**,
**Số lượng**, **Màu xuất** và **Lý do (bắt buộc)**, rồi bấm **Ghi sổ điều chỉnh**.

Điều chỉnh chỉ **giảm** kho và không tạo doanh thu. Muốn tăng tồn thì dùng phiếu nhận hoặc kiểm kê.

## Thẻ kho

**Kho › Thẻ kho** hiện số đầu kỳ, từng lần nhập/xuất và số dư sau mỗi dòng của một mặt hàng. Số cuối khớp với Tồn kho.

Ở **Hiển thị số lượng**, chọn **Quy đổi tự động** hoặc xem theo từng đơn vị, ví dụ **Theo dây** hay **Theo thùng**. Chỉ
đơn vị đang dùng mới có trong danh sách này. Đơn vị đã **Ngừng dùng** không còn hiện.

## Giỏ quà và combo (Giỏ quà)

Màn hình giỏ quà cần gói thuê bao có module Giỏ quà.

- **Giỏ quà & combo**: bấm **Tạo giỏ / combo**, thêm thành phần (có thể dùng **+ Thêm thùng mix vị**), rồi chọn
  **Lưu & phát hành** hoặc **Lưu nháp**.
  - Giỏ mới ở trạng thái **Nháp** để **Xem trước** giá, lãi và mẫu trình bày. Chỉ bán được sau khi **Phát hành**.
  - Muốn đổi công thức đã phát hành thì bấm **Tạo phiên bản mới**.
- **Giỏ đóng sẵn** có tồn riêng và phải qua lệnh đóng giỏ. **Combo** được ghép lúc bán, không có tồn riêng. Khi bán combo,
  hệ thống trừ thẳng từng thành phần.
- **Lệnh đóng giỏ**:
  - **Tạo lệnh đóng** → **Tạo lệnh** chỉ lên kế hoạch, chưa tiêu hao nguyên liệu;
  - **Chốt & giữ NL** giữ nguyên liệu;
  - **Ghi nhận hoàn thành** → **Ghi sổ hoàn thành** xuất thành phần và nhập giỏ thành phẩm trong cùng một nghiệp vụ.
    Bạn có thể ghi hoàn thành từng phần.
- **Tiến độ & truy xuất lô**: theo dõi giỏ cần cho đơn, đã đóng và đã giao. Bạn truy được từ lô giỏ về lô của từng thành phần.
- **Tháo giỏ** → **Tháo giỏ quà** → **Ghi sổ tháo giỏ** trả thành phần về kho theo lô đã dùng. Vật tư bao bì hỏng không tự
  trở lại tồn bán được.
