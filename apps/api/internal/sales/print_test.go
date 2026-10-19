package sales

import "testing"

func TestPrintGroups(t *testing.T) {
	if got := printGroups(nil); got == nil || len(got) != 0 {
		t.Fatalf("printGroups(nil) = %#v, want an empty list", got)
	}
	a, b := "mix-a", "mix-b"
	lines := []Line{
		{LineNo: 1},
		{LineNo: 2, MixKey: &a},
		{LineNo: 3, MixKey: &a},
		{LineNo: 4, MixKey: &b},
		{LineNo: 5},
		{LineNo: 6},
		{LineNo: 7, MixKey: &a},
	}
	got := printGroups(lines)
	want := [][]int{{1}, {2, 3}, {4}, {5}, {6}, {7}}
	if len(got) != len(want) {
		t.Fatalf("groups = %d, want %d", len(got), len(want))
	}
	for i, g := range got {
		if len(g.Lines) != len(want[i]) {
			t.Fatalf("group %d has %d lines, want %v", i, len(g.Lines), want[i])
		}
		for j, l := range g.Lines {
			if l.LineNo != want[i][j] {
				t.Errorf("group %d line %d = %d, want %d", i, j, l.LineNo, want[i][j])
			}
		}
	}
	if got[1].MixKey == nil || *got[1].MixKey != a || got[0].MixKey != nil {
		t.Errorf("mix keys = %v, %v", got[0].MixKey, got[1].MixKey)
	}
}

func TestPrintLineAddsTheOrderDiscount(t *testing.T) {
	pl := printLine(Line{Discount: vnd("5000"), OrderAlloc: vnd("2500")})
	wantMoney(t, "discount", pl.Discount, "7500")
}
