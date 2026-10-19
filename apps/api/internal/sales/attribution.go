package sales

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/posting"
)

// LineCampaign is the primary campaign of one line of a posted sales document, as sale.posted
// carries it.
type LineCampaign struct {
	LineID     uuid.UUID  `json:"sales_document_line_id"`
	CampaignID *uuid.UUID `json:"campaign_id"`
}

// attributeLines records the primary campaign of every line of a sales document being posted and
// sets it on the lines before they are written. A line the attributor leaves out keeps the
// document's campaign.
func (s *Service) attributeLines(ctx context.Context, tx posting.Tx, docID uuid.UUID, docCampaign *uuid.UUID, lines []DocumentLine) ([]LineCampaign, error) {
	in := CampaignDoc{ID: docID, CampaignID: docCampaign, PostedBy: tx.Actor().ID, PostedAt: tx.Now(),
		Lines: make([]CampaignLine, len(lines))}
	for i, l := range lines {
		in.Lines[i] = CampaignLine{ID: l.ID, ParentLineID: l.ParentLineID, PriceSource: json.RawMessage(l.PriceSource)}
	}
	got, err := s.campaigns.AttributeLines(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	out := make([]LineCampaign, len(lines))
	for i := range lines {
		c, ok := got[lines[i].ID]
		if !ok {
			c = docCampaign
		}
		lines[i].CampaignID = c
		out[i] = LineCampaign{LineID: lines[i].ID, CampaignID: c}
	}
	return out, nil
}
