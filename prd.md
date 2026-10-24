# PRD — Phần mềm quản lý bán hàng, kho và công nợ cho cửa hàng hàng Tết

&gt; Tên làm việc: \*\*Tết Sales Management\*\*. Tên thương mại chưa chốt.
&gt; Phiên bản: \*\*1.1 — Cập nhật theo góp ý ngày 13–16/10/2026 và bản mẫu tương tác v6.14\*\*.
&gt; Trạng thái: \*\*Đề xuất sản phẩm để discovery, thiết kế và phát triển; chưa được xác nhận qua pilot trả phí\*\*.
&gt; Ngôn ngữ: Tiếng Việt. Thị trường mục tiêu giả định: Việt Nam.
&gt; Đây là PRD phạm vi đầy đủ, \*\*không phải danh sách MVP\*\*. Thứ tự triển khai ở cuối tài liệu chỉ thể hiện phụ thuộc, không loại bỏ chức năng khỏi phạm vi.
&gt; Từ bản 1.1, khách hàng mục tiêu là \*\*cửa hàng nhỏ do chủ cửa hàng tự vận hành\*\*: một tài khoản, một cửa hàng, một kho. Các chức năng đã bỏ được ghi tại mục 0 và mục 2.5 để không bị đưa ngược lại vào backlog.

## 0. Lịch sử thay đổi

| Phiên bản | Ngày | Thay đổi chính |
|---|---|---|
| 1.0 | — | PRD phạm vi đầy đủ ban đầu |
| 1.1 | 16/10/2026 | Thu hẹp mô hình còn một vai trò (chủ cửa hàng), một cửa hàng, một kho; bỏ giao nhận/COD, chuyển kho, đề nghị nhập, menu Tiền, tích hợp, import/export; thêm quản lý màu sắc xuyên suốt, giá theo màu, thùng mix vị, giỏ hàng POS, đổi hàng, thẻ kho mới, tồn kho gộp màu; đổi tên “Mua hàng” → “Nhập hàng”, “Đơn bán” → “Hóa đơn” |

\*\*Tóm tắt quyết định bản 1.1\*\*

| Nhóm | Quyết định | Mã yêu cầu bị ảnh hưởng |
|---|---|---|
| Đối tượng | Chỉ chủ cửa hàng; bỏ mọi vai trò khác, bỏ quy trình duyệt. Ngoại lệ (vượt hạn mức, dưới giá tối thiểu…) do chủ cửa hàng tự xác nhận, ghi lý do vào nhật ký | 3.2, SEC-01..03, SAL-02, PRC-04, PUR-02 |
| Tổ chức | Một cửa hàng, một kho; ẩn mọi ô chọn kho/chi nhánh, cột kho, bộ lọc phạm vi | ORG-03, INV-04, 7.5 |
| Bán hàng | “Đơn bán” đổi tên “Hóa đơn”; nhập số tiền khách đã trả, tự tính còn lại và trạng thái thanh toán; xuất kho một bước; giỏ hàng POS lưu/sửa/xóa/sao chép; đổi hàng | SAL-01, SAL-04, SAL-14, SAL-15 |
| Giao nhận | Bỏ toàn bộ: phiếu giao nhiều bước, vận đơn, chuyến, người giao, COD/thu hộ | 6.8, DEBT-12, AT-07 |
| Nhập hàng | “Mua hàng” đổi tên “Nhập hàng”; bỏ đề nghị nhập; thứ tự cột đơn nhập cố định; thêm hàng hóa mới và sửa giá bán ngay trong đơn nhập | PUR-01, PUR-02, CAT-11 |
| Màu sắc | 7 màu cố định chọn bằng ô màu; màu gắn theo lô, theo dõi trên mọi chứng từ và biến động kho; bắt buộc chọn màu khi bán hàng có tồn theo màu; giá bán theo màu | CAT-09, PRC-07, INV-12, 7.9 |
| Mix vị | Nhóm mix vị khai báo được; “thùng mix vị” dùng trong báo giá, hóa đơn, giỏ quà và combo | CAT-10, 7.10 |
| Tiền | Bỏ menu Tiền (quỹ, phiếu thu/chi, chuyển quỹ, sao kê, ca, chi phí); POS tự mở ca ngầm; thu/trả tiền thực hiện ở Công nợ, Hóa đơn, POS | 6.10 |
| Thiết lập | Bỏ Tích hợp, Import/Export; Nhà cung cấp chuyển sang Danh mục; bỏ trang Đối tác | 6.14, 9.1 |


## 1. Tóm tắt sản phẩm

### 1.1. Tầm nhìn

Xây dựng phần mềm thương mại cho cửa hàng nhỏ bán sỉ/bán lẻ bánh kẹo, đồ uống, thực phẩm đóng gói và giỏ quà, do \*\*chủ cửa hàng tự vận hành\*\*. Hệ thống liên kết \*\*hàng hóa (kể cả màu sắc), hóa đơn, xuất kho và tiền\*\* để chủ cửa hàng biết:

- Đã đặt mua gì, thực nhận bao nhiêu, còn chờ nhà cung cấp giao gì.
- Đã nhận đơn nào, giữ hàng bao nhiêu, đã giao gì và còn phải giao gì.
- Tồn thực tế, tồn có thể bán, tồn theo từng màu, hàng hỏng/cách ly và hàng gần hết hạn.
- Khách nào còn nợ, nợ từ chứng từ nào, đến hạn lúc nào.
- Cửa hàng đang nợ nhà cung cấp nào và cần chuẩn bị bao nhiêu tiền.
- Chiến dịch Tết có doanh thu, lãi gộp, dòng tiền và hàng dư như thế nào.

\*\*Định vị đề xuất:\*\* Quản lý bán sỉ và quà Tết cho cửa hàng một người, từ báo giá, hóa đơn, xuất kho nhiều lần đến đối soát công nợ; theo dõi được hàng nhiều màu và thùng mix vị; sử dụng quanh năm, có chế độ chiến dịch mùa vụ.

### 1.2. Phân biệt dữ kiện và giả định

| Loại | Nội dung |
|---|---|
| Yêu cầu đã xác nhận | Người yêu cầu nghiên cứu để xây phần mềm bán cho cửa hàng, không phải chọn phần mềm để tự sử dụng |
| Yêu cầu đã xác nhận | Ba nghiệp vụ trung tâm: quản lý hàng nhập, hàng xuất, công nợ |
| Yêu cầu đã xác nhận | Tài liệu phải bao quát chức năng đầy đủ, không chỉ MVP |
| Yêu cầu đã xác nhận (13–16/10/2026) | Khách hàng hướng tới là mô hình kinh doanh nhỏ chỉ có chủ cửa hàng; một cửa hàng, một kho; không có giao nhận/vận chuyển |
| Yêu cầu đã xác nhận (13–16/10/2026) | Hàng hóa có màu sắc (xanh, đỏ, tím, vàng, nâu, đen, trắng) cần theo dõi tồn, giá và chứng từ theo màu |
| Giả định sản phẩm | Khách hàng ưu tiên là cửa hàng bán sỉ/bán lẻ hàng Tết và giỏ quà quy mô nhỏ |
| Giả định vận hành | Có bán chịu, đặt cọc, xuất kho nhiều lần cho một hóa đơn, nhiều đơn vị tính, bán thùng mix vị và kinh doanh ngoài mùa Tết |
| Đề xuất thiết kế | SaaS đa tenant, web responsive/PWA; làm việc online, POS offline có giới hạn |
| Chưa xác minh | Mức sẵn sàng trả phí, quy mô thị trường, mức độ đau, tần suất sử dụng, chi phí hỗ trợ và nghĩa vụ pháp lý cụ thể |

Không coi các giả định trong PRD là bằng chứng thị trường. Những yêu cầu có thể phụ thuộc ngành được cấu hình bật/tắt nhưng vẫn thuộc phạm vi sản phẩm đầy đủ.

### 1.3. Nguyên tắc sản phẩm

1. Đơn hàng (báo giá, hóa đơn, đơn nhập), xuất/nhập kho, ghi nhận phải thu/phải trả và thanh toán là các sự kiện riêng.
2. Mọi số dư tồn kho và công nợ truy ngược được về chứng từ.
3. Chứng từ đã ghi sổ không được sửa/xóa âm thầm; dùng đảo, điều chỉnh hoặc chứng từ thay thế có liên kết.
4. Không trừ kho hai lần cho giỏ quà, không ghi doanh thu hai lần cho giao hàng và hóa đơn.
5. Doanh thu khác tiền thu; lãi gộp khác lợi nhuận và số dư tiền mặt.
6. Đơn giản cho một người tự vận hành: không có bước duyệt nội bộ; ngoại lệ do chủ cửa hàng xác nhận và luôn được ghi nhật ký.
9. Màu sắc là thuộc tính của lô hàng: nhập theo màu, bán theo màu, trả/đổi theo màu; tồn theo màu luôn khớp sổ.
10. Mọi ô chọn hàng hóa, khách hàng, nhà cung cấp đều tìm kiếm được (không dấu, mã, mã vạch, điện thoại) và chọn bằng bàn phím.
7. Bảo toàn dữ liệu khi mạng yếu, thao tác lặp, callback trùng và nhiều người cùng thao tác.
8. Có khả năng nhập dữ liệu đầu kỳ, đối soát và xuất dữ liệu để khách hàng không bị khóa trong hệ thống.

## 2. Mục tiêu, kết quả và giới hạn

### 2.1. Mục tiêu kinh doanh của nhà cung cấp phần mềm

- Bán được sản phẩm chuẩn hóa, không biến mỗi triển khai thành dự án tùy chỉnh.
- Tạo doanh thu thuê bao hoặc gói theo mùa, với phí triển khai/dịch vụ minh bạch.
- Giảm chi phí onboarding và hỗ trợ thông qua dữ liệu mẫu, hướng dẫn và công cụ tự phục vụ.
- Giữ khách qua lịch sử giao dịch, quy trình hữu ích và chất lượng dữ liệu, không bằng hạn chế quyền xuất dữ liệu.
- Mở rộng từ mùa Tết sang bán sỉ và quản lý công nợ quanh năm.

### 2.2. Mục tiêu người dùng

- Giảm ghi chép trùng giữa sổ, Excel, kho và người thu tiền.
- Giảm sai quy đổi, sai màu, bỏ sót xuất hàng, nhầm tiền cọc và công nợ.
- Biết tồn có thể bán trước khi chốt đơn.
- Xuất bảng kê công nợ có bằng chứng, dễ gửi và xác nhận.
- Nhìn được nhu cầu hàng và tiền trước cao điểm.

### 2.3. KPI cần đo

Các chỉ tiêu kinh doanh chưa có baseline; phải đo trước/sau pilot, không tự công bố tỷ lệ cải thiện.

| KPI | Định nghĩa | Cách đo |
|---|---|---|
| Activation | Tenant hoàn tất danh mục, tồn/nợ đầu kỳ và giao dịch thật đầu tiên | Sự kiện onboarding; loại trừ dữ liệu demo |
| Time to first value | Thời gian từ tạo tài khoản đến báo cáo tồn/công nợ được chủ cửa hàng đối soát | Nhật ký sự kiện và xác nhận pilot |
| Tỷ lệ lệch tồn | Chênh lệch kiểm kê theo SKU, màu, đơn vị cơ sở và giá trị | Biên bản kiểm kê trước/sau |
| Thời gian đối soát | Thời gian lập và kiểm tra bảng kê công nợ | Quan sát thao tác thực tế |
| Tỷ lệ hóa đơn xuất đúng đủ | Hóa đơn xuất kho đủ số lượng, đúng màu theo cam kết | Hóa đơn và phiếu xuất kho |
| Công nợ quá hạn | Giá trị phải thu quá hạn tại thời điểm báo cáo | Sổ nợ theo hạn; tiền ứng trước trình bày riêng |
| Retention theo mùa | Tenant trả phí tiếp tục sử dụng trong mùa tiếp theo | Cohort theo mùa và phân khúc |
| Retention quanh năm | Tenant có giao dịch thật ngoài mùa Tết | Hoạt động sử dụng và trạng thái thuê bao |
| Chi phí phục vụ | Onboarding, support và hạ tầng theo tenant | Thời gian hỗ trợ và dữ liệu chi phí |
| Chất lượng tài chính | Số lỗi tồn/công nợ không giải thích được bằng chứng từ | Công cụ đối soát và incident |

### 2.4. Trong phạm vi đầy đủ

Quản trị tenant; một cửa hàng – một kho; danh mục hàng hóa, nhà cung cấp, bảng giá, nhóm mix vị; nhập hàng; báo giá; hóa đơn và xuất kho; POS và giỏ hàng; trả hàng và đổi hàng; nhập–xuất–tồn, thẻ kho; lô/hạn; \*\*màu sắc và giá theo màu\*\*; giỏ quà/combo; công nợ, ứng trước, đối soát; báo cáo và dashboard thu–chi–công nợ; chiến dịch; giá/khuyến mại; CRM cơ bản; tài khoản chủ cửa hàng; nhật ký; thông báo trong ứng dụng; hoạt động thương mại và hỗ trợ của nhà cung cấp SaaS.

### 2.5. Ngoài phạm vi sản phẩm này

- ERP sản xuất với MRP, định mức nhiều công đoạn và hoạch định nhà máy. Đóng giỏ đơn giản vẫn trong phạm vi.
- Kế toán tổng hợp hoàn chỉnh, hợp nhất báo cáo tài chính và tự động xác nhận tuân thủ thuế.
- Cho vay, thu hồi nợ thay khách, chấm điểm tín dụng chính thức.
- Tự giữ tiền khách hàng hoặc cung cấp dịch vụ trung gian thanh toán không thông qua đối tác phù hợp.
- Marketplace mua bán hàng Tết, quản lý payroll đầy đủ và phần mềm vận hành nhà hàng.
- Tự động quyết định mua hàng, xóa nợ hoặc thay đổi hạn mức bằng AI không có xác nhận của chủ cửa hàng.

\*\*Đã bỏ khỏi phạm vi ở bản 1.1 (13–16/10/2026)\*\* — không đưa lại vào backlog nếu chưa có quyết định mới:

