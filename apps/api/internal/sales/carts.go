package sales

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Statuses of a saved POS cart.
const (
	CartOpen = "open"
	CartSold = "sold"
)

// CartInput is the whole content of a saved POS cart (GH): its customer (nil is a walk-in), label
// and lines. Saving a cart prices it but reserves no stock and posts nothing; an update replaces
// all of it.
type CartInput struct {
	CustomerID *uuid.UUID `json:"customer_party_id,omitempty"`
	Label      *string    `json:"label,omitempty"`
	DraftInput
}

// CartCheckoutInput sells a saved cart: how it is paid and the promotion codes typed at the
// counter. The lines are the cart's, priced again at checkout.
type CartCheckoutInput struct {
	Payments       []PosPayment `json:"payments"`
	CashTendered   *money.Money `json:"cash_tendered_amount,omitempty"`
	PromotionCodes []string     `json:"promotion_codes,omitempty"`
	// LoyaltyPoints are whole points the cart's customer spends on the sale.
	LoyaltyPoints *money.Qty `json:"loyalty_points,omitempty"`
}

// CartFilter narrows the cart list.
type CartFilter struct {
	Status string
	Page   ListPage
}

func validateCart(in CartInput) error {
	fields := map[string]string{}
	mergeFields(fields, validateDraft(in.DraftInput))
	checkText(fields, "label", in.Label, maxLabelLen)
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// CreateCart saves a POS cart priced for its customer today.
func (s *Service) CreateCart(ctx context.Context, actor audit.Actor, in CartInput) (Cart, error) {
	in.CustomerID = nonNilID(in.CustomerID)
	if err := validateCart(in); err != nil {
		return Cart{}, err
	}
	var out Cart
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		built, err := s.buildCart(ctx, q, actor.TenantID, in)
		if err != nil {
			return err
		}
		docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixPOSCart)
		if err != nil {
			return err
		}
		c := Cart{ID: ids.New(), DocNo: docNo, CustomerID: in.CustomerID, Label: in.Label, Total: built.Totals.Total}
		if err := s.insertCart(ctx, q, actor.TenantID, c, built.Lines); err != nil {
			return err
		}
		if out, err = cartDetail(ctx, q, c.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionCartCreate, EntityType: entityCart, EntityID: c.ID, DocNo: docNo, After: out})
	})
	return out, err
}

// buildCart checks the cart's customer and prices its lines today.
func (s *Service) buildCart(ctx context.Context, q db.Querier, tenantID uuid.UUID, in CartInput) (builtDoc, error) {
	if in.CustomerID != nil {
		if err := checkCustomer(ctx, q, *in.CustomerID, "customer_party_id"); err != nil {
			return builtDoc{}, err
		}
	}
	today, err := s.today(ctx, q, tenantID)
	if err != nil {
		return builtDoc{}, err
	}
	return s.buildLines(ctx, q, tenantID, lineBuild{PartyID: in.CustomerID, Date: today, Draft: in.DraftInput})
}

func (s *Service) insertCart(ctx context.Context, q db.Querier, tenantID uuid.UUID, c Cart, lines []Line) error {
	now := s.clock.Now()
	if _, err := q.ExecContext(ctx, `
		INSERT INTO pos_carts (id, tenant_id, doc_no, customer_party_id, label, status, total_amount, copied_from_id,
		                       created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, 'open', $6, $7, $8, $8, 1)`,
		c.ID, tenantID, c.DocNo, c.CustomerID, c.Label, c.Total, c.CopiedFromID, now); err != nil {
		return wrap("insert pos cart", err)
	}
	return cartLines.insert(ctx, q, tenantID, c.ID, lines, nil)
}

// lockCart reads an open cart at version for update.
func lockCart(ctx context.Context, q db.Querier, id uuid.UUID, version int) (Cart, error) {
	c, err := getCart(ctx, q, id, true)
	if err != nil {
		return c, err
	}
	if c.Version != version {
		return c, errStale()
	}
	if c.Status != CartOpen {
		return c, errConflict(CodeCartSold, "The cart was already sold; copy it to sell it again")
	}
	return c, nil
}

// UpdateCart replaces the content of an open cart at version and prices it again today.
func (s *Service) UpdateCart(ctx context.Context, actor audit.Actor, id uuid.UUID, version int, in CartInput) (Cart, error) {
	in.CustomerID = nonNilID(in.CustomerID)
	if err := validateCart(in); err != nil {
		return Cart{}, err
	}
	var out Cart
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		c, err := lockCart(ctx, q, id, version)
		if err != nil {
			return err
		}
		before, err := cartDetail(ctx, q, id)
		if err != nil {
			return err
		}
		built, err := s.buildCart(ctx, q, actor.TenantID, in)
		if err != nil {
			return err
		}
		if err := cartLines.remove(ctx, q, id); err != nil {
			return err
		}
		if err := cartLines.insert(ctx, q, actor.TenantID, id, built.Lines, nil); err != nil {
			return err
		}
		if _, err := db.UpdateWithVersion(ctx, q, "pos_carts", id, version, s.clock.Now(),
			db.Set{Column: "customer_party_id", Value: in.CustomerID}, db.Set{Column: "label", Value: in.Label},
			db.Set{Column: "total_amount", Value: built.Totals.Total}); err != nil {
			return err
		}
		if out, err = cartDetail(ctx, q, id); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionCartUpdate, EntityType: entityCart, EntityID: id, DocNo: c.DocNo,
			Before: before, After: out})
	})
	return out, err
}

