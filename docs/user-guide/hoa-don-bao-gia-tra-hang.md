# Báo giá, hóa đơn, trả và đổi hàng

[Về mục lục](README.md)

Hóa đơn dùng cho bán sỉ và quà doanh nghiệp. Đây là **chứng từ nội bộ, không phải hóa đơn điện tử**.

## Báo giá (Bán hàng › Báo giá)

Vòng đời của báo giá: **Nháp → Đã gửi → Khách chấp nhận / từ chối → Chuyển thành hóa đơn**.

- Bấm **Tạo báo giá**, thêm dòng hàng, rồi chọn **Lưu & gửi khách** hoặc **Lưu nháp**. Mỗi dòng có **Quy cách** (ghi chú
  tự do) và **Đơn giá** báo cho khách. Cột **Giá bán lẻ** chỉ để so sánh.
- **+ Thêm thùng mix vị** thêm một thùng gồm nhiều vị cùng quy cách. Thùng mix lấy vị từ nhóm khai báo ở
  [Danh mục › Nhóm mix vị](danh-muc-bang-gia.md#nhóm-mix-vị).
- Báo giá đã gửi không sửa được. Muốn đổi nội dung thì dùng **Phiên bản mới** và ghi **Lý do thay đổi (bắt buộc)**.
- Quá ngày hiệu lực, báo giá tự chuyển **Hết hạn**. Khi đó dùng **Gia hạn / phiên bản mới**.
- **Báo giá không giữ hàng.** Khi khách chấp nhận, bấm **Chuyển thành hóa đơn**.

## Hóa đơn (Bán hàng › Hóa đơn)

1. Bấm **Tạo hóa đơn**, chọn khách và thêm dòng hàng. Hàng có màu thì chọn màu cho từng dòng.
2. **Lưu nháp** để sửa tiếp, hoặc **Lưu & xác nhận bán**. Hóa đơn nháp sửa hoặc xóa được. Hóa đơn đã xác nhận bị khóa,
   chỉ **Sao chép** được.
3. **Xác nhận bán chỉ giữ hàng.** Tồn kho, doanh thu và công nợ chỉ thay đổi khi xuất kho.
4. **Xuất kho giao khách** → **Ghi sổ xuất kho**. Một bước này trừ kho, ghi doanh thu và công nợ. Bạn có thể xuất nhiều đợt.
   Khi đó hóa đơn ở trạng thái **Giao một phần** cho tới khi **Hoàn tất**.
5. **Giữ thêm hàng** khi muốn giữ thêm tồn cho hóa đơn. **Hủy phần chưa giao** → **Hủy phần còn lại** sẽ trả phần
   giữ hàng còn lại.
6. **Ghi nhận thanh toán** → **Ghi phiếu thu**. Tiền thu trước khi xuất kho là **tiền ứng trước / cọc** của khách. Hệ thống
   tự trừ khoản này vào công nợ khi xuất kho và tự tính lại trạng thái thanh toán (xem
   [Ứng trước & cọc](cong-no-quy.md#ứng-trước--cọc)).

Nếu vượt hạn mức nợ của khách hoặc bán dưới giá tối thiểu, bạn phải tích **Tôi xác nhận vẫn bán nếu vượt hạn mức nợ hoặc
dưới giá tối thiểu (ghi vào nhật ký)**.

Bạn lọc danh sách theo trạng thái (**Nháp**, **Chưa xuất đủ**, **Hoàn tất**) và theo thanh toán (**Chưa thanh toán**,
**Một phần**, **Đã đủ**).

> Bản 1.1 không có giao nhận: chưa có vận đơn, người giao hay thu hộ COD. "Xuất kho giao khách" ghi nhận hàng đã rời kho.

## Trả hàng (Bán hàng › Trả hàng)

- **Tạo phiếu trả** → **Tạo phiếu trả hàng**. Bạn trả theo chứng từ bán gốc, và số trả không vượt số đã giao. Không có
  chứng từ gốc thì tích **Không có chứng từ gốc**.
- Hàng trả có thể đưa vào **khu cách ly** để kiểm tra trước khi bán lại (xem [Lô & hạn dùng](kho-kiem-ke-gio-qua.md#lô--hạn-dùng)).
- Bấm **Ghi sổ trả hàng**. Giá trị hàng trả được trừ vào công nợ của khách trước. Phần dư thành khoản phải hoàn tiền cho
  khách, và bạn chi bằng **Hoàn tiền** → **Ghi phiếu chi**.
- Với khách lẻ, có thể chọn **Hoàn tiền từ quỹ** ngay trên phiếu trả.

## Đổi hàng, đổi màu

Mở chứng từ bán và bấm **Đổi hàng**. Phiếu đổi gồm hai phần:

1. **Hàng khách trả lại**;
2. **Hàng khách lấy đổi**. Bấm **Đổi sang màu khác** để đổi đúng mặt hàng sang màu khác.

Chênh lệch tiền được thu hoặc trả qua quỹ chọn ở **Thu / trả chênh lệch qua**. Lý do mặc định là "Đổi màu / đổi mẫu".
Bấm **Ghi sổ đổi hàng** để ghi trả và xuất trong cùng một chứng từ `DH`.

## In phiếu

Trên chứng từ bán, bấm **In phiếu** và chọn **Khổ A5** hoặc **Giấy cuộn 80mm**.
