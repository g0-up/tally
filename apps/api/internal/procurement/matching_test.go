package procurement

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

func TestValidateDocument(t *testing.T) {
	ok := DocumentInput{SupplierPartyID: ids.New(), DocumentNo: " HD-001 ", DocumentDate: ptr(day("2026-10-17")),
		GoodsAmount: vnd("1000000"), TaxAmount: vnd("100000"), TotalAmount: ptr(vnd("1100000")), ReceiptIDs: []uuid.UUID{ids.New()}}
	wantFields(t, validateDocument(ok), map[string]string{})

	wantFields(t, validateDocument(DocumentInput{DocumentNo: "  "}), map[string]string{
		"supplier_party_id": "required", "document_no": "required", "document_date": "required",
	})
	receipt := ids.New()
	bad := DocumentInput{SupplierPartyID: ids.New(), DocumentNo: strings.Repeat("9", MaxDocumentNo+1),
		DocumentDate: ptr(day("2026-10-17")), GoodsAmount: vnd("-1"), TaxAmount: vnd("-1"), TotalAmount: ptr(vnd("0")),
		ReceiptIDs: []uuid.UUID{receipt, uuid.Nil, receipt}}
	wantFields(t, validateDocument(bad), map[string]string{
		"document_no":   "too_long",
		"goods_amount":  "must_not_be_negative",
		"tax_amount":    "must_not_be_negative",
		"total_amount":  "total_mismatch",
		"receipt_ids.1": "required",
		"receipt_ids.2": "duplicate",
	})
}

func TestValidateReceiptIDsCapsTheList(t *testing.T) {
	fields := map[string]string{}
	validateReceiptIDs(fields, "receipt_ids", make([]uuid.UUID, MaxLines+1))
	wantFields(t, fields, map[string]string{"receipt_ids": "too_many"})
}