| Chức năng bỏ | Mã cũ | Lý do / thay thế |
|---|---|---|
| Nhiều vai trò (quản lý, bán hàng, kho, thu ngân, kế toán, giao hàng), phân quyền theo vai trò, quy trình duyệt | 3.2, ORG-04, SEC-01..03, SAL-11 | Cửa hàng một người; ngoại lệ do chủ xác nhận và ghi nhật ký |
| Nhiều chi nhánh/kho, chuyển kho, bộ lọc phạm vi chi nhánh | ORG-03, INV-04, AT-08 (phần chuyển kho) | Một cửa hàng – một kho |
| Đề nghị nhập (gợi ý mua) | PUR-01 | Chủ cửa hàng tự lập đơn nhập; cảnh báo dưới tồn tối thiểu vẫn còn ở Thông báo |
| Giao nhận: phiếu giao nhiều bước, vận đơn, chuyến, người giao, giao thất bại/hoàn, nhiều điểm nhận | 6.8 (DEL-01..07), SAL-04 (phần địa chỉ) | Thay bằng “Xuất kho giao khách” một bước trên hóa đơn |
| COD / tiền thu hộ, đối soát COD, đơn vị vận chuyển | DEL-05, DEL-06, DEBT-12, RPT-06 (phần COD), AT-07 | Không có giao nhận |
| Danh sách “Cần giao, cần thu”, thẻ “Cần xử lý” trên Bảng điều khiển | SAL-12 | Theo yêu cầu 13/10; dùng Thông báo và Lịch thu/trả |
| Menu Tiền: trang quỹ, danh sách phiếu thu/chi, chuyển quỹ, sao kê ngân hàng/QR, mở/đóng ca, ghi chi phí | CASH-03, CASH-05..10 | Quỹ vẫn được tính nền và hiện trên Bảng điều khiển/Dòng tiền. \*\*Ghi chi phí chưa có chỗ thay thế\*\* — xem OPEN-13 |
| Tích hợp bên thứ ba (ngân hàng, giao vận, HĐĐT, website/sàn, kế toán), API công khai, cổng khách hàng/NCC | API-01..04, PORT-01..02, NTF-02, AT-16 | Theo yêu cầu 15/10 |
| Import/Export dữ liệu trong Thiết lập | DATA-01..03 | Theo yêu cầu 15/10. Nhập đầu kỳ và quyền lấy dữ liệu khi ngừng thuê cần cách thay thế — xem OPEN-14, OPEN-15 |
| Trang Đối tác trong Danh mục | — | Khách hàng ở “Chiến dịch &amp; khách → Khách hàng”; NCC ở “Danh mục → Nhà cung cấp” |
| Màn “Tồn theo màu” riêng | — | Gộp vào “Kho → Tồn kho” (INV-12) |

## 3. Phân khúc và người sử dụng

### 3.1. Phân khúc

| Phân khúc | Quy trình nổi bật | Khả năng cấu hình |
|---|---|---|
| Đầu mối bán sỉ nhỏ | Giá theo khách, bán chịu, xuất kho nhiều lần, đối soát | Hóa đơn, hạn mức nợ, bảng kê |
| Cửa hàng bán lẻ | Bán tại quầy, mã vạch, tiền mặt/chuyển khoản, hàng nhiều màu | POS, giỏ hàng lưu, chọn màu |
| Cửa hàng giỏ quà | Thành phần, đóng trước/ghép khi bán, đặt cọc, thành phần có màu | Công thức giỏ, đóng gói, giá vốn, màu thành phần |

Cửa hàng nhiều điểm bán không thuộc phân khúc mục tiêu từ bản 1.1.

### 3.2. Vai trò

| Vai trò | Trách nhiệm |
|---|---|
| Chủ cửa hàng/Tenant Owner | Người dùng duy nhất của cửa hàng: thực hiện mọi nghiệp vụ, xem toàn bộ số liệu kể cả giá vốn, tự xác nhận ngoại lệ, quản lý thuê bao |
| Quản trị nền tảng | Quản lý gói, tenant, vận hành hạ tầng; không mặc định được đọc dữ liệu kinh doanh |
| Hỗ trợ nền tảng | Truy cập chẩn đoán có giới hạn, chỉ đọc theo phiên được chủ cửa hàng cho phép |

Người giao hàng thuê ngoài, nhân viên thời vụ không có tài khoản. Người trả tiền SaaS và người sử dụng là cùng một người, nên thiết kế ưu tiên thao tác ít bước, không có màn hình duyệt.

## 4. Thuật ngữ và mô hình nghiệp vụ

| Thuật ngữ | Định nghĩa |
|---|---|
| Tenant | Một khách hàng thuê phần mềm; đơn vị cách ly dữ liệu |
| Cửa hàng | Điểm kinh doanh duy nhất của tenant |
| Kho | Kho duy nhất của cửa hàng (“Kho cửa hàng”) |
| SKU | Mã hàng/biến thể có thể quản lý độc lập |
| Đơn vị cơ sở | Đơn vị lưu tồn chuẩn của SKU |
| Đơn nhập (PO) | Đơn đặt hàng NCC, chưa tự tạo tồn/phải trả |
| Hóa đơn (SO) | Đơn bán của cửa hàng, chưa tự tạo doanh thu/phải thu cho tới khi xuất kho. Là chứng từ nội bộ, \*\*không phải hóa đơn điện tử\*\* |
| Phiếu nhận/nhập | Chứng từ hàng thực nhận vào kho |
| Phiếu xuất kho | Chứng từ xuất hàng cho hóa đơn, ghi sổ ngay khi tạo (một bước) |
| Giỏ hàng (POS) | Danh sách hàng đang soạn tại quầy cho một khách; lưu được, chưa tác động kho/tiền cho tới khi thanh toán |
| Màu | Một trong 7 màu cố định gắn trên lô hàng; “Chưa gán màu” là trạng thái riêng |
| Nhóm mix vị / Thùng mix vị | Nhóm các vị cùng quy cách (VD: 1 thùng = 12 dây = 48 hộp); khách chọn số dây/lốc từng vị, tổng không vượt số thùng |
| Đổi hàng | Một nghiệp vụ gồm phiếu trả theo chứng từ gốc và hóa đơn bán mới, chỉ thu/trả phần chênh lệch |
| Chứng từ bán nội bộ | Chứng từ ghi nhận bán hàng/phải thu theo quy tắc tại mục 7; không mặc định là hóa đơn điện tử |
| Hóa đơn mua | Thông tin chứng từ từ nhà cung cấp, gắn nhận hàng; không tự tạo lại phải trả |
| Ghi sổ | Xác nhận tác động vào sổ tồn/tiền/công nợ theo quyền |
| Đảo chứng từ | Tạo bút toán ngược liên kết bản gốc; không xóa lịch sử |
| Giỏ thành phẩm | Giỏ đóng sẵn, có tồn riêng |
| Combo ảo | Ghép khi bán, không có tồn vật lý riêng |
| Giữ hàng | Cam kết lượng tồn cho đơn; chưa phải xuất kho |
| Chiến dịch | Nhãn phân tích hoạt động như Tết, Trung thu; không phải một sổ độc lập |

## 5. Luồng nghiệp vụ đầu–cuối

### 5.1. Nhập hàng

Đơn nhập (chọn NCC từ danh mục, có thể thêm NCC nhanh) → chốt đơn → nhận một phần/toàn bộ (ghi màu, lô, hạn) → phiếu nhập ghi sổ → cập nhật tồn theo màu và phải trả → đối chiếu chứng từ mua → thanh toán/phân bổ tại Công nợ → đối soát → đóng đơn nhập.

- Không bắt buộc đơn nhập cho nhập nhanh nhưng phải có nguồn, hàng và giá trị rõ ràng.
- Có thể tạo hàng hóa mới và sửa giá bán lẻ ngay trên dòng đơn nhập.
- Trả hàng NCC theo từng dòng phiếu nhận, đúng lô và đúng màu; tạo chứng từ giảm phải trả; hoàn tiền được ghi riêng.
- Thanh toán trước khi nhận là ứng trước nhà cung cấp, chưa ghi thành giảm một khoản nợ chưa tồn tại.

### 5.2. Bán sỉ (hóa đơn)

Báo giá (Nháp → Đã gửi → Khách chấp nhận/Từ chối/Hết hạn) → chuyển thành hóa đơn → xác nhận (ngoại lệ do chủ cửa hàng tích xác nhận) → giữ hàng → ghi nhận tiền khách đã trả nếu có → \*\*Xuất kho giao khách\*\* (một bước, có thể nhiều lần) → ghi chứng từ bán nội bộ và phải thu → tự trừ tiền khách trả trước → thu phần còn lại tại Công nợ → đối soát → hoàn tất.

Hàng có tồn theo màu phải chọn màu trên dòng hóa đơn trước khi xác nhận; xuất kho trừ đúng màu đó.

### 5.3. Bán tại quầy

Quét/tìm hàng → chọn khách (giá theo nhóm khách) → chọn màu cho hàng có màu và cho thành phần combo chưa cố định màu → (tùy chọn) lưu giỏ hàng để làm tiếp → thanh toán hoặc bán chịu → ghi sổ kho và bán hàng nguyên tử → giỏ hàng chuyển “Đã bán” và bị khóa → trả/đổi hàng nếu có.

Ca bán hàng được mở ngầm; người dùng không phải mở/kết ca.

### 5.4. Đóng giỏ

Công thức và phiên bản → lệnh đóng → giữ nguyên liệu → lấy nguyên liệu theo lô → xác nhận tiêu hao → nhập giỏ thành phẩm → bán và xuất giỏ. Combo ghép khi bán đi theo luồng xuất thành phần, không qua nhập giỏ.

### 5.5. Kết thúc mùa

Hoàn thành/điều chỉnh hóa đơn còn mở → kiểm kê (theo màu với hàng có màu) → đối soát khách/NCC → rà soát hàng dư/hạn dùng → báo cáo mùa → khóa kỳ phù hợp. Khoản nợ chưa thanh toán vẫn tiếp tục ở mùa sau, không bị reset.

### 5.6. Trả hàng và đổi hàng

- \*\*Trả hàng:\*\* chọn chứng từ bán gốc → các dòng tách theo màu đã bán, kèm số còn được trả → nhập số trả → chọn đưa vào khu cách ly hay nhập lại bán được → ghi sổ; giảm nợ trước, phần vượt nợ thành khoản cần hoàn. Chỉ chứng từ cũ chưa ghi màu mới phải chọn màu khách trả.
- \*\*Đổi hàng:\*\* chọn chứng từ gốc → chọn hàng khách trả (theo màu) → chọn hàng khách lấy (cùng mặt hàng khác màu hoặc mặt hàng khác; giá theo khách và màu) → xem chênh lệch → ghi sổ một lần: phiếu trả + hóa đơn bán mới + phiếu đổi hàng liên kết hai chứng từ.

## 6. Danh mục yêu cầu chức năng đầy đủ

Mỗi mã yêu cầu là điểm truy vết cho thiết kế, backlog và kiểm thử. “Full scope” bao gồm tất cả yêu cầu dưới đây. Mã đánh dấu \*\*(1.1)\*\* là mới hoặc đã sửa ở bản 1.1; mã đã bỏ được liệt kê ở mục 2.5 và không còn trong bảng.

### 6.1. Tài khoản, tenant và tổ chức

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| ORG-01 | Đăng ký, xác thực liên hệ, đăng nhập, khôi phục tài khoản, kết thúc phiên | Link/token hết hạn và dùng một lần; chống dò mật khẩu; không lộ tenant khác |
| ORG-02 | Tạo tenant, cấu hình tên pháp lý/tên hiển thị, địa chỉ, mã số thuế nếu có, tiền tệ, timezone | VND mặc định; ngày hiển thị theo tenant; đổi cấu hình không làm đổi giá trị chứng từ cũ |
| ORG-03 (1.1) | Một cửa hàng, một kho | Không hiển thị ô chọn kho/chi nhánh, cột kho hay bộ lọc phạm vi ở bất kỳ màn hình nào; dữ liệu vẫn lưu kho\_id để mở rộng sau |
| ORG-05 | Mẫu chứng từ, đánh số | Số chứng từ duy nhất trong tenant |
| ORG-06 | Cửa hàng mẫu và sandbox đào tạo | Dữ liệu demo không đi vào báo cáo thật; có nhãn nhận diện và thao tác đặt lại mẫu |
| ORG-07 (1.1) | Chuyển quyền chủ sở hữu, thu hồi thiết bị/phiên | Có xác nhận bảo mật; không mất liên kết chứng từ cũ |

### 6.2. Hàng hóa và đối tác

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| CAT-01 | SKU, mã vạch, tên, ảnh, nhóm, thương hiệu, biến thể, trạng thái | Kiểm tra trùng mã trong tenant; ngừng bán vẫn xem được lịch sử |
| CAT-02 | Đơn vị cơ sở và quy đổi thùng/hộp/dây/lốc/chai/kg | Hệ số dương, snapshot trên dòng chứng từ; đổi hệ số không sửa lịch sử |
| CAT-03 | Giá mua tham khảo, giá bán, ngưỡng tồn, tracking lô/hạn, quy cách | Giá tham khảo không tự ghi thành giá vốn; không đổi mode tracking khi còn tồn chưa chuyển đổi |
| CAT-04 | Bộ thuộc tính và mẫu ngành | Tìm/lọc theo thuộc tính; không dùng thuộc tính thay SKU khi cần tồn độc lập |
| CAT-05 (1.1) | Nhà cung cấp tại \*\*Danh mục → Nhà cung cấp\*\*: thêm, sửa, ngừng giao dịch, xóa; tìm theo tên/điện thoại/mặt hàng cung cấp/MST | NCC ngừng giao dịch ẩn khỏi ô chọn nhưng vẫn giữ lịch sử và trả nợ được; chỉ xóa được NCC chưa có chứng từ; thêm NCC nhanh từ form đơn nhập/nhận hàng và chọn sẵn sau khi lưu |
| CAT-06 | Khách hàng: liên hệ, nhóm, địa chỉ, điều khoản, hạn mức; một đối tác có thể vừa là khách vừa là NCC | Hai sổ phải thu/phải trả tách biệt; không bù trừ tự động |
| CAT-07 | Danh mục lý do trả, hỏng, điều chỉnh, phương thức thanh toán | Dữ liệu đã dùng chỉ được ngừng sử dụng, không xóa đứt liên kết |
| CAT-08 (1.1) | Ô tìm &amp; chọn dùng chung cho mọi chỗ chọn hàng hóa, khách, NCC | Tìm không dấu theo mã/tên/mã vạch/điện thoại/vị; điều khiển bằng phím mũi tên, Enter, Esc, Tab; gợi ý hàng có tồn theo màu hiện chấm màu; in tem theo đơn vị |
| CAT-09 (1.1) | \*\*Màu sắc\*\*: bảng 7 màu cố định — xanh, đỏ, tím, vàng, nâu, đen, trắng — chọn bằng ô màu, không nhập chữ | Màu gắn lên lô khi nhập; hàng nhập không chọn màu thuộc “Chưa gán màu”; mọi màn hình hiển thị màu bằng cùng một kiểu ô màu (chấm màu + tên) |
| CAT-10 (1.1) | \*\*Nhóm mix vị\*\* tại Danh mục: tên nhóm, đơn vị thùng, số đơn vị lẻ/thùng, đơn vị chọn vị (dây, lốc…), số đơn vị lẻ/đơn vị chọn vị, danh sách vị (mặt hàng + tên vị ngắn) | ≥ 2 vị; các vị cùng đơn vị lẻ; số lẻ/thùng chia hết cho số lẻ/đơn vị chọn vị và ≥ 2; tên nhóm không trùng; nhóm đã dùng trên chứng từ không đổi được quy cách, không gỡ được vị đã bán, không xóa được (chỉ ngừng dùng); cảnh báo khi các vị khác giá lẻ |
| CAT-11 (1.1) | Thêm hàng hóa mới ngay trong đơn nhập; danh sách hàng hóa trong menu Nhập hàng | Hàng mới cần tên, đơn vị cơ sở, quy đổi (nếu có), giá nhập, giá bán; chặn trùng tên; danh sách hiện tồn theo màu dưới tên hàng |

