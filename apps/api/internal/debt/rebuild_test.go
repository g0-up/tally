package debt

import "testing"

// Every repair rewrites the rows of a verify check, so its name must be one Verify runs.
func TestRebuildChecksAreVerifyChecks(t *testing.T) {
	if len(RebuildChecks()) == 0 {
		t.Fatal("no rebuild checks")
	}
	for _, name := range RebuildChecks() {
		if _, err := checkQuery(name); err != nil {
			t.Error(err)
		}
	}
	if _, err := checkQuery("no such check"); err == nil {
		t.Error("an unknown check resolved")
	}
}
