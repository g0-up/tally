package provider

import (
	"fmt"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// ManualName is the provider key of manual bank transfers.
const ManualName = "manual"

// ManualTransfer is a bank transfer the owner makes with the invoice number as the transfer
// note; a platform admin marks the invoice paid with the bank reference.
type ManualTransfer struct {
	account string
}

// NewManualTransfer returns the adapter; account describes the receiving bank account and may
// be empty until one is configured.
func NewManualTransfer(account string) ManualTransfer {
	return ManualTransfer{account: strings.TrimSpace(account)}
}

// Name implements Provider.
func (ManualTransfer) Name() string { return ManualName }

// Instructions implements Provider.
func (m ManualTransfer) Instructions(invoiceNumber string, amount money.Money) string {
	s := fmt.Sprintf("Chuyển khoản %s đ, nội dung chuyển khoản: %s.", amount.FormatVND(), invoiceNumber)
	if m.account != "" {
		s += " Tài khoản nhận: " + m.account + "."
	}
	return s + " Hóa đơn được xác nhận khi bộ phận hỗ trợ nhận được tiền."
}

// ManualEventID is the payment event id of an operator confirmation; one per invoice, so a
// repeated confirmation is a no-op.
func ManualEventID(invoiceID string) string { return "manual:" + invoiceID }
