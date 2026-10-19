package debt

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Reason kinds a debt adjustment may cite.
const (
	reasonDebtAdjustment = "debt_adjustment"
	reasonWriteOff       = "write_off"
)

// AdjustmentInput is a DN. An increase or an opening balance creates an obligation; a decrease,
// write-off or dispute settlement settles part of an existing one (the target).
type AdjustmentInput struct {
	PartyID   uuid.UUID
	Direction string
	Kind      string
	Amount    money.Money
	// TargetObligationID is the document a decrease, write-off or dispute settlement reduces.
	TargetObligationID *uuid.UUID
	ReasonID           uuid.UUID
	// OpeningCutoffDate is the date of the opening balance (its business date), not after today.
	OpeningCutoffDate *time.Time
	// DueDate of the obligation an increase or opening balance creates; default: payment terms.
	DueDate *time.Time
	Note    string
	// ConfirmationID and PayloadHash spend the owner's debt_write_off confirmation.
	ConfirmationID uuid.UUID
	PayloadHash    []byte
}

// AdjustmentResult is a recorded DN with the obligation it created or the settlement it wrote.
type AdjustmentResult struct {
	Adjustment Adjustment
	Obligation *Obligation
	Settlement *Settlement
}

func reducesTarget(kind string) bool {
	return kind == AdjustDecrease || kind == AdjustWriteOff || kind == AdjustDisputeSettlement
}

