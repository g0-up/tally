package textnorm

import "testing"

func TestKey(t *testing.T) {
	cases := map[string]string{
		"Đường Phèn":             "duong phen",
		"  Đường   Phèn  ":       "duong phen",
		"ĐẬU XANH":               "dau xanh",
		"Bánh\tquy\nbơ":          "banh quy bo",
		"Mứt gừng – hộp 500g":    "mut gung – hop 500g",
		"Trà Ô Long":             "tra o long",
		"Nước mắm Phú Quốc":      "nuoc mam phu quoc",
		"Kẹo dẻo ổi, ướp lạnh":   "keo deo oi, uop lanh",
		"":                       "",
		"   ":                    "",
		"Café Crème Brûlée":      "cafe creme brulee",
		"Hạt điều rang muối 1kg": "hat dieu rang muoi 1kg",
	}
	for in, want := range cases {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeyIsIdempotentAndCaseInsensitive(t *testing.T) {
	for _, s := range []string{"Đường Phèn", "đường phèn", "DUONG PHEN", "Duong   Phen"} {
		if Key(s) != "duong phen" || Key(Key(s)) != Key(s) {
			t.Errorf("Key(%q) = %q", s, Key(s))
		}
	}
}

func TestSearchNorm(t *testing.T) {
	cases := map[string]string{
		"Bánh-quy (hộp)":    "banh quy hop",
		"Đường Phèn, 500g!": "duong phen 500g",
		"SKU#A-01/Đỏ":       "sku a 01 do",
		"":                  "",
		"--":                "",
	}
	for in, want := range cases {
		if got := SearchNorm(in); got != want {
			t.Errorf("SearchNorm(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\t Owner@Example.TEST ", "owner@example.test"},
		{"a@b.c", "a@b.c"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := EmailKey(tc.in); got != tc.want {
			t.Errorf("EmailKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPhoneKey(t *testing.T) {
	cases := map[string]string{
		"0912 418 226":      "0912418226",
		"+84 912-418-226":   "0912418226",
		"+84 (0) 912418226": "0912418226",
		"0084912418226":     "0912418226",
		"84912418226":       "0912418226",
		"024 3795 6612":     "02437956612",
		"(024) 3863.2299":   "02438632299",
		"  ":                "",
		"không có":          "",
		"8491241822":        "8491241822",
		"+1 202 555 0100":   "12025550100",
	}
	for in, want := range cases {
		if got := PhoneKey(in); got != want {
			t.Errorf("PhoneKey(%q) = %q, want %q", in, got, want)
		}
	}
}
