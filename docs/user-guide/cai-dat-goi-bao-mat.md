# Cài đặt, thuê bao và bảo mật

[Về mục lục](README.md)

Menu **Thiết lập** có bốn màn hình: **Tài khoản & bảo mật**, **Khởi tạo cửa hàng**
([xem tiến độ thiết lập](bat-dau.md#thiết-lập-cửa-hàng-bắt-buộc-trước-khi-dùng)), **Thuê bao** và **Nhật ký**.

Tally dành cho cửa hàng một người: chỉ có tài khoản chủ cửa hàng, với toàn quyền trên mọi màn hình.

## Tài khoản & bảo mật

### Chủ cửa hàng và mật khẩu

Thẻ **Chủ cửa hàng** hiện họ tên, cửa hàng, lần đăng nhập gần nhất và trạng thái xác thực 2 lớp.

**Đổi mật khẩu** sẽ đăng xuất các thiết bị khác. Thiết bị đang dùng vẫn giữ đăng nhập.

### Xác thực 2 lớp và mã khôi phục

1. Bấm **Bật xác thực 2 lớp** rồi nhập lại mật khẩu.
2. Thêm **Khóa thiết lập** vào ứng dụng xác thực (Google Authenticator, Microsoft Authenticator…). Trên điện thoại, có thể bấm
   **Mở bằng ứng dụng xác thực trên máy này**.
3. Nhập mã 6 số mà ứng dụng hiện ra.
4. Màn hình **Mã khôi phục** hiện danh sách mã. **Mã chỉ hiện lần này.** Bấm **Sao chép** hoặc **Tải .txt**, cất ở nơi an
   toàn, rồi tích **Tôi đã lưu mã** trước khi đóng.

Khi bật xác thực 2 lớp, các thiết bị khác bị đăng xuất. Mỗi mã khôi phục dùng được một lần khi bạn không có ứng dụng xác thực.

**Tắt xác thực 2 lớp** cũng cần nhập lại mật khẩu. Sau khi tắt, các mã khôi phục cũ không còn dùng được.

> Bản 1.1 chưa có nút tạo lại mã khôi phục. Nếu đã dùng gần hết mã, hãy tắt rồi bật lại xác thực 2 lớp để nhận bộ mã mới.

### Thiết bị đang đăng nhập

Bảng **Thiết bị đang đăng nhập** liệt kê từng thiết bị và lần cuối dùng. Bạn **Đăng xuất** từng thiết bị, hoặc bấm
**Đăng xuất các thiết bị khác**. Thiết bị bị đăng xuất phải đăng nhập lại ở lần dùng tiếp theo.

### Thông tin cửa hàng

Bạn sửa tên cửa hàng, **Tên pháp lý** (in trên chứng từ), mã số thuế, điện thoại và địa chỉ. **Địa chỉ cửa hàng** (địa chỉ đăng
nhập) không đổi được, và **Ngày chốt số dư đầu kỳ** không đổi được sau khi đã xác nhận số dư đầu kỳ.

### Khóa sổ kỳ

- **Khóa sổ** đến hết một ngày. Chứng từ có ngày từ đó trở về trước **không ghi, sửa hay đảo được nữa**.
- **Mở lại kỳ** cần nhập lại mật khẩu, nhập **Lý do** và xác nhận ngoại lệ. Bạn chọn **Khóa lại đến ngày** mới, hoặc để trống
  để bỏ khóa hoàn toàn.
- **Lịch sử khóa sổ** ghi mọi lần khóa và mở lại.

### Chính sách bán ngoại tuyến

Tích **Bán ngoại tuyến tại quầy**, đặt **Hạn mức tiền mặt ngoại tuyến** và **Số giờ tối đa không đồng bộ** (1 đến 72 giờ), rồi
**Lưu**. Cách bán khi mất mạng xem ở [Bán hàng tại quầy](ban-hang-pos.md#bán-khi-mất-mạng).

### Xuất toàn bộ dữ liệu

**Tạo bản xuất** (cần nhập lại mật khẩu) tạo một tệp zip trong nền, mỗi bảng một tệp CSV. Bản xuất gồm danh mục, đối tác, chứng
từ, sổ kho, công nợ, quỹ, nhật ký và danh sách tệp đính kèm, giữ nguyên mã và quan hệ giữa các bảng.

Khi xong, bấm **Tải về**. Tệp tải được trong 7 ngày. Cửa hàng đang ở chế độ chỉ xem vẫn xuất được dữ liệu.

### Chuyển quyền chủ cửa hàng

Nhập **Email người nhận** và **Họ tên người nhận**, rồi bấm **Gửi lời mời chuyển quyền** và nhập lại mật khẩu.

Người nhận có **72 giờ** để mở liên kết trong email và chấp nhận. Sau khi người nhận chấp nhận, họ trở thành chủ cửa hàng. Tài
khoản của bạn bị vô hiệu và đăng xuất trên mọi thiết bị.

### Đóng tài khoản

**Đóng tài khoản không xóa dữ liệu ngay.** Các bước:

1. **Yêu cầu đóng tài khoản** (cần nhập lại mật khẩu). Hệ thống gửi liên kết xác nhận tới email chủ cửa hàng. Yêu cầu chỉ có
   hiệu lực sau khi bạn bấm liên kết đó và chọn **Xác nhận đóng tài khoản**.
2. Sau khi xác nhận, cửa hàng **vẫn hoạt động 30 ngày**. Trong thời gian này bạn có thể **Hủy yêu cầu đóng**.
3. Hết thời gian chờ, cửa hàng chuyển sang chỉ xem. Hệ thống tạo **bản xuất cuối**, gửi liên kết tải về qua email, rồi mới đóng.

Thẻ **Đóng tài khoản** cho biết trạng thái yêu cầu và bản xuất cuối. Nếu bản xuất cuối bị lỗi, bộ phận hỗ trợ sẽ cho chạy lại.
Nếu cửa hàng đang bị giữ dữ liệu theo yêu cầu pháp lý, cửa hàng chưa đóng khi hết thời gian chờ.

## Thuê bao và hóa đơn

Gói và quyền dùng module được kiểm tra ở máy chủ.

- **Thuê bao hiện tại** hiện gói, loại gói, thời gian hiệu lực, hạn mức (số SKU, dung lượng lưu trữ), các module đang bật và điều
  gì xảy ra **Sau hết hạn**.
- Trạng thái thuê bao: **Dùng thử**, **Còn hạn**, **Quá hạn, đang ân hạn**, **Chỉ xem**, **Đã đóng**. Hết hạn thì cửa hàng
  chuyển sang **chỉ xem** theo chính sách của gói. Dữ liệu không bị xóa và nợ không bị giấu. Bạn vẫn xem và xuất được dữ liệu,
  nhưng không ghi sổ được cho tới khi gia hạn.
- **Đổi sang gói này** (hoặc **Mua add-on** với gói bổ sung): xem giá và kỳ do máy chủ tính, tích **Tôi đồng ý điều khoản gói**,
  rồi bấm **Đổi gói** (hoặc **Mua add-on**). Gói mới hoặc add-on có hiệu lực **khi hóa đơn được thanh toán**.
- **Hóa đơn thuê bao**: bấm **Thanh toán** để xem hướng dẫn chuyển khoản. Hóa đơn được ghi nhận đã thanh toán khi nhà cung cấp
  dịch vụ nhận được tiền.

> Giá các gói chưa được chốt. Màn hình hiện "Chưa chốt" cho tới khi bảng giá được công bố.

## Hỗ trợ

### Yêu cầu hỗ trợ

Trên màn hình **Thuê bao**, bấm **Tạo yêu cầu**, nhập **Tiêu đề**, **Nội dung**, **Mức ưu tiên**, rồi bấm **Gửi yêu cầu**.

**Không gửi mật khẩu hay mã xác thực.** Nếu nội dung có vẻ chứa mật khẩu, hệ thống cảnh báo. Khi chắc chắn không phải mật khẩu,
bạn bấm **Vẫn gửi**. Mở một yêu cầu để xem trao đổi và **Gửi trả lời**.

Trạng thái yêu cầu: Đang mở, Chờ cửa hàng trả lời, Đang xử lý, Đã giải quyết, Đã đóng.

### Truy cập hỗ trợ

Nhân viên hỗ trợ chỉ xem được dữ liệu cửa hàng khi bạn cấp quyền, trong phạm vi bạn chọn và có thời hạn. Họ chỉ xem,
không sửa được dữ liệu.

1. Cần một yêu cầu hỗ trợ đang mở đã có nhân viên phụ trách.
2. Bấm **Cấp quyền hỗ trợ 2 giờ (chỉ đọc)**, chọn yêu cầu hỗ trợ và **Phạm vi xem** (các phần nhân viên được xem), rồi bấm
   **Cấp quyền**.
3. Thẻ hiện phiên hỗ trợ đang mở và giờ hết hạn. Bấm **Thu hồi** để kết thúc sớm bất cứ lúc nào.

Mỗi lần cấp, xem và thu hồi đều được ghi vào **Nhật ký**.

## Nhật ký

**Thiết lập › Nhật ký** ghi lại tạo, sửa, duyệt, ghi sổ, đảo, xuất file và thay đổi quyền: ai làm, lúc nào, từ đâu (trình duyệt,
máy bán hàng, đồng bộ ngoại tuyến, phiên hỗ trợ…). Nhật ký chỉ ghi thêm, không ai sửa hay xóa được. Bạn tìm được theo số chứng từ, mã
thao tác hoặc tên người.
