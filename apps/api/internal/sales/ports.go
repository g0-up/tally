package sales

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// The ports are the other domains as sales sees them. Sales never imports a domain package: the
// composition root adapts pricing, inventory, debt, cash and the session store onto these types.
// Every call that takes a posting.Tx runs inside the caller's posting and never commits.

// PriceDraft is a document to price: its buyer, date and lines.
type PriceDraft struct {
	// PartyID nil prices for a walk-in customer on the retail list.
	PartyID        *uuid.UUID
	BusinessDate   *time.Time
	Lines          []PriceDraftLine
	OrderDiscount  money.Money
	PromotionCodes []string
}

// PriceDraftLine is one line to price; UnitID nil is the SKU's base unit and UnitPrice nil
// resolves the price from the price lists.
type PriceDraftLine struct {
	SkuID     uuid.UUID
	ColorID   *uuid.UUID
	UnitID    *uuid.UUID
	Qty       money.Qty
	UnitPrice *money.Money
	Discount  money.Money
}

// PricedLine is a priced line. DraftIndex is the position of the draft line it prices, -1 for a
// gift a promotion added.
type PricedLine struct {
	DraftIndex   int
	Gift         bool
	SkuID        uuid.UUID
	ColorID      *uuid.UUID
	UnitID       uuid.UUID
	UnitFactor   money.Factor
	Qty          money.Qty
	BaseQty      money.Qty
	UnitPrice    money.Money
	Gross        money.Money
	Discount     money.Money
	OrderAlloc   money.Money
	TaxRate      money.Percent
	Tax          money.Money
	LineAmount   money.Money
	NetUnitPrice money.Money
	MinPrice     *money.Money
	// Source is the price source as stored on the line (price_source jsonb); Manual tells a price
	// typed on the line from one resolved from the lists.
	Source json.RawMessage
	Manual bool
	// Promotions are the promotions that gave this line something, with their share.
	Promotions []PromotionShare
}

// PromotionShare is what one promotion gave one line.
type PromotionShare struct {
	ID     uuid.UUID   `json:"id"`
	Amount money.Money `json:"amount"`
}

// PricedDoc is a priced document; Total is the sum of the line amounts.
type PricedDoc struct {
	PricesIncludeTax bool
	Lines            []PricedLine
	Goods            money.Money
	Discount         money.Money
	Tax              money.Money
	Total            money.Money
}

// RetailQuery asks the retail price of a SKU in a unit (nil is the base unit).
type RetailQuery struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  *uuid.UUID
	Qty     money.Qty
}

// Pricer is the pricing domain: document pricing, the retail price shown next to a quote line,
// and promotion usage.
type Pricer interface {
	Price(ctx context.Context, q db.Querier, tenantID uuid.UUID, d PriceDraft) (PricedDoc, error)
	// RetailPrices returns the walk-in price of each query, in its order; nil when the retail list
	// has no price for it.
	RetailPrices(ctx context.Context, q db.Querier, tenantID uuid.UUID, lines []RetailQuery) ([]*money.Money, error)
	// RecordUsage counts one use of a promotion by a document (docType "invoice" or
	// "sales_document").
	RecordUsage(ctx context.Context, q db.Querier, tenantID, promotionID uuid.UUID, docType string, docID uuid.UUID, amount money.Money) error
}

// StockDoc names the document stock moves are booked under.
type StockDoc struct {
	Type string
	ID   uuid.UUID
}

// Stock document types sales books moves under.
const (
	StockDocSalesDocument = "sales_document"
	StockDocStockOut      = "stock_out"
	StockDocSalesReturn   = "sales_return"
)

// StockIssue takes BaseQty of a SKU out for a sale. ColorID nil issues the uncolored stock.
type StockIssue struct {
	DocLineID uuid.UUID
	SkuID     uuid.UUID
	BaseQty   money.Qty
	ColorID   *uuid.UUID
}

// StockReceive puts returned stock back. LotID returns it into the lot it was sold from, at the
// cost of SourceMoveID; without them the stock goes to a lot found or created from ColorID, LotNo
// and ExpiryDate at the SKU's current average cost.
type StockReceive struct {
	DocLineID    uuid.UUID
	SkuID        uuid.UUID
	BaseQty      money.Qty
	ColorID      *uuid.UUID
	LotID        *uuid.UUID
	SourceMoveID *uuid.UUID
	LotNo        string
	ExpiryDate   *time.Time
}