### 6.3. Giá bán, khuyến mại và báo giá

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| PRC-01 | Bảng giá theo nhóm khách, mùa và khoảng hiệu lực | Xác định một giá theo thứ tự ưu tiên cấu hình; hiển thị nguồn giá |
| PRC-02 | Giá bậc thang theo số lượng/đơn vị, giá riêng khách | Quy đổi đúng trước xét bậc; snapshot giá khi xác nhận hóa đơn |
| PRC-03 | Chiết khấu dòng/đơn, mã khuyến mại, quà tặng, combo | Có quy tắc cộng dồn, trần chiết khấu; không áp trùng ngoài chính sách |
| PRC-04 (1.1) | Bán dưới giá tối thiểu hoặc vượt hạn mức | Hiện cảnh báo nêu rõ ngoại lệ; chủ cửa hàng tích “Tôi xác nhận vẫn bán” mới lưu được; ghi ngoại lệ vào nhật ký. Giá tối thiểu hiện là một mức chung cho cả sản phẩm (xem OPEN-19) |
| PRC-05 (1.1) | Báo giá: trạng thái Nháp → Đã gửi → Khách chấp nhận / Khách từ chối → Đã chuyển hóa đơn; tự chuyển “Hết hạn” khi quá ngày hiệu lực. Sửa, xóa, sao chép, chuyển thành hóa đơn | Báo giá không giữ tồn, không tạo doanh thu; chỉ sửa/xóa khi Nháp/Đã gửi; đã chuyển hóa đơn thì khóa. Cột dòng hàng theo thứ tự: \*\*Tên SP, ĐVT, Số lượng, Quy cách (chữ tự do), Đơn giá, CK, Thành tiền, Giá bán lẻ\*\*; chọn màu (đủ 7 màu, không giới hạn theo tồn); thêm thùng mix vị |
| PRC-06 | Thuế/phí, giá đã gồm/chưa gồm thuế, làm tròn | Hiển thị rõ; thay đổi thuế suất không sửa giao dịch cũ; không khẳng định tính hợp pháp tự động |
| PRC-07 (1.1) | \*\*Giá bán theo màu\*\*: mỗi màu có thể đặt giá lẻ/giá sỉ riêng; màu không đặt dùng giá chung của sản phẩm | Sửa ở Kho → Tồn kho (dòng màu). POS, giỏ hàng, hóa đơn, báo giá, đổi hàng lấy giá theo màu đã chọn và theo nhóm khách (lẻ/sỉ); đổi màu trên dòng hóa đơn/báo giá thì đơn giá tự cập nhật; “Bỏ giá riêng” đưa màu về giá chung; sửa giá chung có tùy chọn “Áp dụng cho tất cả màu”; lịch sử giá ghi rõ màu nào hay cả sản phẩm |
| PRC-08 (1.1) | Sửa giá bán từ hai nơi: Nhập hàng (dòng đơn nhập, danh sách hàng hóa) và Kho (tồn kho) | Áp dụng cho giao dịch mới; chứng từ đã lập giữ giá đã chụp; mọi thay đổi vào lịch sử giá với nơi sửa, người sửa, giá cũ → mới |

### 6.4. Nhập hàng

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| PUR-02 (1.1) | Đơn nhập: NCC (chọn từ danh mục, chỉ NCC đang giao dịch), ngày dự kiến, dòng hàng, chốt đơn | Nháp không tác động sổ; chốt không cần duyệt. Cột dòng hàng theo thứ tự: \*\*Tên SP, ĐVT (thùng/hộp…), Số lượng, Quy cách (chữ tự do), Đơn giá, Thành tiền, Giá bán lẻ\*\*; mỗi dòng chọn màu bằng ô màu; một mặt hàng nhiều màu nhập thành nhiều dòng |
| PUR-03 | Nhập nhanh không đơn nhập hoặc nhận theo đơn nhập | Lưu nguồn; không ghi nhận lại khi nhấn xác nhận lặp |
| PUR-04 | Nhận nhiều đợt, thừa/thiếu, hỏng, sai hàng | Đơn nhập hiển thị đặt/nhận/trả/còn chờ riêng |
| PUR-05 (1.1) | Lô, hạn dùng, màu, quy đổi, ảnh chứng từ | Bắt buộc trường theo mode SKU; màu của dòng nhận ghi lên lô; cùng lô khác màu là hai lô tồn riêng |
| PUR-06 | Chi phí mua và phân bổ vận chuyển/bốc xếp | Phân bổ vào giá vốn theo giá trị; tổng phân bổ khớp chi phí. Giữ lại ở bản 1.1 vì ảnh hưởng giá vốn, không phải chức năng giao nhận |
| PUR-07 | Ứng trước, trả ngay/một phần, lịch trả | Số còn phải trả truy được từng phiếu nhập |
| PUR-08 | Đối chiếu hóa đơn NCC và phiếu nhận | Một hóa đơn có thể gắn nhiều phiếu; không tạo lại phải trả đã ghi lúc nhập |
| PUR-09 (1.1) | Trả NCC, giảm giá sau mua, hàng đổi | Trả theo từng dòng phiếu nhận; xuất đúng lô \*\*và đúng màu\*\* của dòng đó; tạo credit/điều chỉnh giá trị riêng |
| PUR-10 | Chênh lệch giá cuối cùng so với giá tạm | Tạo điều chỉnh có log, phân bổ phần còn tồn/phần đã bán theo chính sách giá vốn |
| PUR-11 | Theo dõi lịch nhận, NCC giao chậm, đóng đơn nhập còn thiếu | Đóng phần thiếu cần lý do; không hủy chứng từ đã nhận |

### 6.5. Kho, tồn và truy xuất

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| INV-01 (1.1) | Sổ chuyển động tồn theo SKU/lô/\*\*màu\*\*/đơn vị cơ sở | Mỗi biến động có nguồn, người ghi và màu của lô; số dư khớp cộng sổ cả theo tổng lẫn theo từng màu |
| INV-02 (1.1) | Tồn thực, giữ hàng, có thể bán, cách ly/hỏng | Có định nghĩa nhất quán (mục 7.4); không còn khái niệm hàng đang chuyển |
| INV-03 | Giữ hàng thủ công/tự động, giải phóng khi hủy | Nhiều hóa đơn tranh cùng tồn không giữ vượt; giữ hàng theo mặt hàng, không theo màu (xem OPEN-17) |
| INV-05 (1.1) | Kiểm kê toàn kho/theo nhóm, đếm mù | Hàng có tồn theo màu tách một dòng đếm cho mỗi màu; lệch màu nào điều chỉnh màu đó; chủ cửa hàng xác nhận chênh lệch trước khi ghi sổ |
| INV-06 (1.1) | Hỏng, hao hụt, biếu, dùng nội bộ | Bắt buộc loại/lý do; hàng có tồn theo màu bắt buộc chọn màu (ô màu hiện số tồn từng màu); không tự tạo doanh thu |
| INV-07 | Quản lý lô/hạn dùng, cảnh báo, FEFO đề xuất | Lô hết hạn bị chặn bán mặc định; tiêu hủy do chủ cửa hàng xác nhận; truy xuất lô mua→bán; thông báo lô ghi kèm màu |
| INV-08 | Tồn tối thiểu, cảnh báo thiếu và hàng dư | Thông báo không tự sinh chứng từ nhập |
| INV-09 | Giá vốn và giá trị tồn | Chính sách ở mục 7; không sử dụng giá bán làm giá trị tồn |
| INV-10 (1.1) | \*\*Thẻ kho\*\* | Chọn hàng bằng ô tìm &amp; chọn; lọc khoảng ngày (Từ–Đến + nhanh: Hôm nay, 7 ngày, Tháng này, Tháng trước, Từ đầu; mặc định tháng này); lọc màu (khi hàng có màu); hiển thị số lượng “Quy đổi tự động” (VD: 17 bó 10 cái, kèm số đơn vị cơ sở) hoặc theo một đơn vị cố định; dòng đầu Tồn đầu kỳ, các dòng theo thời gian có cột Màu, Nhập, Xuất, Tồn cộng dồn; 4 ô tổng Tồn đầu/Nhập/Xuất/Tồn cuối; nhãn “Đã khớp sổ và tồn” (theo màu khi đang lọc màu); bấm số chứng từ để mở |
| INV-11 | Kho cách ly, trả lại chờ kiểm tra | Hàng cách ly không có thể bán; khôi phục bán được do chủ cửa hàng xác nhận |
| INV-12 (1.1) | \*\*Tồn kho gộp màu\*\* (Kho → Tồn kho, thay cho màn “Tồn theo màu” riêng) | Mỗi mặt hàng một dòng; hàng có màu hiện ô màu + số tồn từng màu \*\*trên cùng một dòng\*\*; nút ▸/▾ mở/thu dòng con từng màu (tồn thực, bán được, giá, giá trị); “Mở tất cả / Thu gọn tất cả”; lọc theo màu tự mở các mặt hàng có màu đó; lọc “Chỉ hàng có màu”; cột Có thể bán ghi kèm “giữ X · cách ly Y”; nút “Sửa giá” trên dòng sản phẩm (giá chung) và dòng màu (giá riêng); link Thẻ kho theo sản phẩm hoặc theo màu |

### 6.6. Bán hàng, POS và trả hàng

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| SAL-01 (1.1) | \*\*Hóa đơn\*\* (trước đây “Đơn bán”): khách, bên trả tiền, dòng hàng (Tên SP, Giá, CK, ĐVT, SL, Thành tiền), màu, thùng mix vị, ghi chú | Nháp không ghi sổ. Ô “Khách đã thanh toán” nhập số tiền; tự tính “Còn lại” và trạng thái Chưa thanh toán / Thanh toán một phần / Đã thanh toán đủ / Thừa tiền. Sửa/xóa khi chưa xuất kho và chưa có tiền; sao chép thành nháp mới; đã xuất kho thì khóa sửa/xóa |
| SAL-02 (1.1) | Xác nhận hóa đơn, kiểm tra tồn/hạn mức/giá tối thiểu | Ngoại lệ hiện rõ, chủ cửa hàng tích xác nhận, ghi nhật ký; hàng có tồn theo màu chưa chọn màu thì không xác nhận được |
| SAL-03 | Đặt trước/backorder | Tách lượng đã giữ và lượng thiếu; không coi toàn bộ đơn là hàng sẵn |
| SAL-04 (1.1) | \*\*Xuất kho giao khách\*\* một bước, nhiều lần cho một hóa đơn | Nhập số lượng xuất ≤ lượng đã giữ chưa xuất; ghi sổ ngay: trừ kho đúng màu đã chọn, ghi chứng từ bán và phải thu, tự trừ tiền khách trả trước; hóa đơn hiển thị mục “Phiếu xuất kho”; thiếu tồn màu đã chọn thì báo lỗi, không lấy màu khác |
| SAL-05 (1.1) | POS: quét/tìm hàng (≤ 30 ô hàng, ô hàng có màu hiện chấm màu), chọn khách, đổi đơn vị, chiết khấu dòng, tiền mặt/chuyển khoản, bán chịu | Ghi sổ nguyên tử; hàng có tồn theo màu bắt buộc chọn màu (chỉ hiện màu còn hàng kèm số tồn; còn một màu thì tự chọn); không bán vượt tồn của màu; “+ Thêm màu khác” tách dòng, trùng màu tự gộp; giá theo màu; ca tự mở ngầm |
| SAL-06 | Bán chịu, cọc, trả một phần, lịch thu | Cọc là ứng trước trước giao; công nợ phát sinh theo quy tắc ghi nhận |
| SAL-07 | In/gửi phiếu, chứng từ bán nội bộ | Phiếu nội bộ có nhãn rõ “không phải hóa đơn điện tử” |
| SAL-08 (1.1) | Trả toàn bộ/một phần, hoàn tiền/giảm nợ | Dòng trả tách theo màu đã bán (lấy từ chứng từ gốc, không cho chọn tùy ý); chỉ chứng từ cũ chưa ghi màu mới bắt chọn màu khách trả; không trả vượt số đã bán của màu đó; tùy chọn đưa vào khu cách ly hoặc nhập lại bán được; giảm nợ trước, phần vượt thành khoản cần hoàn |
| SAL-09 | Trả không có chứng từ gốc | Lý do bắt buộc; không tự khôi phục giá vốn giả định |
| SAL-10 | Hủy phần chưa xuất, thay đổi hóa đơn sau xác nhận | Không xóa phần đã ghi; sửa bằng phiên bản |
| SAL-13 | Giao dịch POS khi mất mạng có giới hạn | Chỉ bán tiền ngay với dữ liệu cache hợp lệ, không bán chịu; cảnh báo tồn chưa chắc chắn; đồng bộ một lần, lỗi vào hàng đợi ngoại lệ |
| SAL-14 (1.1) | \*\*Giỏ hàng POS\*\*: lưu giỏ, hiện tên khách, sửa/xóa/sao chép; danh sách giỏ Đang soạn/Đã bán | Giỏ lưu chưa giữ hàng, chưa ghi sổ; giữ màu và màu thành phần combo; thanh toán xong giỏ chuyển “Đã bán”, khóa sửa/xóa, chỉ sao chép |
| SAL-15 (1.1) | \*\*Đổi hàng\*\* (nút ở Trả hàng và trong chi tiết chứng từ bán) | Phần 1 hàng khách trả (theo màu, link “Đổi sang màu khác” thêm sẵn dòng cùng mặt hàng); phần 2 hàng khách lấy (ô tìm &amp; chọn, chọn màu còn hàng, giá theo khách và màu); hiển thị giá trị trả/lấy, “Khách trả thêm” hoặc “Cửa hàng trả lại khách”, giảm nợ cũ; ghi sổ một lần tạo phiếu trả + hóa đơn bán mới + phiếu đổi (DH-xxxx); trang Trả hàng có bảng “Phiếu đổi hàng” |

