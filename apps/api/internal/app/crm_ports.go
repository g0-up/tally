package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// crmDrafts is sales invoicing as the CRM re-order sees it: a draft invoice priced now.
type crmDrafts struct{ s *sales.Service }

func (d crmDrafts) DraftInvoice(ctx context.Context, q db.Querier, actor audit.Actor, customerID uuid.UUID, lines []crm.DraftLine, note string) (crm.Draft, error) {
	in := sales.InvoiceInput{CustomerID: customerID, Note: &note}
	for _, l := range lines {
		skuID, unitID := l.SkuID, l.UnitID
		in.Lines = append(in.Lines, sales.LineInput{SkuID: &skuID, ColorID: l.ColorID, UnitID: &unitID, Qty: l.Qty})
	}
	inv, err := d.s.CreateInvoiceIn(ctx, q, actor, in)
	if err != nil {
		return crm.Draft{}, err
	}
	return crm.Draft{InvoiceID: inv.ID, DocNo: inv.DocNo, TotalAmount: inv.Total, Version: inv.Version}, nil
}

// salesLoyalty is the loyalty ledger as the POS sees it: points spent at checkout in the sale's
// posting, booked on its date.
type salesLoyalty struct{}

func (salesLoyalty) Redeem(ctx context.Context, tx posting.Tx, partyID uuid.UUID, points money.Qty, docID uuid.UUID) (money.Money, error) {
	by := tx.Actor().ID
	r, err := crm.Redeem(ctx, tx, crm.RedeemInput{
		PartyID: partyID, Points: points, SourceID: docID, By: &by, At: tx.Now(), BusinessDate: tx.BusinessDate(),
	})
	if err != nil {
		return money.Money{}, err
	}
	return r.Value, nil
}
