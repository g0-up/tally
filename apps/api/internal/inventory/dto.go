package inventory

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Wire formats of the inventory endpoints. Money, quantities and factors travel as decimal
// strings; business dates as "YYYY-MM-DD".

func mapSlice[T, D any](in []T, fn func(T) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

// date is a civil date on the wire ("YYYY-MM-DD"), held as midnight UTC.
type date time.Time

// UnmarshalJSON reads a "YYYY-MM-DD" string.
func (d *date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return &json.UnmarshalTypeError{Value: "non-string", Type: reflect.TypeFor[date]()}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return &json.UnmarshalTypeError{Value: "string " + s, Type: reflect.TypeFor[date]()}
	}
	*d = date(clock.Date(t.Year(), t.Month(), t.Day()))
	return nil
}

func (d *date) time() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

func dateOf(t time.Time) string { return t.Format(time.DateOnly) }

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := dateOf(*t)
	return &s
}

// Requests.

type reservationRequest struct {
	SkuID     uuid.UUID  `json:"sku_id"`
	Qty       money.Qty  `json:"qty"`
	ExpiresAt *time.Time `json:"expires_at"`
	Note      *string    `json:"note"`
}

func (r reservationRequest) input() ManualReservation {
	return ManualReservation(r)
}

type releaseRequest struct {
	Qty    *money.Qty `json:"qty"`
	Reason string     `json:"reason"`
}

type adjustmentLineRequest struct {
	SkuID      uuid.UUID    `json:"sku_id"`
	UnitID     *uuid.UUID   `json:"unit_id"`
	Qty        money.Qty    `json:"qty"`
	ColorID    *uuid.UUID   `json:"color_id"`
	Uncolored  bool         `json:"uncolored"`
	LotID      *uuid.UUID   `json:"lot_id"`
	LotNo      string       `json:"lot_no"`
	ExpiryDate *date        `json:"expiry_date"`
	UnitCost   *money.Money `json:"unit_cost"`
}

type adjustmentRequest struct {
	Kind                    string                  `json:"kind"`
	ReasonID                *uuid.UUID              `json:"reason_id"`
	BusinessDate            *date                   `json:"business_date"`
	Note                    *string                 `json:"note"`
	ExceptionConfirmationID *uuid.UUID              `json:"exception_confirmation_id"`
	Lines                   []adjustmentLineRequest `json:"lines"`
}

func (r adjustmentRequest) input(payloadHash []byte) AdjustmentInput {
	return AdjustmentInput{
		Kind: r.Kind, ReasonID: r.ReasonID, BusinessDate: r.BusinessDate.time(), Note: r.Note,
		ExceptionConfirmationID: r.ExceptionConfirmationID, PayloadHash: payloadHash,
		Lines: mapSlice(r.Lines, func(l adjustmentLineRequest) AdjustmentLineInput {
			return AdjustmentLineInput{SkuID: l.SkuID, UnitID: l.UnitID, Qty: l.Qty, ColorID: l.ColorID, Uncolored: l.Uncolored,
				LotID: l.LotID, LotNo: l.LotNo, ExpiryDate: l.ExpiryDate.time(), UnitCost: l.UnitCost}
		}),
	}
}

// reverseRequest is the optional body of a DC reversal: reversing a quarantine DC names the
// owner's quarantine release confirmation.
type reverseRequest struct {
	ExceptionConfirmationID *uuid.UUID `json:"exception_confirmation_id"`
}

type countRequest struct {
	Scope      string      `json:"scope"`
	CategoryID *uuid.UUID  `json:"category_id"`
	SkuIDs     []uuid.UUID `json:"sku_ids"`
	Blind      bool        `json:"blind"`
	Note       *string     `json:"note"`
}

func (r countRequest) input() CountInput {
	return CountInput(r)
}

