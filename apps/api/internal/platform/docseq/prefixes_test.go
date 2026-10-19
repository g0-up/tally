package docseq

import "testing"

func TestPrefixesAreValidAndUnique(t *testing.T) {
	all := Prefixes()
	if len(all) != 22 {
		t.Fatalf("prefixes = %d, want the 22 of the ubiquitous language", len(all))
	}
	seen := map[string]bool{}
	for _, p := range all {
		if !ValidPrefix(p) {
			t.Errorf("prefix %q is not valid", p)
		}
		if seen[p] {
			t.Errorf("prefix %q is listed twice", p)
		}
		seen[p] = true
	}
}
