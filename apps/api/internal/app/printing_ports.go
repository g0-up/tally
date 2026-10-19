package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// printRegistry registers the provider of every printable document type: the sales documents
// as sales prints them, and the purchase, stock, statement and kit label documents read through
// each domain's queries.
func printRegistry(s *sales.Service, p *procurement.Service, inv *inventory.Service, d *debt.Service, k *kits.Service) *printing.Registry {
	r := printing.NewRegistry()
	for _, t := range []string{sales.PrintQuote, sales.PrintStockOut, sales.PrintSalesDocument, sales.PrintSalesReturn, sales.PrintExchange} {
		r.Register(t, salesPrint(s, t), false)
	}
	r.Register(sales.PrintInvoice, salesPrint(s, sales.PrintInvoice), true)
	pp := purchasePrints{s: p}
	r.Register(printing.DocPurchaseOrder, pp.order, false)
	r.Register(printing.DocGoodsReceipt, pp.receipt, false)
	r.Register(printing.DocPurchaseReturn, pp.purchaseReturn, false)
	sp := stockPrints{s: inv}
	r.Register(printing.DocStockCount, sp.count, false)
	r.Register(printing.DocStockAdjustment, sp.adjustment, false)
	r.Register(printing.DocStatement, statementPrints{s: d}.statement, true)
	r.Register(printing.DocKitLabel, kitPrints{s: k}.label, false)
	return r
}

// salesPrint prints a sales document type through sales; a version prints a confirmed invoice
// version from its snapshot.
func salesPrint(s *sales.Service, docType string) printing.Provider {
	return func(ctx context.Context, scope printing.Scope, id uuid.UUID, version *int) (printing.Doc, error) {
		sc := sales.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}
		var d sales.PrintDoc
		var err error
		if version != nil {
			d, err = s.PrintInvoiceVersion(ctx, sc, id, *version)
		} else {
			d, err = s.Print(ctx, sc, docType, id)
		}
		if err != nil {
			return printing.Doc{}, err
		}
		out := fromSalesPrint(d)
		out.Version = version
		return out, nil
	}
}

func fromSalesPrint(d sales.PrintDoc) printing.Doc {
	out := printing.Doc{
		Title: d.Title, ID: d.ID, DocNo: d.DocNo, Date: d.Date.String(), Status: d.Status, Note: d.Note,
		Groups: make([]printing.Group, len(d.Groups)), Returned: fromSalesLines(d.Returned),
		Totals:  printing.Totals{Goods: d.Totals.Goods, Discount: d.Totals.Discount, Tax: d.Totals.Tax, Total: d.Totals.Total},
		Amounts: make([]printing.Amount, len(d.Amounts)), Related: make([]printing.Related, len(d.Related)),
	}
	if d.Tenant != nil {
		out.Tenant = &printing.Tenant{Name: d.Tenant.Name, Legal: d.Tenant.Legal, TaxCode: d.Tenant.TaxCode,
			Address: d.Tenant.Address, Phone: d.Tenant.Phone}
	}
	if d.Customer != nil {
		out.Customer = &printing.Party{ID: d.Customer.ID, Name: d.Customer.Name, Phone: d.Customer.Phone,
			TaxCode: d.Customer.TaxCode, Address: d.Customer.Address}
	}
	for i, g := range d.Groups {
		out.Groups[i] = printing.Group{MixKey: g.MixKey, Lines: fromSalesLines(g.Lines)}
	}
	if d.Payment != nil {
		out.Payment = &printing.Payment{Total: d.Payment.Total, Paid: d.Payment.Paid, Open: d.Payment.Open,
			Pending: d.Payment.Pending, Code: d.Payment.Code, Label: d.Payment.Label}
	}
	for i, a := range d.Amounts {
		out.Amounts[i] = printing.Amount{Code: a.Code, Label: a.Label, Amount: a.Amount}
	}
	for i, r := range d.Related {
		out.Related[i] = printing.Related{DocType: r.DocType, ID: r.ID, DocNo: r.DocNo}
	}
	return out
}

func fromSalesLines(lines []sales.PrintLine) []printing.Line {
	if lines == nil {
		return nil
	}
	out := make([]printing.Line, len(lines))
	for i, l := range lines {
		out[i] = printing.Line{
			LineNo: l.LineNo, Kind: l.Kind, SkuCode: l.SkuCode, Name: l.Name, VariantLabel: l.VariantLabel, Color: l.Color,
			SpecText: l.SpecText, UnitName: l.UnitName, Qty: l.Qty, UnitPrice: l.UnitPrice, Discount: l.Discount, Tax: l.Tax,
			LineAmount: l.LineAmount, TaxRate: l.TaxRate, ColorID: l.ColorID,
		}
	}
	return out
}