type countLineRequest struct {
	LineID     *uuid.UUID `json:"line_id"`
	SkuID      *uuid.UUID `json:"sku_id"`
	ColorID    *uuid.UUID `json:"color_id"`
	CountedQty *money.Qty `json:"counted_qty"`
	ReasonID   *uuid.UUID `json:"reason_id"`
	LotNo      *string    `json:"lot_no"`
	ExpiryDate *date      `json:"expiry_date"`
}

type countLinesRequest struct {
	Lines []countLineRequest `json:"lines"`
}

func (r countLinesRequest) input() []CountLineInput {
	return mapSlice(r.Lines, func(l countLineRequest) CountLineInput {
		return CountLineInput{LineID: l.LineID, SkuID: l.SkuID, ColorID: l.ColorID, CountedQty: l.CountedQty, ReasonID: l.ReasonID,
			LotNo: l.LotNo, ExpiryDate: l.ExpiryDate.time()}
	})
}

type recountRequest struct {
	LineIDs []uuid.UUID `json:"line_ids"`
}

type countPostRequest struct {
	ExceptionConfirmationID *uuid.UUID `json:"exception_confirmation_id"`
}

type recostRequest struct {
	SkuIDs   []uuid.UUID `json:"sku_ids"`
	FromDate *date       `json:"from_date"`
}

// Responses.

type colorRefDTO struct {
	ColorID   *uuid.UUID `json:"color_id"`
	ColorCode *string    `json:"color_code"`
	ColorName *string    `json:"color_name"`
	ColorHex  *string    `json:"color_hex"`
}

type colorStockDTO struct {
	colorRefDTO
	OnHand     money.Qty   `json:"on_hand"`
	Sellable   money.Qty   `json:"sellable"`
	Quarantine money.Qty   `json:"quarantine"`
	Expired    money.Qty   `json:"expired"`
	Value      money.Money `json:"value"`
}

func toColorStockDTO(c ColorStock) colorStockDTO {
	return colorStockDTO{
		colorRefDTO: colorRefDTO{ColorID: c.ColorID, ColorCode: c.ColorCode, ColorName: c.ColorName, ColorHex: c.ColorHex},
		OnHand:      c.OnHand, Sellable: c.Sellable, Quarantine: c.Quarantine, Expired: c.Expired, Value: c.Value,
	}
}

