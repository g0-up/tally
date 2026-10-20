# Danh mục và bảng giá

[Về mục lục](README.md)

## Hàng hóa (Danh mục › Hàng hóa)

- Bấm **Thêm hàng hóa** để tạo hàng. Mỗi mã hàng (SKU) có **Mã SKU**, **Tên hàng**, **Mã vạch**, **Nhóm**, **Đơn vị cơ sở**
  và các đơn vị quy đổi với **Hệ số quy đổi**. Bạn cũng đặt được **Tồn tối thiểu**.
- Bật **Quản lý theo lô và hạn dùng** cho hàng cần theo dõi lô và hạn. Hàng loại này cần số lô và hạn dùng khi nhập, và
  bán theo lô hết hạn trước (FEFO).
- Hệ số quy đổi được chụp trên từng dòng chứng từ. **Đổi hệ số không sửa lịch sử.**
- Bấm **Mã vạch** trên dòng hàng để xem các mã vạch của hàng đó và đơn vị mỗi mã được tính khi quét. Chọn đơn vị khác
  trong cột **Đơn vị khi quét** rồi bấm **Lưu**, ví dụ đổi mã in trên thùng sang đơn vị thùng. Ô chọn chỉ đưa ra các đơn
  vị đang dùng. Nếu bị từ chối, ô chọn quay về đơn vị cũ và lý do hiện ngay bên dưới. Thêm hoặc xóa mã vạch không làm
  trong ngăn này.
- Sau khi lưu, Kiểm kê, Nhập hàng và POS (với hàng không có ô hàng bên trái màn POS) tính mã đó theo đơn vị mới ngay từ lần
  quét sau. Với hàng có ô hàng trên POS, POS ở tab hay máy khác vẫn tính theo đơn vị cũ cho tới khi tải lại hàng bán: sau khi
  ghi sổ một đơn bán, lưu hoặc xóa một giỏ, khi quay lại cửa sổ POS, khi có mạng trở lại, hoặc khi mở lại màn POS. Dữ
  liệu bán khi mất mạng nhận đơn vị mới ở lần làm mới kế tiếp (khi mở POS lúc có mạng, khi có mạng trở lại, hoặc mỗi 30
  phút).
- Giá mua tham khảo không tự thành giá vốn. Giá vốn tính từ phiếu nhận đã ghi sổ (bình quân di động).

## Nhà cung cấp (Danh mục › Nhà cung cấp)

- **Thêm nhà cung cấp** gồm các trường: tên, người liên hệ, số điện thoại, mã số thuế, địa chỉ, **Số ngày nợ được phép**
  (0 = trả ngay khi nhận hàng), mặt hàng cung cấp và ghi chú.
- **Ngừng giao dịch** ẩn NCC khỏi ô chọn khi lập đơn nhập. NCC vẫn giữ lịch sử và cửa hàng vẫn trả nợ được.
- Chỉ **Xóa** được NCC chưa có chứng từ nào.

## Bảng giá (Danh mục › Bảng giá)

Giá bán được chọn theo thứ tự ưu tiên: **giá riêng khách → bảng giá nhóm khách → giá lẻ**. Mỗi dòng chứng từ hiện nguồn giá
và chụp giá khi xác nhận.

- **Sửa giá bán** → **Lưu giá**: bạn sửa **Giá bán lẻ** và **Giá sỉ** theo đơn vị cơ sở. Giá mới chỉ áp dụng cho giao dịch
  mới. Báo giá và hóa đơn đã lập giữ giá đã chụp.
- Một màu có thể có giá riêng. Sửa ở **Kho › Tồn kho** bằng **Sửa giá** → **Lưu giá màu**. Khi sửa giá chung, bạn có thể
  chọn áp dụng cho tất cả màu để bỏ giá riêng của từng màu.
- Hộp thoại sửa giá hiện **Giá vốn BQ**, biên lãi và **Giá tối thiểu (cảnh báo nếu bán thấp hơn)**. Bán dưới giá tối thiểu
  cần bạn xác nhận ngoại lệ.
- **Lịch sử thay đổi giá bán** ghi ai sửa, sửa ở đâu và lúc nào.
- Khách được gán nhóm giá ở hồ sơ khách (xem [Khách hàng](chien-dich-khach-hang.md#khách-hàng-crm)).

## Nhóm mix vị

Nhóm mix gom các vị cùng quy cách để bán theo **thùng mix**. Khách chọn số dây/lốc của từng vị, và tổng
không vượt số thùng × số dây/lốc mỗi thùng.

- **Thêm nhóm mix** → **Tạo nhóm**. Bạn khai báo **Tên nhóm mix**, **Đơn vị thùng**, **Số đơn vị lẻ / thùng**,
  **Đơn vị chọn vị** và **Số đơn vị lẻ / đơn vị chọn vị**.
- Các vị trong nhóm phải cùng đơn vị lẻ.
- Trên báo giá/hóa đơn, đơn giá thùng mix mặc định là giá bình quân các vị. Thùng chưa đủ dây/lốc vẫn lưu được và
  tính tiền theo số dây/lốc thực chọn. Trong giỏ quà, giá tính theo từng vị.
- Trên chứng từ đã lưu và bản in, đơn giá thùng mix = tiền các vị trước chiết khấu ÷ số thùng, để SL × đơn giá khớp
  thành tiền trước chiết khấu. Vì vậy thùng chưa đủ dây/lốc có đơn giá thấp hơn thùng đủ.
- Nhóm xuất hiện ở nút **+ Thêm thùng mix vị** của báo giá, hóa đơn, giỏ quà và combo.
