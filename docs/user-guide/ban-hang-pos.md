# Bán hàng tại quầy (POS)

[Về mục lục](README.md)

Mở **Bán hàng › POS & giỏ hàng**. Màn hình POS cần gói thuê bao có module Bán tại quầy.

## Bán một đơn

1. Quét mã vạch hoặc gõ mã/tên hàng vào ô tìm rồi nhấn Enter. Bạn cũng có thể bấm ô hàng bên trái, hoặc quét bằng
   camera điện thoại (xem [Quét bằng điện thoại](#quét-bằng-điện-thoại)). Mỗi ô hiện **Có thể bán** và các màu còn hàng.
   Mã vạch của đơn vị lớn thêm dòng theo đơn vị đó: quét mã của thùng thì dòng mới tính theo thùng.
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

## Quét bằng điện thoại

Điện thoại có camera có thể làm máy quét mã vạch cho POS, [Kiểm kê](kho-kiem-ke-gio-qua.md#kiểm-kê) và
[Nhập hàng](nhap-hang.md#quét-mã-vạch-khi-nhập-hàng). Điện thoại không cần cài ứng dụng và không cần đăng nhập. Cả máy
tính và điện thoại đều phải có mạng.

### Ghép đôi điện thoại với máy tính

1. Trên máy tính, ô **Quét bằng điện thoại** nằm ngay dưới ô tìm hàng và hiện một mã QR. Mã QR tự đổi sau khoảng 2
   phút, nên bạn luôn quét mã đang hiện.
2. Mở camera của điện thoại và quét mã QR. Điện thoại mở trang **Quét mã vạch** của cửa hàng và hiện **Mã xác minh**
   gồm 4 chữ số.
3. Máy tính hiện cùng một mã xác minh, kèm hai nút **Cho phép** và **Từ chối**. **Chỉ bấm Cho phép khi mã trên điện
   thoại giống mã trên máy tính.** Nếu mã khác, hoặc bạn không vừa quét mã QR, hãy bấm **Từ chối**. Điện thoại đó bị
   từ chối và máy tính hiện mã QR mới. Nếu không ai bấm gì trong khoảng 2 phút, lần ghép đôi này kết thúc.
4. Sau khi máy tính cho phép, bấm **Bật camera** trên điện thoại rồi cho phép trang dùng camera. Safari trên iPhone có
   thể hỏi lại quyền camera mỗi lần mở trang. Nếu bạn đã lỡ chặn camera, trang quét hiện cách mở lại quyền trong cài đặt
   của trình duyệt.

### Khi quét

- Đưa mã vạch vào khung camera. Mỗi mã chỉ được gửi một lần khi còn nằm trong khung. Muốn thêm lần nữa, đưa mã ra khỏi
  khung rồi đưa vào lại.
- Mã từ điện thoại được thêm vào giỏ giống như khi quét bằng máy quét cầm tay, theo đúng đơn vị của mã. Chữ đang gõ dở
  trong ô tìm không ảnh hưởng tới dòng được thêm.
- Kết quả hiện trong danh sách **Kết quả gần nhất** trên điện thoại, ví dụ tên hàng và đơn vị vừa thêm, hoặc câu báo vì
  sao mã không được thêm. Điện thoại Android rung một lần khi thêm được và rung ba lần khi cần xem lại. iPhone không rung
  được nên phát tiếng bíp kèm nháy màn hình xanh hoặc đỏ. Tiếng bíp chỉ có sau khi bạn chạm **Bật camera**, và có thể
  không nghe thấy khi điện thoại đang tắt tiếng.
- Khi máy tính đang thanh toán hoặc đang mở một hộp thoại, POS không nhận mã từ điện thoại. Điện thoại báo "Màn hình trên
  máy tính không nhận mã quét".
- Khi điện thoại báo "Máy tính chưa phản hồi", hãy nhìn máy tính xem dòng đã được thêm chưa rồi mới quét lại, để không
  thêm một hàng hai lần.
- Tắt màn hình hoặc chuyển sang ứng dụng khác thì camera tắt. Quay lại trang và bấm **Bật camera** để quét tiếp, không
  phải ghép đôi lại.

### Ngắt kết nối và khi phiên kết thúc

- Bấm **Ngắt** trên máy tính hoặc **Ngắt kết nối** trên điện thoại để dừng.
- Lần ghép đôi gắn với tab trình duyệt đang mở trên máy tính. Chuyển giữa POS, Kiểm kê và Nhập hàng trong cùng tab thì
  điện thoại vẫn quét tiếp được, không phải quét lại mã QR. Nếu máy tính báo phiên đang mở ở tab khác, bấm
  **Ghép đôi ở tab này** để dùng tab hiện tại.
- Lần ghép đôi tự kết thúc khi tab trên máy tính bị đóng hoặc mất kết nối quá khoảng 1 phút, khi bạn đăng xuất hoặc bị
  đăng xuất từ thiết bị khác, và sau 12 giờ kể từ lúc bấm Cho phép. Khi đó điện thoại báo đã ngắt kết nối, và bạn quét
  lại mã QR trên máy tính.
- Mỗi tài khoản giữ tối đa 3 lần ghép đôi cùng lúc. Khi bạn mở lần thứ tư, Tally đóng lần cũ nhất chưa có điện thoại
  nào được cho phép. Chỉ khi cả ba đều đang có điện thoại, điện thoại ghép đôi lâu nhất mới bị ngắt. Tab có lần ghép đôi
  bị đóng như vậy không tự tạo mã QR mới mà cho bạn chọn ghép đôi lại.
- Khi máy chủ được cập nhật, hai màn hình hiện "Đang kết nối lại…" rồi tự kết nối lại khi máy chủ chạy trở lại. Nếu
  gián đoạn ngắn, bạn không phải quét lại mã QR. Nếu gián đoạn quá khoảng 1 phút, lần ghép đôi kết thúc và bạn ghép đôi
  lại.

### Khi gặp sự cố

- Quét mã QR mà điện thoại mở trang cũ của Tally hoặc báo không tìm thấy trang: điện thoại đang giữ bản cũ của ứng dụng.
  Nếu trang hiện "Đã có phiên bản mới. Tải lại để cập nhật.", bấm **Tải lại**. Nếu không, đóng mọi tab của cửa hàng trên
  điện thoại, kể cả ứng dụng đã thêm vào Màn hình chính, rồi quét lại mã QR.
- Điện thoại báo "Mã QR đã hết hạn hoặc đã được dùng": quét mã QR đang hiện trên máy tính.
- Điện thoại hoặc máy tính báo trình duyệt không hỗ trợ quét bằng điện thoại: mở trang bằng Safari hoặc Chrome bản mới.

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
- không bán khi đã vượt hạn mức tiền, đã quá số giờ cho phép, hoặc cửa hàng tính giá chưa gồm thuế;
- chỉ bán theo đơn vị cơ sở. Quét mã vạch của đơn vị khác, ví dụ mã của thùng, thì POS không thêm dòng mà báo mã đó là
  của đơn vị nào và cần có mạng để bán theo đơn vị này;
- không quét bằng điện thoại được, vì tính năng này cần có mạng.

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