// StockQuarantine moves BaseQty of a lot into its quarantine twin.
type StockQuarantine struct {
	DocLineID uuid.UUID
	SkuID     uuid.UUID
	BaseQty   money.Qty
	ColorID   *uuid.UUID
	LotID     uuid.UUID
}

// StockMove is a move the ledger wrote. Qty and Value are signed: an issue is negative.
type StockMove struct {
	ID         uuid.UUID
	SkuID      uuid.UUID
	LotID      uuid.UUID
	ColorID    *uuid.UUID
	DocLineID  *uuid.UUID
	Qty        money.Qty
	UnitCost   money.Money
	Value      money.Money
	MoveType   string
	LotNo      string
	Expiry     *time.Time
	Quarantine bool
}

// ColorStock is the sellable on-hand quantity of one color of a SKU; ColorID nil is the stock
// without a color.
type ColorStock struct {
	ColorID *uuid.UUID
	Qty     money.Qty
}

// Reservation is the stock an invoice line holds.
type Reservation struct {
	ID       uuid.UUID
	Qty      money.Qty
	Consumed money.Qty
	Released money.Qty
	Active   bool
}

// Open is the quantity still held.
func (r Reservation) Open() money.Qty { return r.Qty.Sub(r.Consumed).Sub(r.Released) }

// Stock is the inventory ledger: moves, reservations and the sellable stock per color.
type Stock interface {
	Issue(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockIssue) ([]StockMove, error)
	Receive(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockReceive) ([]StockMove, error)
	Quarantine(ctx context.Context, tx posting.Tx, doc StockDoc, lines []StockQuarantine) ([]StockMove, error)
	// Reverse books the mirror of every move of doc under by; it fails when doc was reversed.
	Reverse(ctx context.Context, tx posting.Tx, by, doc StockDoc) ([]StockMove, error)
	// MovesOf lists the moves booked under doc, oldest first.
	MovesOf(ctx context.Context, q db.Querier, doc StockDoc) ([]StockMove, error)
	OnHandByColor(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID][]ColorStock, error)
	// Available is the sellable stock of each SKU that no reservation holds (sellable − reserved);
	// a SKU without stock is missing from the map.
	Available(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]money.Qty, error)

	// Reserve holds qty of a SKU for an invoice line.
	Reserve(ctx context.Context, tx posting.Tx, skuID uuid.UUID, qty money.Qty, invoiceLineID uuid.UUID, docNo string) (Reservation, error)
	// ReservationOf is the reservation of an invoice line, nil when it has none.
	ReservationOf(ctx context.Context, q db.Querier, invoiceLineID uuid.UUID) (*Reservation, error)
	Consume(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty money.Qty) error
	// Release frees qty of a reservation; nil frees all it still holds.
	Release(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, qty *money.Qty) error
	// Adjust sets the total quantity of a reservation, reopening a settled one.
	Adjust(ctx context.Context, tx posting.Tx, reservationID uuid.UUID, total money.Qty) error
}

// Debt directions and payment values of the debt ledger sales writes.
const (
	DirectionIn  = "in"
	DirectionOut = "out"

	PurposeSettlement    = "settlement"
	PurposeDeposit       = "deposit"
	PurposeRefund        = "refund"
	PurposeAdvanceRefund = "advance_refund"

	PaymentSourcePOS         = "pos"
	PaymentSourceInvoice     = "invoice"
	PaymentSourceSalesReturn = "sales_return"
	PaymentSourceExchange    = "exchange"
)

// ReceivableInput is what a sales document makes the payer owe.
type ReceivableInput struct {
	PartyID     uuid.UUID
	SourceID    uuid.UUID
	SourceDocNo string
	Amount      money.Money
	DueDate     *time.Time
	Schedule    []Installment
	CampaignID  *uuid.UUID
	// EarmarkedInvoiceID applies the payer's confirmed deposits for that invoice right away.
	EarmarkedInvoiceID *uuid.UUID
}

// Installment is one part of a payment schedule.
type Installment struct {
	DueDate Date        `json:"due_date"`
	Amount  money.Money `json:"amount"`
}

// Obligation is a receivable (or refund due) as sales sees it.
type Obligation struct {
	ID      uuid.UUID
	Amount  money.Money
	Settled money.Money
}

// Open is what is still owed.
func (o Obligation) Open() money.Money { return o.Amount.Sub(o.Settled) }

