package db

import (
	"slices"
	"testing"
)

func TestParentsFirstOrdersParentsBeforeChildren(t *testing.T) {
	refs := []tableReference{
		{Child: "sale_lines", Parent: "sales", Constraint: "fk_sale_lines__sale_id", Columns: JSONList[string]{"sale_id"}},
		{Child: "sales", Parent: "parties", Constraint: "fk_sales__party_id", Columns: JSONList[string]{"party_id"}, Nullable: true},
		{Child: "elsewhere", Parent: "outside", Constraint: "fk_elsewhere__outside_id", Columns: JSONList[string]{"outside_id"}},
	}
	order, deferred, err := parentsFirst([]string{"sale_lines", "sales", "parties", "audit"}, refs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"audit", "parties", "sales", "sale_lines"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if len(deferred) != 0 {
		t.Errorf("deferred = %v, want none without a cycle", deferred)
	}
}

func TestParentsFirstDefersANullableReferenceOnACycle(t *testing.T) {
	refs := []tableReference{
		{Child: "invoices", Parent: "quotes", Constraint: "fk_invoices__quote_id", Columns: JSONList[string]{"quote_id"}, Nullable: true},
		{Child: "quotes", Parent: "invoices", Constraint: "fk_quotes__converted_invoice_id", Columns: JSONList[string]{"converted_invoice_id"}, Nullable: true},
		{Child: "invoice_lines", Parent: "invoices", Constraint: "fk_invoice_lines__invoice_id", Columns: JSONList[string]{"invoice_id"}},
		{Child: "quotes", Parent: "parties", Constraint: "fk_quotes__party_id", Columns: JSONList[string]{"party_id"}, Nullable: true},
	}
	order, deferred, err := parentsFirst([]string{"quotes", "invoices", "invoice_lines", "parties"}, refs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"parties", "invoices", "quotes", "invoice_lines"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	want := map[string][]DeferredRef{"invoices": {{Constraint: "fk_invoices__quote_id", Columns: []string{"quote_id"}}}}
	if len(deferred) != 1 || len(deferred["invoices"]) != 1 || deferred["invoices"][0].Constraint != want["invoices"][0].Constraint ||
		!slices.Equal(deferred["invoices"][0].Columns, want["invoices"][0].Columns) {
		t.Errorf("deferred = %v, want %v", deferred, want)
	}
}

func TestParentsFirstRefusesACycleOfRequiredReferences(t *testing.T) {
	refs := []tableReference{
		{Child: "a", Parent: "b", Constraint: "fk_a__b_id", Columns: JSONList[string]{"b_id"}},
		{Child: "b", Parent: "a", Constraint: "fk_b__a_id", Columns: JSONList[string]{"a_id"}},
	}
	if _, _, err := parentsFirst([]string{"a", "b"}, refs); err == nil {
		t.Fatal("a cycle of NOT NULL references must be refused")
	}
}