### 6.7. Giỏ quà, combo và đóng gói

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| KIT-01 (1.1) | Công thức gồm hàng, vỏ hộp, giỏ, phụ kiện và chi phí đóng | Phiên bản và hiệu lực rõ; bảng thành phần có cột giá bán, thành tiền theo giá bán, giá vốn, có thể bán; thành phần chọn được màu; thêm “thùng mix vị” (chọn nhóm, số lượng theo dây/thùng, giá tính theo từng vị) |
| KIT-02 | Giỏ đóng sẵn có tồn độc lập | Xuất thành phần và nhập giỏ trong một nghiệp vụ cân bằng; bán chỉ trừ giỏ; thành phần có màu cố định trừ đúng màu khi đóng |
| KIT-03 (1.1) | Combo ghép khi bán không có tồn riêng | Xuất thành phần lúc bán; thành phần cố định màu trừ đúng màu; thành phần có tồn theo màu nhưng chưa cố định thì chọn màu tại quầy (bắt buộc), không bán vượt tồn màu đó |
| KIT-04 | Lệnh đóng, lượng kế hoạch/thực tế, giữ nguyên liệu | Không tiêu hao nguyên liệu mới chỉ lên kế hoạch; hoàn thành ghi sổ nguyên tử |
| KIT-05 | Thay thành phần theo khách/thiếu hàng | Chụp phiên bản thực tế, cập nhật giá/giá vốn |
| KIT-06 (1.1) | Đóng một phần, hao hụt, tháo giỏ, đóng lại | Tháo giỏ nhập lại thành phần đúng lô và \*\*đúng màu\*\* đã dùng; vật tư hỏng không tự trở lại tồn bán được |
| KIT-07 | Theo dõi tiến độ đóng, tem giỏ | Gắn lệnh đóng; truy xuất lô hiện màu thành phần |
| KIT-08 | Truy xuất lô và hạn của thành phần trong giỏ | Có genealogy lô; hạn bán giỏ mặc định không vượt hạn sớm nhất của thành phần |
| KIT-09 | Giỏ tùy chọn theo ngân sách và báo giá | Hiển thị giá/lãi dự kiến; không tự thay món trên hóa đơn đã xác nhận |
| KIT-10 (1.1) | Giỏ/combo \*\*nháp\*\*: lưu nháp, xem trước (thẻ giới thiệu giỏ), sửa, xóa, phát hành | Nháp không bán được, không hiện ở POS/hóa đơn; phát hành mới bán; giỏ đã bán không xóa được |

### 6.8. Giao nhận và COD — đã bỏ ở bản 1.1

Toàn bộ DEL-01..07 đã bỏ (xem mục 2.5). Xuất hàng cho khách thực hiện bằng “Xuất kho giao khách” (SAL-04). Không có trạng thái vận chuyển, vận đơn, chuyến, người giao, thu hộ.

### 6.9. Công nợ, ứng trước và đối soát

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| DEBT-01 | Sổ phải thu/phải trả theo đối tác, chứng từ | Số dư tổng khớp tổng chi tiết; không tự bù trừ hai chiều |
| DEBT-02 | Nợ đầu kỳ và tiền ứng trước đầu kỳ | Có ngày cutoff, nguồn và xác nhận của chủ cửa hàng (cách nhập theo OPEN-14); không ghi lại doanh thu/giá trị hàng |
| DEBT-03 | Hạn thanh toán, nhiều kỳ hạn và tuổi nợ | Tuổi nợ dựa vào hạn của phần chưa thanh toán; báo cáo as-of tái lập được |
| DEBT-04 (1.1) | Thu/trả nhiều lần và phân bổ nhiều chứng từ — thực hiện tại menu Công nợ (không còn menu Tiền) | Không phân bổ vượt dư chứng từ hoặc dư tiền; phần chưa phân bổ thành ứng trước |
| DEBT-05 | Đặt cọc khách/ứng trước NCC, hoàn cọc, chuyển phân bổ | Không đưa ứng trước vào doanh thu; hoàn theo phiếu thu/chi |
| DEBT-06 (1.1) | Hạn mức, cảnh báo quá hạn, chặn bán chịu | Vượt hạn mức cần chủ cửa hàng xác nhận ngoại lệ, có log |
| DEBT-07 | Bảng kê, biên bản đối soát và xác nhận | Có kỳ, số dư đầu/cuối, giao dịch; phiên bản đã gửi không bị thay âm thầm |
| DEBT-08 | Nhắc nợ theo lịch, ghi chú liên hệ, hẹn thanh toán | Có consent/chính sách liên hệ; không gửi lặp vì retry; không công khai thông tin nợ |
| DEBT-09 | Giảm giá, xóa nợ, tranh chấp, điều chỉnh | Bắt buộc lý do và ghi nhật ký; không dùng điều chỉnh nợ để che giấu thu tiền |
| DEBT-10 | Bù trừ khách–NCC hoặc chuyển nghĩa vụ có biên bản | Explicit, chứng từ hai chiều; không net mặc định |
| DEBT-11 | Đảo khoản thu/chi và tái phân bổ | Khôi phục dư đúng; chặn đảo nếu tiền đã dùng ở nghiệp vụ sau chưa xử lý |
| DEBT-13 | Dự kiến thu/trả và ưu tiên nhắc nợ | Dùng hạn và cam kết; phân biệt dự báo với tiền chắc chắn đã nhận |

### 6.10. Quỹ và thanh toán (menu Tiền đã bỏ ở bản 1.1)

Không còn menu Tiền. Các quy tắc tiền vẫn chạy ngầm để số liệu dashboard, công nợ và dòng tiền đúng:

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| CASH-01 (1.1) | Hai quỹ: Tiền mặt tại cửa hàng, Tài khoản ngân hàng; số dư đầu | Số dư đầu không tự thành doanh thu; số dư hiện ở Bảng điều khiển (“Tiền trong quỹ”) và Báo cáo → Dòng tiền |
| CASH-02 (1.1) | Phiếu thu/chi phát sinh từ POS, Hóa đơn (khách thanh toán), Công nợ (thu khách/trả NCC), Trả hàng (hoàn tiền), Đổi hàng | Ghi sổ một lần; không có màn hình danh sách phiếu thu/chi riêng |
| CASH-04 | Thanh toán nhiều phương thức, tiền thừa, hoàn tiền | Tổng khớp; khoản chuyển khoản chờ xác minh không thành tiền đã nhận |
| CASH-06 (1.1) | Ca bán hàng tự mở ngầm khi vào POS | Người dùng không phải mở/kết ca; không có đối chiếu tiền cuối ca (xem OPEN-13) |

Đã bỏ: CASH-03 chuyển quỹ, CASH-05 QR/sao kê, CASH-07/08 ghi chi phí, CASH-09 sổ quỹ (thay bằng báo cáo Dòng tiền), CASH-10 tiền chủ góp/rút.

### 6.11. Chiến dịch, CRM và thông báo

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| SEA-01 | Mùa Tết/chiến dịch: khoảng thời gian, ngân sách, mục tiêu | Giao dịch ngoài khoảng vẫn có thể gắn khi có quyền; không thay sổ tài chính |
| SEA-02 | Theo dõi nhu cầu theo hóa đơn đã xác nhận, hàng sẵn, đơn nhập và lịch nhận | Không cộng lượng dự kiến thành tồn thực; báo thiếu theo ngày |
| SEA-03 | Checklist trước cao điểm và kết mùa | Nhắc kiểm kê, đối soát, đơn mở, hàng dư; không xóa nợ khi kết mùa |
| CRM-01 | Lịch sử mua, nhóm khách, ghi chú | Lưu consent liên hệ |
| CRM-02 | Tái đặt đơn, danh sách khách mùa trước, lịch chăm sóc | Tái đặt lấy giá/quy cách hiện hành và cần xác nhận, không copy mù lịch sử |
| CRM-03 | Điểm/voucher/ưu đãi khách thân thiết tùy chọn | Sổ điểm có lịch sử; trả hàng đảo điểm; không đổi công nợ thành điểm tự động |
| NTF-01 (1.1) | Trung tâm thông báo trong ứng dụng: nợ quá hạn, sắp phải trả NCC, lô sắp hết hạn/hết hạn (kèm màu), dưới tồn tối thiểu | Chống trùng; không còn “Cần giao”, “Tiền thu hộ chưa về”, “Sao kê chưa ghép” |

### 6.12. Báo cáo, dashboard và trợ lý phân tích

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| RPT-01 (1.1) | \*\*Bảng điều khiển thu – chi – công nợ\*\* | Lọc theo Ngày / 7 ngày / Tháng / Năm, chọn ngày, kỳ trước/kỳ sau, về hôm nay; nhóm Thu–chi (tổng thu, tổng chi, chênh lệch, tiền trong quỹ) và Công nợ (khách còn nợ, cửa hàng còn nợ NCC, sắp thu, sắp trả) có so sánh kỳ trước và biểu đồ; bỏ khách lẻ và số đầu kỳ khỏi biến động nợ; không có thẻ “Cần xử lý”; bấm thẻ mở màn chi tiết |
| RPT-02 | Nhập–xuất–tồn, tồn theo lô, hạn, giá trị và giữ hàng | Số đầu + biến động = cuối; tồn có thể bán theo định nghĩa ở mục 7. Hiện tính theo mã hàng, chưa tách màu (xem OPEN-20) |
| RPT-03 | Bán hàng theo hàng/khách/kênh | Doanh thu thuần sau trả/giảm, tách thuế; không lấy tổng hóa đơn làm doanh thu thực hiện |
| RPT-04 | Lãi gộp, biên lãi và kết quả quản trị | Giá vốn truy vết; phân biệt số tạm/chính thức; không gọi là BCTC đã kiểm toán; ghi rõ chưa trừ chi phí vận hành (OPEN-13) |
| RPT-05 | Công nợ chi tiết, tuổi nợ, lịch thu/trả, ứng trước | As-of và hạn; không gộp ứng trước thành nợ âm khó hiểu |
| RPT-06 (1.1) | Dòng tiền | Tiền thực vào/ra quỹ theo phân loại (bán hàng, cọc, trả NCC, hoàn tiền); thay cho các trang của menu Tiền |
| RPT-07 | Giỏ quà: tiến độ, thiếu thành phần, chi phí, lãi | Không cộng trùng doanh thu giỏ và thành phần |
| RPT-08 | Mùa: kế hoạch/thực tế, hàng dư, so sánh các mùa | Không đếm trùng một giao dịch ở tổng hợp; giải thích attribution |
| RPT-09 | Tùy biến cột, báo cáo lưu, xuất CSV từ trang báo cáo | File lớn tạo async; link hết hạn |
| RPT-10 | Gợi ý mua/xả hàng và dự báo dòng tiền | Có nguồn, phương pháp, độ tin cậy; chỉ là đề xuất cần xác nhận |
| RPT-11 | Hỏi đáp dữ liệu bằng ngôn ngữ tự nhiên tùy chọn | Chỉ đọc trong tenant/quyền; dẫn chứng số liệu/chứng từ; không tự ghi sổ hay gửi dữ liệu ra model chưa được cho phép |

### 6.13. Bảo mật, xác nhận ngoại lệ và audit

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| SEC-01 (1.1) | Một tài khoản chủ cửa hàng, toàn quyền trong tenant | Server vẫn kiểm tra xác thực và tenant ở mọi request; không dựa vào ẩn nút |
| SEC-03 (1.1) | Xác nhận ngoại lệ thay cho quy trình duyệt: vượt hạn mức, dưới giá tối thiểu, tiêu hủy, chênh lệch kiểm kê, xóa nợ | Hiện rõ ngoại lệ và tác động; người dùng tích xác nhận; nhật ký ghi loại ngoại lệ, chi tiết, thời điểm |
| SEC-04 | Nhật ký tạo/sửa/xác nhận/ghi/đảo/export | Actor, thời gian, before/after phù hợp, nguồn; không chứa mật khẩu/token; xem tại Thiết lập → Nhật ký |
| SEC-05 | Khóa kỳ, mở lại có lý do, ngày nghiệp vụ khác ngày hệ thống | Không backdate vào kỳ khóa; mở lại có log và báo cáo ảnh hưởng |
| SEC-06 | MFA, quản lý thiết bị và phiên | Thu hồi có hiệu lực; thao tác nhạy cảm yêu cầu tái xác thực |
| SEC-07 | Truy cập hỗ trợ có đồng ý, phạm vi/thời hạn, log | Owner có thể thu hồi; mặc định read-only; không impersonate âm thầm |

### 6.14. Tài liệu và đính kèm (tích hợp, import/export đã bỏ ở bản 1.1)

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| DATA-04 | Mẫu PDF/in cho đơn nhập, hóa đơn, phiếu xuất/nhập, đối soát, nhãn | Unicode đúng, đơn vị/thuế/màu rõ; không đổi snapshot bản đã gửi |
| DATA-05 | Đính kèm ảnh/chứng từ và phiên bản | Signed URL, hạn chế loại/dung lượng, quét file; tenant khác không tải được |

Đã bỏ: DATA-01..03 (import/export), PORT-01..02 (cổng khách/NCC), API-01..04 (API, tích hợp ngân hàng/giao vận/HĐĐT/kênh bán/kế toán). Hệ quả cần quyết định: OPEN-14 (nhập đầu kỳ), OPEN-15 (xuất dữ liệu khi ngừng thuê).

### 6.15. Quản trị SaaS và bán sản phẩm cho cửa hàng