// PaymentInput is a PT or PC a sales posting records.
type PaymentInput struct {
	Direction string
	// PartyID nil is a walk-in customer (POS sale, its return or exchange).
	PartyID            *uuid.UUID
	Purpose            string
	MethodID           uuid.UUID
	Amount             money.Money
	SourceType         string
	SourceID           *uuid.UUID
	EarmarkedInvoiceID *uuid.UUID
	ShiftID            *uuid.UUID
	RefundOf           *uuid.UUID
	Note               string
	Allocations        []PaymentAllocation
	// Key makes the payment idempotent: a replayed posting records it once and gets the first
	// payment back.
	Key string
}

// DocPrefix is the number prefix of the payment: PT in, PC out.
func (in PaymentInput) DocPrefix() string {
	if in.Direction == DirectionOut {
		return "PC"
	}
	return "PT"
}

// PaymentAllocation applies part of a payment to an obligation.
type PaymentAllocation struct {
	ObligationID uuid.UUID
	Amount       money.Money
}

// Payment is a recorded payment.
type Payment struct {
	ID     uuid.UUID   `json:"id"`
	DocNo  string      `json:"doc_no"`
	Status string      `json:"status"`
	Amount money.Money `json:"amount"`
}

// CreditNoteInput is the credit of a sales return on the customer's receivables.
type CreditNoteInput struct {
	PartyID     uuid.UUID
	SourceID    uuid.UUID
	SourceDocNo string
	Amount      money.Money
	ReasonID    *uuid.UUID
	// Targets are settled first, in order; OnlyTargets turns the rest into a refund due instead
	// of settling other documents of the party.
	Targets     []uuid.UUID
	OnlyTargets bool
}

// CreditNote is a recorded credit note: what it took off the receivables and the refund due it
// left, if any.
type CreditNote struct {
	ID        uuid.UUID
	Settled   money.Money
	RefundDue *Obligation
}

// CreditQuery is a credit-limit check of a payer confirming an order.
type CreditQuery struct {
	PartyID     uuid.UUID
	OrderID     *uuid.UUID
	Undelivered money.Money
	AsOf        time.Time
}

// Debt is the debt ledger.
type Debt interface {
	RecordReceivable(ctx context.Context, tx posting.Tx, in ReceivableInput) (Obligation, error)
	// RecordPayment takes the PT/PC number the posting reserved (in.DocPrefix()).
	RecordPayment(ctx context.Context, tx posting.Tx, in PaymentInput) (Payment, error)
	RecordCreditNote(ctx context.Context, tx posting.Tx, in CreditNoteInput) (CreditNote, error)
	// ReverseReceivable reverses the receivable of a sales document under the posting's number;
	// what payments had settled on it becomes the payer's advance again and is returned. A document
	// with credit notes (a return) fails with 409 obligation_has_credits.
	ReverseReceivable(ctx context.Context, tx posting.Tx, salesDocumentID uuid.UUID, reason string) (money.Money, error)
	// CheckCredit returns the credit_limit_exceeded exception, or nil.
	CheckCredit(ctx context.Context, q db.Querier, in CreditQuery) error
	// ReceivableOf is the receivable of a sales document, nil when it has none.
	ReceivableOf(ctx context.Context, q db.Querier, salesDocumentID uuid.UUID) (*Obligation, error)
	// InvoicesPaid is what the customer paid toward each invoice: its earmarked deposits plus what
	// other payments settled on the receivables of its sales documents that still stand. Every id
	// asked for has an entry.
	InvoicesPaid(ctx context.Context, q db.Querier, invoiceIDs []uuid.UUID) (map[uuid.UUID]InvoicePaid, error)
	// Deposits lists the deposits earmarked for an invoice that were not reversed, oldest first.
	Deposits(ctx context.Context, q db.Querier, invoiceID uuid.UUID) ([]Deposit, error)
	// OpenReceivables is what a party still owes on its open receivables.
	OpenReceivables(ctx context.Context, q db.Querier, partyID uuid.UUID) (money.Money, error)
}

// Deposit is a deposit paid toward an invoice: what was paid, what was refunded of it and what is
// still free to refund or allocate. A pending deposit is a transfer not verified yet.
type Deposit struct {
	ID          uuid.UUID
	DocNo       string
	Amount      money.Money
	Refunded    money.Money
	Unallocated money.Money
	Pending     bool
	// MethodID is how the deposit was paid; a refund of it goes back the same way.
	MethodID uuid.UUID
}

// Kept is what the deposit still counts toward the invoice.
func (d Deposit) Kept() money.Money { return d.Amount.Sub(d.Refunded) }

// InvoicePaid splits the payments of an invoice into confirmed ones and transfers still waiting
// for verification.
type InvoicePaid struct {
	Confirmed money.Money
	Pending   money.Money
}

