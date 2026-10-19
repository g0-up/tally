package partner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

func ptr[T any](v T) *T { return &v }

func TestDedupeWarningsMatchByPhoneTaxCodeAndAccentFreeName(t *testing.T) {
	keys := DedupeKeys{PhoneKey: "0912418226", TaxCode: "0109245781", NameKey: textnorm.Key("tap hoa bich ngoc")}
	byName := dedupeCandidate{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Name: "Tạp hóa Bích Ngọc",
		NameKey: textnorm.Key("Tạp hóa Bích Ngọc"), IsCustomer: true}
	byPhone := dedupeCandidate{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Name: "Chị Ngọc",
		NameKey: textnorm.Key("Chị Ngọc"), PhoneKey: ptr("0912418226"), IsSupplier: true}
	byBoth := dedupeCandidate{ID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Name: "Bích Ngọc",
		NameKey: textnorm.Key("Bích Ngọc"), PhoneKey: ptr("0912418226"), TaxCode: ptr("0109245781")}
	unrelated := dedupeCandidate{ID: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Name: "Hoa Mai",
		NameKey: textnorm.Key("Hoa Mai"), PhoneKey: ptr("0904771365")}

	got := PartyDeduplicator{}.Warnings(keys, []dedupeCandidate{byName, byPhone, byBoth, unrelated})
	if len(got) != 3 {
		t.Fatalf("got %d warnings, want 3: %+v", len(got), got)
	}
	if got[0].PartyID != byBoth.ID || !slices.Equal(got[0].Matches, []string{MatchPhone, MatchTaxCode}) {
		t.Errorf("strongest match first: got %+v", got[0])
	}
	if got[1].PartyID != byPhone.ID || !got[1].IsSupplier || !slices.Equal(got[1].Matches, []string{MatchPhone}) {
		t.Errorf("phone match second: got %+v", got[1])
	}
	if got[2].PartyID != byName.ID || !got[2].IsCustomer || !slices.Equal(got[2].Matches, []string{MatchName}) {
		t.Errorf("accent-free name match last: got %+v", got[2])
	}
}

func TestDedupeWarningsIgnoreEmptyKeysAndCapTheList(t *testing.T) {
	var candidates []dedupeCandidate
	for range 8 {
		candidates = append(candidates, dedupeCandidate{ID: uuid.New(), Name: "Shop", NameKey: "shop", PhoneKey: ptr(""), TaxCode: ptr("")})
	}
	if got := (PartyDeduplicator{}).Warnings(DedupeKeys{}, candidates); len(got) != 0 {
		t.Fatalf("empty keys must not match: %+v", got)
	}
	if got := (PartyDeduplicator{}).Warnings(DedupeKeys{NameKey: "shop"}, candidates); len(got) != maxDuplicateWarnings {
		t.Fatalf("got %d warnings, want the cap %d", len(got), maxDuplicateWarnings)
	}
}

func TestPartyInputValidation(t *testing.T) {
	cases := []struct {
		name  string
		in    PartyInput
		field string
		code  string
	}{
		{"name required", PartyInput{Name: ptr("   ")}, "name", "required"},
		{"name too long", PartyInput{Name: ptr(strings.Repeat("a", maxNameLen+1))}, "name", "too_long"},
		{"name with control char", PartyInput{Name: ptr("a\x00b")}, "name", "invalid_text"},
		{"kind unknown", PartyInput{Name: ptr("A"), Kind: ptr("robot")}, "kind", "invalid"},
		{"phone too short", PartyInput{Name: ptr("A"), Phone: Some("0912")}, "phone", "invalid"},
		{"phone too long", PartyInput{Name: ptr("A"), Phone: Some("0912 418 226 418 226")}, "phone", "invalid"},
		{"phone with letters", PartyInput{Name: ptr("A"), Phone: Some("0912 ABC 226")}, "phone", "invalid"},
		{"email malformed", PartyInput{Name: ptr("A"), Email: Some("ban@")}, "email", "invalid"},
		{"email display name", PartyInput{Name: ptr("A"), Email: Some("Ban <ban@x.vn>")}, "email", "invalid"},
		{"tax code with symbols", PartyInput{Name: ptr("A"), TaxCode: Some("0109/245")}, "tax_code", "invalid"},
		{"note too long", PartyInput{Name: ptr("A"), Note: Some(strings.Repeat("n", maxNoteLen+1))}, "note", "too_long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fieldErrors{}
			var p Party
			tc.in.apply(&p, f, true)
			if f[tc.field] != tc.code {
				t.Fatalf("field %s = %q, want %q (all: %v)", tc.field, f[tc.field], tc.code, f)
			}
			if apperr.KindOf(f.err()) != apperr.Validation {
				t.Fatalf("err kind = %v, want validation", apperr.KindOf(f.err()))
			}
		})
	}
}

