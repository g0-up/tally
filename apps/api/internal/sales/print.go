package sales

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// PrintLabel marks every printed sales document as an internal record: Tally issues no
// e-invoices.
const PrintLabel = "Chứng từ nội bộ — không phải hóa đơn điện tử"

// Document types that can be printed.
const (
	PrintQuote         = "quote"
	PrintInvoice       = "invoice"
	PrintStockOut      = "stock_out"
	PrintSalesDocument = "sales_document"
	PrintSalesReturn   = "sales_return"
	PrintExchange      = "exchange"
)

// PrintDoc is the data a printed sales document is rendered from.
type PrintDoc struct {
	Label    string       `json:"label"`
	DocType  string       `json:"doc_type"`
	Title    string       `json:"title"`
	ID       uuid.UUID    `json:"id"`
	DocNo    string       `json:"doc_no"`
	Date     Date         `json:"date"`
	Status   string       `json:"status"`
	Tenant   *PrintTenant `json:"tenant"`
	Customer *PrintParty  `json:"customer"`
	Note     *string      `json:"note"`
	// Groups are the lines in order, the flavors of a mix pack together under one group.
	Groups []PrintGroup `json:"groups"`
	// Returned are the lines an exchange took back.
	Returned []PrintLine    `json:"returned_lines,omitempty"`
	Totals   Totals         `json:"totals"`
	Payment  *PaymentState  `json:"payment"`
	Amounts  []PrintAmount  `json:"amounts"`
	Related  []PrintRelated `json:"related"`
	// PrintedAt is when the data was read.
	PrintedAt time.Time `json:"printed_at"`
}

// PrintTenant is the store header.
type PrintTenant struct {
	Name    string  `db:"display_name" json:"name"`
	Legal   string  `db:"legal_name" json:"legal_name"`
	TaxCode *string `db:"tax_code" json:"tax_code"`
	Address *string `db:"address" json:"address"`
	Phone   *string `db:"phone" json:"phone"`
}

// PrintParty is the customer of a printed document.
type PrintParty struct {
	ID      uuid.UUID `db:"id" json:"id"`
	Name    string    `db:"name" json:"name"`
	Phone   *string   `db:"phone" json:"phone"`
	TaxCode *string   `db:"tax_code" json:"tax_code"`
	Address *string   `db:"address" json:"address"`
}

// PrintGroup is one line, or the lines of one mix pack.
type PrintGroup struct {
	MixKey *string     `json:"mix_key"`
	Lines  []PrintLine `json:"lines"`
}

// PrintLine is a printed line.
type PrintLine struct {
	LineNo       int           `json:"line_no"`
	Kind         string        `json:"line_kind"`
	SkuCode      *string       `json:"sku_code"`
	Name         string        `json:"name"`
	VariantLabel string        `json:"variant_label"`
	Color        *string       `json:"color"`
	SpecText     *string       `json:"spec_text"`
	UnitName     string        `json:"unit_name"`
	Qty          money.Qty     `json:"qty"`
	UnitPrice    money.Money   `json:"unit_price"`
	Discount     money.Money   `json:"discount_amount"`
	Tax          money.Money   `json:"tax_amount"`
	LineAmount   money.Money   `json:"line_amount"`
	MixKey       *string       `json:"-"`
	ColorID      *uuid.UUID    `json:"-"`
	TaxRate      money.Percent `json:"tax_rate"`
}

// PrintAmount is a labelled amount below the totals.
type PrintAmount struct {
	Code   string      `json:"code"`
	Label  string      `json:"label"`
	Amount money.Money `json:"amount"`
}

// PrintRelated is a document the printed one refers to.
type PrintRelated struct {
	DocType string    `json:"doc_type"`
	ID      uuid.UUID `json:"id"`
	DocNo   string    `json:"doc_no"`
}

