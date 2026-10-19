package pricing

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// MinPriceViolation is an item line sold below its SKU's minimum price.
type MinPriceViolation struct {
	// DraftIndex is the position of the draft line.
	DraftIndex   int
	SkuID        uuid.UUID
	NetUnitPrice money.Money
	MinPrice     money.Money
}

// MinPriceConfirmation is the owner confirmation a document presents to sell below the minimum
// price, and the document it is checked against and spent on.
type MinPriceConfirmation struct {
	ConfirmationID uuid.UUID
	DocType        string
	DocID          uuid.UUID
	DocVersion     int
	// PayloadHash is exception.PayloadHash of the request body.
	PayloadHash   []byte
	UsedByDocType string
	UsedByDocID   uuid.UUID
}

// MinPriceGuard stops a document that sells a line below its SKU's minimum price (net price per
// base unit after every discount, before tax is added) unless the owner confirmed it.
type MinPriceGuard struct {
	Exceptions *exception.Guard
}

// Violations lists the item lines of doc below their minimum price.
func (MinPriceGuard) Violations(doc CalculatedDocument) []MinPriceViolation {
	var out []MinPriceViolation
	for _, l := range doc.Lines {
		if l.LineKind == LineItem && l.BelowMinPrice && l.MinPriceSnap != nil {
			out = append(out, MinPriceViolation{DraftIndex: l.DraftIndex, SkuID: l.SkuID, NetUnitPrice: l.NetUnitPrice, MinPrice: *l.MinPriceSnap})
		}
	}
	return out
}

// errBelowMinPrice asks the owner to confirm the below-minimum lines (each named in the fields).
func errBelowMinPrice(violations []MinPriceViolation) error {
	e := apperr.NewExceptionRequired(string(exception.BelowMinPrice),
		"Some lines are below the minimum price; the owner must confirm before saving")
	e.Fields = make(map[string]string, len(violations))
	for _, v := range violations {
		e.Fields[lineField(v.DraftIndex, "unit_price")] = string(exception.BelowMinPrice)
	}
	return e
}

// Enforce passes a document without below-minimum lines; otherwise it consumes the confirmation
// inside the caller's tenant transaction. A missing, expired or mismatched confirmation answers
// exception_required for below_min_price with the offending lines; a used one answers
// exception_confirmation_used.
func (g MinPriceGuard) Enforce(ctx context.Context, q db.Querier, tenantID uuid.UUID, doc CalculatedDocument, conf MinPriceConfirmation) error {
	violations := g.Violations(doc)
	if len(violations) == 0 {
		return nil
	}
	if conf.ConfirmationID == uuid.Nil {
		return errBelowMinPrice(violations)
	}
	err := g.Exceptions.Consume(ctx, q, tenantID, exception.Expectation{
		ConfirmationID: conf.ConfirmationID, Kind: exception.BelowMinPrice,
		DocType: conf.DocType, DocID: conf.DocID, DocVersion: conf.DocVersion, PayloadHash: conf.PayloadHash,
		UsedByDocType: conf.UsedByDocType, UsedByDocID: conf.UsedByDocID,
	})
	var ae *apperr.Error
	if errors.As(err, &ae) && ae.Kind == apperr.ExceptionRequired {
		return errBelowMinPrice(violations)
	}
	return err
}

// EnforceMinPrice runs the minimum-price guard on a calculated document inside the caller's
// tenant transaction; sales posting commands call it before saving.
func (s *Service) EnforceMinPrice(ctx context.Context, q db.Querier, tenantID uuid.UUID, doc CalculatedDocument, conf MinPriceConfirmation) error {
	return s.guard.Enforce(ctx, q, tenantID, doc, conf)
}