type stockRowDTO struct {
	SkuID        uuid.UUID       `json:"sku_id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	VariantLabel string          `json:"variant_label"`
	UnitName     string          `json:"unit_name"`
	CategoryID   *uuid.UUID      `json:"category_id"`
	Status       string          `json:"status"`
	TrackLot     bool            `json:"track_lot"`
	TrackExpiry  bool            `json:"track_expiry"`
	MinStock     money.Qty       `json:"min_stock"`
	MaxStock     *money.Qty      `json:"max_stock"`
	OnHand       money.Qty       `json:"on_hand"`
	Sellable     money.Qty       `json:"sellable"`
	Reserved     money.Qty       `json:"reserved"`
	Available    money.Qty       `json:"available"`
	Quarantine   money.Qty       `json:"quarantine"`
	Expired      money.Qty       `json:"expired"`
	AvgCost      money.Money     `json:"avg_cost"`
	StockValue   money.Money     `json:"stock_value"`
	Price        *money.Money    `json:"price"`
	Low          bool            `json:"low"`
	Over         bool            `json:"over"`
	Colors       []colorStockDTO `json:"colors"`
}

func toStockRowDTO(r StockRow) stockRowDTO {
	return stockRowDTO{
		SkuID: r.SkuID, Code: r.Code, Name: r.DisplayName, VariantLabel: r.VariantLabel, UnitName: r.UnitName,
		CategoryID: r.CategoryID, Status: r.Status, TrackLot: r.TrackLot, TrackExpiry: r.TrackExpiry, MinStock: r.MinStock,
		MaxStock: r.MaxStock, OnHand: r.OnHand, Sellable: r.Sellable, Reserved: r.Reserved, Available: r.Available(),
		Quarantine: r.Quarantine, Expired: r.Expired, AvgCost: r.AvgCost, StockValue: r.StockValue, Price: r.Price,
		Low: r.Low(), Over: r.Over(), Colors: mapSlice(r.Colors, toColorStockDTO),
	}
}

type lotDTO struct {
	colorRefDTO
	ID           uuid.UUID `json:"id"`
	SkuID        uuid.UUID `json:"sku_id"`
	SkuCode      string    `json:"sku_code"`
	SkuName      string    `json:"sku_name"`
	VariantLabel string    `json:"variant_label"`
	UnitName     string    `json:"unit_name"`
	ProductType  string    `json:"product_type"`
	LotNo        string    `json:"lot_no"`
	ExpiryDate   *string   `json:"expiry_date"`
	DaysToExpiry *int      `json:"days_to_expiry"`
	IsQuarantine bool      `json:"is_quarantine"`
	ReceivedOn   string    `json:"received_on"`
	Qty          money.Qty `json:"qty"`
	State        string    `json:"state"`
}

// lotDTOOn maps lots with their states on day under a warning window of warnDays.
func lotDTOOn(day time.Time, warnDays int) func(LotView) lotDTO {
	return func(l LotView) lotDTO {
		out := lotDTO{
			colorRefDTO: colorRefDTO{ColorID: l.ColorID, ColorCode: l.ColorCode, ColorName: l.ColorName, ColorHex: l.ColorHex},
			ID:          l.ID, SkuID: l.SkuID, SkuCode: l.SkuCode, SkuName: l.SkuName, VariantLabel: l.VariantLabel,
			UnitName: l.UnitName, ProductType: l.ProductType, LotNo: l.LotNo, ExpiryDate: dateString(l.ExpiryDate),
			IsQuarantine: l.IsQuarantine, ReceivedOn: dateOf(l.ReceivedOn), Qty: l.Qty, State: l.StateOn(day, warnDays),
		}
		if l.ExpiryDate != nil {
			days := int(l.ExpiryDate.Sub(day).Hours() / 24)
			out.DaysToExpiry = &days
		}
		return out
	}
}

type stockDetailDTO struct {
	stockRowDTO
	Lots []lotDTO `json:"lots"`
}

func toStockDetailDTO(d StockDetail) stockDetailDTO {
	return stockDetailDTO{stockRowDTO: toStockRowDTO(d.StockRow), Lots: mapSlice(d.Lots, lotDTOOn(d.Today, d.WarnDays))}
}

type moveDTO struct {
	ID           uuid.UUID   `json:"id"`
	SkuID        uuid.UUID   `json:"sku_id"`
	LotID        uuid.UUID   `json:"lot_id"`
	LotNo        string      `json:"lot_no"`
	ExpiryDate   *string     `json:"expiry_date"`
	IsQuarantine bool        `json:"is_quarantine"`
	ColorID      *uuid.UUID  `json:"color_id"`
	Qty          money.Qty   `json:"qty"`
	UnitCost     money.Money `json:"unit_cost"`
	Value        money.Money `json:"value"`
	MoveType     MoveType    `json:"move_type"`
	DocType      string      `json:"doc_type"`
	DocID        uuid.UUID   `json:"doc_id"`
	DocNo        *string     `json:"doc_no"`
	BusinessDate string      `json:"business_date"`
	PostedAt     time.Time   `json:"posted_at"`
	PostedBy     uuid.UUID   `json:"posted_by"`
	ReversalOf   *uuid.UUID  `json:"reversal_of"`
}

func toMoveDTO(m TraceMove) moveDTO {
	return moveDTO{
		ID: m.ID, SkuID: m.SkuID, LotID: m.LotID, LotNo: m.LotNo, ExpiryDate: dateString(m.ExpiryDate),
		IsQuarantine: m.IsQuarantine, ColorID: m.ColorID, Qty: m.Qty, UnitCost: m.UnitCost, Value: m.Value, MoveType: m.MoveType,
		DocType: m.DocType, DocID: m.DocID, DocNo: m.DocNo, BusinessDate: dateOf(m.BusinessDate), PostedAt: m.PostedAt,
		PostedBy: m.PostedBy, ReversalOf: m.ReversalOf,
	}
}

type lotTraceDTO struct {
	Lot        lotDTO    `json:"lot"`
	Moves      []moveDTO `json:"moves"`
	NextCursor *string   `json:"next_cursor"`
}

// qtyDisplayDTO is a quantity with its text in the card's unit mode.
type qtyDisplayDTO struct {
	Qty     money.Qty `json:"qty"`
	Display string    `json:"display"`
}

func displayed(d UnitDisplay, q money.Qty) qtyDisplayDTO {
	return qtyDisplayDTO{Qty: q, Display: d.Format(q)}
}

type cardRowDTO struct {
	moveDTO
	In      qtyDisplayDTO `json:"in"`
	Out     qtyDisplayDTO `json:"out"`
	Balance qtyDisplayDTO `json:"balance"`
}

type stockCardDTO struct {
	SkuID        uuid.UUID     `json:"sku_id"`
	SkuCode      string        `json:"sku_code"`
	SkuName      string        `json:"sku_name"`
	VariantLabel string        `json:"variant_label"`
	BaseUnit     string        `json:"base_unit"`
	ColorID      *uuid.UUID    `json:"color_id"`
	Uncolored    bool          `json:"uncolored"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	UnitMode     string        `json:"unit_mode"`
	Opening      qtyDisplayDTO `json:"opening"`
	In           qtyDisplayDTO `json:"in"`
	Out          qtyDisplayDTO `json:"out"`
	Closing      qtyDisplayDTO `json:"closing"`
	OnHand       qtyDisplayDTO `json:"on_hand"`
	Reconciled   bool          `json:"reconciled"`
	Rows         []cardRowDTO  `json:"rows"`
	NextCursor   *string       `json:"next_cursor"`
}