| ID | Chức năng/yêu cầu | Tiêu chí nghiệm thu |
|---|---|---|
| SAAS-01 | Gói quanh năm/theo mùa, add-on, trial và entitlement | Quyền module/limit ở server; giá chưa chốt; thử nghiệm không tự thu phí |
| SAAS-02 | Đăng ký/gia hạn/nâng/hạ gói, bill và thanh toán dịch vụ | Điều khoản prorate rõ; callback trùng không gia hạn/thu hai lần |
| SAAS-03 | Grace period, hết hạn, read-only và export | Không xóa dữ liệu hay giấu nợ; read-only hết quyền sau khoảng thời gian công bố |
| SAAS-04 | Onboarding wizard, checklist, video hướng dẫn cho chủ cửa hàng | Theo dõi hoàn thành; có bước xác nhận số dư đầu kỳ |
| SAAS-05 | Ticket hỗ trợ, trạng thái, đính kèm, SLA theo gói | Không yêu cầu gửi mật khẩu; có lịch sử và phân quyền support |
| SAAS-06 | Admin tenant metadata, usage, sự cố, billing, feature flag | Không mặc định mở dữ liệu bán hàng; thay flag không thay lịch sử |
| SAAS-07 | Theo dõi sức khỏe, backup/restore, incident và status | Phục hồi tenant có quy trình đối soát; không ghi đè tenant khác |
| SAAS-08 | Quyền sở hữu, lưu giữ, đóng tài khoản và xóa dữ liệu | Có yêu cầu/xác minh, thời gian chờ, export và ngoại lệ lưu giữ pháp lý đã rà soát |
| SAAS-09 | Partner/reseller, referral, tenant được giới thiệu | Không mặc định cho reseller quyền đọc dữ liệu; hoa hồng subscription tách hoa hồng bán hàng |
| SAAS-10 | Feedback, analytics và consent | Thu thập tối thiểu; analytics sản phẩm không chứa toàn bộ chứng từ/PII |

## 7. Quy tắc nghiệp vụ bắt buộc

### 7.1. Mốc ghi nhận và phạm vi sổ quản trị

Để tránh nhập nhằng, bản thiết kế này chọn \*\*mô hình sổ quản trị theo xuất/nhập kho\*\*:

- PO/SO/báo giá: cam kết, chưa ghi tồn thực, doanh thu hay công nợ.
- Nhập hàng ghi sổ: tăng tồn và phát sinh phải trả theo giá nhận. Nếu chứng từ NCC đến sau, chỉ đối chiếu/điều chỉnh phần chênh, không ghi lại toàn bộ nợ.
- Xuất bán ghi sổ: giảm tồn; tạo chứng từ bán nội bộ cho lượng thực giao; ghi nhận doanh thu quản trị và phải thu tương ứng.
- POS: giao hàng và bán hàng diễn ra cùng lúc; ghi sổ nguyên tử.
- Đóng giỏ/biếu/hao hụt: không tạo doanh thu bán hàng.
- “Xuất kho giao khách” là thời điểm ghi nhận: trừ kho, chứng từ bán, phải thu cùng lúc. Không có trạng thái vận chuyển; khách trả lại hàng đi theo quy trình trả hàng/đổi hàng.
- Đổi hàng = trả hàng theo chứng từ gốc + bán mới; hai sự kiện ghi sổ riêng, liên kết bằng phiếu đổi, không ghi đè chứng từ gốc.
- Thu/chi không tự sinh doanh thu/chi phí nếu đang thanh toán nghĩa vụ đã ghi.
- HĐĐT và chứng từ kế toán tích hợp liên kết sự kiện đã ghi; không tạo sự kiện thứ hai.

Đây là quy tắc \*\*quản trị vận hành\*\*, không khẳng định đáp ứng mọi thời điểm ghi nhận kế toán/thuế. Nếu ngành hoặc hợp đồng yêu cầu ghi nhận khi khách chấp nhận giao, cần thiết kế mode “hàng gửi đi” và hoàn thành rà soát trước áp dụng, không trộn hai mode trong cùng sổ.

### 7.2. Trạng thái và tác động

| Đối tượng | Trạng thái | Quy tắc |
|---|---|---|
| Báo giá | Nháp → Đã gửi → Khách chấp nhận / Khách từ chối → Đã chuyển hóa đơn; Hết hạn tự động | Không giữ tồn, không doanh thu; chỉ sửa/xóa khi Nháp/Đã gửi |
| Đơn nhập | Nháp → Đã chốt → Nhận một phần → Hoàn tất / Đóng – thiếu | Trạng thái thực hiện tính từ dòng; không hủy phần đã nhận |
| Hóa đơn | Nháp → Đã xác nhận → Giao một phần → Hoàn tất; Đã hủy phần còn lại | Trạng thái xuất kho và thanh toán độc lập; đã xuất kho thì khóa sửa/xóa |
| Thanh toán hóa đơn | Chưa thanh toán → Thanh toán một phần → Đã thanh toán đủ / Thừa tiền | Tính từ nghĩa vụ và phân bổ; không nhập tay trạng thái |
| Phiếu xuất kho | Đã xuất kho (ghi sổ ngay khi tạo) | Một bước; không có trạng thái soạn/giao |
| Giỏ hàng POS | Đang soạn → Đã bán | Đã bán thì khóa, chỉ sao chép |
| Giỏ quà/combo | Nháp → Đang bán → Ngừng bán | Nháp không bán được |
| Nhập/xuất/thu/chi | Ghi sổ → đảo một phần/toàn bộ | Dùng dịch vụ ghi sổ duy nhất |
| Kiểm kê | Đang đếm → Chờ ghi sổ (có lệch) → Đã ghi sổ | Khớp sổ thì ghi ngay; có lệch chờ chủ cửa hàng xác nhận |
| Lệnh đóng giỏ | Nháp → giữ nguyên liệu → đang đóng → hoàn thành một phần/toàn bộ → đóng | Phần đã ghi phải đảo/tháo giỏ, không chỉ đổi trạng thái |
| Đối soát | Nháp → gửi → xác nhận/tranh chấp → điều chỉnh phiên bản | Số liệu bản đã gửi bất biến |
| Nhóm mix vị / NCC | Đang dùng ↔ Ngừng dùng (NCC: Đang giao dịch ↔ Ngừng giao dịch) | Đã có chứng từ thì không xóa, chỉ ngừng |
| Kỳ | Mở → khóa → mở lại có lý do | Không đổi thời gian hệ thống để né khóa |

### 7.3. Đơn vị, tiền, thuế và làm tròn

- Số lượng dùng decimal chính xác theo cấu hình SKU; mặt hàng rời có thể bắt buộc số nguyên.
- Tồn lưu bằng đơn vị cơ sở; hệ số quy đổi trên mỗi dòng là snapshot.
- Tiền dùng decimal hoặc integer minor unit, không float. VND hiển thị mặc định không có phần lẻ; phép phân bổ trung gian giữ độ chính xác và làm tròn ở ranh giới đã định.
- Giá và thuế snapshot; thứ tự tính: giá dòng → giảm giá dòng → phân bổ giảm giá đơn → cơ sở thuế/phí theo cấu hình → tổng phải thanh toán.
- Chính sách cộng dồn, làm tròn theo dòng/đơn và phân bổ phần dư phải được cấu hình, công bố và áp dụng nhất quán.
- Giảm giá toàn đơn phân bổ theo giá trị đủ điều kiện; quà tặng vẫn có xuất kho/giá vốn.
- Ngày lưu gồm thời điểm hệ thống UTC và ngày nghiệp vụ theo timezone tenant; không tính hạn bằng timezone máy người dùng.

### 7.4. Tồn và giữ hàng

- \*\*Tồn thực:\*\* lượng đã ghi nhận tại kho, gồm cả khu vực bán được và không bán được.
- \*\*Tồn bán được:\*\* tồn thực trừ lượng cách ly, hỏng, hết hạn hoặc bị chặn.
- \*\*Tồn có thể bán:\*\* tồn bán được trừ lượng giữ hợp lệ. Không trừ hai lần nếu dashboard đã dùng một view có sẵn reservation.
- Đơn nhập đang chờ nhận không nằm trong tồn có thể bán; có thể hiển thị riêng “dự kiến có”.
- Giữ hàng có SKU, lô nếu cần, nguồn, lượng và hạn; không giữ vượt tồn bán được trong thao tác đồng thời. \*\*Giữ hàng tính theo mặt hàng, không theo màu\*\*; màu được xác định và kiểm tra lúc xuất kho.
- Backorder thể hiện nhu cầu chưa được giữ, không ép tồn âm.
- Tồn âm bị chặn mặc định; không cho override khi tenant hết hạn hoặc POS offline.
- Kiểm kê không âm thầm ghi đè số hiện tại. Chênh lệch tính theo cutoff, giao dịch đã phát sinh và số thực đếm được chủ cửa hàng xác nhận.

### 7.5. Giá vốn

- Chính sách mặc định: bình quân di động theo SKU (một cửa hàng, một cost pool). Giá vốn không tách theo màu.
- FEFO là thứ tự lấy hàng theo hạn, \*\*không đồng nghĩa FIFO tính giá vốn\*\*.
- Giá vốn của xuất được chụp tại ghi sổ; trả theo giao dịch gốc dùng giá vốn gốc khi truy được.
- Giỏ đóng sẵn: giá trị thành phẩm bằng giá trị nguyên liệu thực dùng + chi phí đóng được vốn hóa theo chính sách. Không tính lại bằng giá mua hiện tại.
- Combo ảo: giá vốn là tổng xuất thành phần thực tế; doanh thu ghi cho combo, không cộng thêm doanh thu thành phần.
- Chi phí mua muộn/chênh giá: tạo adjustment; phân bổ vào giá trị còn tồn và điều chỉnh giá vốn phần đã xuất. Không sửa âm thầm báo cáo kỳ khóa.
- Backdate có thể làm thay đổi giá vốn sau đó: cần job tính lại có phiên bản, preview ảnh hưởng và xác nhận của chủ cửa hàng. Kỳ khóa dùng điều chỉnh kỳ mở trừ khi mở lại được phép.
- Chính sách khác như FIFO là điểm mở rộng cần đặc tả riêng trước bật; không tùy ý đổi phương pháp khi còn dữ liệu.

### 7.6. Công nợ và phân bổ thanh toán

- Phải thu = nghĩa vụ bán đã ghi + debit adjustment − credit/trả/giảm đã ghi − khoản thu được phân bổ.
- Phải trả = nghĩa vụ mua đã ghi + adjustment tăng − trả/giảm đã ghi − khoản chi được phân bổ.
- Tiền thu chưa phân bổ là ứng trước khách; tiền chi chưa phân bổ là ứng trước NCC. Trình bày cả số dư gộp và net có giải thích, không mặc định coi nợ âm.
- Phân bổ thủ công hoặc gợi ý theo hạn cũ nhất; người dùng thấy và xác nhận. Không phân bổ sang đối tác/currency khác.
- Một khoản tiền không được phân bổ vượt giá trị xác nhận; một nghĩa vụ không được thanh toán vượt dư.
- Hạn mức mặc định kiểm tra: phải thu mở + giá trị đơn bán chịu đã xác nhận nhưng chưa ghi, trừ ứng trước đã dành rõ cho đơn. Không trừ mọi cọc của khách khỏi mọi đơn tùy ý.
- Trả hàng giảm nghĩa vụ và có thể tạo khoản cần hoàn. Khoản cần hoàn được ghi riêng; chỉ giảm quỹ khi thực hoàn.
- Hàng trả vào kho cách ly có thể đã giảm nghĩa vụ, nhưng chưa trở lại tồn bán được. Người dùng có thể chọn nhập lại bán được ngay khi hàng còn nguyên.
- \*\*Đổi hàng:\*\* giá trị hàng trả trước hết giảm nợ còn mở của chứng từ gốc; phần còn lại (nếu có) hoàn ngay qua quỹ đã chọn; hàng mới được bán và thu đủ qua cùng quỹ. Vì vậy với khách đang nợ, nợ giảm và tiền thu thêm có thể lớn hơn phần chênh lệch hai bên; tổng vẫn khớp (xem OPEN-18).
- Bù trừ chỉ với biên bản và xác nhận của chủ cửa hàng; không xóa hai sổ phải thu/phải trả.
- Xóa nợ là sự kiện quản trị có lý do và nhật ký; không phải tiền thu và không tăng doanh thu.

### 7.7. Chiến dịch và attribution

- Mỗi dòng doanh thu/chi phí có một chiến dịch chính hoặc không có; nhiều nhãn chỉ để lọc, không dùng cộng tổng nhiều lần.
- Mua hàng có nhãn mục đích nhưng không tự thành chi phí mùa. Giá vốn vào mùa khi xuất bán liên quan.
- Khoản thu sau Tết có thể thuộc dòng tiền kỳ thu và đồng thời truy về đơn mùa Tết; hai báo cáo có tiêu đề/phạm vi khác nhau.
- Kết mùa không reset tồn, tiền hoặc nợ. Hàng dư và công nợ tiếp tục trong sổ chung.
- So sánh mùa dùng cùng định nghĩa, ghi rõ số ngày và phạm vi; không đưa số giả làm benchmark thị trường.

### 7.8. Đảo, điều chỉnh và khóa kỳ

- Không xóa cứng chứng từ ghi sổ. Bản gốc và toàn bộ chuỗi đảo/điều chỉnh được lưu.
- Đảo nhập nếu hàng đã bán/đóng/chuyển cần xử lý phụ thuộc hoặc dùng điều chỉnh hợp lệ; không tạo âm kho ngoài policy.
- Đảo thu/chi phải gỡ phân bổ và xử lý khoản hoàn/chuyển đã liên quan.
- Sửa hóa đơn đã xác nhận tạo version; xác nhận ngoại lệ cũ mất hiệu lực nếu thay trường trọng yếu.
- Điều chỉnh ngày trước cutoff đầu kỳ bị chặn trừ quy trình migration riêng.
- Một giao dịch ghi sổ thực hiện trong transaction với idempotency key, kiểm soát version và tenant; không cập nhật kho thành công nhưng nợ thất bại.

### 7.9. Màu sắc (bản 1.1)

- Bảng màu cố định 7 màu: xanh (xanh dương), đỏ, tím, vàng, nâu, đen, trắng. Chưa có xanh lá (OPEN-21).
- Màu là thuộc tính của \*\*lô\*\*: cùng mã lô khác màu là hai lô tồn riêng. Mỗi chuyển động kho ghi màu của lô bị tác động.
- Một mặt hàng được coi là “có tồn theo màu” khi có ít nhất một lô có màu còn tồn bán được.
- \*\*Bán bắt buộc chọn màu\*\* với hàng có tồn theo màu: POS, giỏ hàng, hóa đơn (trước khi xác nhận), thành phần combo chưa cố định màu, đổi hàng. Ô chọn chỉ hiện màu còn hàng kèm số tồn. Báo giá được chọn đủ 7 màu vì có thể báo hàng chưa về.
- \*\*Trừ kho đúng màu đã chọn\*\* (không tự lấy màu khác khi thiếu). Không chọn màu thì trừ theo hạn dùng (FEFO) trên mọi màu.
- Mọi nghiệp vụ cộng/trừ kho giữ màu: nhập, trả NCC (theo dòng phiếu nhận), điều chỉnh (bắt buộc chọn màu), kiểm kê (dòng theo màu), đóng giỏ (màu cố định của thành phần), tháo giỏ (trả lại đúng màu đã dùng), trả hàng/đổi hàng (màu theo chứng từ gốc).
- \*\*Giá theo màu\*\* là tùy chọn trên từng màu; giá vốn không tách theo màu.
- Bất biến kiểm thử: với mọi SKU, tổng chuyển động theo từng màu = tồn lô theo màu đó.

