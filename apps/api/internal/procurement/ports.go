package procurement

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// The ports are what procurement needs of inventory, debt and pricing. The composition root
// adapts those modules to them; every call runs inside the caller's posting transaction (or, for
// pricing, the caller's tenant transaction), so a document and its stock, debt and price rows are
// written together or not at all.

// StockDoc names the procurement document a stock call books.
type StockDoc struct {
	Type string
	ID   uuid.UUID
}

// StockReceive is a receipt line going into stock. Qty is in base units; the lot is identified
// by the color, lot number and expiry date (created when new).
type StockReceive struct {
	LineID     uuid.UUID
	SkuID      uuid.UUID
	Qty        money.Qty
	UnitCost   money.Money
	ColorID    *uuid.UUID
	LotNo      string
	ExpiryDate *time.Time
}

// StockQuarantine moves received goods of a lot into its quarantine twin.
type StockQuarantine struct {
	LineID uuid.UUID
	SkuID  uuid.UUID
	Qty    money.Qty
	LotID  uuid.UUID
}

// StockIssue takes goods out of exactly one lot (a purchase return).
type StockIssue struct {
	LineID uuid.UUID
	SkuID  uuid.UUID
	Qty    money.Qty
	LotID  uuid.UUID
}

// StockCost changes the value of the stock of a SKU without changing its quantity. LotID nil
// books it on the first lot still holding the SKU.
type StockCost struct {
	LineID *uuid.UUID
	SkuID  uuid.UUID
	LotID  *uuid.UUID
	Value  money.Money
}

// StockMove is a move the ledger wrote for a document line.
type StockMove struct {
	LineID     *uuid.UUID
	SkuID      uuid.UUID
	LotID      uuid.UUID
	Qty        money.Qty
	Value      money.Money
	Quarantine bool
}

// Stock is the inventory ledger. The SKUs a call touches must be in the posting's
// posting.LockSkuStock keys.
type Stock interface {
	Receive(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockReceive) ([]StockMove, error)
	Quarantine(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockQuarantine) ([]StockMove, error)
	Issue(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockIssue) ([]StockMove, error)
	CostAdjust(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockCost) ([]StockMove, error)
	// Reverse mirrors every move of doc under the reversing document by; it fails with 422
	// insufficient_stock when a lot no longer holds what the document put into it.
	Reverse(ctx context.Context, tx posting.Tx, by, doc StockDoc) error
}

// Installment is one part of a payable schedule.
type Installment struct {
	DueDate time.Time
	Amount  money.Money
}

// PayableInput is the payable of a procurement document, recorded once per source.
type PayableInput struct {
	PartyID     uuid.UUID
	SourceType  string
	SourceID    uuid.UUID
	SourceDocNo string
	Amount      money.Money
	DueDate     *time.Time
	Schedule    []Installment
}

// Payable is a recorded payable.
type Payable struct {
	ID      uuid.UUID
	Amount  money.Money
	DueDate time.Time
}

// PaymentInput is money paid to a party now (a PC) and allocated to ObligationID.
type PaymentInput struct {
	PartyID      uuid.UUID
	MethodID     uuid.UUID
	Amount       money.Money
	SourceType   string
	SourceID     uuid.UUID
	ObligationID uuid.UUID
	// Key makes the payment replay with the posting.
	Key  string
	Note string
}

// Payment is a recorded payment.
type Payment struct {
	ID    uuid.UUID
	DocNo string
}

// CreditInput is a credit note reducing what the store owes a supplier. Targets are settled
// first; OnlyTargets turns the rest into a refund due instead of settling other documents.
type CreditInput struct {
	PartyID     uuid.UUID
	SourceType  string
	SourceID    uuid.UUID
	SourceDocNo string
	Amount      money.Money
	ReasonID    *uuid.UUID
	Targets     []uuid.UUID
	OnlyTargets bool
}

// Credit is a recorded credit note; RefundDueID is the refund the supplier owes, if any.
type Credit struct {
	ID          uuid.UUID
	RefundDueID *uuid.UUID
}

// Debt is the debt ledger on the payable side.
type Debt interface {
	RecordPayable(ctx context.Context, tx posting.Tx, in PayableInput) (Payable, error)
	// PayNow records a PC under the number the posting reserved (PaymentPrefix in its
	// ExtraDocPrefixes).
	PayNow(ctx context.Context, tx posting.Tx, in PaymentInput) (Payment, error)
	// UseAdvance applies amount of the supplier's advance paymentID to obligationID.
	UseAdvance(ctx context.Context, tx posting.Tx, paymentID, obligationID uuid.UUID, amount money.Money) error
	Credit(ctx context.Context, tx posting.Tx, in CreditInput) (Credit, error)
	// ReversePayable reverses the payable of a source; its payments become the supplier's
	// advance again.
	ReversePayable(ctx context.Context, tx posting.Tx, sourceType string, sourceID uuid.UUID, reason string) error
}

// PaymentPrefix is the document prefix of a payment to a supplier (PC).
const PaymentPrefix = docseq.PrefixCashPayment

// PriceLine is a purchase line to price: UnitPrice is the price typed on the line.
type PriceLine struct {
	SkuID     uuid.UUID
	ColorID   *uuid.UUID
	UnitID    *uuid.UUID
	Qty       money.Qty
	UnitPrice money.Money
	Discount  money.Money
}

// PricedLine is a priced purchase line in whole đồng.
type PricedLine struct {
	UnitID     uuid.UUID
	Factor     money.Factor
	BaseQty    money.Qty
	UnitPrice  money.Money
	Gross      money.Money
	Discount   money.Money
	TaxRate    money.Percent
	Tax        money.Money
	LineAmount money.Money
}

// PricedDoc is a priced purchase document: Total is Σ LineAmount, which includes tax.
type PricedDoc struct {
	PricesIncludeTax bool
	Lines            []PricedLine
	Goods            money.Money
	Discount         money.Money
	Tax              money.Money
	Total            money.Money
}

// RetailPrice is a "Giá bán lẻ" edited on a PO line.
type RetailPrice struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  uuid.UUID
	Price   money.Money
	OrderID uuid.UUID
}

// Pricing is the pricing engine in purchase mode: no promotions, tax per line.
type Pricing interface {
	Price(ctx context.Context, q db.Querier, tenantID uuid.UUID, date time.Time, lines []PriceLine) (PricedDoc, error)
	// RetailPrices is the retail list price of each line in its unit, nil when there is none.
	RetailPrices(ctx context.Context, q db.Querier, tenantID uuid.UUID, date time.Time, lines []PriceLine) ([]*money.Money, error)
	// SetRetailPrice writes the retail list price for documents priced afterwards.
	SetRetailPrice(ctx context.Context, q db.Querier, actor audit.Actor, p RetailPrice) error
}

// Catalog creates the products that purchase lines introduce (CreateProduct returns the SKU), in
// the document's transaction q, and holds the rules their barcodes follow.
type Catalog interface {
	CreateProduct(ctx context.Context, q db.Querier, actor audit.Actor, p NewProductInput) (uuid.UUID, error)
	// ValidateBarcode answers 422 when a trimmed code cannot be a barcode.
	ValidateBarcode(code string) error
}

// Campaigns tags a receipt with the campaign the goods were bought for, in the receipt's
// transaction. A tag only filters reports: the cost enters a season when the goods are sold.
type Campaigns interface {
	// TagReceipt records the tag; a campaign that does not exist is 422 on campaign_id.
	TagReceipt(ctx context.Context, tx posting.Tx, receiptID, campaignID uuid.UUID) error
}