// PaymentMethod is a payment method and the kind of fund it pays into.
type PaymentMethod struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	FundID   uuid.UUID `json:"fund_account_id"`
	FundKind string    `json:"fund_kind"`
	// Cash is true for methods paying into a cash fund (the only ones an offline sale may use).
	Cash   bool `json:"cash"`
	Active bool `json:"active"`
}

// PaymentMethods reads payment methods.
type PaymentMethods interface {
	Method(ctx context.Context, q db.Querier, id uuid.UUID) (PaymentMethod, error)
	// ActiveMethods lists the active payment methods in display order.
	ActiveMethods(ctx context.Context, q db.Querier) ([]PaymentMethod, error)
}

// ComboLine is one component a combo expands to.
type ComboLine struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	BaseQty money.Qty
}

// ComboExpander expands a combo SKU into its components. Until kits are built every combo is
// rejected.
type ComboExpander interface {
	Expand(ctx context.Context, q db.Querier, comboSkuID uuid.UUID, qty money.Qty) ([]ComboLine, error)
}

// NoCombos rejects every combo with 422 combo_not_sellable.
type NoCombos struct{}

// Expand implements ComboExpander.
func (NoCombos) Expand(context.Context, db.Querier, uuid.UUID, money.Qty) ([]ComboLine, error) {
	return nil, apperr.NewValidation(CodeComboNotSellable, "Combos cannot be sold yet", nil)
}

// CampaignAttributor picks the campaign a sale is attributed to; nil is none.
type CampaignAttributor interface {
	Attribute(ctx context.Context, q db.Querier, partyID *uuid.UUID, businessDate time.Time) (*uuid.UUID, error)
	// AttributeLines gives each line of a posted sales document its primary campaign — the active
	// campaign of the price list or promotion that priced it, else the document's — records it in
	// the posting's transaction q, and returns the campaign of every attributed line. Lines it
	// leaves out keep the document's campaign.
	AttributeLines(ctx context.Context, q db.Querier, doc CampaignDoc) (map[uuid.UUID]*uuid.UUID, error)
}

// CampaignDoc is a sales document being posted, as campaign attribution sees it.
type CampaignDoc struct {
	ID         uuid.UUID
	CampaignID *uuid.UUID
	PostedBy   uuid.UUID
	PostedAt   time.Time
	Lines      []CampaignLine
}

// CampaignLine is one line of a CampaignDoc. A component line (combo component, mix flavor)
// follows the campaign of its parent line.
type CampaignLine struct {
	ID           uuid.UUID
	ParentLineID *uuid.UUID
	PriceSource  json.RawMessage
}

// NoCampaigns attributes no sale to a campaign.
type NoCampaigns struct{}

// Attribute implements CampaignAttributor.
func (NoCampaigns) Attribute(context.Context, db.Querier, *uuid.UUID, time.Time) (*uuid.UUID, error) {
	return nil, nil
}

// AttributeLines implements CampaignAttributor: every line keeps the document's campaign.
func (NoCampaigns) AttributeLines(context.Context, db.Querier, CampaignDoc) (map[uuid.UUID]*uuid.UUID, error) {
	return nil, nil
}

// Loyalty spends a customer's loyalty points on a sale inside its posting.
type Loyalty interface {
	// Redeem books points of partyID spent on the sales document docID in tx and returns the order
	// discount they are worth. It refuses with 409 when the program is off or the balance is short.
	Redeem(ctx context.Context, tx posting.Tx, partyID uuid.UUID, points money.Qty, docID uuid.UUID) (money.Money, error)
}

// NoLoyalty has no loyalty program: every redemption is refused.
type NoLoyalty struct{}

// Redeem implements Loyalty.
func (NoLoyalty) Redeem(context.Context, posting.Tx, uuid.UUID, money.Qty, uuid.UUID) (money.Money, error) {
	return money.Money{}, apperr.NewConflict(CodeLoyaltyDisabled, "The loyalty program is off")
}

// OfflineSessions grants and reads the offline POS window of a login session. Sessions live in the
// platform schema, so the store runs its own transaction.
type OfflineSessions interface {
	// OfflineUntil is the end of the session's offline window, nil when it has none or the session
	// is revoked or expired.
	OfflineUntil(ctx context.Context, tenantID, sessionID uuid.UUID) (*time.Time, error)
	// GrantOffline sets the session's offline window to until, capped at the session's expiry, and
	// returns the window set.
	GrantOffline(ctx context.Context, tenantID, sessionID uuid.UUID, until time.Time) (time.Time, error)
}
