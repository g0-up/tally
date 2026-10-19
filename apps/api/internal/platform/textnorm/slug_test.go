package textnorm

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Cửa hàng Đường Phèn":    "cua-hang-duong-phen",
		"Cửa hàng A":             "cua-hang-a",
		"  Tạp hóa   Bà Tư  ":    "tap-hoa-ba-tu",
		"Bánh-quy (hộp) & Kẹo!!": "banh-quy-hop-keo",
		"Shop 247":               "shop-247",
		"---":                    "",
		"":                       "",
		"Ưu Đãi Ngọc Ánh":        "uu-dai-ngoc-anh",
		"Nhà phân phối Hưng Thịnh Phát Đạt":  "nha-phan-phoi-hung-thinh-phat",
		"abcdefghijklmnopqrstuvwxyz-0123456": "abcdefghijklmnopqrstuvwxyz-012",
		"東京":                                 "",
	}
	for in, want := range cases {
		got := Slug(in)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
		if len(got) > SlugMaxLen {
			t.Errorf("Slug(%q) is %d characters", in, len(got))
		}
	}
}
