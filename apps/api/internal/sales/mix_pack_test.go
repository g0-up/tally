package sales

import (
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// mixFixture is a group of two flavors sold 24 pieces a pack, 6 pieces a part (4 parts a pack).
type mixFixture struct {
	cat        *catalog
	group      uuid.UUID
	cola, cam  uuid.UUID
	pieceUnit  uuid.UUID
	packUnitID uuid.UUID
}

func newMixFixture() mixFixture {
	f := mixFixture{group: uuid.New(), cola: uuid.New(), cam: uuid.New(), pieceUnit: uuid.New(), packUnitID: uuid.New()}
	f.cat = &catalog{skus: map[uuid.UUID]skuInfo{}, units: map[uuid.UUID][]unitInfo{}, mixes: map[uuid.UUID]*mixGroup{}}
	for _, id := range []uuid.UUID{f.cola, f.cam} {
		f.cat.skus[id] = skuInfo{ID: id, Code: id.String()[:8], Name: "Flavor", BaseUnitID: f.pieceUnit,
			Status: "active", ProductStatus: "active", ProductType: "goods"}
		f.cat.units[id] = []unitInfo{{SkuID: id, UnitID: f.pieceUnit, Name: "hộp", Factor: money.FactorOne, IsBase: true, Active: true}}
	}
	f.cat.mixes[f.group] = &mixGroup{ID: f.group, Name: "Nước ngọt lon", PackUnitID: f.packUnitID, PackUnitName: "thùng",
		UnitsPerPack: 24, UnitsPerPart: 6, PieceUnitID: f.pieceUnit, Status: "active",
		flavors: map[uuid.UUID]bool{f.cola: true, f.cam: true}}
	return f
}

func (f mixFixture) request(packs string, cola, cam int) LineInput {
	g := f.group
	return LineInput{MixGroupID: &g, Qty: qty(packs), Parts: []MixPart{{SkuID: f.cola, Parts: cola}, {SkuID: f.cam, Parts: cam}}}
}

func TestExpandMixSellsAShortPackByThePartsChosen(t *testing.T) {
	f := newMixFixture()
	header := uuid.New()
	out, err := (&Service{}).expandMix(f.cat, f.request("2", 3, 2), 0, header, nil)
	if err != nil {
		t.Fatalf("2 packs holding 5 of 8 parts: %v", err)
	}
	if len(out) != 3 || out[0].line.Kind != KindMixPack || !out[0].line.Qty.Equal(qty("2")) {
		t.Fatalf("lines = %+v, want the header of 2 packs and two flavors", out)
	}
	for i, want := range map[int]string{1: "18", 2: "12"} {
		l := out[i].line
		if l.Kind != KindMixFlavor || l.ParentLineID == nil || *l.ParentLineID != header || !l.Qty.Equal(qty(want)) {
			t.Errorf("flavor %d = %+v, want %s pieces under the header", i, l, want)
		}
	}
}

func TestExpandMixRejectsPartsOverThePacks(t *testing.T) {
	f := newMixFixture()
	_, err := (&Service{}).expandMix(f.cat, f.request("1", 4, 1), 0, uuid.New(), nil)
	fields := wantCode(t, err, apperr.Validation, CodeMixPartsExceedPack)
	if fields["lines.0.parts"] != CodeMixPartsExceedPack {
		t.Errorf("fields = %v, want lines.0.parts", fields)
	}
	if _, err := (&Service{}).expandMix(f.cat, f.request("1", 3, 1), 0, uuid.New(), nil); err != nil {
		t.Errorf("a full pack: %v", err)
	}
}

func TestExpandMixRejectsPartsTooLargeToAdd(t *testing.T) {
	f := newMixFixture()
	for _, parts := range [][2]int{{math.MaxInt, 1}, {1, math.MaxInt}, {math.MaxInt, math.MaxInt}} {
		_, err := (&Service{}).expandMix(f.cat, f.request("1", parts[0], parts[1]), 0, uuid.New(), nil)
		fields := wantCode(t, err, apperr.Validation, CodeMixPartsExceedPack)
		if fields["lines.0.parts"] != CodeMixPartsExceedPack {
			t.Errorf("parts %v: fields = %v, want lines.0.parts", parts, fields)
		}
	}
}

func TestValidateDraftRequiresSomePartsOfAMixPack(t *testing.T) {
	f := newMixFixture()
	empty := f.request("1", 0, 0)
	empty.Parts = nil
	wantFields(t, validateDraft(DraftInput{Lines: []LineInput{empty}}), map[string]string{"lines.0.parts": "required"})
	wantFields(t, validateDraft(DraftInput{Lines: []LineInput{f.request("1", 0, 1)}}),
		map[string]string{"lines.0.parts.0.parts": "must_be_positive"})
}

func TestFinishMixHeadersPricesAShortPackByItsFlavors(t *testing.T) {
	f := newMixFixture()
	out, err := (&Service{}).expandMix(f.cat, f.request("2", 3, 2), 0, uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(out); i++ {
		l := &out[i].line
		l.UnitPrice = vnd("10000")
		l.LineAmount = vnd("10000").MulQty(l.Qty)
	}
	finishMixHeaders(out, true)
	h := out[0].line
	wantMoney(t, "header amount", h.LineAmount, "300000")
	wantMoney(t, "header price per pack", h.UnitPrice, "150000")
}

// planDelivery never reads the pack size, so a short pack ships like any other: the header's packs
// whole, with each flavor's own remaining pieces.
func TestPlanDeliveryDeliversAMixPackWithEachFlavorsOwnPieces(t *testing.T) {
	f := newMixFixture()
	header := uuid.New()
	out, err := (&Service{}).expandMix(f.cat, f.request("2", 3, 2), 0, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]InvoiceLine, len(out))
	for i, dl := range out {
		l := dl.line
		l.BaseQty = l.Qty
		lines[i] = InvoiceLine{Line: l, Delivered: money.ZeroQty, Cancelled: money.ZeroQty}
		if holdsStock(lines[i]) {
			lines[i].Reserved = l.BaseQty
		}
	}
	for _, req := range [][]StockOutLineInput{nil, {{InvoiceLineID: header}}} {
		plan, err := planDelivery(lines, req)
		if err != nil {
			t.Fatalf("deliver %v: %v", req, err)
		}
		if len(plan) != 3 || !plan[header].Equal(qty("2")) || !plan[lines[1].ID].Equal(qty("18")) ||
			!plan[lines[2].ID].Equal(qty("12")) {
			t.Errorf("deliver %v = %v, want the 2 packs with 18 and 12 pieces", req, plan)
		}
	}
}

func TestDraftOfCopiesTheChosenPartsOfAShortPack(t *testing.T) {
	f := newMixFixture()
	out, err := (&Service{}).expandMix(f.cat, f.request("2", 3, 2), 0, uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]Line, len(out))
	for i, dl := range out {
		lines[i] = dl.line
	}
	got := draftOf(lines)
	if len(got) != 1 || got[0].MixGroupID == nil || *got[0].MixGroupID != f.group || !got[0].Qty.Equal(qty("2")) ||
		len(got[0].Parts) != 2 || got[0].Parts[0].Parts != 3 || got[0].Parts[1].Parts != 2 {
		t.Errorf("draft = %+v, want 2 packs of 3 and 2 parts", got)
	}
}
