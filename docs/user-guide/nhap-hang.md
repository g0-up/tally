# Nhập hàng

[Về mục lục](README.md)

Menu **Nhập hàng** gồm bốn màn hình: Đơn nhập, Danh sách hàng hóa, Nhận hàng và Trả NCC.

## Đơn nhập

**Đơn nhập không tạo tồn hay nợ nhà cung cấp (NCC).** Tồn và nợ chỉ phát sinh khi phiếu nhận được ghi sổ.

1. Bấm **Tạo đơn nhập**, chọn NCC (hoặc **+ NCC mới**), thêm dòng hàng và **Ngày dự kiến giao**. Hàng chưa có trong danh mục
   thì bấm **+ Hàng hóa mới**.
2. Chọn **Lưu & chốt đơn**, hoặc **Lưu nháp** rồi **Chốt đơn** sau.
3. **Nhận hàng** theo đơn. Đơn nhận được **nhiều đợt**.
4. Nếu NCC không giao đủ, bấm **Đóng phần còn thiếu** → **Đóng đơn**. Phần đã nhận giữ nguyên, phần còn chờ không còn
   tính là "dự kiến về".
5. Trả tiền trước khi nhận hàng thì dùng **Ứng trước NCC** → **Ghi phiếu chi**. Khoản này là ứng trước, chưa phải giảm nợ.
   Hệ thống tự phân bổ khi phiếu nhận được ghi.

## Nhận hàng

- **Nhập nhanh** dùng khi không có đơn nhập. Bạn chọn hàng có sẵn hoặc thêm hàng mới, nhập **Số hóa đơn NCC (nếu có)** và
  **Chi phí vận chuyển/bốc xếp**. Chi phí này được phân bổ vào giá vốn theo giá trị hàng.
- Bấm **Ghi sổ nhập kho**. Phiếu nhận ghi sổ làm **tăng tồn** và **phát sinh nợ NCC** theo giá nhận.
- Hàng theo lô hoặc có hạn dùng thì cần nhập số lô và hạn dùng.
- **Đối chiếu HĐ** → **Lưu đối chiếu** gắn số hóa đơn NCC đến sau. Bước này chỉ đối chiếu chứng từ, không ghi lại nợ.
- **Đính kèm ảnh chứng từ** để lưu ảnh hóa đơn giấy cùng phiếu nhận.

## Quét mã vạch khi nhập hàng

Các hộp thoại **Tạo đơn nhập**, **Nhập nhanh** và **Nhận hàng theo** một đơn đều có ô **Quét bằng điện thoại** ngay
trên bảng dòng hàng. Cách ghép đôi điện thoại giống ở POS, xem
[Quét bằng điện thoại](ban-hang-pos.md#quét-bằng-điện-thoại). Sau đó mỗi mã điện thoại đọc được đi vào bảng dòng hàng
như sau.

- **Mã đã có trong danh mục:** thêm một dòng hàng đó theo đúng đơn vị của mã, ví dụ mã của thùng thêm dòng theo thùng.
  Quét lại cùng mã thì dòng đó cộng thêm 1. Mã của giỏ quà hay combo không được thêm, vì đó không phải hàng nhập.
- **Mã lạ:** mở một dòng hàng mới có ô **Mã vạch** điền sẵn mã đó, con trỏ ở ô tên để bạn nhập thông tin. Quét lại cùng
  mã thì dòng hàng mới cộng thêm 1. Nếu bạn đã đổi dòng hàng mới đó sang nhập theo thùng, quét lại không tự cộng nữa.
  Điện thoại báo cần sửa số lượng trên máy tính.
- **Ô Mã vạch** của dòng hàng mới không bắt buộc và gõ tay được. Khi lưu, mã vạch được gắn vào đơn vị cơ sở của hàng mới.
  Hai dòng hàng mới mang cùng một mã bị chặn trước khi gửi. Mã đã thuộc hàng khác thì lỗi "Mã vạch đã được dùng cho hàng
  khác." hiện ngay dưới ô Mã vạch của đúng dòng. Tally coi các cách viết của cùng một mã EAN/UPC, chẳng hạn thêm hay bớt
  số 0 ở đầu, là cùng một mã.
- **Nhận hàng theo đơn:** chỉ nhận hàng có trong đơn. Mỗi dòng mở ra với số còn chờ nhận. Lần quét đầu tiên vào một dòng
  thay số đó bằng đúng lượng vừa quét, tính theo hệ số của đơn vị mang mã. Các lần quét sau cộng thêm. Ví dụ dòng còn
  chờ 24 hộp: quét một thùng 12 hộp thì dòng thành 12, quét thêm một thùng thì thành 24, quét một hộp lẻ thì thành 25.
  Số gõ tay trước lần quét đầu không được tính, nên lần quét đầu vẫn thay số đó. Mở lại phiếu thì các dòng lại tính như
  chưa quét. Dung sai khi ghi sổ vẫn kiểm tra như cũ.
- Khi một hàng nằm ở nhiều dòng của đơn, ví dụ khác màu hoặc khác giá, mỗi lần quét vào dòng đầu tiên chưa đủ số còn chờ.
  Khi mọi dòng của hàng đó đã đủ, phần quét thêm cộng vào dòng đầu tiên. Hãy kiểm tra lại số trên từng dòng trước khi ghi
  sổ, vì mã vạch không phân biệt màu.
- Mã của hàng ngoài đơn hoặc mã lạ không thêm gì: máy tính báo mã không có trong đơn nhập này, và điện thoại báo "Hàng
  này không có trong đơn nhập."
- Trong lúc đang lưu hoặc ghi sổ, hoặc khi một hộp thoại khác đang mở đè lên phiếu (ví dụ thêm nhà cung cấp mới), phiếu
  không nhận mã và điện thoại báo "Màn hình trên máy tính không nhận mã quét". Lưu xong hoặc đóng hộp thoại đó thì quét
  tiếp được.

## Trả nhà cung cấp

Bấm **Tạo phiếu trả NCC**, chọn **Phiếu nhận gốc**, rồi chọn hàng **đúng lô đã nhận** và lý do. Bấm **Ghi sổ xuất trả**.

Phiếu trả làm giảm nợ NCC. Nếu đã trả tiền nhiều hơn số còn nợ, phần chênh ghi thành khoản NCC cần hoàn lại cho cửa hàng.

## Danh sách hàng hóa

Màn hình này cho xem đơn vị và quy đổi, giá nhập gần nhất, giá vốn và giá bán. Bạn **Thêm hàng hóa mới** hoặc **Sửa giá bán**
ngay tại đây. Chi tiết danh mục nằm ở [Danh mục và bảng giá](danh-muc-bang-gia.md).

## Trả tiền nhà cung cấp

Trả nợ NCC ở **Công nợ › Cửa hàng còn nợ NCC** (xem [Công nợ và quỹ](cong-no-quy.md#cửa-hàng-còn-nợ-ncc)).