func toStockCardDTO(c StockCard, next *string) stockCardDTO {
	d := c.Display
	return stockCardDTO{
		SkuID: c.SkuID, SkuCode: c.SkuCode, SkuName: c.SkuName, VariantLabel: c.VariantLabel, BaseUnit: c.BaseUnit,
		ColorID: c.ColorID, Uncolored: c.Uncolored, From: dateOf(c.From), To: dateOf(c.To), UnitMode: c.UnitMode,
		Opening: displayed(d, c.Opening), In: displayed(d, c.In), Out: displayed(d, c.Out), Closing: displayed(d, c.Closing),
		OnHand: displayed(d, c.OnHand), Reconciled: c.Reconciled, NextCursor: next,
		Rows: mapSlice(c.Rows, func(r CardRow) cardRowDTO {
			return cardRowDTO{moveDTO: toMoveDTO(r.TraceMove), In: displayed(d, r.In), Out: displayed(d, r.Out), Balance: displayed(d, r.Balance)}
		}),
	}
}

type reservationDTO struct {
	ID            uuid.UUID  `json:"id"`
	SkuID         uuid.UUID  `json:"sku_id"`
	SourceType    string     `json:"source_type"`
	SourceID      uuid.UUID  `json:"source_id"`
	SourceDocNo   *string    `json:"source_doc_no"`
	Qty           money.Qty  `json:"qty"`
	ConsumedQty   money.Qty  `json:"consumed_qty"`
	ReleasedQty   money.Qty  `json:"released_qty"`
	OpenQty       money.Qty  `json:"open_qty"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Note          *string    `json:"note"`
	ReleaseReason *string    `json:"release_reason"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Version       int        `json:"version"`
}