// single puts each line in a group of its own.
func single(lines []printing.Line) []printing.Group {
	out := make([]printing.Group, len(lines))
	for i, l := range lines {
		out[i] = printing.Group{Lines: []printing.Line{l}}
	}
	return out
}

func strPtr(s string) *string { return &s }

// unitCost is a cost per base unit as the price of one unit of a line's unit.
func unitCost(cost money.Money, factor money.Factor) money.Money {
	return cost.MulFactor(factor).RoundVND()
}

type purchasePrints struct{ s *procurement.Service }

func procurementScope(scope printing.Scope) procurement.Scope {
	return procurement.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}
}

func (p purchasePrints) order(ctx context.Context, scope printing.Scope, id uuid.UUID, _ *int) (printing.Doc, error) {
	o, err := p.s.Order(ctx, procurementScope(scope), id)
	if err != nil {
		return printing.Doc{}, err
	}
	lines := make([]printing.Line, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = printing.Line{
			LineNo: l.LineNo, Kind: "goods", SkuCode: strPtr(l.SkuCodeSnap), Name: l.NameSnap, VariantLabel: l.VariantLabelSnap,
			SpecText: l.SpecText, UnitName: l.UnitNameSnap, Qty: l.Qty, UnitPrice: l.UnitPrice, Discount: l.DiscountAmount,
			Tax: money.ZeroMoney, LineAmount: l.LineAmount, TaxRate: l.TaxRateSnap, ColorID: l.ColorID,
		}
	}
	out := printing.Doc{
		Title: "Đơn đặt hàng nhà cung cấp", ID: o.ID, DocNo: o.DocNo, Date: printing.DateString(o.OrderDate), Status: o.Status,
		Supplier: &printing.Party{ID: o.SupplierPartyID}, Note: o.Note, Groups: single(lines),
		Totals: printing.Totals{Goods: o.GoodsAmount, Discount: o.DiscountAmount, Tax: o.TaxAmount, Total: o.TotalAmount},
	}
	return out, nil
}

func (p purchasePrints) receipt(ctx context.Context, scope printing.Scope, id uuid.UUID, _ *int) (printing.Doc, error) {
	r, err := p.s.Receipt(ctx, procurementScope(scope), id)
	if err != nil {
		return printing.Doc{}, err
	}
	lines := make([]printing.Line, len(r.Lines))
	for i, l := range r.Lines {
		var lotNo *string
		if l.LotNo != "" {
			lotNo = strPtr(l.LotNo)
		}
		lines[i] = printing.Line{
			LineNo: l.LineNo, Kind: l.Condition, SkuCode: strPtr(l.SkuCodeSnap), Name: l.NameSnap, VariantLabel: l.VariantLabelSnap,
			SpecText: l.SpecText, UnitName: l.UnitNameSnap, Qty: l.Qty, UnitPrice: l.UnitPrice, Discount: l.DiscountAmount,
			Tax: money.ZeroMoney, LineAmount: l.LineAmount, TaxRate: l.TaxRateSnap, ColorID: l.ColorID,
			LotNo: lotNo, ExpiryDate: printing.DatePtr(l.ExpiryDate),
		}
	}
	out := printing.Doc{
		Title: "Phiếu nhập kho", ID: r.ID, DocNo: r.DocNo, Date: printing.DateString(r.BusinessDate), Status: r.Status,
		Supplier: &printing.Party{ID: r.SupplierPartyID}, Note: r.Note, Groups: single(lines),
		Totals: printing.Totals{Goods: r.GoodsAmount, Discount: r.DiscountAmount, Tax: r.TaxAmount, Total: r.TotalAmount},
		Amounts: []printing.Amount{
			{Code: "landed_cost_amount", Label: "Chi phí mua hàng", Amount: r.LandedCostAmount},
			{Code: "paid_now_amount", Label: "Đã trả ngay", Amount: r.PaidNowAmount},
		},
	}
	if r.PurchaseOrderID != nil && r.OrderDocNo != nil {
		out.Related = append(out.Related, printing.Related{DocType: printing.DocPurchaseOrder, ID: *r.PurchaseOrderID, DocNo: *r.OrderDocNo})
	}
	return out, nil
}

// settlementLabels name how a purchase return is settled.
var settlementLabels = map[string]string{
	procurement.SettlementCreditPayable: "Trừ vào công nợ phải trả",
	procurement.SettlementRefund:        "Nhà cung cấp hoàn tiền",
	procurement.SettlementReplacement:   "Nhà cung cấp đổi hàng",
}

