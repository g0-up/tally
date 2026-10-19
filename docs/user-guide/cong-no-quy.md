# Công nợ và quỹ

[Về mục lục](README.md)

Công nợ phát sinh từ chứng từ đã ghi sổ: xuất kho bán chịu, nhận hàng của NCC, trả hàng. Bạn không sửa thẳng số nợ được. Mọi
thay đổi đều qua phiếu thu, phiếu chi, phân bổ hoặc điều chỉnh có lý do.

Một đối tác vừa là khách vừa là NCC có **hai sổ nợ riêng**. Hệ thống không tự bù trừ hai sổ này.

## Khách còn nợ

- **Thu tiền khách** → **Ghi phiếu thu**. Hệ thống gợi ý phân bổ vào khoản đến hạn **cũ nhất trước**. Bạn không phân bổ được
  quá số còn nợ. Phần tiền chưa phân bổ thành **tiền khách ứng trước**.
- Mở một khách để xem chứng từ còn nợ và làm các việc sau:
  - **Lập bảng kê**;
  - **Ghi chú nhắc nợ**, có thể kèm **Khách hẹn trả ngày**;
  - **Giảm/xóa nợ** → **Ghi giảm nợ** trên từng chứng từ. Đây là quyết định của chủ cửa hàng, cần xác nhận ngoại lệ và được
    ghi nhật ký.

## Cửa hàng còn nợ NCC

**Trả tiền NCC** → **Ghi phiếu chi**, phân bổ vào các phiếu nhận còn nợ.

## Ứng trước & cọc

Màn hình này liệt kê tiền khách ứng trước, tiền cọc của hóa đơn và tiền cửa hàng ứng trước cho NCC.

- **Phân bổ** → **Ghi phân bổ** trừ khoản ứng trước vào chứng từ còn nợ.
- Hệ thống không tự đem ứng trước bù vào nợ khi bạn chưa phân bổ. Riêng cọc của hóa đơn thì được tự trừ khi xuất kho
  (xem [Hóa đơn](hoa-don-bao-gia-tra-hang.md#hóa-đơn-bán-hàng--hóa-đơn)).
- Trả hàng vượt số đang nợ thì phần dư thành khoản phải hoàn tiền cho khách. Khoản này không tự gộp với ứng trước.

## Đối soát (bảng kê)

1. **Lập bảng kê** cho một khách trong một khoảng ngày.
2. **Gửi khách**. Bảng kê đã gửi **không đổi được**, vì nó lưu đúng nội dung khách đã nhận.
3. Ghi nhận phản hồi: **Khách xác nhận** hoặc **Tranh chấp** → **Ghi nhận**.
4. Nếu cần sửa thì **Lập phiên bản mới**. **In phiếu** để in bảng kê.

## Lịch thu/trả

Màn hình này cho thấy các khoản sắp đến hạn và dự báo **Dự kiến thu 7 ngày**, **Dự kiến trả 7 ngày**, so với **Tiền đang có**.

## Quỹ tiền

Cửa hàng có sẵn hai quỹ **Tiền mặt** và **Ngân hàng**. Số dư đầu kỳ của quỹ nhập lúc thiết lập cửa hàng. Sau đó quỹ chỉ thay
đổi qua phiếu thu/chi từ bán hàng, thu nợ, trả NCC, hoàn tiền và ứng trước. Bạn xem số dư và dòng tiền ở
[Tổng quan và báo cáo Dòng tiền](bao-cao.md).

Giới hạn của bản 1.1:

- chưa có màn hình chuyển tiền giữa các quỹ, nộp tiền mặt vào ngân hàng, hay ghi chi phí vận hành (tiền thuê, lương thời vụ…);
- xem được biên bản bù trừ công nợ (`BT`) nhưng chưa có màn hình để lập biên bản này.