func toReservationDTO(r Reservation) reservationDTO {
	return reservationDTO{
		ID: r.ID, SkuID: r.SkuID, SourceType: r.SourceType, SourceID: r.SourceID, SourceDocNo: r.SourceDocNo, Qty: r.Qty,
		ConsumedQty: r.ConsumedQty, ReleasedQty: r.ReleasedQty, OpenQty: r.Open(), Status: r.Status, ExpiresAt: r.ExpiresAt,
		Note: r.Note, ReleaseReason: r.ReleaseReason, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Version: r.Version,
	}
}

type adjustmentLineDTO struct {
	ID           uuid.UUID    `json:"id"`
	SkuID        uuid.UUID    `json:"sku_id"`
	SkuCode      string       `json:"sku_code"`
	Name         string       `json:"name"`
	VariantLabel string       `json:"variant_label"`
	UnitID       uuid.UUID    `json:"unit_id"`
	UnitName     string       `json:"unit_name"`
	UnitFactor   money.Factor `json:"unit_factor"`
	Qty          money.Qty    `json:"qty"`
	BaseQty      money.Qty    `json:"base_qty"`
	ColorID      *uuid.UUID   `json:"color_id"`
	SpecText     *string      `json:"spec_text"`
	Direction    int          `json:"direction"`
	LotID        *uuid.UUID   `json:"lot_id"`
	LotNo        *string      `json:"lot_no"`
	ExpiryDate   *string      `json:"expiry_date"`
	UnitCost     *money.Money `json:"unit_cost"`
}

func toAdjustmentLineDTO(l StockAdjustmentLine) adjustmentLineDTO {
	return adjustmentLineDTO{
		ID: l.ID, SkuID: l.SkuID, SkuCode: l.SkuCodeSnap, Name: l.NameSnap, VariantLabel: l.VariantLabelSnap, UnitID: l.UnitID,
		UnitName: l.UnitNameSnap, UnitFactor: l.UnitFactorSnap, Qty: l.Qty, BaseQty: l.BaseQty, ColorID: l.ColorID,
		SpecText: l.SpecText, Direction: l.Direction, LotID: l.LotID, LotNo: l.LotNo, ExpiryDate: dateString(l.ExpiryDate),
		UnitCost: l.UnitCost,
	}
}

type adjustmentDTO struct {
	ID           uuid.UUID           `json:"id"`
	DocNo        string              `json:"doc_no"`
	Kind         string              `json:"kind"`
	ReasonID     *uuid.UUID          `json:"reason_id"`
	StockCountID *uuid.UUID          `json:"stock_count_id"`
	BusinessDate string              `json:"business_date"`
	Note         *string             `json:"note"`
	Status       string              `json:"status"`
	PostedAt     time.Time           `json:"posted_at"`
	PostedBy     uuid.UUID           `json:"posted_by"`
	ReversalOf   *uuid.UUID          `json:"reversal_of"`
	ReversedBy   *uuid.UUID          `json:"reversed_by,omitempty"`
	Lines        []adjustmentLineDTO `json:"lines,omitempty"`
	// The list carries a summary of the lines instead of the lines.
	LineCount    *int                       `json:"line_count,omitempty"`
	TotalBaseQty *money.Qty                 `json:"total_base_qty,omitempty"`
	ValueIn      *money.Money               `json:"value_in,omitempty"`
	ValueOut     *money.Money               `json:"value_out,omitempty"`
	LinePreview  []adjustmentLinePreviewDTO `json:"line_preview,omitempty"`
}

type adjustmentLinePreviewDTO struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	VariantLabel string     `json:"variant_label"`
	ColorID      *uuid.UUID `json:"color_id"`
	LotNo        *string    `json:"lot_no"`
}

