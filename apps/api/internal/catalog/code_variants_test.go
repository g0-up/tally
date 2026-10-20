package catalog

import (
	"slices"
	"testing"
)

func TestCodeVariantsCoverGTINPaddingAndUPCE(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		want    []string
		without []string
	}{
		{name: "UPC-A pads to 13 and 14 digits", code: "036000291452",
			want: []string{"036000291452", "0036000291452", "00036000291452"}},
		{name: "a 13 digit form strips back to the UPC-A", code: "0036000291452",
			want: []string{"0036000291452", "036000291452", "00036000291452"}},
		{name: "an EAN-13 has no 12 digit form", code: "4006381333931",
			want: []string{"4006381333931", "04006381333931"}, without: []string{"006381333931"}},
		{name: "a UPC-E expands to its UPC-A", code: "01234565",
			want: []string{"01234565", "012345000065", "0012345000065", "00012345000065"}},
		{name: "a UPC-A compresses to its UPC-E", code: "0012345000065",
			want: []string{"0012345000065", "012345000065", "00012345000065", "01234565"}},
		{name: "a UPC-E with a wrong check digit stays alone", code: "01234566", want: []string{"01234566"}},
		{name: "an 8 digit code with number system 2 stays alone", code: "21234565", want: []string{"21234565"}},
		{name: "letters stay as typed", code: "SP001", want: []string{"SP001"}},
		{name: "a mixed code stays as typed", code: "0036000291452A", want: []string{"0036000291452A"}},
		{name: "other digit lengths stay as typed", code: "123456", want: []string{"123456"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CodeVariants(tc.code)
			if got[0] != tc.code {
				t.Fatalf("CodeVariants(%q)[0] = %q, want the code as typed first", tc.code, got[0])
			}
			for _, w := range tc.want {
				if !slices.Contains(got, w) {
					t.Errorf("CodeVariants(%q) = %v, missing %q", tc.code, got, w)
				}
			}
			if len(got) != len(tc.want) {
				t.Errorf("CodeVariants(%q) = %v, want exactly %v", tc.code, got, tc.want)
			}
			for _, w := range tc.without {
				if slices.Contains(got, w) {
					t.Errorf("CodeVariants(%q) = %v, must not contain %q", tc.code, got, w)
				}
			}
			for _, v := range got {
				if len(v) > 14 && v != tc.code {
					t.Errorf("CodeVariants(%q) has %q, longer than 14 digits", tc.code, v)
				}
			}
		})
	}
}

func TestCodeVariantsAreSymmetricAndShareTheCanonicalCode(t *testing.T) {
	for _, code := range []string{"036000291452", "4006381333931", "01234565", "012000003455", "01234543", "00000000000000"} {
		canon := canonicalCode(code)
		for _, v := range CodeVariants(code) {
			if !slices.Contains(CodeVariants(v), code) {
				t.Errorf("%q is a variant of %q but not the other way round (%v)", v, code, CodeVariants(v))
			}
			if got := canonicalCode(v); got != canon {
				t.Errorf("canonicalCode(%q) = %q, want %q as for %q", v, got, canon, code)
			}
		}
	}
	if got := canonicalCode("SP001"); got != "SP001" {
		t.Errorf("canonicalCode(SP001) = %q, want the code itself", got)
	}
}

func TestUPCECompressionRulesRoundTrip(t *testing.T) {
	// One UPC-E per last-digit rule, each with its GS1 check digit.
	for upcE, upcA := range map[string]string{
		"01234505": "012000003455", // last digit 0-2: the third manufacturer digit, product keeps three digits
		"01234531": "012300000451", // last digit 3
		"01234543": "012340000053", // last digit 4
		"01234565": "012345000065", // last digit 5-9
	} {
		got, ok := expandUPCE(upcE)
		if !ok || got != upcA {
			t.Errorf("expandUPCE(%q) = %q, %v; want %q", upcE, got, ok, upcA)
		}
		back, ok := compressUPCA(upcA)
		if !ok || back != upcE {
			t.Errorf("compressUPCA(%q) = %q, %v; want %q", upcA, back, ok, upcE)
		}
	}
}
