package docseq

// Document prefixes (ubiquitous language §5). Every posting command issues its number through
// Issuer.Next with one of these; the tenant provisioner creates their counters up front.
const (
	PrefixQuote           = "BG" // Báo giá
	PrefixInvoice         = "SO" // Hóa đơn
	PrefixStockOut        = "PX" // Phiếu xuất kho
	PrefixSalesDocument   = "BH" // Chứng từ bán (from a stock-out)
	PrefixPOSDocument     = "HD" // Chứng từ bán POS
	PrefixPOSCart         = "GH" // Giỏ hàng POS
	PrefixSalesReturn     = "TH" // Trả hàng
	PrefixExchange        = "DH" // Đổi hàng
	PrefixPurchaseOrder   = "PO" // Đơn nhập
	PrefixReceipt         = "PN" // Phiếu nhận
	PrefixSupplierReturn  = "XT" // Trả NCC
	PrefixStockCount      = "KK" // Kiểm kê
	PrefixStockAdjustment = "DC" // Điều chỉnh kho
	PrefixKitAssembly     = "LD" // Lệnh đóng giỏ
	PrefixKitDisassembly  = "TG" // Tháo giỏ
	PrefixCashReceipt     = "PT" // Phiếu thu
	PrefixCashPayment     = "PC" // Phiếu chi
	PrefixStatement       = "DS" // Bảng kê đối soát
	PrefixShift           = "CA" // Ca bán hàng
	PrefixDebtAdjustment  = "DN" // Điều chỉnh công nợ
	PrefixDebtOffset      = "BT" // Biên bản bù trừ
	PrefixCostAdjustment  = "DG" // Điều chỉnh giá nhập
)

// Prefixes lists every document prefix in the order of the ubiquitous-language table.
func Prefixes() []string {
	return []string{
		PrefixQuote, PrefixInvoice, PrefixStockOut, PrefixSalesDocument, PrefixPOSDocument, PrefixPOSCart,
		PrefixSalesReturn, PrefixExchange, PrefixPurchaseOrder, PrefixReceipt, PrefixSupplierReturn,
		PrefixStockCount, PrefixStockAdjustment, PrefixKitAssembly, PrefixKitDisassembly, PrefixCashReceipt,
		PrefixCashPayment, PrefixStatement, PrefixShift, PrefixDebtAdjustment, PrefixDebtOffset,
		PrefixCostAdjustment,
	}
}
