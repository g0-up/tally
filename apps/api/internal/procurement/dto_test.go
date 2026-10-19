package procurement

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDateReadsCivilDates(t *testing.T) {
	var in struct {
		D *date `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"d":"2026-10-17"}`), &in); err != nil {
		t.Fatal(err)
	}
	if got := dateOf(*in.D.time()); got != "2026-10-17" {
		t.Errorf("date = %s", got)
	}
	for _, body := range []string{`{"d":20261017}`, `{"d":"17/10/2026"}`, `{"d":"2026-02-30"}`} {
		var bad struct {
			D *date `json:"d"`
		}
		var typeErr *json.UnmarshalTypeError
		if err := json.Unmarshal([]byte(body), &bad); !errors.As(err, &typeErr) {
			t.Errorf("%s: err = %v, want a type error", body, err)
		}
	}
	var none *date
	if none.time() != nil || dateString(nil) != nil {
		t.Error("a missing date is nil")
	}
}

func TestReceiptLineReturnableExcludesWrongItems(t *testing.T) {
	kept := receiptLine(ConditionDamaged, "1000", "10", "4")
	if got := toReceiptLineDTO(kept).ReturnableBaseQty; !got.Equal(qty("6")) {
		t.Errorf("returnable = %s, want 6", got)
	}
	wrong := receiptLine(ConditionWrongItem, "1000", "10", "0")
	if got := toReceiptLineDTO(wrong).ReturnableBaseQty; !got.IsZero() {
		t.Errorf("wrong item returnable = %s, want 0", got)
	}
}

func TestPayableShowsWhatIsStillOpen(t *testing.T) {
	p := PayableInfo{Amount: vnd("1000000"), SettledAmount: vnd("300000"), DueDate: day("2026-11-01"),
		Schedule: []Installment{{DueDate: day("2026-11-01"), Amount: vnd("1000000")}}}
	d := toPayableDTO(p)
	if d.OpenAmount.String() != "700000" || d.DueDate != "2026-11-01" || len(d.Schedule) != 1 || d.ReversedOn != nil {
		t.Errorf("payable = %+v", d)
	}
}