// Print returns the print data of a sales document of the tenant.
func (s *Service) Print(ctx context.Context, scope Scope, docType string, id uuid.UUID) (PrintDoc, error) {
	switch docType {
	case PrintQuote, PrintInvoice, PrintStockOut, PrintSalesDocument, PrintSalesReturn, PrintExchange:
	default:
		return PrintDoc{}, errInvalid(map[string]string{"doc_type": "unknown"})
	}
	var out PrintDoc
	err := s.read(ctx, scope, func(q db.Querier) error {
		var err error
		switch docType {
		case PrintQuote:
			out, err = s.printQuote(ctx, q, scope.TenantID, id)
		case PrintInvoice:
			out, err = s.printInvoice(ctx, q, id)
		case PrintStockOut:
			out, err = s.printStockOut(ctx, q, id)
		case PrintSalesDocument:
			out, err = s.printDocument(ctx, q, id)
		case PrintSalesReturn:
			out, err = s.printReturn(ctx, q, id)
		case PrintExchange:
			out, err = s.printExchange(ctx, q, id)
		}
		if err != nil {
			return err
		}
		return s.finishPrint(ctx, q, scope, docType, &out)
	})
	return out, err
}

// PrintInvoiceVersion returns the print data of confirmed version n of an invoice, from the
// snapshot frozen when it was confirmed: printing it again shows the same numbers.
func (s *Service) PrintInvoiceVersion(ctx context.Context, scope Scope, id uuid.UUID, n int) (PrintDoc, error) {
	var out PrintDoc
	err := s.read(ctx, scope, func(q db.Querier) error {
		if _, err := getInvoice(ctx, q, id, false); err != nil {
			return err
		}
		var snap RawJSON
		err := q.QueryRowxContext(ctx, `SELECT snapshot FROM invoice_versions WHERE invoice_id = $1 AND version_no = $2`, id, n).Scan(&snap)
		if db.IsNoRows(err) {
			return errNotFound("Invoice version")
		}
		if err != nil {
			return wrap("read invoice version", err)
		}
		var inv Invoice
		if err := json.Unmarshal(snap, &inv); err != nil {
			return fmt.Errorf("sales: decode invoice version: %w", err)
		}
		out = invoicePrint(inv)
		return s.finishPrint(ctx, q, scope, PrintInvoice, &out)
	})
	return out, err
}

// finishPrint adds what every printed copy shows besides the document: the label, the store
// header, the customer, the color names, and empty lists for absent ones.
func (s *Service) finishPrint(ctx context.Context, q db.Querier, scope Scope, docType string, out *PrintDoc) error {
	var err error
	out.Label, out.DocType, out.PrintedAt = PrintLabel, docType, s.clock.Now()
	if out.Tenant, err = printTenant(ctx, q, scope.TenantID); err != nil {
		return err
	}
	if out.Customer != nil {
		if out.Customer, err = printParty(ctx, q, out.Customer.ID); err != nil {
			return err
		}
	}
	if out.Amounts == nil {
		out.Amounts = []PrintAmount{}
	}
	if out.Related == nil {
		out.Related = []PrintRelated{}
	}
	return nameColors(ctx, q, out)
}

func (s *Service) printQuote(ctx context.Context, q db.Querier, tenantID, id uuid.UUID) (PrintDoc, error) {
	qt, err := s.quoteDetail(ctx, q, tenantID, id)
	if err != nil {
		return PrintDoc{}, err
	}
	lines := make([]Line, len(qt.Lines))
	for i, l := range qt.Lines {
		lines[i] = l.Line
	}
	out := PrintDoc{
		Title: "Báo giá", ID: qt.ID, DocNo: qt.DocNo, Date: qt.QuoteDate, Status: qt.Status, Customer: partyRef(&qt.CustomerID),
		Note: qt.Note, Groups: printGroups(lines), Totals: qt.Totals,
	}
	if qt.ConvertedInvoiceID != nil {
		inv, err := getInvoice(ctx, q, *qt.ConvertedInvoiceID, false)
		if err != nil {
			return PrintDoc{}, err
		}
		out.Related = append(out.Related, PrintRelated{DocType: PrintInvoice, ID: inv.ID, DocNo: inv.DocNo})
	}
	return out, nil
}