func (p purchasePrints) purchaseReturn(ctx context.Context, scope printing.Scope, id uuid.UUID, _ *int) (printing.Doc, error) {
	r, err := p.s.Return(ctx, procurementScope(scope), id)
	if err != nil {
		return printing.Doc{}, err
	}
	receipt, err := p.s.Receipt(ctx, procurementScope(scope), r.ReceiptID)
	if err != nil {
		return printing.Doc{}, err
	}
	lines := make([]printing.Line, len(r.Lines))
	for i, l := range r.Lines {
		lines[i] = printing.Line{
			LineNo: i + 1, Kind: "return", SkuCode: strPtr(l.SkuCodeSnap), Name: l.NameSnap, VariantLabel: l.VariantLabelSnap,
			SpecText: l.SpecText, UnitName: l.UnitNameSnap, Qty: l.Qty, UnitPrice: unitCost(l.UnitCost, l.UnitFactorSnap),
			Discount: money.ZeroMoney, Tax: money.ZeroMoney, LineAmount: l.LineAmount, ColorID: l.ColorID, LotNo: strPtr(l.LotNo),
		}
	}
	return printing.Doc{
		Title: "Phiếu trả hàng nhà cung cấp", ID: r.ID, DocNo: r.DocNo, Date: printing.DateString(r.BusinessDate), Status: r.Status,
		Supplier: &printing.Party{ID: r.SupplierPartyID}, Groups: single(lines),
		Totals:  printing.Totals{Goods: r.TotalAmount, Discount: money.ZeroMoney, Tax: money.ZeroMoney, Total: r.TotalAmount},
		Amounts: []printing.Amount{{Code: "settlement_" + r.Settlement, Label: settlementLabels[r.Settlement], Amount: r.TotalAmount}},
		Related: []printing.Related{{DocType: printing.DocGoodsReceipt, ID: receipt.ID, DocNo: receipt.DocNo}},
	}, nil
}

type stockPrints struct{ s *inventory.Service }

func inventoryScope(scope printing.Scope) inventory.Scope {
	return inventory.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}
}

func (p stockPrints) count(ctx context.Context, scope printing.Scope, id uuid.UUID, _ *int) (printing.Doc, error) {
	c, err := p.s.Count(ctx, inventoryScope(scope), id)
	if err != nil {
		return printing.Doc{}, err
	}
	lines := make([]printing.Line, len(c.Lines))
	for i, l := range c.Lines {
		system := l.SystemQty
		variance := money.ZeroQty
		if l.VarianceQty != nil {
			variance = *l.VarianceQty
		}
		lotNo, expiry := l.LotNo, l.ExpiryDate
		if lotNo == nil {
			lotNo, expiry = l.NewLotNo, l.NewExpiryDate
		}
		lines[i] = printing.Line{
			LineNo: i + 1, Kind: "count", SkuCode: strPtr(l.SkuCode), Name: l.SkuName, VariantLabel: l.VariantLabel,
			Color: l.ColorName, UnitName: l.UnitName, Qty: variance, UnitPrice: money.ZeroMoney, Discount: money.ZeroMoney,
			Tax: money.ZeroMoney, LineAmount: money.ZeroMoney, ColorID: l.ColorID, LotNo: lotNo,
			ExpiryDate: printing.DatePtr(expiry), SystemQty: &system, CountedQty: l.CountedQty,
		}
	}
	out := printing.Doc{
		Title: "Phiếu kiểm kê", ID: c.ID, DocNo: c.DocNo, Date: printing.DateString(c.BusinessDate), Status: c.Status,
		Note: c.Note, Groups: single(lines), Totals: printing.ZeroTotals,
	}
	if c.AdjustmentID != nil && c.AdjustmentNo != nil {
		out.Related = append(out.Related, printing.Related{DocType: printing.DocStockAdjustment, ID: *c.AdjustmentID, DocNo: *c.AdjustmentNo})
	}
	return out, nil
}

func (p stockPrints) adjustment(ctx context.Context, scope printing.Scope, id uuid.UUID, _ *int) (printing.Doc, error) {
	a, err := p.s.Adjustment(ctx, inventoryScope(scope), id)
	if err != nil {
		return printing.Doc{}, err
	}
	lines := make([]printing.Line, len(a.Lines))
	total := money.ZeroMoney
	for i, l := range a.Lines {
		kind := inventory.KindIncrease
		if l.Direction < 0 {
			kind = inventory.KindDecrease
		}
		price, amount := money.ZeroMoney, money.ZeroMoney
		if l.UnitCost != nil {
			price, amount = unitCost(*l.UnitCost, l.UnitFactorSnap), l.UnitCost.MulQty(l.BaseQty).RoundVND()
			total = total.Add(amount)
		}
		lines[i] = printing.Line{
			LineNo: i + 1, Kind: kind, SkuCode: strPtr(l.SkuCodeSnap), Name: l.NameSnap, VariantLabel: l.VariantLabelSnap,
			SpecText: l.SpecText, UnitName: l.UnitNameSnap, Qty: l.Qty, UnitPrice: price, Discount: money.ZeroMoney,
			Tax: money.ZeroMoney, LineAmount: amount, ColorID: l.ColorID, LotNo: l.LotNo, ExpiryDate: printing.DatePtr(l.ExpiryDate),
		}
	}
	out := printing.Doc{
		Title: "Phiếu điều chỉnh kho", ID: a.ID, DocNo: a.DocNo, Date: printing.DateString(a.BusinessDate), Status: a.Status,
		Note: a.Note, Groups: single(lines),
		Totals: printing.Totals{Goods: total, Discount: money.ZeroMoney, Tax: money.ZeroMoney, Total: total},
	}
	if a.StockCountID != nil {
		c, err := p.s.Count(ctx, inventoryScope(scope), *a.StockCountID)
		if err != nil {
			return printing.Doc{}, err
		}
		out.Related = append(out.Related, printing.Related{DocType: printing.DocStockCount, ID: c.ID, DocNo: c.DocNo})
	}
	return out, nil
}

