# Bán hàng tại quầy (POS)

[Về mục lục](README.md)

Mở **Bán hàng › POS & giỏ hàng**. Màn hình POS cần gói thuê bao có module Bán tại quầy.

## Bán một đơn

1. Quét mã vạch hoặc gõ mã/tên hàng vào ô tìm rồi nhấn Enter. Bạn cũng có thể bấm ô hàng bên trái. Mỗi ô hiện
   **Có thể bán** và các màu còn hàng.
2. Hàng có màu thì chọn **Màu khách lấy**. Khách lấy nhiều màu của cùng một hàng thì bấm **+ Thêm màu khác** để tách dòng.
   POS chỉ trừ đúng màu đã chọn.
3. Sửa số lượng, đơn vị tính hoặc chiết khấu % trên từng dòng nếu cần.
4. Mặc định là bán cho khách lẻ, theo **Giá bán lẻ**. Chọn khách hàng thì giá đổi theo bảng giá của khách. Dòng
   **Nguồn giá** cho biết giá lấy từ đâu, còn hạn mức nợ và số khách đang nợ hiện cạnh tên khách.
5. Bấm **Thanh toán · F9**. Nhập **Tiền mặt khách đưa** và/hoặc **Chuyển khoản / QR**. Màn hình tính **Trả lại khách**.
6. Khách có hồ sơ mà trả chưa đủ thì tích **Bán chịu phần còn lại**. Không bán chịu được cho khách lẻ. Nếu vượt hạn mức
   nợ hoặc bán dưới giá tối thiểu, bạn phải xác nhận ngoại lệ.

Phím tắt: **F2** tìm hàng, **F4** lưu giỏ, **F9** thanh toán.

Ca bán hàng tự mở khi có đơn đầu tiên trong ngày. Đơn bấm thanh toán hai lần vẫn chỉ được ghi sổ một lần.

> Tiền mặt và chuyển khoản cần một hình thức thanh toán đang dùng. Nếu POS báo "Chưa có hình thức … đang dùng",
> hãy liên hệ hỗ trợ. Bản 1.1 chưa có màn hình để bạn tự khai báo hình thức thanh toán.

## Giỏ hàng đã lưu

- **Lưu giỏ · F4** giữ giỏ cho một khách để bán sau. **Giỏ lưu chưa giữ hàng**, nên tồn kho vẫn có thể bị bán mất.
- Bảng **Giỏ hàng đã lưu** lọc theo **Đang soạn**, **Đã bán** hoặc **Tất cả**. Giỏ đang soạn thì **Sửa** hoặc **Xóa** được.
- Giỏ đã bán thành công bị khóa, chỉ **Sao chép** được thành giỏ mới.

## Bán khi mất mạng

Tính năng này phải được bật trước tại **Thiết lập › Tài khoản & bảo mật › Chính sách bán ngoại tuyến**. Bạn đặt:

- **Hạn mức tiền mặt ngoại tuyến**: tổng tiền các giao dịch chờ đồng bộ trên một máy;
- **Số giờ tối đa không đồng bộ**: từ 1 đến 72 giờ kể từ lần tải dữ liệu gần nhất.

Máy bán hàng tải dữ liệu ngoại tuyến (hàng, giá lẻ, tồn) khi mở POS lúc có mạng. Trên iPhone/iPad, hãy thêm ứng dụng vào
Màn hình chính. Nếu không, trình duyệt có thể xóa dữ liệu ngoại tuyến.

Khi mất mạng, POS hiện **Đang ngoại tuyến** và chỉ cho bán theo các quy tắc sau:

- chỉ bán **thu đủ tiền mặt**: không chuyển khoản/QR, không bán chịu, không hoàn tiền;
- chỉ bán **giá lẻ cho khách lẻ**, theo giá đã tải về máy. Tồn hiển thị có thể chưa chính xác;
- không lưu giỏ và không thanh toán giỏ đã lưu. Bấm **Giỏ mới** để bán;
- không bán khi đã vượt hạn mức tiền, đã quá số giờ cho phép, hoặc cửa hàng tính giá chưa gồm thuế.

Mỗi giao dịch được lưu trên máy và hiện trong **Giao dịch chờ đồng bộ**. Khi có mạng lại, hệ thống tự gửi lên. Bạn cũng
có thể bấm **Đồng bộ ngay**. Mỗi giao dịch chỉ được ghi sổ một lần, kể cả khi tải lại trang hoặc gửi lại.

Lúc đồng bộ, máy chủ kiểm tra lại giá, tồn và khuyến mãi. Có hai trường hợp không ghi sổ được:

- **Hàng đợi ngoại lệ**: máy chủ chưa ghi sổ được giao dịch, ví dụ vì giá dưới giá tối thiểu. Giao dịch không bị mất.
  Bấm **Xử lý**, rồi chọn **Ghi sổ lại** (có thể kèm xác nhận ngoại lệ) hoặc **Bỏ qua giao dịch**.
- **Giao dịch ngoại tuyến không đồng bộ được**: máy chủ từ chối hẳn. Bấm **Gửi lại** hoặc **Xóa khỏi máy**.

Trong cả hai trường hợp, nếu giao dịch không được ghi sổ thì **tiền đã thu vẫn nằm trong két**. Bạn cần đối chiếu và lập
chứng từ bằng tay nếu khách đã lấy hàng.

Nếu máy còn giao dịch ngoại tuyến của cửa hàng khác, các giao dịch đó chỉ được gửi khi đăng nhập lại đúng cửa hàng đó trên
máy này.

## In phiếu

Mở chứng từ bán và bấm **In phiếu**, rồi chọn khổ **Khổ A5** hoặc **Giấy cuộn 80mm**. Danh sách máy in đã kiểm tra nằm ở
tài liệu vận hành; hỏi bộ phận hỗ trợ nếu cần.
