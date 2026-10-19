package posting

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// countingDocs numbers every prefix from 1 and records the order numbers were issued in.
type countingDocs struct {
	next  map[string]int
	order []string
}

func (d *countingDocs) Next(_ context.Context, _ db.Querier, _ uuid.UUID, prefix string) (string, error) {
	if d.next == nil {
		d.next = map[string]int{}
	}
	d.next[prefix]++
	no := fmt.Sprintf("%s-%d", prefix, d.next[prefix])
	d.order = append(d.order, no)
	return no, nil
}

// extraDocsCommand is a testCommand that writes further numbered documents.
type extraDocsCommand struct {
	testCommand
	extras []string
}

func (c extraDocsCommand) ExtraDocPrefixes() []string { return c.extras }

func TestPostIssuesExtraDocNumbersBeforeTheRowLocks(t *testing.T) {
	h := newHarness()
	docs := &countingDocs{}
	h.deps.Docs = docs
	c := h.coordinator(t)
	issuedAtLock := -1
	c.RegisterLocker(LockObligations, func(context.Context, db.Querier, []uuid.UUID) error {
		issuedAtLock = len(docs.order)
		return nil
	})
	var locks LockSet
	locks.Add(LockObligations, idA)
	var taken []string
	cmd := extraDocsCommand{extras: []string{"PT", "PC", "PT"}, testCommand: testCommand{scope: "stock_out.post", prefix: "PX", locks: locks,
		apply: func(_ context.Context, tx Tx) (Result, error) {
			for _, p := range []string{"PT", "PC", "PT"} {
				no, err := TakeDocNo(tx, p)
				if err != nil {
					return Result{}, err
				}
				taken = append(taken, no)
			}
			if _, err := TakeDocNo(tx, "PT"); err == nil {
				t.Error("a third PT was taken from two reserved")
			}
			return Result{Body: map[string]string{"doc_no": tx.DocNo()}}, nil
		}}}
	resp, err := c.Post(context.Background(), owner, key, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if resp.DocNo != "PX-1" {
		t.Errorf("own number = %q", resp.DocNo)
	}
	if want := []string{"PC-1", "PT-1", "PT-2", "PX-1"}; !slices.Equal(docs.order, want) {
		t.Errorf("issue order = %v, want prefixes ascending %v", docs.order, want)
	}
	if issuedAtLock != 4 {
		t.Errorf("%d numbers issued when the rows were locked, want all 4 before", issuedAtLock)
	}
	if want := []string{"PT-1", "PC-1", "PT-2"}; !slices.Equal(taken, want) {
		t.Errorf("taken = %v, want %v", taken, want)
	}
}

func TestPostExtraDocOfTheCommandsOwnPrefixComesAfterItsNumber(t *testing.T) {
	h := newHarness()
	h.deps.Docs = &countingDocs{}
	var extra string
	resp, err := h.coordinator(t).Post(context.Background(), owner, key, extraDocsCommand{extras: []string{"PT"},
		testCommand: testCommand{scope: "payment.post", prefix: "PT", apply: func(_ context.Context, tx Tx) (Result, error) {
			var err error
			extra, err = TakeDocNo(tx, "PT")
			return Result{}, err
		}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.DocNo != "PT-1" || extra != "PT-2" {
		t.Errorf("own %q extra %q, want PT-1 and PT-2", resp.DocNo, extra)
	}
}

func TestPostFailsWhenAReservedNumberIsNotTaken(t *testing.T) {
	h := newHarness()
	h.deps.Docs = &countingDocs{}
	_, err := h.coordinator(t).Post(context.Background(), owner, key, extraDocsCommand{extras: []string{"PT", "PC"},
		testCommand: testCommand{scope: "sale.post", apply: func(_ context.Context, tx Tx) (Result, error) {
			_, err := TakeDocNo(tx, "PC")
			return Result{}, err
		}}})
	if err == nil || !strings.Contains(err.Error(), "did not take the reserved document numbers [PT-1]") {
		t.Fatalf("err = %v, want the untaken PT reported", err)
	}
	if len(h.idem.completed) != 0 {
		t.Error("a posting that left a number untaken stored its response")
	}
}

func TestTakeDocNoWithoutReservation(t *testing.T) {
	h := newHarness()
	_, err := h.coordinator(t).Post(context.Background(), owner, key, testCommand{scope: "sale.post", prefix: "BH",
		apply: func(_ context.Context, tx Tx) (Result, error) {
			_, err := TakeDocNo(tx, "PT")
			return Result{}, err
		}})
	if err == nil || !strings.Contains(err.Error(), "list it in the command's ExtraDocPrefixes") {
		t.Fatalf("err = %v", err)
	}
	if _, err := h.coordinator(t).Post(context.Background(), owner, key, extraDocsCommand{extras: []string{""},
		testCommand: testCommand{scope: "sale.post"}}); err == nil || !strings.Contains(err.Error(), "empty extra document prefix") {
		t.Fatalf("empty prefix: %v", err)
	}
}
