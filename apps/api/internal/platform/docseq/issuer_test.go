package docseq

import "testing"

func TestFormat(t *testing.T) {
	cases := map[string]string{
		Format("PN", 1):      "PN-0001",
		Format("HD", 42):     "HD-0042",
		Format("BH", 9999):   "BH-9999",
		Format("BHX", 10000): "BHX-10000",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestValidPrefix(t *testing.T) {
	for p, want := range map[string]bool{"PN": true, "BHX": true, "P": false, "PNXX": false, "pn": false, "P1": false, "": false} {
		if got := ValidPrefix(p); got != want {
			t.Errorf("ValidPrefix(%q) = %v", p, got)
		}
	}
}