### 7.10. Thùng mix vị (bản 1.1)

- Dòng thùng mix vị trên báo giá/hóa đơn: chọn nhóm mix, số thùng, đơn giá một thùng (mặc định = giá bình quân các vị × số đơn vị lẻ/thùng), CK; nhập số dây/lốc từng vị; tổng không vượt số thùng × số dây/thùng; tính tiền theo số dây/lốc thực chọn.
- Đổi nhóm mix trên dòng thì bảng vị, số đã chọn, đơn giá và đơn vị được làm mới.
- Trong giỏ quà/combo: số lượng mix tính theo dây hoặc thùng; giá bán và giá vốn tính theo từng vị.
- Khi ghi sổ, dòng mix được tách thành các dòng SKU vị với đơn vị “dây/lốc”; giữ khóa nhóm để mở lại vẫn hiện như một thùng mix.

## 8. User stories trọng tâm

| ID | Người dùng và nhu cầu | Hoàn thành khi |
|---|---|---|
| US-01 | Chủ cửa hàng muốn thấy thu, chi, công nợ theo ngày/tuần/tháng/năm | Bảng điều khiển lọc kỳ, so sánh kỳ trước, drill-down được |
| US-02 | Chủ muốn nhận một đơn nhập nhiều lần, nhiều màu | Hiển thị đủ đặt/nhận/trả/còn chờ; tồn theo màu cập nhật mỗi lần |
| US-03 | Chủ muốn chốt hóa đơn khi một phần hàng chưa về | Phân biệt sẵn có và backorder; không giả vờ giữ hàng thiếu |
| US-05 | Chủ muốn nhận một khoản tiền trả nhiều hóa đơn | Phân bổ, dư tiền và bảng kê khớp |
| US-06 | Khách muốn đặt cọc rồi nhận hàng nhiều lần | Cọc không thành doanh thu trước xuất kho; phân bổ truy được |
| US-07 | Chủ muốn đóng giỏ rồi bán giỏ | Chỉ tiêu hao nguyên liệu một lần, đúng màu, truy được lô |
| US-08 | Chủ muốn ghép combo tại quầy với thành phần có màu | Chọn màu thành phần, xuất đúng màu mà không cần tồn giỏ |
| US-10 | Chủ muốn tổng kết Tết nhưng vẫn thu nợ sau Tết | Báo cáo mùa ổn định, nợ không biến mất |
| US-13 | Chủ muốn ngừng thuê nhưng lấy lại dữ liệu | Cách xuất dữ liệu được chốt (OPEN-15) |
| US-14 | Support muốn giúp mà không xâm phạm dữ liệu | Phiên hỗ trợ có consent, phạm vi và lịch sử |
| US-15 (1.1) | Khách mua túi quà màu đỏ, muốn đổi sang màu vàng | Một thao tác “Đổi hàng”: tồn đỏ tăng, vàng giảm, chỉ thu/trả chênh lệch, có phiếu đổi liên kết |
| US-16 (1.1) | Chủ bán hàng nhiều màu tại quầy | Chọn màu nhanh, không bán vượt tồn màu, thẻ kho ghi đúng màu |
| US-17 (1.1) | Khách đặt 1 thùng sữa mix 5 vị theo dây | Thùng mix vị kiểm soát không vượt thùng, xuất đúng từng vị |
| US-18 (1.1) | Chủ muốn đặt giá riêng cho màu bán chạy | Sửa giá màu ở Tồn kho; POS/hóa đơn tự lấy giá màu |
| US-19 (1.1) | Chủ muốn biết vì sao tồn một màu bị lệch | Thẻ kho lọc màu + khoảng ngày, khớp sổ với tồn thực |

## 9. Kiến trúc thông tin và UX

### 9.1. Menu cửa hàng (theo bản mẫu v6.14)

1. Tổng quan: Bảng điều khiển; Thông báo &amp; việc cần làm.
2. Bán hàng: POS &amp; giỏ hàng; Báo giá; Hóa đơn; Trả hàng (gồm Đổi hàng).
3. Nhập hàng: Đơn nhập; Danh sách hàng hóa; Nhận hàng; Trả NCC.
4. Kho: Tồn kho (gộp tồn theo màu); Giữ hàng; Kiểm kê; Lô &amp; hạn dùng; Điều chỉnh; Thẻ kho.
5. Giỏ quà: Giỏ quà &amp; combo; Lệnh đóng giỏ; Tiến độ &amp; truy xuất lô; Tháo giỏ.
6. Công nợ: Khách còn nợ; Cửa hàng còn nợ NCC; Ứng trước &amp; cọc; Đối soát; Lịch thu/trả.
7. Chiến dịch &amp; khách: Chiến dịch mùa vụ; Khách hàng (CRM).
8. Báo cáo: Nhập–xuất–tồn; Bán hàng; Lãi gộp; Tuổi nợ; Dòng tiền; Giỏ quà.
9. Danh mục: Hàng hóa; Nhà cung cấp; Bảng giá; Nhóm mix vị.
10. Thiết lập: Tài khoản &amp; bảo mật; Thuê bao; Nhật ký.

Đã bỏ khỏi menu: Đề nghị nhập, Giao nhận, Chuyển kho, Tồn theo màu (gộp), Tiền, Đối tác, Tích hợp, Import/Export. Link cũ của các trang đã bỏ tự chuyển sang trang thay thế gần nhất.

### 9.2. Mẫu màn hình nghiệp vụ

- Danh sách: tìm kiếm, bộ lọc, cột tùy biến, trạng thái, tổng có quyền, export và thao tác theo quyền.
- Chi tiết: thông tin chung, dòng hàng (kèm màu), phiếu xuất kho, thanh toán/phân bổ, attachment, lịch sử và liên kết.
- Tạo phiếu: ưu tiên tìm mã/tên/quét qua ô tìm &amp; chọn (CAT-08); thêm nhanh hàng hóa/NCC ngay trong form; lưu nháp; tính toán rõ; preview tác động trước ghi sổ.
- Dòng hàng có màu: ô màu ngay dưới tên hàng; ô chọn khi bán chỉ hiện màu còn hàng kèm số tồn; hiển thị màu trên mọi chứng từ bằng cùng một kiểu ô màu.
- Bảng có dòng con (tồn theo màu): dòng chính tóm tắt trên một dòng, mở/thu chi tiết bằng nút ▸/▾.
- Chốt/đảo/điều chỉnh: xác nhận phạm vi, lý do, ảnh hưởng kho–nợ–tiền; ngoại lệ do chủ cửa hàng tích xác nhận.
- Dashboard: số thực, cam kết, dự báo tách rõ; có thời điểm dữ liệu và drill-down.
- Mobile: ưu tiên kiểm hàng, bán tại quầy, thu tiền, xem công nợ; không ép form desktop thu nhỏ.
- Hoạt động dài: xuất CSV, tính giá vốn và báo cáo có progress, lỗi và khả năng thử lại.

### 9.3. Quy tắc trải nghiệm

- Tiếng Việt dễ hiểu; ghi rõ “Khách còn nợ”, “Cửa hàng còn nợ NCC”, “Tiền khách ứng trước”.
- Không chỉ dùng màu phân biệt trạng thái; có chữ/icon.
- Có phím tắt POS và hỗ trợ máy quét dạng keyboard wedge.
- Tránh nhấn hai lần ghi trùng; hiển thị đang xử lý và kết quả có mã chứng từ.
- Không hiển thị thành công trước khi server xác nhận ghi sổ.
- Khi một ô nhập rời focus làm vẽ lại màn hình đúng lúc người dùng bấm nút, cú bấm không được mất (đã gặp ở POS bản mẫu).
- Thao tác chính dùng được ở màn hình 1440px không phải kéo ngang; 1280px chấp nhận kéo ngang nhẹ ở bảng nhiều cột.
- Autosave nháp không đồng nghĩa đã xác nhận đơn.
- Khi hết quyền/module: giải thích, giữ dữ liệu và cho thao tác hợp lệ như tra cứu/export theo gói.
- Bản offline có banner, thời gian dữ liệu cache và danh sách chờ đồng bộ.

## 10. Mô hình dữ liệu mức nghiệp vụ

Đây là mô hình khái niệm, chưa phải DDL hay quyết định framework.

| Nhóm | Thực thể chính | Quan hệ/bất biến |
|---|---|---|
| Tổ chức | Tenant, Store (1), Warehouse (1), User (owner), Session | Tất cả dữ liệu nghiệp vụ thuộc tenant; giữ warehouse\_id để mở rộng |
| Danh mục | Product, SKU, UOM, SKUConversionVersion, Barcode, Category, \*\*Color (7 giá trị cố định)\*\*, \*\*MixGroup (skus, pack, packF, part, partF, status)\*\* | SKU có đơn vị cơ sở và mode tracking |
| Đối tác | Party, CustomerProfile, SupplierProfile (status Đang/Ngừng giao dịch), Contact, Address | Một party có hai profile nhưng hai sổ nợ |
| Giá | PriceList, PriceRule, \*\*ColorPrice (sku, color, price, whole)\*\*, PriceLog (có color), Promotion, Quote, QuoteVersion | Snapshot giá trên chứng từ |
| Mua | PurchaseOrder, Receipt, ReceiptLine (color, spec), SupplierDocument, PurchaseReturn (line idx, color) | Đơn nhập 1:n nhận; receipt có movement/obligation |
| Bán | SalesInvoice (Hóa đơn), InvoiceLine (color, mix key), StockOut (Phiếu xuất kho), SalesDocument (lines có color, taken lots, combo comps có color), SalesReturn (lines có color), \*\*Exchange (sdoc, ret, sale, retAmt, newAmt, credit, cash)\*\*, \*\*Cart (lines có color/compColors, status)\*\* | Doanh thu theo phần đã xuất kho |
| Kho | \*\*Lot (color)\*\*, \*\*StockMovement (color)\*\*, StockBalance, Reservation (theo SKU), Count (lines có color), Adjustment (color) | Balance là projection rebuild được từ sổ; bất biến theo màu |
| Giỏ | KitRecipeVersion (comps có color/mix), AssemblyOrder, AssemblyPosting, LotGenealogy (used có color) | Lần đóng snapshot tiêu hao, giá trị và màu |
| Nợ | Obligation, ObligationSchedule, DebtEntry, PaymentAllocation, Advance, CreditNote | Dư theo ledger và phân bổ, không là ô nhập tay |
| Tiền | FundAccount (2), CashEntry, Shift (tự mở) | Không có UI quản lý quỹ riêng |
| Mùa/CRM | Campaign, CampaignAttribution, CustomerActivity, Consent | Một attribution chính trên dòng tài chính |
| Kiểm soát | ExceptionConfirmation, AuditEvent, PeriodLock, DocumentVersion, Attachment | Log append-only |
| SaaS | Plan, Subscription, Entitlement, SaaSInvoice, SupportTicket, SupportSession | Billing SaaS cách ly tài chính cửa hàng |

Đã bỏ khỏi mô hình: Branch/Location nhiều điểm, Role/Permission/Approval nhiều vai trò, Transfer, Shipment/Package/Route/DeliveryAttempt/ProofOfDelivery, CollectorReceivable/CODSettlement, FundTransfer/BankTransaction, Expense/ExpenseAllocation (chờ OPEN-13), APIKey/Webhook/ExternalMapping/ImportJob.

Trường chung quan trọng: tenant\_id, document\_id/line\_id, color (trên dòng hàng, lô, chuyển động), status, business\_date, posted\_at, created\_by, version, idempotency\_key, source và reason.

Không có foreign key/reference cho phép nối dữ liệu hai tenant. Bản đầu chỉ vận hành VND.

## 11. Kiến trúc định hướng và ranh giới kỹ thuật

### 11.1. Định hướng đề xuất

- Web responsive/PWA cho cửa hàng; admin nền tảng là ứng dụng/phạm vi quyền riêng.
- Backend modular monolith phù hợp giai đoạn sản phẩm đầu; chưa cần microservice.
- Nếu theo stack của người yêu cầu: Go, domain/application/infrastructure, repository method rõ ràng, transaction cho ghi sổ; SQL database có hỗ trợ transaction.
- Worker cho thông báo, báo cáo lớn, xuất CSV và tính lại giá vốn.
- Object storage cho file; signed URL và chính sách retention.
- Tenant isolation có kiểm tra ứng dụng và biện pháp DB phù hợp; triển khai kỹ thuật phải được review bảo mật.

### 11.2. Ranh giới domain đề xuất

Organization &amp; Access; Catalog &amp; Pricing (gồm màu, giá theo màu, nhóm mix); Procurement; Inventory (sở hữu màu trên lô/chuyển động); Sales (hóa đơn, POS, giỏ hàng, trả/đổi hàng); Kits; Receivables &amp; Payables; Cash (nền, không UI riêng); Campaign &amp; CRM; Reporting; SaaS Billing.

Inventory sở hữu movement và giá vốn; Debt sở hữu nghĩa vụ/phân bổ; Cash sở hữu dòng tiền. Một application service điều phối ghi sổ nguyên tử; module không cập nhật trực tiếp bảng của module khác bằng thao tác tùy ý.

### 11.3. POS offline

- Cache danh mục/giá và chính sách có version, tenant và thời hạn; lưu tối thiểu dữ liệu nhạy cảm.
- Chỉ cho giao dịch POS thu tiền ngay trong phạm vi đã cho phép. Không tạo bán chịu, hoàn tiền, điều chỉnh kho hay xác nhận vượt hạn mức offline.
- Giới hạn rủi ro lượng bán offline theo thiết bị/ca; tồn cache không được bảo đảm.
- Đơn offline có local ID và idempotency key; khi đồng bộ tạo chứng từ một lần.
- Nếu thiếu tồn hoặc dữ liệu thay đổi: đưa hàng đã bán vào hàng đợi ngoại lệ cần quản lý xử lý; không bỏ mất giao dịch hoặc tự bịa tồn.
- Thanh toán điện tử cần trạng thái xác nhận từ đối tác; không giả định thành công vì màn hình offline.
- Remote revoke không có hiệu lực ngay khi thiết bị mất mạng: hạn offline/cached credentials giới hạn, buộc xác thực lại khi kết nối.