func TestPartyInputNormalisesAndReportsKeyChanges(t *testing.T) {
	f := fieldErrors{}
	var p Party
	changed := PartyInput{
		Name: ptr("  Công ty TNHH Minh An "), Phone: Some("024 3795 6612"), Email: Some("KeToan@MinhAn.vn"),
		TaxCode: Some("0109 245.781-001"), Note: Some("Giao giờ hành chính\nGọi trước"),
	}.apply(&p, f, true)
	if len(f) != 0 {
		t.Fatalf("unexpected errors: %v", f)
	}
	if !changed {
		t.Error("a new party changes its dedupe keys")
	}
	if p.Kind != KindOrganization || p.Name != "Công ty TNHH Minh An" || p.NameKey != textnorm.Key("cong ty tnhh minh an") {
		t.Errorf("name/kind: %+v", p)
	}
	if deref(p.PhoneKey) != "02437956612" || deref(p.Phone) != "024 3795 6612" {
		t.Errorf("phone = %q key %q", deref(p.Phone), deref(p.PhoneKey))
	}
	if deref(p.Email) != "ketoan@minhan.vn" {
		t.Errorf("email = %q", deref(p.Email))
	}
	if deref(p.TaxCode) != "0109245781-001" {
		t.Errorf("tax code = %q", deref(p.TaxCode))
	}

	// Changing only the address keeps the keys; clearing the phone changes them.
	if (PartyInput{Address: Some("52 Duy Tân")}).apply(&p, f, false) {
		t.Error("an address change does not touch the dedupe keys")
	}
	if !(PartyInput{Phone: Opt[string]{Set: true}}).apply(&p, f, false) || p.Phone != nil || p.PhoneKey != nil {
		t.Errorf("clearing the phone: %+v", p)
	}
}

func TestOptDistinguishesMissingNullAndValue(t *testing.T) {
	var body struct {
		A Opt[string]      `json:"a"`
		B Opt[string]      `json:"b"`
		C Opt[string]      `json:"c"`
		D Opt[money.Money] `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"b": null, "c": "x", "d": "1500000.50"}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.A.Set {
		t.Error("missing field must stay unset")
	}
	if !body.B.Set || body.B.Value != nil {
		t.Errorf("null must be set and empty: %+v", body.B)
	}
	if !body.C.Set || deref(body.C.Value) != "x" {
		t.Errorf("value: %+v", body.C)
	}
	if !body.D.Set || body.D.Value == nil || body.D.Value.Cmp(money.MustMoney("1500000.5")) != 0 {
		t.Errorf("money: %+v", body.D)
	}
}

func TestProfileFieldChecks(t *testing.T) {
	f := fieldErrors{}
	f.terms("payment_terms_days", -1)
	f.terms("other_terms", MaxPaymentTermsDays+1)
	f.creditLimit("credit_limit", ptr(money.MustMoney("-1")))
	f.creditLimit("big_limit", ptr(money.MustMoney("999999999999999.5")))
	f.creditLimit("no_limit", nil)
	f.creditLimit("zero_limit", ptr(money.MustMoney("0")))
	want := map[string]string{
		"payment_terms_days": "out_of_range", "other_terms": "out_of_range",
		"credit_limit": "negative", "big_limit": "too_large",
	}
	if len(f) != len(want) {
		t.Fatalf("errors = %v, want %v", f, want)
	}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
}

func TestSearchPatternsAndNorm(t *testing.T) {
	got, err := searchPatterns("  Bánh  QUY ")
	if err != nil || !slices.Equal(got, []string{"%banh%", "%quy%"}) {
		t.Fatalf("patterns = %v, %v", got, err)
	}
	if _, err := searchPatterns(strings.Repeat("x", maxSearchTermLen+1)); apperr.KindOf(err) != apperr.Validation {
		t.Fatalf("long filter: %v", err)
	}
	norm := partySearchNorm(Party{Name: "Vang Lâm Viên", PhoneKey: ptr("02633821468")}, ptr("Rượu vang Đà Lạt"))
	for _, w := range []string{"vang", "lam", "vien", "02633821468", "ruou", "da", "lat"} {
		if !strings.Contains(norm, w) {
			t.Errorf("search norm %q lacks %q", norm, w)
		}
	}
}
