package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/sales"
)

// salesCampaigns is campaign attribution as sales posting sees it.
type salesCampaigns struct{ s *campaign.Service }

func (c salesCampaigns) Attribute(ctx context.Context, q db.Querier, partyID *uuid.UUID, businessDate time.Time) (*uuid.UUID, error) {
	return c.s.Attribute(ctx, q, partyID, businessDate)
}

func (c salesCampaigns) AttributeLines(ctx context.Context, q db.Querier, doc sales.CampaignDoc) (map[uuid.UUID]*uuid.UUID, error) {
	lines := make([]campaign.SalesLine, len(doc.Lines))
	for i, l := range doc.Lines {
		lines[i] = campaign.SalesLine{ID: l.ID, ParentLineID: l.ParentLineID, PriceSource: l.PriceSource}
	}
	return c.s.AttributeLines(ctx, q, campaign.SalesDoc{
		ID: doc.ID, CampaignID: doc.CampaignID, PostedBy: doc.PostedBy, PostedAt: doc.PostedAt, Lines: lines,
	})
}