## 12. Yêu cầu phi chức năng

Các con số dưới đây là \*\*mục tiêu thiết kế và kiểm thử đề xuất\*\*, không phải số đo hiện tại hay SLA đã cam kết với khách.

| ID | Yêu cầu | Mục tiêu/kiểm chứng |
|---|---|---|
| NFR-01 | Tenant isolation | Không có truy cập chéo tenant qua API, ID đoán, export, file, cache, worker hoặc support; kiểm thử tự động |
| NFR-02 | Tính nhất quán tài chính | Ghi sổ nguyên tử; idempotent; số dư khớp ledger; không mất/nhân đôi sự kiện |
| NFR-03 | Hiệu năng tác vụ thường | API tra cứu p95 ≤ 1 giây; ghi sổ p95 ≤ 2 giây trong tải chuẩn, không tính độ trễ bên thứ ba |
| NFR-04 (1.1) | Tải chuẩn tham chiếu | Tenant 20.000 SKU, 1.000.000 dòng chuyển động; một chủ cửa hàng dùng 2–3 thiết bị đồng thời (quầy, điện thoại); chạy load test trên tổng số tenant mục tiêu |
| NFR-05 | Báo cáo lớn | Báo cáo tương tác p95 ≤ 5 giây trong tải chuẩn; tác vụ lớn chạy async có progress/cancel |
| NFR-06 | Sẵn sàng | SLO đề xuất 99,9% theo tháng; cách tính downtime/bảo trì công bố trước cam kết SLA |
| NFR-07 | Sao lưu và phục hồi | Mục tiêu RPO ≤ 15 phút, RTO ≤ 4 giờ; phục hồi và đối soát thử định kỳ trước go-live |
| NFR-08 | Bảo mật | TLS, mã hóa dữ liệu lưu phù hợp, secret manager, MFA nhạy cảm, rate limit, scan dependency và penetration review |
| NFR-09 | Quan sát hệ thống | Trace/correlation ID, metric/log/alert không lộ PII; cảnh báo job treo, lệch sổ, webhook lỗi |
| NFR-10 | Trình duyệt/thiết bị | Chỉ hỗ trợ máy tính (để bàn, xách tay) và máy tính bảng; không thiết kế cho màn hình điện thoại, điện thoại chỉ dùng làm máy quét ghép đôi. Hai phiên bản chính gần nhất của trình duyệt mục tiêu; test máy tính bảng Android/iPad thực tế; danh sách máy in/máy quét được chứng nhận |
| NFR-11 | Khả dụng | Luồng chính keyboard-accessible, tương phản tốt, focus rõ; hướng đến WCAG 2.2 AA cho phần web cốt lõi |
| NFR-12 | Riêng tư | Thu thập tối thiểu, consent, retention, export/xóa có kiểm soát; rà soát nghĩa vụ Việt Nam trước thương mại |
| NFR-13 | Mở rộng | Phân trang, index tenant/time, job queue, giới hạn tài nguyên tenant và quota minh bạch |
| NFR-14 | Nâng cấp an toàn | Migration tương thích, backup trước thay đổi nhạy cảm, rollout/rollback và đối soát sau upgrade |
| NFR-15 | Dữ liệu và độ chính xác | Decimal, version, time zone, snapshot; unit/property tests cho các bất biến |
| NFR-16 | Dữ liệu analytics/AI | Không gửi chứng từ/PII ra bên thứ ba ngoài thỏa thuận; có tùy chọn tắt AI và log truy cập |

Retention chứng từ, audit, backup, dữ liệu kết thúc thuê bao và yêu cầu xóa phải có policy được rà soát; không chốt số năm khi chưa xác minh nghĩa vụ pháp lý.

## 13. Tiêu chí nghiệm thu đầu–cuối

Các kịch bản dưới đây là acceptance tests bắt buộc của full product, bổ sung cho AC từng yêu cầu. Fixture số tiền phải được tính bằng công cụ trong bộ kiểm thử, không đối chiếu bằng giá trị viết tay thiếu kiểm chứng.

### AT-01 — Nhập nhiều đợt và trả NCC

\*\*Given:\*\* Đơn nhập có SKU quy đổi thùng→chai, nhiều màu, ứng trước được xác nhận.
\*\*When:\*\* Nhận một phần, phân bổ ứng trước, nhận đợt còn lại, trả một phần lô đầu.
\*\*Then:\*\* Tồn theo đơn vị cơ sở và theo màu đúng; trả NCC trừ đúng màu dòng phiếu nhận; đơn nhập hiển thị đặt/nhận/trả; phải trả theo chứng từ và ứng trước còn dư đúng; không ghi lại nợ khi gắn hóa đơn NCC.

### AT-02 — Đặt cọc và xuất kho nhiều lần

\*\*Given:\*\* Hóa đơn có hai SKU (một SKU có màu), khách trả trước một phần.
\*\*When:\*\* Ghi nhận tiền khách trả, xuất kho một phần, xuất tiếp và thu nốt tại Công nợ.
\*\*Then:\*\* Tiền trả trước không là doanh thu; doanh thu/phải thu phát sinh theo phần xuất kho; tiền trả trước tự trừ; màu xuất đúng màu đã chọn; trạng thái thanh toán tự tính đúng.

### AT-03 — Nhiều thiết bị tranh tồn

\*\*Given:\*\* Chủ cửa hàng mở POS trên hai thiết bị và cùng bán lượng tồn cuối của một màu.
\*\*When:\*\* Request đồng thời và retry sau timeout.
\*\*Then:\*\* Không xuất vượt tồn màu đó; một kết quả hợp lệ hoặc lỗi xung đột rõ; mỗi nguồn chỉ ghi một lần.

### AT-04 — Giỏ đóng sẵn

\*\*Given:\*\* Công thức có nhiều thành phần/lô và chi phí đóng.
\*\*When:\*\* Đóng một phần (thành phần có màu cố định), thay món, bán giỏ và tháo phần còn tồn.
\*\*Then:\*\* Thành phần tiêu hao một lần; bán không trừ lại; giá vốn/genealogy đúng; tháo trả đúng lượng thực có và vật tư hỏng xử lý riêng.

### AT-05 — Combo ảo

\*\*Given:\*\* Combo ghép tại kho và có giảm giá toàn đơn.
\*\*When:\*\* Bán, xuất thành phần, trả một phần hợp lệ.
\*\*Then:\*\* Không tạo tồn combo vật lý; doanh thu không cộng trùng thành phần; giá vốn và phân bổ giảm giá truy được.

### AT-06 — Thu tiền nhiều đơn và trả hàng

\*\*Given:\*\* Khách có nhiều nghĩa vụ và một khoản tiền.
\*\*When:\*\* Phân bổ vào nhiều đơn, trả hàng một đơn rồi hoàn một phần tiền.
\*\*Then:\*\* Không phân bổ vượt dư; credit/hoàn có nguồn; quỹ chỉ giảm khi hoàn được ghi; bảng kê khớp.

### AT-07 (1.1) — Đổi hàng khác màu

\*\*Given:\*\* Khách lẻ mua 5 túi vàng và 2 hộp xanh tại quầy; một khách sỉ đang nợ hóa đơn 10 túi tím.
\*\*When:\*\* Khách lẻ đổi 2 túi vàng → đỏ và 1 hộp xanh → vàng; khách sỉ trả 3 túi tím, lấy 1 hộp xanh.
\*\*Then:\*\* Tồn: túi vàng +2, đỏ −2; hộp xanh +1, vàng −1; chênh lệch 0 ₫ với khách lẻ; với khách sỉ nợ giảm đúng giá trị trả và hàng mới thu đủ; phiếu trả + hóa đơn mới + phiếu đổi liên kết; tồn khớp sổ theo màu.

### AT-08 (1.1) — Kiểm kê theo màu, điều chỉnh và lô hết hạn

\*\*Given:\*\* Hàng có 3 màu; kiểm kê trong lúc có giao dịch; một lô hết hạn.
\*\*When:\*\* Đếm lệch một màu, xác nhận ghi sổ; điều chỉnh hỏng không chọn màu rồi chọn màu; soạn hàng FEFO.
\*\*Then:\*\* Dòng đếm tách theo màu, chỉ màu lệch được điều chỉnh; điều chỉnh không chọn màu bị chặn; không ghi đè biến động sau cutoff; không bán lô hết hạn.

### AT-09 — Đa tenant

\*\*Given:\*\* Tài khoản có membership hai tenant.
\*\*When:\*\* Gọi UI/API/export/file URL/cache/worker bằng ID tenant khác.
\*\*Then:\*\* Bị từ chối; không rò dữ liệu; audit ghi sự kiện phù hợp.

### AT-10 — Chứng từ đã ghi và kỳ khóa

\*\*Given:\*\* Phiếu nhập đã có hàng bán; kỳ cũ bị khóa.
\*\*When:\*\* Thử sửa/xóa/đảo/backdate và điều chỉnh hợp lệ kỳ hiện tại.
\*\*Then:\*\* Không xóa lịch sử; kiểm tra phụ thuộc; quyền và lý do bắt buộc; báo cáo kỳ khóa không đổi âm thầm.

### AT-11 — Mất mạng và crash giữa ghi sổ

\*\*Given:\*\* POS offline và một request online bị crash trong transaction.
\*\*When:\*\* Đồng bộ nhiều lần; server restart; request bị gửi lặp.
\*\*Then:\*\* Offline không tạo bán chịu; ngoại lệ thiếu tồn không mất giao dịch; kho–nợ–tiền online all-or-nothing; không trùng chứng từ.

### AT-12 — Cutover (chờ OPEN-14)

\*\*Given:\*\* Cửa hàng có tồn (kể cả theo màu), nợ và ứng trước đầu kỳ.
\*\*When:\*\* Nhập đầu kỳ theo cách được chốt ở OPEN-14 và đối soát cutoff.
\*\*Then:\*\* Không nhân đôi đầu kỳ; tổng được chủ cửa hàng xác nhận trước khi mở giao dịch.

### AT-13 — Kết mùa và giá vốn muộn

\*\*Given:\*\* Đơn mùa Tết, thu tiền sau mùa, chi phí mua đến muộn.
\*\*When:\*\* Kết mùa, khóa kỳ, ghi nhận adjustment theo policy.
\*\*Then:\*\* Nợ tiếp tục; dòng tiền theo ngày thu; doanh thu mùa không bị tính lại từ tiền thu; thay đổi giá vốn có phiên bản/điều chỉnh giải thích.

### AT-14 — Thuê bao và quyền dữ liệu

\*\*Given:\*\* Tenant hết hạn gói, có chứng từ và nợ còn mở.
\*\*When:\*\* Grace period hết, chủ export và yêu cầu đóng tài khoản.
\*\*Then:\*\* Read-only theo chính sách, không xóa đột ngột; export đủ dữ liệu trong thời hạn; không phát sinh thu phí tự động chưa có đồng ý.

### AT-15 — Restore và tính lại projection

\*\*Given:\*\* Backup và snapshot số dư được lưu trước sự cố.
\*\*When:\*\* Restore môi trường kiểm thử, rebuild tồn/công nợ và đối soát.
\*\*Then:\*\* Không mất liên kết/tenant; sổ và projection khớp; RPO/RTO có kết quả đo thật.

### AT-16 (1.1) — Bán hàng có màu và combo

\*\*Given:\*\* Túi quà 3 màu (đỏ có giá riêng), combo có một thành phần chọn màu tại quầy và một thành phần cố định màu.
\*\*When:\*\* Bán tại POS không chọn màu; chọn màu, tách dòng, gộp dòng trùng màu; bán combo; bán vượt tồn một màu; lưu giỏ rồi thanh toán.
\*\*Then:\*\* Không chọn màu bị chặn; giá theo màu đúng; trừ đúng màu từng thành phần; vượt tồn màu bị chặn dù tổng đủ; giỏ giữ màu và khóa sau khi bán; tồn khớp sổ theo màu.

### AT-17 (1.1) — Thùng mix vị

\*\*Given:\*\* Nhóm mix sữa 5 vị (1 thùng = 12 dây = 48 hộp) và nhóm nước ngọt 4 vị (1 thùng = 4 lốc).
\*\*When:\*\* Báo giá 1 thùng mix, chọn vị vượt số dây; đổi nhóm trên dòng; chuyển hóa đơn và xuất kho; giỏ quà có mix tính theo dây.
\*\*Then:\*\* Vượt số dây bị chặn; đổi nhóm làm mới bảng vị/đơn giá; xuất đúng số hộp từng vị; giá giỏ tính theo từng vị.

### AT-18 (1.1) — Giá theo màu và thẻ kho

\*\*Given:\*\* Sản phẩm 3 màu.
\*\*When:\*\* Đặt giá riêng màu đỏ; bán đỏ và tím; sửa giá chung “áp dụng cho tất cả màu”; mở thẻ kho lọc màu và khoảng ngày.
\*\*Then:\*\* Đỏ bán giá riêng, tím giá chung; sau khi áp dụng tất cả màu không còn giá riêng; lịch sử giá ghi đủ; thẻ kho tồn đầu + nhập − xuất = tồn cuối và khớp tồn thực của màu.

## 14. Đóng gói thương mại và vận hành khách hàng

### 14.1. Gói chức năng đề xuất

Các gói là giả thuyết thương mại, không phải cắt scope PRD hay báo giá thị trường.

| Gói | Khách phù hợp | Quyền lợi đề xuất |
|---|---|---|
| Cửa hàng | Điểm bán lẻ đơn giản | POS &amp; giỏ hàng, hàng theo màu, nhập/xuất/tồn, công nợ, báo cáo cơ bản |
| Bán sỉ | Đầu mối bán chịu | Báo giá, hóa đơn xuất kho nhiều lần, giá theo khách, hạn mức, đối soát, thùng mix vị |
| Giỏ quà | Đóng giỏ và đơn quà | Công thức, đóng/tháo giỏ, lô, màu thành phần, tiến độ |

Có thể bán theo năm hoặc theo mùa. Không chốt mức tiền khi chưa thử đề nghị trả phí với cửa hàng. Hạn mức SKU/giao dịch phải công khai; không chặn bất ngờ thao tác thiết yếu trong mùa cao điểm.

### 14.2. Onboarding đầy đủ