func (s *Service) printInvoice(ctx context.Context, q db.Querier, id uuid.UUID) (PrintDoc, error) {
	inv, err := s.invoiceDetail(ctx, q, id)
	if err != nil {
		return PrintDoc{}, err
	}
	return invoicePrint(inv), nil
}

// invoicePrint is the print data of an invoice detail.
func invoicePrint(inv Invoice) PrintDoc {
	lines := make([]Line, len(inv.Lines))
	for i, l := range inv.Lines {
		lines[i] = l.Line
	}
	out := PrintDoc{
		Title: "Hóa đơn", ID: inv.ID, DocNo: inv.DocNo, Date: inv.InvoiceDate, Status: inv.Status,
		Customer: partyRef(&inv.PayerID), Note: inv.Note, Groups: printGroups(lines), Totals: inv.Totals, Payment: inv.Payment,
	}
	out.Amounts = []PrintAmount{{Code: "delivered_amount", Label: "Đã giao", Amount: inv.Delivered}}
	return out
}

func (s *Service) printStockOut(ctx context.Context, q db.Querier, id uuid.UUID) (PrintDoc, error) {
	so, err := s.stockOutDetail(ctx, q, id)
	if err != nil {
		return PrintDoc{}, err
	}
	inv, err := getInvoice(ctx, q, so.InvoiceID, false)
	if err != nil {
		return PrintDoc{}, err
	}
	out := PrintDoc{
		Title: "Phiếu xuất kho", ID: so.ID, DocNo: so.DocNo, Date: so.BusinessDate, Status: so.Status,
		Customer: partyRef(&inv.PayerID), Note: so.Note, Groups: []PrintGroup{},
		Related: []PrintRelated{{DocType: PrintInvoice, ID: inv.ID, DocNo: inv.DocNo}},
	}
	if so.Document != nil {
		out.Groups = printGroups(linesOf(so.Document.Lines))
		out.Totals, out.Payment = so.Document.Totals, &so.Document.Payment
		out.Related = append(out.Related, PrintRelated{DocType: PrintSalesDocument, ID: so.Document.ID, DocNo: so.Document.DocNo})
	}
	return out, nil
}

// linesOf are the sales lines of document lines.
func linesOf(lines []DocumentLine) []Line {
	out := make([]Line, len(lines))
	for i, l := range lines {
		out[i] = l.Line
	}
	return out
}

func (s *Service) printDocument(ctx context.Context, q db.Querier, id uuid.UUID) (PrintDoc, error) {
	d, err := s.documentDetail(ctx, q, id)
	if err != nil {
		return PrintDoc{}, err
	}
	title := "Chứng từ bán hàng"
	if d.Source == SourcePOS || d.Source == SourcePOSOffline {
		title = "Hóa đơn bán lẻ"
	}
	out := PrintDoc{
		Title: title, ID: d.ID, DocNo: d.DocNo, Date: d.BusinessDate, Status: d.Status, Customer: partyRef(d.PayerID),
		Groups: printGroups(linesOf(d.Lines)), Totals: d.Totals, Payment: &d.Payment,
	}
	if d.CashTendered.IsPositive() {
		out.Amounts = append(out.Amounts,
			PrintAmount{Code: "cash_tendered_amount", Label: "Tiền khách đưa", Amount: d.CashTendered},
			PrintAmount{Code: "change_amount", Label: "Tiền thối lại", Amount: d.Change})
	}
	if d.StockOutID != nil {
		so, err := getStockOut(ctx, q, *d.StockOutID, false)
		if err != nil {
			return PrintDoc{}, err
		}
		out.Related = append(out.Related, PrintRelated{DocType: PrintStockOut, ID: so.ID, DocNo: so.DocNo})
	}
	if d.ReversalOf != nil {
		orig, err := getDocument(ctx, q, *d.ReversalOf, false)
		if err != nil {
			return PrintDoc{}, err
		}
		out.Related = append(out.Related, PrintRelated{DocType: PrintSalesDocument, ID: orig.ID, DocNo: orig.DocNo})
	}
	return out, nil
}

