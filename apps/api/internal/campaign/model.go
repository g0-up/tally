// Package campaign implements seasons and promotions as campaigns: their period, budget and
// targets, the price lists and promotions they own, the checklists before the peak and at close,
// the attribution of financial documents to them (one primary campaign per line, tags that only
// filter) and the demand tracking that compares confirmed demand with stock and pending purchase
// orders. Closing a campaign never touches stock, cash or debt.
package campaign

import (
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Campaign kinds.
const (
	KindTet       = "tet"
	KindSeason    = "season"
	KindPromotion = "promotion"
	KindOther     = "other"
)

func validKind(k string) bool {
	switch k {
	case KindTet, KindSeason, KindPromotion, KindOther:
		return true
	}
	return false
}

// Campaign statuses: planning → active → closed.
const (
	StatusPlanning = "planning"
	StatusActive   = "active"
	StatusClosed   = "closed"
)

func validStatus(s string) bool {
	switch s {
	case StatusPlanning, StatusActive, StatusClosed:
		return true
	}
	return false
}

// Checklist phases and item kinds.
const (
	PhaseBeforePeak = "before_peak"
	PhaseClosing    = "closing"

	ItemStockCount   = "stock_count"
	ItemStatement    = "statement"
	ItemOpenOrders   = "open_orders"
	ItemSurplusStock = "surplus_stock"
	ItemCustom       = "custom"
)

func validPhase(p string) bool { return p == PhaseBeforePeak || p == PhaseClosing }

// Attribution document types and roles.
const (
	DocSalesDocument   = "sales_document"
	DocSalesReturn     = "sales_return"
	DocReceipt         = "receipt"
	DocAssemblyPosting = "assembly_posting"
	DocPayment         = "payment"

	RolePrimary = "primary"
	RoleTag     = "tag"
)

// Campaign is a season, Tết or promotion with its period, budget and targets.
type Campaign struct {
	ID                  uuid.UUID      `db:"id"`
	Name                string         `db:"name"`
	Kind                string         `db:"kind"`
	StartsOn            time.Time      `db:"starts_on"`
	EndsOn              time.Time      `db:"ends_on"`
	Budget              *money.Money   `db:"budget_amount"`
	TargetRevenue       *money.Money   `db:"target_revenue"`
	TargetMarginPercent *money.Percent `db:"target_margin_percent"`
	Status              string         `db:"status"`
	ClosedAt            *time.Time     `db:"closed_at"`
	CreatedAt           time.Time      `db:"created_at"`
	UpdatedAt           time.Time      `db:"updated_at"`
	Version             int            `db:"version"`
}

// Detail is a campaign with the price lists and promotions linked to it and its checklist.
type Detail struct {
	Campaign
	PriceListIDs []uuid.UUID
	PromotionIDs []uuid.UUID
	Checklist    []ChecklistItem
}

// ChecklistItem is one reminder of a campaign checklist.
type ChecklistItem struct {
	ID        uuid.UUID  `db:"id"`
	Phase     string     `db:"phase"`
	Kind      string     `db:"kind"`
	Title     string     `db:"title"`
	DoneAt    *time.Time `db:"done_at"`
	DoneBy    *uuid.UUID `db:"done_by"`
	SortOrder int        `db:"sort_order"`
	CreatedAt time.Time  `db:"created_at"`
}

// ChecklistSummary counts the done and open items of a checklist; Open lists the open ones.
type ChecklistSummary struct {
	Total int
	Done  int
	Open  []ChecklistItem
}

func summarize(items []ChecklistItem) ChecklistSummary {
	out := ChecklistSummary{Total: len(items), Open: []ChecklistItem{}}
	for _, it := range items {
		if it.DoneAt != nil {
			out.Done++
			continue
		}
		out.Open = append(out.Open, it)
	}
	return out
}

// seedItem is a checklist item every new campaign starts with.
type seedItem struct {
	Phase string
	Kind  string
	Title string
}

// seedChecklist reminds the counts, statements, open orders and surplus stock of a season; the
// titles are shown to the owner as written.
var seedChecklist = []seedItem{
	{PhaseBeforePeak, ItemStockCount, "Kiểm kê tồn kho trước cao điểm"},
	{PhaseBeforePeak, ItemOpenOrders, "Rà soát đơn hàng đã xác nhận chưa giao"},
	{PhaseClosing, ItemStockCount, "Kiểm kê tồn kho cuối mùa"},
	{PhaseClosing, ItemStatement, "Gửi đối chiếu công nợ cho khách và nhà cung cấp"},
	{PhaseClosing, ItemOpenOrders, "Xử lý đơn hàng và đơn mua còn mở"},
	{PhaseClosing, ItemSurplusStock, "Xử lý hàng tồn dư sau mùa"},
}

// Attribution links a financial document, or one of its lines, to a campaign.
type Attribution struct {
	ID         uuid.UUID  `db:"id"`
	CampaignID uuid.UUID  `db:"campaign_id"`
	DocType    string     `db:"doc_type"`
	DocID      uuid.UUID  `db:"doc_id"`
	DocLineID  *uuid.UUID `db:"doc_line_id"`
	Role       string     `db:"role"`
	CreatedBy  uuid.UUID  `db:"created_by"`
	CreatedAt  time.Time  `db:"created_at"`
}
