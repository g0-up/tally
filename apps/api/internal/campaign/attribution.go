package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Attribute picks the campaign a sales document dated businessDate belongs to: the active
// campaign whose period covers the date, a Tết or season campaign before a promotion, then the
// latest start. Nil when none is active.
func (s *Service) Attribute(ctx context.Context, q db.Querier, _ *uuid.UUID, businessDate time.Time) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `
		SELECT id FROM campaigns
		WHERE status = 'active' AND $1::date BETWEEN starts_on AND ends_on
		ORDER BY kind IN ('tet', 'season') DESC, starts_on DESC, id
		LIMIT 1`, businessDate).Scan(&id)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("campaign: attribute: %w", err)
	}
	return &id, nil
}

// SalesDoc is a sales document being posted, as attribution sees it.
type SalesDoc struct {
	ID         uuid.UUID
	CampaignID *uuid.UUID
	PostedBy   uuid.UUID
	PostedAt   time.Time
	Lines      []SalesLine
}

// SalesLine is one line of a sales document being posted. PriceSource is the line's snapshot of
// where its price came from: {price_list_id, promotions: [{id}]}.
type SalesLine struct {
	ID           uuid.UUID
	ParentLineID *uuid.UUID
	PriceSource  json.RawMessage
}

type priceSource struct {
	PriceListID *uuid.UUID `json:"price_list_id"`
	Promotions  []struct {
		ID uuid.UUID `json:"id"`
	} `json:"promotions"`
}