// DeleteCart deletes an open cart at version.
func (s *Service) DeleteCart(ctx context.Context, actor audit.Actor, id uuid.UUID, version int) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		c, err := lockCart(ctx, q, id, version)
		if err != nil {
			return err
		}
		before, err := cartDetail(ctx, q, id)
		if err != nil {
			return err
		}
		if err := cartLines.remove(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM pos_carts WHERE id = $1`, id); err != nil {
			return wrap("delete pos cart", err)
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionCartDelete, EntityType: entityCart, EntityID: id, DocNo: c.DocNo, Before: before})
	})
}

// CopyCart starts a new open cart from any cart (a sold one is sold again this way), repriced
// today.
func (s *Service) CopyCart(ctx context.Context, actor audit.Actor, id uuid.UUID) (Cart, error) {
	var out Cart
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		src, err := cartDetail(ctx, q, id)
		if err != nil {
			return err
		}
		in := CartInput{CustomerID: src.CustomerID, Label: src.Label,
			DraftInput: DraftInput{Lines: draftOf(src.Lines), OrderDiscount: orderDiscountOf(src.Lines)}}
		built, err := s.buildCart(ctx, q, actor.TenantID, in)
		if err != nil {
			return err
		}
		docNo, err := s.docs.Next(ctx, q, actor.TenantID, docseq.PrefixPOSCart)
		if err != nil {
			return err
		}
		c := Cart{ID: ids.New(), DocNo: docNo, CustomerID: src.CustomerID, Label: src.Label, Total: built.Totals.Total,
			CopiedFromID: &src.ID}
		if err := s.insertCart(ctx, q, actor.TenantID, c, built.Lines); err != nil {
			return err
		}
		if out, err = cartDetail(ctx, q, c.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, audit.Event{Action: ActionCartCreate, EntityType: entityCart, EntityID: c.ID, DocNo: docNo, After: out})
	})
	return out, err
}

// Cart returns a cart with its lines.
func (s *Service) Cart(ctx context.Context, scope Scope, id uuid.UUID) (Cart, error) {
	var out Cart
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = cartDetail(ctx, q, id)
		return err
	})
	return out, err
}

func cartDetail(ctx context.Context, q db.Querier, id uuid.UUID) (Cart, error) {
	c, err := getCart(ctx, q, id, false)
	if err != nil {
		return c, err
	}
	c.Lines, err = cartLinesOf(ctx, q, id)
	return c, err
}

// Carts lists carts newest first, without lines.
func (s *Service) Carts(ctx context.Context, scope Scope, f CartFilter) ([]Cart, error) {
	var out []Cart
	err := s.read(ctx, scope, func(q db.Querier) error {
		var w where
		switch f.Status {
		case "":
		case CartOpen, CartSold:
			w.add("status = $%d", f.Status)
		default:
			return errInvalid(map[string]string{"status": "unknown"})
		}
		tail := w.page(f.Page, "")
		if err := sqlx.SelectContext(ctx, q, &out, `SELECT `+cartColumns+` FROM pos_carts`+w.sql()+tail, w.args...); err != nil {
			return wrap("list pos carts", err)
		}
		return nil
	})
	return out, err
}

// CheckoutCart sells an open cart at version like a direct checkout of its lines; the cart
// becomes sold with the HD in the same posting. A confirmation the sale needs binds the cart at
// that version.
func (s *Service) CheckoutCart(ctx context.Context, actor audit.Actor, key idempotency.Key, id uuid.UUID, version int,
	in CartCheckoutInput, exc Exceptions,
) (posting.Response, error) {
	var c Cart
	if err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		c, err = cartDetail(ctx, q, id)
		return err
	}); err != nil {
		return posting.Response{}, err
	}
	draft := DraftInput{Lines: draftOf(c.Lines), OrderDiscount: orderDiscountOf(c.Lines), PromotionCodes: in.PromotionCodes}
	if err := validateCheckout(CheckoutInput{CustomerID: c.CustomerID, DraftInput: draft, Payments: in.Payments,
		CashTendered: in.CashTendered, LoyaltyPoints: in.LoyaltyPoints}); err != nil {
		return posting.Response{}, err
	}
	sl := sale{
		scope: ScopePOSCheckout, source: SourcePOS, customer: c.CustomerID, draft: draft, payments: in.Payments,
		tendered: in.CashTendered, points: in.LoyaltyPoints, cart: &Cart{ID: c.ID, DocNo: c.DocNo, Version: version},
		exc: exc.bind(entityCart, id, version),
	}
	return s.postSale(ctx, actor, key, sl)
}