func toAdjustmentListDTO(a AdjustmentSummary) adjustmentDTO {
	out := toAdjustmentSummaryDTO(a.StockAdjustment)
	out.LineCount, out.TotalBaseQty, out.ValueIn, out.ValueOut = &a.LineCount, &a.TotalBaseQty, &a.ValueIn, &a.ValueOut
	out.LinePreview = mapSlice(a.Preview, func(l AdjustmentLinePreview) adjustmentLinePreviewDTO {
		return adjustmentLinePreviewDTO{ID: l.ID, Name: l.NameSnap, VariantLabel: l.VariantLabelSnap, ColorID: l.ColorID, LotNo: l.LotNo}
	})
	return out
}

// confirmedAdjustmentAudit is the audited state of a DC posted under an owner confirmation: the
// confirmation names one SKU, so the audit records every SKU it was used for.
type confirmedAdjustmentAudit struct {
	adjustmentDTO
	ExceptionKind           exception.Kind `json:"exception_kind"`
	ExceptionConfirmationID uuid.UUID      `json:"exception_confirmation_id"`
	SkuIDs                  []uuid.UUID    `json:"sku_ids"`
}

func toAdjustmentSummaryDTO(a StockAdjustment) adjustmentDTO {
	return adjustmentDTO{
		ID: a.ID, DocNo: a.DocNo, Kind: a.Kind, ReasonID: a.ReasonID, StockCountID: a.StockCountID,
		BusinessDate: dateOf(a.BusinessDate), Note: a.Note, Status: a.Status, PostedAt: a.PostedAt, PostedBy: a.PostedBy,
		ReversalOf: a.ReversalOf,
	}
}

func toAdjustmentDTO(d AdjustmentDetail) adjustmentDTO {
	out := toAdjustmentSummaryDTO(d.StockAdjustment)
	out.ReversedBy = d.ReversedBy
	out.Lines = mapSlice(d.Lines, toAdjustmentLineDTO)
	return out
}

type countLineDTO struct {
	colorRefDTO
	ID           uuid.UUID  `json:"id"`
	SkuID        uuid.UUID  `json:"sku_id"`
	SkuCode      string     `json:"sku_code"`
	SkuName      string     `json:"sku_name"`
	VariantLabel string     `json:"variant_label"`
	UnitName     string     `json:"unit_name"`
	TrackLot     bool       `json:"track_lot"`
	TrackExpiry  bool       `json:"track_expiry"`
	LotID        *uuid.UUID `json:"lot_id"`
	LotNo        *string    `json:"lot_no"`
	ExpiryDate   *string    `json:"expiry_date"`
	// SystemQty, MovedSinceStartQty and VarianceQty are null on a blind count still counting.
	SystemQty          *money.Qty `json:"system_qty"`
	CountedQty         *money.Qty `json:"counted_qty"`
	MovedSinceStartQty *money.Qty `json:"moved_since_start_qty"`
	VarianceQty        *money.Qty `json:"variance_qty"`
	ReasonID           *uuid.UUID `json:"reason_id"`
}