// AttributeLines records the primary campaign of each line of a sales document in its posting
// transaction and returns it by line id. A line takes the active campaign of its price list, else
// of the first of its promotions that has one, else the document's campaign; component and
// flavor lines take their parent's. Lines without a campaign are absent from the result.
func (s *Service) AttributeLines(ctx context.Context, q db.Querier, doc SalesDoc) (map[uuid.UUID]*uuid.UUID, error) {
	sources := make(map[uuid.UUID]priceSource, len(doc.Lines))
	var listIDs, promoIDs []uuid.UUID
	for _, l := range doc.Lines {
		var src priceSource
		if len(l.PriceSource) > 0 {
			// A source this module cannot read only loses its campaign hint.
			_ = json.Unmarshal(l.PriceSource, &src)
		}
		sources[l.ID] = src
		if src.PriceListID != nil {
			listIDs = append(listIDs, *src.PriceListID)
		}
		for _, p := range src.Promotions {
			promoIDs = append(promoIDs, p.ID)
		}
	}
	byList, err := activeCampaignsOf(ctx, q, "price_lists", listIDs)
	if err != nil {
		return nil, err
	}
	byPromo, err := activeCampaignsOf(ctx, q, "promotions", promoIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]*uuid.UUID, len(doc.Lines))
	pick := func(l SalesLine) *uuid.UUID {
		src := sources[l.ID]
		if src.PriceListID != nil {
			if c, ok := byList[*src.PriceListID]; ok {
				return &c
			}
		}
		for _, p := range src.Promotions {
			if c, ok := byPromo[p.ID]; ok {
				return &c
			}
		}
		return doc.CampaignID
	}
	for _, l := range doc.Lines {
		if l.ParentLineID == nil {
			out[l.ID] = pick(l)
		}
	}
	for _, l := range doc.Lines {
		if l.ParentLineID != nil {
			c, ok := out[*l.ParentLineID]
			if !ok {
				c = pick(l)
			}
			out[l.ID] = c
		}
	}
	for _, l := range doc.Lines {
		c := out[l.ID]
		if c == nil {
			delete(out, l.ID)
			continue
		}
		lineID := l.ID
		if err := insertAttribution(ctx, q, Attribution{
			ID: ids.New(), CampaignID: *c, DocType: DocSalesDocument, DocID: doc.ID, DocLineID: &lineID,
			Role: RolePrimary, CreatedBy: doc.PostedBy, CreatedAt: doc.PostedAt,
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// activeCampaignsOf maps rows of table (price_lists or promotions) to their active campaign.
func activeCampaignsOf(ctx context.Context, q db.Querier, table string, rowIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(rowIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		RowID      uuid.UUID `db:"row_id"`
		CampaignID uuid.UUID `db:"campaign_id"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT t.id AS row_id, c.id AS campaign_id
		FROM `+table+` t JOIN campaigns c ON c.id = t.campaign_id
		WHERE t.id = ANY($1) AND c.status = 'active'`, rowIDs); err != nil {
		return nil, fmt.Errorf("campaign: campaigns of %s: %w", table, err)
	}
	for _, r := range rows {
		out[r.RowID] = r.CampaignID
	}
	return out, nil
}

// TagReceipt tags a receipt (PN) with the campaign its goods were bought for. A tag only filters
// reports: the cost enters a season when the goods are sold.
func (s *Service) TagReceipt(ctx context.Context, tx posting.Tx, receiptID, campaignID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, campaignID).Scan(&exists); err != nil {
		return fmt.Errorf("campaign: tag receipt: %w", err)
	}
	if !exists {
		return errInvalid(map[string]string{"campaign_id": "not_found"})
	}
	return insertAttribution(ctx, tx, Attribution{
		ID: ids.New(), CampaignID: campaignID, DocType: DocReceipt, DocID: receiptID,
		Role: RoleTag, CreatedBy: tx.Actor().ID, CreatedAt: tx.Now(),
	})
}

func insertAttribution(ctx context.Context, q db.Querier, a Attribution) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO campaign_attributions (id, tenant_id, campaign_id, doc_type, doc_id, doc_line_id, role, created_by, created_at)
		VALUES ($1, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT ON CONSTRAINT ux_campaign_attributions__campaign_doc_line DO NOTHING`,
		a.ID, a.CampaignID, a.DocType, a.DocID, a.DocLineID, a.Role, a.CreatedBy, a.CreatedAt); err != nil {
		if db.IsUniqueViolation(err, "ux_campaign_attributions__one_primary") {
			return errAttributionChanged()
		}
		return fmt.Errorf("campaign: insert attribution: %w", err)
	}
	return nil
}

func errAttributionChanged() error {
	return apperr.NewConflict("attribution_changed", "The document was attributed meanwhile; send the request again")
}

// ReattributeInput moves a document or line to a campaign. Role primary replaces the current
// primary campaign; a sales document without a line moves all its lines. Role tag adds a filter
// tag.
type ReattributeInput struct {
	CampaignID uuid.UUID
	DocType    string
	DocID      uuid.UUID
	DocLineID  *uuid.UUID
	Role       string
}

// ReattributeResult lists the attributions of the document after the change and the warnings.
type ReattributeResult struct {
	Attributions []Attribution
	Warnings     []string
}

// docTables are the document and line tables of each attributable document type.
var docTables = map[string]struct{ doc, lines, parent string }{
	DocSalesDocument:   {"sales_documents", "sales_document_lines", "sales_document_id"},
	DocSalesReturn:     {"sales_returns", "sales_return_lines", "sales_return_id"},
	DocReceipt:         {"receipts", "receipt_lines", "receipt_id"},
	DocAssemblyPosting: {"assembly_postings", "", ""},
	DocPayment:         {"payments", "", ""},
}

// Reattribute changes the campaign of a posted document with audit. Ledgers and the line's
// recorded campaign at posting stay as they are: reports read the attributions. A document dated
// outside the campaign period is allowed and warned.
func (s *Service) Reattribute(ctx context.Context, scope Scope, actor audit.Actor, in ReattributeInput) (ReattributeResult, error) {
	fields := map[string]string{}
	t, ok := docTables[in.DocType]
	if !ok {
		fields["doc_type"] = "invalid"
	}
	if in.Role != RolePrimary && in.Role != RoleTag {
		fields["role"] = "invalid"
	}
	if in.DocLineID != nil && ok && t.lines == "" {
		fields["doc_line_id"] = "not_allowed"
	}
	if len(fields) > 0 {
		return ReattributeResult{}, errInvalid(fields)
	}
	var out ReattributeResult
	err := s.db.WithTenantTx(ctx, scope.TenantID, func(q db.Querier) error {
		c, err := loadCampaign(ctx, q, in.CampaignID, false)
		if db.IsNoRows(err) || apperr.KindOf(err) == apperr.NotFound {
			return errInvalid(map[string]string{"campaign_id": "not_found"})
		}
		if err != nil {
			return err
		}
		var businessDate time.Time
		if err := q.QueryRowxContext(ctx, `SELECT business_date FROM `+t.doc+` WHERE id = $1 FOR UPDATE`, in.DocID).Scan(&businessDate); err != nil {
			if db.IsNoRows(err) {
				return errInvalid(map[string]string{"doc_id": "not_found"})
			}
			return fmt.Errorf("campaign: read document: %w", err)
		}
		targets := []*uuid.UUID{in.DocLineID}
		if in.DocLineID != nil || (in.Role == RolePrimary && in.DocType == DocSalesDocument) {
			if targets, err = lineTargets(ctx, q, t.lines, t.parent, in); err != nil {
				return err
			}
		}
		before, err := listAttributions(ctx, q, in.DocType, in.DocID)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		for _, line := range targets {
			if in.Role == RolePrimary {
				if _, err := q.ExecContext(ctx, `
					DELETE FROM campaign_attributions
					WHERE doc_type = $1 AND doc_id = $2 AND doc_line_id IS NOT DISTINCT FROM $3
						AND (role = 'primary' OR campaign_id = $4)`,
					in.DocType, in.DocID, line, in.CampaignID); err != nil {
					return fmt.Errorf("campaign: clear primary: %w", err)
				}
			}
			if err := insertAttribution(ctx, q, Attribution{
				ID: ids.New(), CampaignID: in.CampaignID, DocType: in.DocType, DocID: in.DocID, DocLineID: line,
				Role: in.Role, CreatedBy: actor.ID, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		if out.Attributions, err = listAttributions(ctx, q, in.DocType, in.DocID); err != nil {
			return err
		}
		out.Warnings = []string{}
		if businessDate.Before(c.StartsOn) || businessDate.After(c.EndsOn) {
			out.Warnings = append(out.Warnings, WarnOutsidePeriod)
		}
		return s.audit.Record(ctx, q, actor, audit.Event{
			Action: "campaign.reattributed", EntityType: in.DocType, EntityID: in.DocID,
			Before: before, After: map[string]any{"attributions": out.Attributions, "warnings": out.Warnings},
		})
	})
	return out, err
}

// lineTargets are the lines a re-attribution applies to: the given line and its component or
// flavor lines, or every line of a sales document moved as a whole.
func lineTargets(ctx context.Context, q db.Querier, table, parent string, in ReattributeInput) ([]*uuid.UUID, error) {
	var lines []uuid.UUID
	var err error
	switch {
	case in.DocLineID == nil:
		err = sqlx.SelectContext(ctx, q, &lines, `SELECT id FROM `+table+` WHERE `+parent+` = $1 ORDER BY id`, in.DocID)
	case table == "sales_document_lines":
		err = sqlx.SelectContext(ctx, q, &lines, `
			SELECT id FROM sales_document_lines
			WHERE sales_document_id = $1 AND (id = $2 OR parent_line_id = $2) ORDER BY id`, in.DocID, *in.DocLineID)
	default:
		err = sqlx.SelectContext(ctx, q, &lines, `SELECT id FROM `+table+` WHERE `+parent+` = $1 AND id = $2`, in.DocID, *in.DocLineID)
	}
	if err != nil {
		return nil, fmt.Errorf("campaign: read lines: %w", err)
	}
	if len(lines) == 0 {
		if in.DocLineID != nil {
			return nil, errInvalid(map[string]string{"doc_line_id": "not_found"})
		}
		return []*uuid.UUID{nil}, nil
	}
	out := make([]*uuid.UUID, len(lines))
	for i := range lines {
		out[i] = &lines[i]
	}
	return out, nil
}

func listAttributions(ctx context.Context, q db.Querier, docType string, docID uuid.UUID) ([]Attribution, error) {
	out := []Attribution{}
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT id, campaign_id, doc_type, doc_id, doc_line_id, role, created_by, created_at
		FROM campaign_attributions WHERE doc_type = $1 AND doc_id = $2
		ORDER BY doc_line_id NULLS FIRST, role, campaign_id`, docType, docID); err != nil {
		return nil, fmt.Errorf("campaign: list attributions: %w", err)
	}
	return out, nil
}
