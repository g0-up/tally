package seedload

import (
	"fmt"
	"strings"
)

// productNoun is a kind of goods a Vietnamese shop sells, with the base unit it is counted in
// (one of the units every new store has) and the sizes it comes in.
type productNoun struct {
	name  string
	unit  string
	sizes []string
	// lot marks goods that are bought in lots with an expiry date (food, drinks, care products).
	lot bool
	// colored marks goods that come in colors (textiles, plastics, stationery).
	colored bool
	// price is the typical retail price in thousands of VND.
	price int
}

var productNouns = []productNoun{
	{name: "Nước mắm", unit: "chai", sizes: []string{"500ml", "750ml", "1 lít"}, lot: true, price: 45},
	{name: "Nước tương", unit: "chai", sizes: []string{"250ml", "500ml"}, lot: true, price: 22},
	{name: "Dầu ăn", unit: "chai", sizes: []string{"1 lít", "2 lít", "5 lít"}, lot: true, price: 65},
	{name: "Gạo thơm", unit: "kg", sizes: []string{"túi 5kg", "túi 10kg"}, lot: true, price: 28},
	{name: "Mì gói", unit: "thùng", sizes: []string{"30 gói", "24 gói"}, lot: true, price: 120},
	{name: "Bánh quy bơ", unit: "hộp", sizes: []string{"300g", "500g"}, lot: true, price: 75},
	{name: "Cà phê rang xay", unit: "gói", sizes: []string{"250g", "500g"}, lot: true, price: 95},
	{name: "Trà xanh", unit: "hộp", sizes: []string{"25 túi lọc", "100g"}, lot: true, price: 40},
	{name: "Sữa tươi tiệt trùng", unit: "lốc", sizes: []string{"4 hộp 180ml", "6 hộp 110ml"}, lot: true, price: 35},
	{name: "Nước suối", unit: "thùng", sizes: []string{"24 chai 500ml", "12 chai 1,5 lít"}, lot: true, price: 110},
	{name: "Bột giặt", unit: "gói", sizes: []string{"800g", "3kg", "6kg"}, lot: true, price: 90},
	{name: "Nước rửa chén", unit: "chai", sizes: []string{"400g", "750g", "1,5kg"}, lot: true, price: 38},
	{name: "Dầu gội đầu", unit: "chai", sizes: []string{"340ml", "650ml"}, lot: true, price: 135},
	{name: "Kem đánh răng", unit: "hộp", sizes: []string{"100g", "180g"}, lot: true, price: 32},
	{name: "Hạt nêm", unit: "gói", sizes: []string{"400g", "900g"}, lot: true, price: 48},
	{name: "Đường kính trắng", unit: "kg", sizes: []string{"túi 1kg", "túi 2kg"}, lot: true, price: 25},
	{name: "Khăn mặt cotton", unit: "cái", sizes: []string{"30x50cm", "35x75cm"}, colored: true, price: 35},
	{name: "Khăn tắm", unit: "cái", sizes: []string{"60x120cm", "70x140cm"}, colored: true, price: 120},
	{name: "Áo thun trơn", unit: "cái", sizes: []string{"size M", "size L", "size XL"}, colored: true, price: 150},
	{name: "Dép tổ ong", unit: "cái", sizes: []string{"size 38", "size 40", "size 42"}, colored: true, price: 45},
	{name: "Rổ nhựa", unit: "cái", sizes: []string{"cỡ nhỏ", "cỡ vừa", "cỡ lớn"}, colored: true, price: 30},
	{name: "Thau nhựa", unit: "cái", sizes: []string{"36cm", "45cm"}, colored: true, price: 55},
	{name: "Ly thủy tinh", unit: "hộp", sizes: []string{"6 cái", "12 cái"}, colored: true, price: 85},
	{name: "Bút bi", unit: "hộp", sizes: []string{"20 cây", "50 cây"}, colored: true, price: 60},
	{name: "Vở học sinh", unit: "lốc", sizes: []string{"10 cuốn 96 trang", "10 cuốn 200 trang"}, colored: true, price: 110},
	{name: "Ô dù gấp", unit: "cái", sizes: []string{"3 tầng", "tự động"}, colored: true, price: 140},
	{name: "Nồi inox", unit: "cái", sizes: []string{"18cm", "22cm", "26cm"}, price: 260},
	{name: "Chảo chống dính", unit: "cái", sizes: []string{"20cm", "24cm", "28cm"}, price: 230},
	{name: "Dao làm bếp", unit: "cái", sizes: []string{"lưỡi 20cm", "bộ 3 cây"}, price: 95},
	{name: "Thớt gỗ", unit: "cái", sizes: []string{"30x20cm", "40x28cm"}, price: 80},
	{name: "Đèn LED", unit: "cái", sizes: []string{"9W", "12W", "18W"}, price: 55},
	{name: "Ổ cắm điện", unit: "cái", sizes: []string{"3 lỗ", "6 lỗ"}, price: 120},
	{name: "Pin tiểu", unit: "dây", sizes: []string{"AA 4 viên", "AAA 4 viên"}, price: 35},
	{name: "Băng keo", unit: "cái", sizes: []string{"5cm x 100m", "2,4cm x 50m"}, price: 18},
	{name: "Chổi quét nhà", unit: "cái", sizes: []string{"cán nhựa", "cán gỗ"}, price: 50},
	{name: "Cây lau nhà", unit: "cái", sizes: []string{"xoay 360", "dẹt"}, price: 210},
	{name: "Bộ ấm chén", unit: "hộp", sizes: []string{"6 chén", "10 chén"}, price: 320},
	{name: "Hộp đựng thực phẩm", unit: "hộp", sizes: []string{"3 hộp", "5 hộp"}, price: 145},
	{name: "Kéo văn phòng", unit: "cái", sizes: []string{"19cm", "21cm"}, price: 28},
	{name: "Bình giữ nhiệt", unit: "cái", sizes: []string{"500ml", "750ml"}, price: 240},
}

