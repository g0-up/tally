# Chiến dịch và khách hàng

[Về mục lục](README.md)

## Chiến dịch mùa vụ

Mở **Chiến dịch & khách › Chiến dịch mùa vụ**. Màn hình này cần gói thuê bao có module Chiến dịch mùa vụ.

Chiến dịch chỉ là nhãn để phân tích và không có sổ riêng. Mỗi dòng doanh thu có một chiến dịch chính, nên doanh thu không bị tính
hai lần.

Mỗi chiến dịch hiện thành một thẻ với các số liệu sau:

- **Doanh thu thuần / mục tiêu** và tiến độ theo số ngày của mùa;
- **Lãi gộp**, **Đơn còn phải giao**, **Khách còn nợ từ đơn mùa**;
- **Checklist trước cao điểm & kết mùa**: đánh dấu từng việc cần làm;
- **Nhu cầu hàng mùa Tết theo ngày**, gồm đơn đã chốt còn giao, số có thể bán, PO dự kiến về và lượng thiếu sau khi về.
  Lượng dự kiến về không được cộng vào tồn thực.

Thao tác trên thẻ:

- **Bắt đầu mùa**: chuyển chiến dịch từ **Kế hoạch** sang **Đang chạy**.
- **Kết mùa**: chuyển sang **Đã kết thúc**. Kết mùa không xóa tồn, tiền hay nợ, và số liệu mùa vẫn xem được trong báo cáo.
  Sau khi kết mùa, chiến dịch không nhận thêm thay đổi.

Giới hạn của bản 1.1:

- chưa có màn hình tạo chiến dịch mới;
- **Chi phí mùa** và **Kết quả quản trị** hiện "—", vì phần mềm chưa ghi chi phí vận hành.

## Khách hàng (CRM)

Mở **Chiến dịch & khách › Khách hàng (CRM)**. Màn hình này cần gói thuê bao có module Chăm sóc khách hàng.

- **Thêm khách hàng**: chỉ tên là bắt buộc, mã khách được tạo tự động. Các trường khác:
  - **Loại khách** (Cá nhân / Doanh nghiệp), số điện thoại, mã số thuế, địa chỉ;
  - **Nhóm khách**: quyết định bảng giá áp dụng;
  - **Hạn mức nợ** (để trống là không giới hạn) và **Số ngày nợ được phép** (0 = trả ngay);
  - ghi chú.
- Đổi số ngày nợ chỉ áp dụng cho đơn bán mới. Nợ đã ghi giữ hạn cũ.
- **Đồng ý nhận tin**: tích **Khách đồng ý nhận tin khuyến mãi, chăm sóc mùa vụ**, chọn **Kênh nhận tin** và ghi **Nguồn đồng ý**
  (ví dụ tại quầy, qua điện thoại). Nguồn đồng ý là bắt buộc khi thay đổi đồng ý. Khách chưa đồng ý thì không nhận tin khuyến
  mãi hay tin chiến dịch.
- **Tái đặt đơn** tạo đơn mới từ lần mua trước, theo giá và quy cách hiện hành. Hệ thống không chép nguyên đơn cũ, nên bạn
  cần kiểm tra lại trước khi xác nhận. **Tạo đơn** mở một đơn trống cho khách.
- Bạn cũng thêm nhanh được khách ngay khi đang lập chứng từ bán (**Thêm khách hàng mới** → **Thêm & chọn**).
