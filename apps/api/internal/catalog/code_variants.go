package catalog

import "strings"

// CodeVariants lists the forms under which a scanner may report the same retail barcode: code
// itself first, then, for a 12 to 14 digit GTIN, its zero padded 12, 13 and 14 digit forms and
// the UPC-E it compresses to; for an 8 digit UPC-E, its UPC-A expansion and that one's 13 and 14
// digit forms. Any other code has no variant but itself. Lookups and the duplicate check of
// barcodes both go through it, so it is the only place that knows these equivalences.
func CodeVariants(code string) []string {
	out := []string{code}
	add := func(v string) {
		for _, have := range out {
			if have == v {
				return
			}
		}
		out = append(out, v)
	}
	if !allDigits(code) {
		return out
	}
	switch n := len(code); {
	case n == 8:
		if upcA, ok := expandUPCE(code); ok {
			for _, v := range gtinPads(upcA) {
				add(v)
			}
		}
	case n >= 12 && n <= 14:
		pads := gtinPads(code)
		for _, v := range pads {
			add(v)
		}
		for _, v := range pads {
			if len(v) != 12 {
				continue
			}
			// Only a UPC-E that expands back counts, so each code is a variant of its variants.
			if upcE, ok := compressUPCA(v); ok {
				if back, ok := expandUPCE(upcE); ok && back == v {
					add(upcE)
				}
			}
		}
	}
	return out
}

// canonicalCode is the key the duplicate check of a barcode locks on: the 14 digit form for a
// code with GTIN variants, the code itself otherwise. Codes that are variants of each other share it.
func canonicalCode(code string) string {
	for _, v := range CodeVariants(code) {
		if len(v) == 14 && allDigits(v) {
			return v
		}
	}
	return code
}

// gtinPads returns the 12, 13 and 14 digit forms of a digit code, leading zeros stripped then
// padded again; a form shorter than the significant digits is skipped.
func gtinPads(code string) []string {
	digits := strings.TrimLeft(code, "0")
	var out []string
	for _, n := range []int{12, 13, 14} {
		if len(digits) <= n {
			out = append(out, strings.Repeat("0", n-len(digits))+digits)
		}
	}
	return out
}

// expandUPCE returns the UPC-A a valid UPC-E stands for. A UPC-E is valid when its number system is
// 0 or 1, its check digit is the UPC-A check digit, and it is the form compressUPCA gives back,
// so expanding and compressing are each other's inverse.
func expandUPCE(code string) (string, bool) {
	if len(code) != 8 || !allDigits(code) || (code[0] != '0' && code[0] != '1') {
		return "", false
	}
	ns, d, check := code[:1], code[1:7], code[7:]
	var body string
	switch d[5] {
	case '0', '1', '2':
		body = d[0:2] + d[5:6] + "0000" + d[2:5]
	case '3':
		body = d[0:3] + "00000" + d[3:5]
	case '4':
		body = d[0:4] + "00000" + d[4:5]
	default:
		body = d[0:5] + "0000" + d[5:6]
	}
	upcA := ns + body + check
	if gtinCheckDigit(upcA[:11]) != upcA[11] {
		return "", false
	}
	if back, ok := compressUPCA(upcA); !ok || back != code {
		return "", false
	}
	return upcA, true
}

// compressUPCA returns the UPC-E form of a 12 digit UPC-A, when it has one.
func compressUPCA(code string) (string, bool) {
	if len(code) != 12 || !allDigits(code) || (code[0] != '0' && code[0] != '1') {
		return "", false
	}
	ns, m, p, check := code[:1], code[1:6], code[6:11], code[11:]
	switch {
	case m[2] <= '2' && m[3:5] == "00" && p[0:2] == "00":
		return ns + m[0:2] + p[2:5] + m[2:3] + check, true
	case m[3:5] == "00" && p[0:3] == "000":
		return ns + m[0:3] + p[3:5] + "3" + check, true
	case m[4] == '0' && p[0:4] == "0000":
		return ns + m[0:4] + p[4:5] + "4" + check, true
	case p[0:4] == "0000" && p[4] >= '5':
		return ns + m + p[4:5] + check, true
	}
	return "", false
}

// gtinCheckDigit is the GS1 check digit of the digits before it.
func gtinCheckDigit(body string) byte {
	sum := 0
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if (len(body)-1-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
