package posting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLockTablesCoverEveryLockKind(t *testing.T) {
	for _, kind := range lockOrder {
		if _, ok := lockTables[kind]; !ok {
			t.Errorf("lock kind %q has no table, so LockTenant cannot lock it", kind)
		}
	}
	if len(lockTables) != len(lockOrder) {
		t.Errorf("lockTables names %d kinds, lockOrder %d", len(lockTables), len(lockOrder))
	}
}

func TestRunRepairsReportsUnknownChecks(t *testing.T) {
	want := errors.New("no such check")
	_, err := RunRepairs(context.Background(), nil, func(string) (string, error) { return "", want },
		[]Repair{{Check: "missing", Stmt: "UPDATE x SET y = 1"}}, time.Now())
	if !errors.Is(err, want) {
		t.Fatalf("RunRepairs = %v, want the check lookup error", err)
	}
	if got := strings.Join(Checks([]Repair{{Check: "a"}, {Check: "b"}}), ","); got != "a,b" {
		t.Errorf("Checks = %q", got)
	}
}