func (s *Service) printReturn(ctx context.Context, q db.Querier, id uuid.UUID) (PrintDoc, error) {
	r, err := returnDetail(ctx, q, id)
	if err != nil {
		return PrintDoc{}, err
	}
	out := PrintDoc{
		Title: "Phiếu trả hàng", ID: r.ID, DocNo: r.DocNo, Date: r.BusinessDate, Status: r.Status, Customer: partyRef(r.CustomerID),
		Groups: printGroups(nil), Totals: Totals{Goods: r.Total, Discount: money.ZeroMoney, Tax: money.ZeroMoney, Total: r.Total},
		Amounts: []PrintAmount{
			{Code: "debt_reduction_amount", Label: "Trừ vào công nợ", Amount: r.DebtReduction},
			{Code: "refund_due_amount", Label: "Hoàn lại khách", Amount: r.RefundDue},
		},
	}
	for _, l := range returnPrintLines(r.Lines) {
		out.Groups = append(out.Groups, PrintGroup{Lines: []PrintLine{l}})
	}
	if r.SalesDocumentID != nil {
		d, err := getDocument(ctx, q, *r.SalesDocumentID, false)
		if err != nil {
			return PrintDoc{}, err
		}
		out.Related = append(out.Related, PrintRelated{DocType: PrintSalesDocument, ID: d.ID, DocNo: d.DocNo})
	}
	if r.ExchangeID != nil {
		ex, err := getExchange(ctx, q, *r.ExchangeID)
		if err != nil {
			return PrintDoc{}, err
		}
		out.Related = append(out.Related, PrintRelated{DocType: PrintExchange, ID: ex.ID, DocNo: ex.DocNo})
	}
	return out, nil
}

func (s *Service) printExchange(ctx context.Context, q db.Querier, id uuid.UUID) (PrintDoc, error) {
	ex, err := getExchange(ctx, q, id)
	if err != nil {
		return PrintDoc{}, err
	}
	ret, err := returnDetail(ctx, q, ex.SalesReturnID)
	if err != nil {
		return PrintDoc{}, err
	}
	d, err := s.documentDetail(ctx, q, ex.NewDocumentID)
	if err != nil {
		return PrintDoc{}, err
	}
	orig, err := getDocument(ctx, q, ex.OriginalDocumentID, false)
	if err != nil {
		return PrintDoc{}, err
	}
	sp := ExchangeSplit{Returned: ex.Returned, Taken: ex.Taken, DebtReduction: ex.DebtReduction,
		CustomerPays: ex.CustomerPays, StoreRefunds: ex.StoreRefunds}
	return PrintDoc{
		Title: "Phiếu đổi hàng", ID: ex.ID, DocNo: ex.DocNo, Date: ex.BusinessDate, Status: d.Status, Customer: partyRef(ex.CustomerID),
		Groups: printGroups(linesOf(d.Lines)), Returned: returnPrintLines(ret.Lines), Totals: d.Totals, Payment: &d.Payment,
		Amounts: []PrintAmount{
			{Code: "returned_amount", Label: "Giá trị hàng trả", Amount: sp.Returned},
			{Code: "taken_amount", Label: "Giá trị hàng lấy mới", Amount: sp.Taken},
			{Code: "debt_reduction_amount", Label: "Trừ vào công nợ", Amount: sp.DebtReduction},
			{Code: "customer_pays_amount", Label: "Khách trả thêm", Amount: sp.CustomerPays},
			{Code: "store_refunds_amount", Label: "Cửa hàng trả lại khách", Amount: sp.StoreRefunds},
		},
		Related: []PrintRelated{
			{DocType: PrintSalesDocument, ID: orig.ID, DocNo: orig.DocNo},
			{DocType: PrintSalesReturn, ID: ret.ID, DocNo: ret.DocNo},
			{DocType: PrintSalesDocument, ID: d.ID, DocNo: d.DocNo},
		},
	}, nil
}

