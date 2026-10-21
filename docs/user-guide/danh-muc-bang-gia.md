# Danh mục và bảng giá

[Về mục lục](README.md)

## Hàng hóa (Danh mục › Hàng hóa)

- Bấm **Thêm hàng hóa** để tạo hàng. Mỗi mã hàng (SKU) có **Mã SKU**, **Tên hàng**, **Mã vạch**, **Nhóm** và **Tồn tối
  thiểu**. Đơn vị và giá bán nằm trong bảng **Đơn vị tính & giá bán**.
- **Bảng đơn vị tính & giá bán** có cùng một dạng trên mọi màn tạo hàng: **Thêm hàng hóa** ở đây, **Thêm hàng hóa mới**
  ở Nhập hàng › Danh sách hàng hóa, bước khai báo hàng khi bắt đầu dùng Tally, và hàng mới trong đơn nhập hoặc nhập nhanh
  (xem [Nhập hàng](nhap-hang.md#hàng-mới-có-nhiều-đơn-vị)).
  - Dòng đầu là **đơn vị cơ sở**, ví dụ cái. Tồn kho luôn lưu theo đơn vị này. Giá lẻ và giá sỉ của dòng này là giá chung
    của hàng.
  - Bấm **+ Thêm đơn vị** để thêm đơn vị lớn hơn, ví dụ dây hoặc thùng. Ở cột **Quy đổi**, nhập 1 đơn vị đó bằng bao nhiêu
    đơn vị cơ sở. Ví dụ 1 dây = 50 cái, 1 thùng = 500 cái. Một hàng có tối đa 20 đơn vị.
  - Gõ tên đơn vị chưa có rồi chọn **+ Tạo mới** để thêm đơn vị đó vào danh mục đơn vị.
  - Mỗi đơn vị có **Giá bán lẻ** và **Giá sỉ** riêng. Bỏ trống ô giá thì giá được **tự tính** bằng giá đơn vị cơ sở ×
    hệ số. Ô trống hiện sẵn số tự tính đó.
  - Nếu giá lẻ một đơn vị lớn thấp hơn giá một đơn vị cơ sở, bảng cảnh báo vì có thể bạn đã nhập nhầm giá. Bạn vẫn lưu
    được nếu đúng ý.
  - Không lưu được khi hai dòng trùng đơn vị, hoặc khi hệ số bằng 0. Lỗi hiện ngay dưới ô cần sửa.
- Cột **Đơn vị** trong danh sách hàng hóa liệt kê từng đơn vị lớn kèm hệ số và giá lẻ, ví dụ "1 thùng = 500 cái ·
  250.000 ₫". Đơn vị chưa có giá riêng hiện "tự tính".
- Bấm **Đơn vị & giá** trên dòng hàng để sửa cả bảng đơn vị của hàng đã có, rồi bấm **Lưu đơn vị & giá**. Đơn vị, hệ số
  và giá mới chỉ áp dụng cho giao dịch mới. Chứng từ đã lập giữ đơn vị, hệ số và giá đã chụp.
  - Đơn vị đã dùng trên chứng từ, đã có lịch sử giá hoặc có mã vạch thì không xóa được. Bạn chỉ chọn **Ngừng dùng** cho
    đơn vị đó. Đơn vị ngừng dùng không còn để chọn khi bán, nhập hay xem Thẻ kho. Mã vạch của nó cũng không quét được
    nữa. Bỏ chọn Ngừng dùng thì đơn vị dùng lại được.
  - Xóa giá riêng của một đơn vị thì đơn vị đó quay về giá tự tính.
  - Nếu người khác vừa sửa hàng này, việc lưu bị từ chối. Mở lại hộp thoại để sửa trên bản mới nhất.
- Bật **Quản lý theo lô và hạn dùng** cho hàng cần theo dõi lô và hạn. Hàng loại này cần số lô và hạn dùng khi nhập, và
  bán theo lô hết hạn trước (FEFO).
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

- **Sửa giá bán** → **Lưu giá**: bạn sửa **Giá bán lẻ** và **Giá sỉ** của đơn vị cơ sở và của từng đơn vị lớn đang dùng.
  Bỏ trống giá một đơn vị lớn thì đơn vị đó quay về giá tự tính (giá đơn vị cơ sở × hệ số). Giá mới chỉ áp dụng cho giao
  dịch mới. Báo giá và hóa đơn đã lập giữ giá đã chụp.
- Khi bán theo một đơn vị lớn, mỗi mức giá (giá riêng khách, bảng giá nhóm, giá lẻ) dùng giá đặt cho đúng đơn vị đó. Nếu
  mức giá đó chưa có giá cho đơn vị này, giá bằng giá đơn vị cơ sở × hệ số.
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