type statementPrints struct{ s *debt.Service }

// statement prints a statement version from its frozen snapshot: the version asked for, else the
// sent one, else the current draft.
func (p statementPrints) statement(ctx context.Context, scope printing.Scope, id uuid.UUID, version *int) (printing.Doc, error) {
	sc := debt.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}
	n := 0
	if version != nil {
		n = *version
	} else {
		st, err := p.s.Statement(ctx, sc, id)
		if err != nil {
			return printing.Doc{}, err
		}
		n = st.CurrentVersion
		if st.SentVersion != nil {
			n = *st.SentVersion
		}
	}
	v, err := p.s.StatementVersion(ctx, sc, id, n)
	if err != nil {
		return printing.Doc{}, err
	}
	var snap printing.Statement
	if err := json.Unmarshal(v.Version.Snapshot, &snap); err != nil {
		return printing.Doc{}, fmt.Errorf("app: decode statement snapshot: %w", err)
	}
	if snap.Lines == nil {
		snap.Lines = []printing.StatementLine{}
	}
	st := v.Statement
	out := printing.Doc{
		Title: "Bảng kê đối soát công nợ", ID: st.ID, DocNo: st.DocNo, Date: snap.PeriodTo, Status: v.Status,
		Totals: printing.ZeroTotals, Version: &n, Statement: &snap,
		Amounts: []printing.Amount{
			{Code: "opening_balance", Label: "Số dư đầu kỳ", Amount: snap.OpeningBalance},
			{Code: "closing_balance", Label: "Số dư cuối kỳ", Amount: snap.ClosingBalance},
		},
	}
	if st.SentVersion != nil && *st.SentVersion == n {
		out.SentAt = st.SentAt
	}
	party := &printing.Party{ID: st.PartyID}
	if st.Direction == debt.Payable {
		out.Supplier = party
	} else {
		out.Customer = party
	}
	return out, nil
}

type kitPrints struct{ s *kits.Service }

// label prints the labels of an assembly order: per kit lot, its expiry and the component lots one
// kit holds.
func (p kitPrints) label(ctx context.Context, scope printing.Scope, id uuid.UUID, _ *int) (printing.Doc, error) {
	sc := kits.Scope{TenantID: scope.TenantID, ReadOnly: scope.ReadOnly}
	o, err := p.s.Order(ctx, sc, id)
	if err != nil {
		return printing.Doc{}, err
	}
	l, err := p.s.Label(ctx, sc, id)
	if err != nil {
		return printing.Doc{}, err
	}
	date := o.CreatedAt
	if o.PlannedDate != nil {
		date = *o.PlannedDate
	}
	for _, posting := range o.Postings {
		if posting.BusinessDate.After(date) {
			date = posting.BusinessDate
		}
	}
	out := printing.Doc{
		Title: "Nhãn bộ kit", ID: o.ID, DocNo: o.DocNo, Date: printing.DateString(date), Status: o.Status, Note: o.Note,
		Totals: printing.ZeroTotals, KitLots: make([]printing.KitLot, len(l.Lots)),
	}
	for i, lot := range l.Lots {
		comps := make([]printing.KitComponent, len(lot.Components))
		for k, c := range lot.Components {
			comps[k] = printing.KitComponent{SkuCode: c.SkuCode, Name: c.Name, LotNo: c.LotNo, Color: c.ColorName,
				ExpiryDate: printing.DatePtr(c.Expiry), QtyPerKit: c.QtyPerKit}
		}
		out.KitLots[i] = printing.KitLot{LotNo: lot.LotNo, ExpiryDate: printing.DatePtr(lot.Expiry), Qty: lot.Qty,
			SkuCode: l.SkuCode, Name: l.KitName, Components: comps}
	}
	return out, nil
}
