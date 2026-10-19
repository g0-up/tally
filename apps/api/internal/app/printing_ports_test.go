package app

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A sales document printed through the shared print route keeps the JSON sales printed before.
func TestFromSalesPrintKeepsTheSalesWireFormat(t *testing.T) {
	str := func(s string) *string { return &s }
	colorID := uuid.New()
	line := func(no int, kind string) sales.PrintLine {
		return sales.PrintLine{
			LineNo: no, Kind: kind, SkuCode: str("SKU-1"), Name: "Kẹo dẻo", VariantLabel: "Hộp 500g", Color: str("Đỏ"),
			SpecText: str("gói quà"), UnitName: "hộp", Qty: money.MustQty("2"), UnitPrice: money.MustMoney("50000"),
			Discount: money.MustMoney("1000"), Tax: money.MustMoney("7840"), LineAmount: money.MustMoney("106840"),
			MixKey: str("m1"), ColorID: &colorID, TaxRate: money.MustPercent("8"),
		}
	}
	in := sales.PrintDoc{
		Label: printing.Label, DocType: sales.PrintExchange, Title: "Phiếu đổi hàng", ID: uuid.New(), DocNo: "DH-0001",
		Date: sales.DateOf(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)), Status: "posted",
		Tenant:   &sales.PrintTenant{Name: "Tạp hóa Lan", Legal: "Hộ kinh doanh Lan", TaxCode: str("0101"), Phone: str("0900")},
		Customer: &sales.PrintParty{ID: uuid.New(), Name: "Chị Mai", Phone: str("0911")},
		Note:     str("giao chiều"),
		Groups:   []sales.PrintGroup{{MixKey: str("m1"), Lines: []sales.PrintLine{line(1, "mix_pack"), line(2, "mix_flavor")}}, {Lines: []sales.PrintLine{line(3, "item")}}},
		Returned: []sales.PrintLine{line(4, "item")},
		Totals: sales.Totals{Goods: money.MustMoney("100000"), Discount: money.MustMoney("1000"), Tax: money.MustMoney("7840"),
			Total: money.MustMoney("106840")},
		Payment: &sales.PaymentState{Total: money.MustMoney("106840"), Paid: money.MustMoney("6840"), Open: money.MustMoney("100000"),
			Pending: money.ZeroMoney, Code: "partial", Label: "Trả một phần"},
		Amounts:   []sales.PrintAmount{{Code: "refund", Label: "Hoàn lại", Amount: money.MustMoney("5000")}},
		Related:   []sales.PrintRelated{{DocType: sales.PrintSalesDocument, ID: uuid.New(), DocNo: "BH-0002"}},
		PrintedAt: time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC),
	}
	out := fromSalesPrint(in)
	out.Label, out.DocType, out.PrintedAt = in.Label, in.DocType, in.PrintedAt
	if got, want := jsonValue(t, out), jsonValue(t, in); !reflect.DeepEqual(got, want) {
		t.Fatalf("print JSON changed:\n got %v\nwant %v", got, want)
	}
	if out.Groups[0].Lines[0].ColorID == nil || *out.Groups[0].Lines[0].ColorID != colorID {
		t.Fatal("the color id the print service names colors by was dropped")
	}

	bare := fromSalesPrint(sales.PrintDoc{Date: in.Date})
	if bare.Tenant != nil || bare.Customer != nil || bare.Payment != nil || bare.Returned != nil {
		t.Fatalf("absent parts were filled in: %+v", bare)
	}
}