func checkAdjustmentInput(in AdjustmentInput) error {
	fields := map[string]string{}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if in.Direction != Receivable && in.Direction != Payable {
		fields["direction"] = "invalid"
	}
	switch in.Kind {
	case AdjustIncrease, AdjustOpeningBalance:
		if in.TargetObligationID != nil {
			fields["target_obligation_id"] = "not_allowed"
		}
	case AdjustDecrease, AdjustWriteOff, AdjustDisputeSettlement:
		if in.TargetObligationID == nil {
			fields["target_obligation_id"] = "required"
		}
		if in.DueDate != nil {
			fields["due_date"] = "not_allowed"
		}
	default:
		fields["kind"] = "invalid"
	}
	if (in.Kind == AdjustOpeningBalance) != (in.OpeningCutoffDate != nil) {
		fields["opening_cutoff_date"] = "opening_balance_only"
	}
	if !in.Amount.IsPositive() {
		fields["amount"] = "must_be_positive"
	}
	if in.ReasonID == uuid.Nil {
		fields["reason_id"] = "required"
	}
	if utf8.RuneCountInString(in.Note) > maxNoteLen {
		fields["note"] = "too_long"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// checkReason requires an active reason of the kind the adjustment needs: write_off for a
// write-off, debt_adjustment for every other DN.
func checkReason(ctx context.Context, q db.Querier, id uuid.UUID, adjustment string) error {
	want := reasonDebtAdjustment
	if adjustment == AdjustWriteOff {
		want = reasonWriteOff
	}
	var r struct {
		Kind   string `db:"kind"`
		Active bool   `db:"active"`
	}
	err := getInto(ctx, q, &r, `SELECT kind, active FROM reasons WHERE id = $1`, id)
	if db.IsNoRows(err) || (err == nil && (r.Kind != want || !r.Active)) {
		return errField("reason_invalid", "reason_id", "invalid", "Choose an active reason for this kind of adjustment")
	}
	if err != nil {
		return fmt.Errorf("debt: read reason: %w", err)
	}
	return nil
}

// recordAdjustment writes a DN. The target obligation, if any, is locked here; a write-off spends
// the owner's confirmation for the target at its current version and payload.
func recordAdjustment(ctx context.Context, w writeCtx, ex ExceptionGuard, onboarding Onboarding, in AdjustmentInput,
	docNo, key string,
) (AdjustmentResult, error) {
	if err := checkAdjustmentInput(in); err != nil {
		return AdjustmentResult{}, err
	}
	if err := checkReason(ctx, w.q, in.ReasonID, in.Kind); err != nil {
		return AdjustmentResult{}, err
	}
	if _, err := getParty(ctx, w.q, in.PartyID); err != nil {
		return AdjustmentResult{}, err
	}
	a := Adjustment{
		ID: ids.New(), TenantID: w.tenantID, DocNo: docNo, PartyID: in.PartyID, Direction: in.Direction, Kind: in.Kind,
		Amount: in.Amount, TargetObligationID: in.TargetObligationID, ReasonID: in.ReasonID,
		OpeningCutoffDate: in.OpeningCutoffDate, BusinessDate: w.date, Note: optString(in.Note), PostedAt: w.now,
		PostedBy: w.by, CreatedAt: w.now,
	}
	var target *Obligation
	if reducesTarget(in.Kind) {
		obs, err := lockObligations(ctx, w.q, []uuid.UUID{*in.TargetObligationID})
		if err != nil {
			return AdjustmentResult{}, err
		}
		target = obs[*in.TargetObligationID]
		switch {
		case target == nil:
			return AdjustmentResult{}, errField("obligation_not_found", "target_obligation_id", "not_found", "The document does not exist")
		case target.PartyID != in.PartyID:
			return AdjustmentResult{}, errField("allocation_party_mismatch", "target_obligation_id", "other_party",
				"The document belongs to another party")
		case target.Direction != in.Direction:
			return AdjustmentResult{}, errField("allocation_direction_mismatch", "target_obligation_id", "other_direction",
				"The document is on the other ledger (receivable/payable)")
		case !target.Open().IsPositive():
			return AdjustmentResult{}, errObligationSettled("target_obligation_id")
		case in.Amount.Cmp(target.Open()) > 0:
			return AdjustmentResult{}, errField("allocation_exceeds_open", "amount", "exceeds_open",
				"The amount is more than what is still open on the document")
		}
		if in.Kind == AdjustWriteOff {
			if err := ex.Consume(ctx, w.q, w.tenantID, exception.Expectation{
				ConfirmationID: in.ConfirmationID, Kind: exception.DebtWriteOff, DocType: "obligation", DocID: target.ID,
				DocVersion: target.Version, PayloadHash: in.PayloadHash, UsedByDocType: "debt_adjustment", UsedByDocID: a.ID,
			}); err != nil {
				return AdjustmentResult{}, err
			}
		}
	}
	if _, err := w.q.ExecContext(ctx, `
		INSERT INTO debt_adjustments (id, tenant_id, doc_no, party_id, direction, kind, amount, target_obligation_id, reason_id,
		                              opening_cutoff_date, business_date, note, idempotency_key, posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $14)`,
		a.ID, a.TenantID, a.DocNo, a.PartyID, a.Direction, a.Kind, a.Amount, a.TargetObligationID, a.ReasonID,
		a.OpeningCutoffDate, a.BusinessDate, a.Note, key, a.PostedAt, a.PostedBy); err != nil {
		return AdjustmentResult{}, fmt.Errorf("debt: insert debt adjustment: %w", err)
	}
	out := AdjustmentResult{Adjustment: a}
	if target != nil {
		s, err := settle(ctx, w, target, SettledByDebtAdjustment, a.ID, in.Amount, ModeManual)
		if err != nil {
			return AdjustmentResult{}, err
		}
		out.Settlement = &s
	} else {
		source := SourceDebtAdjustment
		if in.Kind == AdjustOpeningBalance {
			source = SourceOpeningBalance
		}
		r, err := recordObligation(ctx, w, in.Direction, ObligationInput{
			PartyID: in.PartyID, SourceType: source, SourceID: a.ID, SourceDocNo: docNo, Amount: in.Amount, DueDate: in.DueDate,
		})
		if err != nil {
			return AdjustmentResult{}, err
		}
		out.Obligation = &r.Obligation
	}
	if in.Kind == AdjustOpeningBalance && onboarding != nil {
		if err := onboarding.CompleteStep(ctx, w.q, w.tenantID, StepOpeningDebt, w.by, w.now); err != nil {
			return AdjustmentResult{}, fmt.Errorf("debt: complete onboarding: %w", err)
		}
	}
	return out, nil
}

// AttachmentOwnerParty is the attachments.owner_type of a signed offset record: it is uploaded for
// the party of the BT.
const AttachmentOwnerParty = "party"

// OffsetInput is a BT: the same amount settles one receivable and one payable of a party, backed
// by the signed record (biên bản) both sides agreed on, uploaded for that party and completed.
type OffsetInput struct {
	PartyID                uuid.UUID
	ReceivableObligationID uuid.UUID
	PayableObligationID    uuid.UUID
	Amount                 money.Money
	AttachmentID           uuid.UUID
	Note                   string
}

// OffsetResult is a recorded BT and its two settlements (receivable first).
type OffsetResult struct {
	Offset      Offset
	Settlements []Settlement
}

func checkOffsetInput(in OffsetInput) error {
	fields := map[string]string{}
	if in.PartyID == uuid.Nil {
		fields["party_id"] = "required"
	}
	if in.ReceivableObligationID == uuid.Nil {
		fields["receivable_obligation_id"] = "required"
	}
	if in.PayableObligationID == uuid.Nil {
		fields["payable_obligation_id"] = "required"
	}
	if !in.Amount.IsPositive() {
		fields["amount"] = "must_be_positive"
	}
	if in.AttachmentID == uuid.Nil {
		fields["attachment_id"] = "required"
	}
	if utf8.RuneCountInString(in.Note) > maxNoteLen {
		fields["note"] = "too_long"
	}
	if len(fields) > 0 {
		return errInvalid(fields)
	}
	return nil
}

// recordOffset writes a BT. Both ledgers keep their documents: each gets a settlement of the
// offset, nothing is netted away.
func recordOffset(ctx context.Context, w writeCtx, in OffsetInput, docNo, key string) (OffsetResult, error) {
	if err := checkOffsetInput(in); err != nil {
		return OffsetResult{}, err
	}
	// Only a completed upload is clean: a pending attachment may never have been uploaded at all.
	var uploaded bool
	err := w.q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM attachments
		WHERE id = $1 AND owner_type = $2 AND owner_id = $3 AND scan_status = 'clean')`,
		in.AttachmentID, AttachmentOwnerParty, in.PartyID).Scan(&uploaded)
	if err == nil && !uploaded {
		return OffsetResult{}, errField("attachment_invalid", "attachment_id", "invalid",
			"Upload the signed offset record for the party before posting")
	}
	if err != nil {
		return OffsetResult{}, fmt.Errorf("debt: read attachment: %w", err)
	}
	obs, err := lockObligations(ctx, w.q, []uuid.UUID{in.ReceivableObligationID, in.PayableObligationID})
	if err != nil {
		return OffsetResult{}, err
	}
	sides := []struct {
		field, ledger string
		id            uuid.UUID
	}{{"receivable_obligation_id", Receivable, in.ReceivableObligationID}, {"payable_obligation_id", Payable, in.PayableObligationID}}
	for _, side := range sides {
		ob := obs[side.id]
		switch {
		case ob == nil:
			return OffsetResult{}, errField("obligation_not_found", side.field, "not_found", "The document does not exist")
		case ob.PartyID != in.PartyID:
			return OffsetResult{}, errField("allocation_party_mismatch", side.field, "other_party", "The document belongs to another party")
		case ob.Direction != side.ledger:
			return OffsetResult{}, errField("allocation_direction_mismatch", side.field, "other_direction",
				"The document is on the other ledger (receivable/payable)")
		case !ob.Open().IsPositive():
			return OffsetResult{}, errObligationSettled(side.field)
		case in.Amount.Cmp(ob.Open()) > 0:
			return OffsetResult{}, errField("allocation_exceeds_open", "amount", "exceeds_open",
				"The amount is more than what is still open on one of the documents")
		}
	}
	attachment := in.AttachmentID
	o := Offset{
		ID: ids.New(), TenantID: w.tenantID, DocNo: docNo, PartyID: in.PartyID, Amount: in.Amount,
		ReceivableObligationID: in.ReceivableObligationID, PayableObligationID: in.PayableObligationID,
		AttachmentID: &attachment, BusinessDate: w.date, Note: optString(in.Note), PostedAt: w.now, PostedBy: w.by,
		CreatedAt: w.now,
	}
	if _, err := w.q.ExecContext(ctx, `
		INSERT INTO debt_offsets (id, tenant_id, doc_no, party_id, amount, receivable_obligation_id, payable_obligation_id,
		                          attachment_id, business_date, note, idempotency_key, posted_at, posted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $12)`,
		o.ID, o.TenantID, o.DocNo, o.PartyID, o.Amount, o.ReceivableObligationID, o.PayableObligationID, o.AttachmentID,
		o.BusinessDate, o.Note, key, o.PostedAt, o.PostedBy); err != nil {
		return OffsetResult{}, fmt.Errorf("debt: insert debt offset: %w", err)
	}
	out := OffsetResult{Offset: o}
	for _, side := range sides {
		s, err := settle(ctx, w, obs[side.id], SettledByDebtOffset, o.ID, in.Amount, ModeManual)
		if err != nil {
			return OffsetResult{}, err
		}
		out.Settlements = append(out.Settlements, s)
	}
	return out, nil
}
