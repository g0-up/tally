package procurement

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func orderLine(base, received string) PurchaseOrderLine {
	return PurchaseOrderLine{BaseQty: qty(base), ReceivedBaseQty: qty(received)}
}

func TestOrderStatusTransitions(t *testing.T) {
	var m PurchaseOrderStatus
	allowed := [][2]string{
		{StatusDraft, StatusConfirmed},
		{StatusConfirmed, StatusPartiallyReceived},
		{StatusConfirmed, StatusCompleted},
		{StatusConfirmed, StatusClosedShort},
		{StatusPartiallyReceived, StatusCompleted},
		{StatusPartiallyReceived, StatusClosedShort},
		{StatusPartiallyReceived, StatusConfirmed},
		{StatusCompleted, StatusPartiallyReceived},
		{StatusCompleted, StatusConfirmed},
	}
	for _, tr := range allowed {
		if err := m.Transition(tr[0], tr[1]); err != nil {
			t.Errorf("%s -> %s: %v", tr[0], tr[1], err)
		}
	}
	refused := [][2]string{
		{StatusDraft, StatusCompleted},
		{StatusDraft, StatusClosedShort},
		{StatusDraft, StatusDraft},
		{StatusCompleted, StatusClosedShort},
		{StatusClosedShort, StatusConfirmed},
		{StatusClosedShort, StatusClosedShort},
		{StatusConfirmed, StatusDraft},
	}
	for _, tr := range refused {
		_ = wantCode(t, m.Transition(tr[0], tr[1]), apperr.Conflict, CodeOrderStatus)
	}
}

func TestOrderStaysInItsReceivingStatus(t *testing.T) {
	var m PurchaseOrderStatus
	for _, s := range []string{StatusConfirmed, StatusPartiallyReceived, StatusCompleted} {
		if err := m.Transition(s, s); err != nil {
			t.Errorf("%s -> %s: %v", s, s, err)
		}
	}
}

func TestOrderReceivableOnlyWhileWaitingForGoods(t *testing.T) {
	var m PurchaseOrderStatus
	want := map[string]bool{StatusDraft: false, StatusConfirmed: true, StatusPartiallyReceived: true, StatusCompleted: false,
		StatusClosedShort: false}
	for s, ok := range want {
		if got := m.Receivable(s); got != ok {
			t.Errorf("Receivable(%s) = %v, want %v", s, got, ok)
		}
	}
}

func TestOrderProgressFollowsReceivedQuantities(t *testing.T) {
	var m PurchaseOrderStatus
	cases := []struct {
		name  string
		lines []PurchaseOrderLine
		want  string
	}{
		{"nothing received", []PurchaseOrderLine{orderLine("2400", "0"), orderLine("10", "0")}, StatusConfirmed},
		{"first batch of 60 cartons", []PurchaseOrderLine{orderLine("2400", "1440")}, StatusPartiallyReceived},
		{"second batch completes", []PurchaseOrderLine{orderLine("2400", "2400")}, StatusCompleted},
		{"excess still completes", []PurchaseOrderLine{orderLine("2400", "2424")}, StatusCompleted},
		{"one line short", []PurchaseOrderLine{orderLine("2400", "2400"), orderLine("10", "9")}, StatusPartiallyReceived},
		{"no lines", nil, StatusConfirmed},
	}
	for _, tc := range cases {
		if got := m.Progress(tc.lines); got != tc.want {
			t.Errorf("%s: Progress = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestClosedShortOrderKeepsItsStatusAfterReceipts(t *testing.T) {
	var m PurchaseOrderStatus
	lines := []PurchaseOrderLine{orderLine("2400", "0")}
	if got := m.AfterReceipts(StatusClosedShort, lines); got != StatusClosedShort {
		t.Errorf("AfterReceipts(closed_short) = %s", got)
	}
	if got := m.AfterReceipts(StatusCompleted, lines); got != StatusConfirmed {
		t.Errorf("a reversal that empties a completed order = %s, want confirmed", got)
	}
}

func TestFollowKeepsAClosedShortOrderWhenAReceiptIsReversed(t *testing.T) {
	var m PurchaseOrderStatus
	// Closing short again is refused, but a reversal on a closed-short order is no move.
	_ = wantCode(t, m.Transition(StatusClosedShort, StatusClosedShort), apperr.Conflict, CodeOrderStatus)
	next, err := m.Follow(StatusClosedShort, []PurchaseOrderLine{orderLine("2400", "0")})
	if err != nil || next != StatusClosedShort {
		t.Errorf("Follow(closed_short) = %s, %v", next, err)
	}
	next, err = m.Follow(StatusCompleted, []PurchaseOrderLine{orderLine("2400", "1440")})
	if err != nil || next != StatusPartiallyReceived {
		t.Errorf("Follow(completed) after a reversal = %s, %v", next, err)
	}
	next, err = m.Follow(StatusConfirmed, []PurchaseOrderLine{orderLine("2400", "1440")})
	if err != nil || next != StatusPartiallyReceived {
		t.Errorf("Follow(confirmed) after a batch = %s, %v", next, err)
	}
	// A draft takes no receipts: progress on it is not a valid move.
	_, err = m.Follow(StatusDraft, []PurchaseOrderLine{orderLine("2400", "1440")})
	_ = wantCode(t, err, apperr.Conflict, CodeOrderStatus)
}

func TestPendingBaseQtyNeverNegative(t *testing.T) {
	if got := orderLine("2400", "1440").PendingBaseQty(); !got.Equal(qty("960")) {
		t.Errorf("pending = %s, want 960", got)
	}
	if got := orderLine("2400", "2500").PendingBaseQty(); !got.IsZero() {
		t.Errorf("pending after excess = %s, want 0", got)
	}
}
