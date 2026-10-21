# Hỏi đáp nghiệp vụ

Ghi lại các câu hỏi cần thống nhất với chủ cửa hàng. Mỗi mục gồm câu hỏi, câu trả lời theo cách phần mềm đang chạy, các
lựa chọn và điều chủ cửa hàng đã quyết định.

## 1. Thêm hai mặt hàng cùng tên nhưng khác màu thì sao?

*Ngày hỏi: 20/10/2026 · Trạng thái: đã quyết định (20/10/2026)*

### Câu hỏi

Ở màn hình **Thêm hàng hóa**, nếu tôi tạo hai mặt hàng cùng tên, ví dụ "Áo thun cổ tròn" màu đỏ và "Áo thun cổ tròn" màu
xanh, thì phần mềm xử lý thế nào?

### Trả lời ngắn

Phần mềm **không cho tạo mặt hàng thứ hai** và báo: *"Tên hàng đã tồn tại trong cửa hàng."* Khi so tên, phần mềm bỏ
qua chữ hoa/thường, dấu tiếng Việt và khoảng trắng thừa, nên "Áo thun", "áo  thun" và "Ao thun" đều bị coi là trùng.

Đây là chủ ý thiết kế. Trong phần mềm, **màu không phải là một mặt hàng riêng**. Một mặt hàng chỉ có một tên, còn màu được
ghi lúc nhập kho, kiểm kê và bán hàng.

### Quyết định

- **Một mặt hàng, tồn kho chia theo màu.** Các màu của cùng một mẫu dùng chung một mã vạch, nên đây là cách duy nhất phù
  hợp. Không tách mỗi màu thành một mặt hàng hay biến thể riêng.
- **Giữ cố định 7 màu:** xanh, đỏ, tím, vàng, nâu, đen, trắng. Tạm thời chủ cửa hàng không tự thêm màu mới.

### Cách phần mềm quản lý màu

- Mỗi mặt hàng có một mã hàng và một mã vạch dùng chung cho mọi màu. Tồn kho của mặt hàng được **chia theo màu**: bao nhiêu
  cái đỏ, bao nhiêu cái xanh.
- Màu được chọn khi **nhập hàng** và khi **kiểm kê**.
- Muốn một màu bán giá khác: vào **Kho › Tồn kho**, bấm **Sửa giá**, rồi **Lưu giá màu**.

### Khi bán hàng và quét mã vạch

1. Quét mã vạch thì ra đúng mặt hàng, nhưng chưa biết màu.
2. Nếu kho chỉ còn một màu, phần mềm tự chọn màu đó.
3. Nếu còn nhiều màu, dòng hàng hiện **"Màu khách lấy:"** với các màu còn bán. Thu ngân chọn màu. Khách lấy nhiều màu thì
   bấm **"+ Thêm màu khác"**.
4. Quét nhiều lần cùng một mã sẽ cộng dồn vào một dòng. Sau đó thu ngân chia số lượng theo màu.
5. Phần mềm **không cho thanh toán** dòng hàng chưa chọn màu, nên tồn kho không bị trừ sai mà không ai biết.
6. Nếu màu đã chọn không đủ hàng, phần mềm báo số lượng còn của từng màu để thu ngân đổi màu.

### Các lựa chọn đã cân nhắc

| Lựa chọn | Kết luận |
|---|---|
| **A. Một mặt hàng, tồn kho chia theo màu** | **Chọn.** Dùng được ngay, hợp với việc các màu dùng chung mã vạch. Báo cáo gom theo mặt hàng, vẫn xem được tồn của từng màu. |
| **B. Mỗi màu là một biến thể có mã riêng** | Không chọn. Mỗi mã vạch chỉ gắn được với một mã hàng, nên cách này buộc phải in nhãn mã vạch riêng cho từng màu. Phần mềm cũng chưa có màn hình tạo biến thể. |
| **C. Đặt tên khác nhau, ví dụ "Áo thun đỏ" và "Áo thun xanh"** | Không chọn. Báo cáo bị tách rời, không xem được tổng tồn của mẫu áo, dễ tạo trùng và khó sửa về sau. |

### Lưu ý khi vận hành

- **Nhập hàng phải chọn màu.** Hàng nhập mà quên chọn màu sẽ nằm ở mục "Chưa gán màu", khiến tồn kho từng màu bị sai. Cần
  dặn kỹ khi đào tạo nhân viên kho.
- **Thu ngân chọn sai màu thì phần mềm không tự phát hiện.** Sai lệch chỉ lộ ra khi kiểm kê. Nên kiểm kê theo màu định kỳ
  cho các mặt hàng có nhiều màu.
- **Màu ngoài 7 màu có sẵn** (ví dụ "be", "xanh rêu") tạm thời không quản lý được. Nếu sau này cần, sẽ bàn lại.

### Giá theo màu

Chủ cửa hàng xác nhận: **các màu của cùng một mẫu có giá khác nhau.**

- Đặt giá riêng cho từng màu ở **Kho › Tồn kho** → **Sửa giá** → **Lưu giá màu**. Màu nào chưa có giá riêng thì bán theo
  giá chung của mặt hàng.
- Khi sửa giá chung, nếu chọn **áp dụng cho tất cả màu**, giá riêng của từng màu sẽ bị bỏ. Chỉ chọn khi muốn mọi màu về
  cùng một giá.
- Ở màn hình bán hàng, dòng hàng hiện giá chung cho tới khi thu ngân chọn màu, rồi đổi sang giá của màu đó. Thu ngân cần
  chọn màu trước khi báo giá cho khách.
- **Khi máy bán hàng mất mạng**, phần mềm vẫn bán được nhưng tính theo **giá chung**, không theo giá riêng của màu. Đơn bán
  lúc mất mạng với màu có giá khác cần được kiểm tra lại sau khi có mạng.