func partyRef(id *uuid.UUID) *PrintParty {
	if id == nil {
		return nil
	}
	return &PrintParty{ID: *id}
}

// printGroups prints lines in order, the consecutive lines of one mix pack in one group.
func printGroups(lines []Line) []PrintGroup {
	out := []PrintGroup{}
	for _, l := range lines {
		pl := printLine(l)
		if n := len(out); n > 0 && l.MixKey != nil && out[n-1].MixKey != nil && *out[n-1].MixKey == *l.MixKey {
			out[n-1].Lines = append(out[n-1].Lines, pl)
			continue
		}
		out = append(out, PrintGroup{MixKey: l.MixKey, Lines: []PrintLine{pl}})
	}
	return out
}

func printLine(l Line) PrintLine {
	return PrintLine{
		LineNo: l.LineNo, Kind: l.Kind, SkuCode: l.SkuCode, Name: l.Name, VariantLabel: l.VariantLabel, SpecText: l.SpecText,
		UnitName: l.UnitName, Qty: l.Qty, UnitPrice: l.UnitPrice, Discount: l.Discount.Add(l.OrderAlloc), Tax: l.Tax,
		TaxRate: l.TaxRate, LineAmount: l.LineAmount, MixKey: l.MixKey, ColorID: l.ColorID,
	}
}

func returnPrintLines(lines []ReturnLine) []PrintLine {
	out := make([]PrintLine, len(lines))
	for i, l := range lines {
		code := l.SkuCode
		out[i] = PrintLine{
			LineNo: i + 1, Kind: "return", SkuCode: &code, Name: l.Name, VariantLabel: l.VariantLabel, SpecText: l.SpecText,
			UnitName: l.UnitName, Qty: l.Qty, UnitPrice: l.UnitPrice, Discount: money.ZeroMoney, Tax: money.ZeroMoney,
			LineAmount: l.LineAmount, ColorID: l.ColorID,
		}
	}
	return out
}

// printTenant is the store header; nil under a role that cannot read the tenant (support).
func printTenant(ctx context.Context, q db.Querier, tenantID uuid.UUID) (*PrintTenant, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return nil, fmt.Errorf("sales: read tenant privileges: %w", err)
	}
	if !readsTenant {
		return nil, nil
	}
	var t PrintTenant
	if err := sqlx.GetContext(ctx, q, &t, `
		SELECT display_name, legal_name, tax_code, address, phone FROM tenants WHERE id = $1`, tenantID); err != nil {
		return nil, wrap("read tenant header", err)
	}
	return &t, nil
}

func printParty(ctx context.Context, q db.Querier, id uuid.UUID) (*PrintParty, error) {
	var p PrintParty
	if err := sqlx.GetContext(ctx, q, &p, `SELECT id, name, phone, tax_code, address FROM parties WHERE id = $1`, id); err != nil {
		return nil, wrap("read customer", err)
	}
	return &p, nil
}

// nameColors fills in the color names of the printed lines.
func nameColors(ctx context.Context, q db.Querier, d *PrintDoc) error {
	var colorIDs []uuid.UUID
	each := func(fn func(*PrintLine)) {
		for i := range d.Groups {
			for k := range d.Groups[i].Lines {
				fn(&d.Groups[i].Lines[k])
			}
		}
		for i := range d.Returned {
			fn(&d.Returned[i])
		}
	}
	each(func(l *PrintLine) {
		if l.ColorID != nil {
			colorIDs = append(colorIDs, *l.ColorID)
		}
	})
	if len(colorIDs) == 0 {
		return nil
	}
	var rows []struct {
		ID   uuid.UUID `db:"id"`
		Name string    `db:"name"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, name FROM colors WHERE id = ANY($1)`, sortedUnique(colorIDs)); err != nil {
		return wrap("read color names", err)
	}
	names := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	each(func(l *PrintLine) {
		if l.ColorID != nil {
			if n, ok := names[*l.ColorID]; ok {
				l.Color = &n
			}
		}
	})
	return nil
}