1. Tạo tài khoản chủ cửa hàng, tên cửa hàng, hai quỹ (tiền mặt, ngân hàng).
2. Nhập danh mục hàng hóa, đơn vị quy đổi, nhóm mix vị, nhà cung cấp, khách và giá.
3. Chốt ngày cutoff; nhập tồn (kể cả theo màu), quỹ, phải thu/phải trả và ứng trước (cách nhập theo OPEN-14).
4. Đối soát tổng và mẫu chi tiết; chủ cửa hàng xác nhận.
5. Thử trên cửa hàng mẫu (dữ liệu demo, đặt lại được).
6. Go-live, theo dõi tuần đầu, ghi issue và kiểm tra sổ.
7. Sau mùa: đối soát, báo cáo giá trị và đề nghị gia hạn phù hợp.

### 14.3. Hỗ trợ và trách nhiệm

- Khung giờ hỗ trợ mùa cao điểm phải công bố trước bán; chưa cam kết 24/7 trong PRD.
- Phân loại sự cố: mất/rò dữ liệu, sai sổ, không giao dịch được, lỗi chức năng, yêu cầu tư vấn.
- Dữ liệu người dùng nhập và chứng từ thực tế do cửa hàng xác nhận; phần mềm phải bảo đảm quy tắc, kiểm soát và truy xuất theo thiết kế.
- Nhân viên support không chỉnh số dư trực tiếp trong DB. Sửa dữ liệu phải qua quy trình có duyệt, chứng từ hoặc migration receipt.

## 15. Rủi ro và biện pháp

| Rủi ro | Hậu quả | Kiểm soát |
|---|---|---|
| Không có khác biệt đủ lớn | Khó bán, khách không chuyển | Quan sát quy trình, thử workflow và pilot trả phí trước đầu tư rộng |
| Sản phẩm chỉ hữu ích mùa Tết | Churn theo mùa | Hỗ trợ bán sỉ/công nợ quanh năm, gói theo mùa minh bạch |
| Sai tồn/nợ do migration | Mất niềm tin | Cutoff, preview, tổng kiểm soát, owner xác nhận |
| Sai quy đổi/giỏ/giá vốn | Lãi và tồn không đáng tin | Snapshot, genealogy, ledger và acceptance tests |
| Full scope quá rộng | Chậm triển khai, chất lượng thấp | Phân tích phụ thuộc và triển khai theo module; không hứa module chưa có |
| Tùy chỉnh riêng quá nhiều | Chi phí phục vụ cao | Config chuẩn, adapter, giới hạn customization |
| Mạng yếu và thiết bị chung | Bán trùng, lộ dữ liệu | Offline có giới hạn, phiên ngắn, kiểm soát thiết bị |
| Gian lận sửa/xóa phiếu | Thất thoát | Audit, khóa kỳ, không xóa chứng từ đã ghi |
| Rò dữ liệu tenant | Thiệt hại nghiêm trọng | Isolation, signed URL, test bảo mật, support consent |
| Nghĩa vụ thuế/riêng tư thay đổi | Rủi ro pháp lý | Rà soát chuyên môn; adapter HĐĐT; không quảng cáo tuân thủ khi chưa kiểm chứng |
| AI trả lời sai | Quyết định kinh doanh sai | Read-only, dẫn chứng, tắt được, không tự ghi sổ |
| (1.1) Không ghi được chi phí vận hành | Lãi gộp/dòng tiền trông tốt hơn thực tế | Ghi rõ trên báo cáo; quyết định OPEN-13 trước go-live |
| (1.1) Tên “Hóa đơn” bị hiểu là hóa đơn điện tử | Khách nghĩ đã xuất HĐĐT hợp lệ | Nhãn “chứng từ nội bộ, không phải HĐĐT” trên màn hình và bản in |
| (1.1) Một tài khoản, không tách quyền | Không kiểm soát được nếu sau này có nhân viên | Nhật ký đầy đủ; dữ liệu vẫn lưu actor; mở lại vai trò là quyết định sản phẩm riêng |
| (1.1) Một kho | Khách có 2 điểm bán không dùng được | Giữ warehouse\_id trong mô hình; ghi rõ giới hạn khi bán |
| (1.1) Giữ hàng không theo màu | Hóa đơn đã xác nhận có thể thiếu màu lúc xuất | Báo lỗi rõ khi xuất; cân nhắc OPEN-17 |
| (1.1) Bỏ import/export | Onboarding chậm, khách lo bị khóa dữ liệu | Quyết định OPEN-14, OPEN-15 trước bán |

## 16. Phụ thuộc và thứ tự triển khai full product

Không đặt tên “MVP”. Tất cả nhóm vẫn nằm trong đích sản phẩm đầy đủ; đây là thứ tự giảm rủi ro và bảo đảm phụ thuộc.

| Chặng | Phạm vi | Điều kiện hoàn tất |
|---|---|---|
| A — Nền tảng và sổ | Tenant, tài khoản chủ, danh mục, đơn vị, \*\*màu trên lô/chuyển động\*\*, ledger, transaction, audit, kỳ | Chống chéo tenant; bất biến tồn/nợ/tiền và tồn theo màu đã kiểm thử |
| B — Hàng và tiền đầu–cuối | Nhập hàng, báo giá, hóa đơn + xuất kho một bước, POS + giỏ hàng, tồn kho gộp màu, thẻ kho, công nợ, trả hàng, đổi hàng | AT-01, AT-02, AT-06, AT-07, AT-16, AT-18 pass |
| C — Vận hành chuyên sâu | Giữ hàng, lô/hạn, kiểm kê theo màu, giỏ/combo, thùng mix vị, giá theo màu, khuyến mại | AT-03, AT-04, AT-05, AT-08, AT-17 pass |
| D — Quản trị kinh doanh | Chiến dịch, CRM, dashboard thu–chi–công nợ, báo cáo, thông báo | Báo cáo khớp ledger |
| E — Thương mại hóa | Subscription, onboarding, support, offline | Pilot thật, restore, load/security test và vận hành hỗ trợ đạt gate |
| F — Tối ưu và phân tích | Forecast, report nâng cao, AI read-only | Giá trị được đo và dữ liệu ra bên ngoài có kiểm soát |

Không gắn deadline hoặc chi phí phát triển khi chưa có đội ngũ, stack chốt và ước lượng effort. Bản mẫu tương tác v6.14 là căn cứ UX, không phải mã nguồn sản phẩm.

## 17. Release gates và Definition of Done

Một module được coi là hoàn thành khi:

- Yêu cầu và AC có mã, thiết kế UX và API/domain tương ứng.
- Unit/integration/end-to-end tests bao phủ luồng thành công, lỗi, retry, concurrency và quyền.
- Số dư tồn/nợ/tiền đối soát được với ledger; có migration và recovery test.
- Xuất CSV, audit, phân trang và lỗi rõ ràng đã kiểm thử.
- Không còn lỗi severity cao về tenant isolation, mất dữ liệu hoặc ghi sổ tài chính.
- Có hướng dẫn người dùng, checklist support và metric/alert cần thiết.
- Owner pilot xác nhận dữ liệu và luồng thực tế; việc trả phí là bằng chứng thương mại riêng, không thay test kỹ thuật.

Trước phát hành thương mại toàn bộ:

1. Discovery xác nhận phân khúc, luồng quan trọng và giá trị trả phí.
2. Tất cả AC/AT trong phạm vi phát hành pass với evidence lưu được.
3. Load test có dữ liệu chuẩn, soak test mùa cao điểm, backup/restore và security review.
4. Điều khoản, retention, privacy, billing, SLA/support được rà soát và công bố.
5. Có kế hoạch rollback, xử lý lệch sổ, sự cố và thông báo khách hàng.
6. Không quảng cáo module chưa hoạt động; ghi rõ giới hạn một tài khoản, một kho, không giao nhận.

## 18. Câu hỏi cần chốt qua discovery

| ID | Câu hỏi | Cách thu thập/ra quyết định |
|---|---|---|
| OPEN-01 | Bán sỉ, bán lẻ hay giỏ quà là phân khúc đầu tiên? | Phỏng vấn và quan sát nhóm cửa hàng có thể tiếp cận; so sánh lỗi, tần suất và trả phí |
| OPEN-02 | Khách đang dùng gì và vì sao chưa hài lòng? | Xem quy trình thực tế, không chỉ hỏi tính năng mong muốn |
| OPEN-03 | Thời điểm ghi nghĩa vụ được họ dùng và hợp đồng yêu cầu? | Xem mẫu chứng từ, trao đổi kế toán; quyết định mode mục 7.1 |
| OPEN-04 | Lô/hạn và quy đổi phức tạp đến đâu? | Thu mẫu SKU, packing và quy trình kiểm hàng |
| OPEN-05 | Giỏ đóng sẵn hay ghép lúc bán? | Quan sát đóng giỏ, truy lô và xử lý tháo/trả |
| OPEN-06 | Có cần offline thực sự không? | Đo tình trạng mạng, thử quy trình dự phòng và rủi ro đồng bộ |
| OPEN-07 | Quy mô dữ liệu, số thiết bị dùng đồng thời? | Thu dữ liệu đã ẩn danh; điều chỉnh NFR tải chuẩn |
| OPEN-08 | Đối tác tích hợp nào cần đầu tiên? | Xin API docs, sandbox, điều khoản và phí từ đúng đối tác |
| OPEN-09 | Giá/gói và phí onboarding nào được chấp nhận? | Đề nghị pilot trả phí có phạm vi; không chỉ khảo sát mức giá giả định |
| OPEN-10 | Cần hỗ trợ mùa cao điểm ra sao? | Thỏa thuận khung giờ, SLA và đo chi phí phục vụ |
| OPEN-11 | Nghĩa vụ hóa đơn, privacy, retention cụ thể? | Rà soát với chuyên gia pháp lý/kế toán cho mô hình thương mại thực tế |
| OPEN-12 | Khách cho phép AI đọc dữ liệu không? | Consent cụ thể theo nhà cung cấp và loại dữ liệu; mặc định không chia sẻ |
| OPEN-13 (1.1) | Ghi chi phí vận hành (thuê mặt bằng, quảng cáo, lương thời vụ) và đối chiếu tiền cuối ngày đặt ở đâu sau khi bỏ menu Tiền? | Đề xuất: mục “Chi phí” trong Công nợ hoặc Báo cáo; chủ sản phẩm quyết định |
| OPEN-14 (1.1) | Nhập dữ liệu đầu kỳ (tồn theo màu, nợ, ứng trước) bằng cách nào khi không còn Import? | Nhập tay theo wizard, hoặc import chỉ trong onboarding, hoặc support nhập hộ |
| OPEN-15 (1.1) | Xuất dữ liệu khi ngừng thuê (nguyên tắc 8, SAAS-03) thực hiện thế nào? | Xuất CSV từ báo cáo có đủ không, hay cần gói export khi đóng tài khoản |
| OPEN-16 (1.1) | Có giữ tên “Hóa đơn” hay đổi “Đơn bán / Phiếu bán” để tránh nhầm HĐĐT? | Hỏi 3–5 chủ cửa hàng thực tế |
| OPEN-17 (1.1) | Có cần giữ hàng theo màu ngay khi xác nhận hóa đơn? | Đo tần suất thiếu màu lúc xuất trong pilot |
| OPEN-18 (1.1) | Đổi hàng với khách đang nợ: thu đủ hàng mới (hiện tại) hay ghi nợ phần hàng mới? | Hỏi chủ cửa hàng bán sỉ |
| OPEN-19 (1.1) | Giá tối thiểu có cần đặt theo màu? | Chỉ làm khi có màu đặt giá riêng thấp hơn đáng kể |
| OPEN-20 (1.1) | Báo cáo Nhập–xuất–tồn có cần tách theo màu? | Theo nhu cầu kiểm kê/đối soát với NCC |
| OPEN-21 (1.1) | Bảng màu có cần thêm xanh lá, hồng, bạc, vàng kim…? | Thu danh sách màu từ hàng thực tế của cửa hàng pilot |
| OPEN-22 (1.1) | Cột chiết khấu ở báo giá có giữ không? | Hỏi chủ cửa hàng; hiện vẫn giữ |

## 19. Cơ sở tham khảo và giới hạn bằng chứng

Tài liệu được soạn từ yêu cầu người dùng và phân tích sản phẩm. Tham khảo tài liệu chính thức đã đọc trong cuộc trao đổi:

- KiotViet — nhập hàng cập nhật tồn và theo dõi công nợ NCC: https://www.kiotviet.vn/huong-dan-su-dung-kiotviet/retail-lam-quen-voi-kiotviet/buoc-2-nhap-hang/
- Sapo — công nợ NCC ghi khi nhập kho, giảm khi thanh toán/phiếu chi: https://support.sapo.vn/cong-no-nha-cung-cap
- Nhanh.vn — sản phẩm combo, tồn theo thành phần và phiếu xuất khi bán: https://pos.manual.nhanh.vn/pos/san-pham/khainiem-sp/loai-san-pham/san-pham-combo

Nguồn là mô tả first-party về tính năng, không phải đánh giá độc lập, thử nghiệm sản phẩm hay bằng chứng nhu cầu trả phí. Không dùng chúng để khẳng định giải pháp này đã có lợi thế cạnh tranh hoặc đối thủ thiếu chức năng. Giá, thị phần, review người dùng và quy định pháp lý chưa được xác minh trong PRD.

## 20. Checklist bàn giao PRD

- \[ \] Chọn phân khúc và kiểm chứng luồng thật.
- \[ \] Chốt chính sách ghi nhận, giá vốn, hạn mức, offline và khóa kỳ.
- \[ \] Chốt OPEN-13..22 phát sinh từ bản 1.1.
- \[ \] Tách từng mã yêu cầu thành epic/story và test case liên kết.
- \[x\] Prototype tương tác cho báo giá, hóa đơn, POS &amp; giỏ hàng, nhập hàng, tồn kho theo màu, thẻ kho, giỏ quà, trả/đổi hàng, công nợ (bản mẫu v6.14).
- \[ \] Review mô hình ledger và giao dịch với kỹ sư/kế toán nghiệp vụ.
- \[ \] Ước lượng effort/cost sau khi phụ thuộc và acceptance rõ.
- \[ \] Rà soát thương mại, bảo mật và nghĩa vụ pháp lý trước go-live.

\*\*Kết luận:\*\* Từ bản 1.1, sản phẩm tập trung vào cửa hàng nhỏ do chủ tự vận hành: một tài khoản, một kho, không giao nhận. Giá trị khác biệt nằm ở sổ liên kết hàng–hóa đơn–tiền \*\*có theo dõi màu sắc và thùng mix vị xuyên suốt\*\*, cộng lớp vận hành SaaS để bán và hỗ trợ nhiều cửa hàng. Các khoảng trống do thu hẹp phạm vi (chi phí, nhập đầu kỳ, xuất dữ liệu) phải được chốt trước go-live.