type countDTO struct {
	ID            uuid.UUID      `json:"id"`
	DocNo         string         `json:"doc_no"`
	Scope         string         `json:"scope"`
	CategoryID    *uuid.UUID     `json:"category_id"`
	Blind         bool           `json:"blind"`
	Status        string         `json:"status"`
	BusinessDate  string         `json:"business_date"`
	StartedAt     time.Time      `json:"started_at"`
	CountedAt     *time.Time     `json:"counted_at"`
	Note          *string        `json:"note"`
	PostedAt      *time.Time     `json:"posted_at"`
	PostedBy      *uuid.UUID     `json:"posted_by"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Version       int            `json:"version"`
	AdjustmentID  *uuid.UUID     `json:"adjustment_id,omitempty"`
	AdjustmentNo  *string        `json:"adjustment_no,omitempty"`
	Lines         []countLineDTO `json:"lines,omitempty"`
	LineCount     *int           `json:"line_count,omitempty"`
	CountedLines  *int           `json:"counted_lines,omitempty"`
	VarianceLines *int           `json:"variance_lines,omitempty"`
}

// toCountListDTO maps a count of the list with its sheet summary. A blind count still counting
// hides its variance lines, as its sheet hides the book side.
func toCountListDTO(c CountSummary) countDTO {
	out := toCountSummaryDTO(c.StockCount)
	out.LineCount, out.CountedLines = &c.LineCount, &c.CountedLines
	if !c.Blind || c.Status != CountCounting {
		out.VarianceLines = &c.VarianceLines
	}
	return out
}

func toCountSummaryDTO(c StockCount) countDTO {
	return countDTO{
		ID: c.ID, DocNo: c.DocNo, Scope: c.Scope, CategoryID: c.CategoryID, Blind: c.Blind, Status: c.Status,
		BusinessDate: dateOf(c.BusinessDate), StartedAt: c.StartedAt, CountedAt: c.CountedAt, Note: c.Note, PostedAt: c.PostedAt,
		PostedBy: c.PostedBy, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version,
	}
}

// toCountDTO maps a count with its sheet. A blind count hides the system side of its lines
// until it is finished, so the counter cannot copy the book quantity.
func toCountDTO(d CountDetail) countDTO {
	out := toCountSummaryDTO(d.StockCount)
	out.AdjustmentID, out.AdjustmentNo = d.AdjustmentID, d.AdjustmentNo
	hide := d.Blind && d.Status == CountCounting
	counted, variance := 0, 0
	out.Lines = mapSlice(d.Lines, func(l CountLineView) countLineDTO {
		ln := countLineDTO{
			colorRefDTO: colorRefDTO{ColorID: l.ColorID, ColorCode: l.ColorCode, ColorName: l.ColorName, ColorHex: l.ColorHex},
			ID:          l.ID, SkuID: l.SkuID, SkuCode: l.SkuCode, SkuName: l.SkuName, VariantLabel: l.VariantLabel,
			UnitName: l.UnitName, TrackLot: l.TrackLot, TrackExpiry: l.TrackExpiry, LotID: l.LotID, LotNo: l.LotNo, ExpiryDate: dateString(l.ExpiryDate),
			CountedQty: l.CountedQty, ReasonID: l.ReasonID,
		}
		if l.CountedQty != nil {
			counted++
		}
		if l.VarianceQty != nil && !l.VarianceQty.IsZero() {
			variance++
		}
		if !hide {
			system, moved := l.SystemQty, l.MovedSinceStartQty
			ln.SystemQty, ln.MovedSinceStartQty, ln.VarianceQty = &system, &moved, l.VarianceQty
		}
		return ln
	})
	lineCount := len(d.Lines)
	out.LineCount, out.CountedLines = &lineCount, &counted
	if !hide {
		out.VarianceLines = &variance
	}
	return out
}

type recostDTO struct {
	ID          uuid.UUID    `json:"id"`
	SkuIDs      []uuid.UUID  `json:"sku_ids"`
	FromDate    string       `json:"from_date"`
	Status      string       `json:"status"`
	Impact      RecostImpact `json:"impact"`
	RunVersion  int          `json:"run_version"`
	RequestedBy uuid.UUID    `json:"requested_by"`
	ConfirmedBy *uuid.UUID   `json:"confirmed_by"`
	CreatedAt   time.Time    `json:"created_at"`
	AppliedAt   *time.Time   `json:"applied_at"`
}

func toRecostDTO(r RecostRun) recostDTO {
	impact := r.Impact
	if impact.Skus == nil {
		impact.Skus = []SkuRecost{}
	}
	return recostDTO{
		ID: r.ID, SkuIDs: r.SkuIDs, FromDate: dateOf(r.FromDate), Status: r.Status, Impact: impact, RunVersion: r.RunVersion,
		RequestedBy: r.RequestedBy, ConfirmedBy: r.ConfirmedBy, CreatedAt: r.CreatedAt, AppliedAt: r.AppliedAt,
	}
}
