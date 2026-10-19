package inventory

import "testing"

// Every repair rewrites the rows of a verify check, so its name must be one Verify runs; the
// rebuild reads each check over every SKU, so no SKU restriction mark survives.
func TestRebuildChecksAreVerifyChecks(t *testing.T) {
	for _, name := range RebuildChecks() {
		q, err := checkQuery(name)
		if err != nil {
			t.Error(err)
			continue
		}
		if skuFilterRe.MatchString(q) {
			t.Errorf("check %q keeps a SKU restriction mark", name)
		}
	}
	if _, err := checkQuery("no such check"); err == nil {
		t.Error("an unknown check resolved")
	}
}