var brandNames = []string{
	"Phú Quốc", "Hồng Hà", "Thiên Long", "Sài Gòn", "Hà Nội", "Bình Minh", "Hạnh Phúc", "An Khang",
	"Đại Việt", "Phước Lộc", "Kim Long", "Tân Tiến", "Hoàng Gia", "Mê Kông", "Trường Sơn", "Ngọc Lan",
	"Sen Vàng", "Đông Á", "Việt Tiến", "Hòa Bình",
}

var companyKinds = []string{"Công ty TNHH", "Công ty CP", "Cửa hàng", "Đại lý", "Nhà phân phối"}

var companyTrades = []string{
	"Thực phẩm", "Hàng tiêu dùng", "Gia dụng", "Văn phòng phẩm", "Thương mại", "Xuất nhập khẩu",
	"Điện gia dụng", "Dệt may",
}

var familyNames = []string{
	"Nguyễn", "Trần", "Lê", "Phạm", "Hoàng", "Huỳnh", "Phan", "Vũ", "Võ", "Đặng", "Bùi", "Đỗ",
	"Hồ", "Ngô", "Dương", "Lý",
}

var middleNames = []string{"Văn", "Thị", "Minh", "Ngọc", "Đức", "Thanh", "Hữu", "Quốc", "Thu", "Gia"}

var givenNames = []string{
	"An", "Bình", "Châu", "Dũng", "Giang", "Hà", "Hải", "Hạnh", "Hiếu", "Hoa", "Huy", "Khánh",
	"Lan", "Linh", "Long", "Mai", "Nam", "Nga", "Phúc", "Phương", "Quân", "Sơn", "Tâm", "Thảo",
	"Trang", "Trung", "Tuấn", "Uyên", "Việt", "Yến",
}

var streets = []string{
	"Lê Lợi", "Nguyễn Huệ", "Trần Hưng Đạo", "Hai Bà Trưng", "Lý Thường Kiệt", "Phan Đình Phùng",
	"Nguyễn Trãi", "Điện Biên Phủ", "Cách Mạng Tháng Tám", "Võ Văn Tần", "Bạch Đằng", "Quang Trung",
}

var districts = []string{
	"Quận 1, TP. Hồ Chí Minh", "Quận 3, TP. Hồ Chí Minh", "Quận Bình Thạnh, TP. Hồ Chí Minh",
	"Quận Hoàn Kiếm, Hà Nội", "Quận Đống Đa, Hà Nội", "Quận Hải Châu, Đà Nẵng", "TP. Thủ Đức, TP. Hồ Chí Minh",
	"Quận Ninh Kiều, Cần Thơ",
}

var kitThemes = []string{
	"Giỏ quà Tết", "Hộp quà Trung thu", "Combo bếp gia đình", "Bộ quà tặng khách hàng", "Combo tiết kiệm",
	"Giỏ quà sức khỏe", "Bộ đồ dùng học sinh", "Combo vệ sinh nhà cửa",
}

// personName draws a full Vietnamese name.
func personName(g *RNG) string {
	return pick(g, familyNames) + " " + pick(g, middleNames) + " " + pick(g, givenNames)
}

// companyName draws a company or shop name.
func companyName(g *RNG) string {
	return pick(g, companyKinds) + " " + pick(g, companyTrades) + " " + pick(g, brandNames)
}

// address draws a street address.
func address(g *RNG) string {
	return fmt.Sprintf("%d %s, %s", g.Between(1, 399), pick(g, streets), pick(g, districts))
}

// phone draws a Vietnamese mobile number.
func phone(g *RNG) string {
	prefixes := []string{"090", "091", "093", "097", "098", "086", "088", "070", "077", "081"}
	var b strings.Builder
	b.WriteString(pick(g, prefixes))
	for range 7 {
		b.WriteByte("0123456789"[g.Intn(10)])
	}
	return b.String()
}
