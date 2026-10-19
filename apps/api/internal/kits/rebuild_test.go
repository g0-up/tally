package kits

import (
	"slices"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Every repair rewrites the rows of a verify check, so its name must be one Verify runs: a check
// query or a status check.
func TestRebuildChecksAreVerifyChecks(t *testing.T) {
	if len(RebuildChecks()) == 0 {
		t.Fatal("no rebuild checks")
	}
	statuses := posting.StatusCheckNames(statusChecks)
	if len(statuses) == 0 {
		t.Error("no status check")
	}
	for _, name := range RebuildChecks() {
		if slices.Contains(statuses, name) {
			continue
		}
		if _, err := checkQuery(name); err != nil {
			t.Error(err)
		}
	}
	if _, err := checkQuery("no such check"); err == nil {
		t.Error("an unknown check resolved")
	}
}
