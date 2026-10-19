package seedload

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// DocKind is a kind of document the generator posts.
type DocKind string

// The document kinds, in the order the summary prints them.
const (
	DocReceipts        DocKind = "receipts"
	DocSupplierReturns DocKind = "supplier_returns"
	DocPosSales        DocKind = "pos_sales"
	DocSalesReturns    DocKind = "sales_returns"
	DocInvoices        DocKind = "invoices"
	DocStockOuts       DocKind = "stock_outs"
	DocPayments        DocKind = "payments"
	DocCounts          DocKind = "counts"
	DocKitBatches      DocKind = "kit_batches"
)

// DocKinds lists every document kind in summary order.
var DocKinds = []DocKind{
	DocReceipts, DocSupplierReturns, DocPosSales, DocSalesReturns, DocInvoices, DocStockOuts,
	DocPayments, DocCounts, DocKitBatches,
}

// Summary is what one tenant's run posted.
type Summary struct {
	Slug   string
	Skus   int
	Rounds int
	Docs   map[DocKind]int
	// Moves is the stock moves the tenant holds after the last round.
	Moves   int64
	Elapsed time.Duration
}

// Write prints the summary on one line.
func (s Summary) Write(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "tenant %s: skus=%d rounds=%d", s.Slug, s.Skus, s.Rounds)
	for _, k := range DocKinds {
		fmt.Fprintf(&b, " %s=%d", k, s.Docs[k])
	}
	secs := s.Elapsed.Seconds()
	rate := 0.0
	if secs > 0 {
		rate = float64(s.Moves) / secs
	}
	fmt.Fprintf(&b, " stock_moves=%d elapsed=%s moves_per_second=%.0f\n", s.Moves, s.Elapsed.Round(time.Millisecond), rate)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("seedload: write summary: %w", err)
	}
	return nil
}
