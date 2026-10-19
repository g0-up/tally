import { colorLabel, type ColorKey } from "../../../shared/domain/color";
import { ZERO_MONEY, toMoney } from "../../../shared/domain/money";
import { dec, toDecimalString } from "../../../shared/lib/decimal";
import type { Invoice, InvoiceStatus, PaymentState, PriceSource, QuoteStatus, SalesDocument, SalesReturn } from "../schemas";

// Display adapters of the sales screens: Vietnamese labels for API enums (StatusPill picks the
// tone from the label) and the small derivations the prototype shows next to a document.

export const QUOTE_STATUS_LABELS: Readonly<Record<QuoteStatus, string>> = {
  draft: "Nháp",
  sent: "Đã gửi",
  accepted: "Khách chấp nhận",
  rejected: "Khách từ chối",
  converted: "Đã chuyển hóa đơn",
  expired: "Hết hạn",
};

export const INVOICE_STATUS_LABELS: Readonly<Record<InvoiceStatus, string>> = {
  draft: "Nháp",
  confirmed: "Đã xác nhận",
  partially_delivered: "Giao một phần",
  completed: "Hoàn tất",
  cancelled_remaining: "Đã hủy phần còn lại",
};

export type PaymentCode = PaymentState["code"];

export const PAYMENT_LABELS: Readonly<Record<PaymentCode, string>> = {
  unpaid: "Chưa thanh toán",
  partial: "Thanh toán một phần",
  paid: "Đã thanh toán đủ",
  overpaid: "Thừa tiền",
};

/**
 * What the customer paid against the total, checked in the server's order (paymentStateOf): a
 * total met exactly is paid even at 0, so an invoice of nothing reads the same here and on the server.
 */
export function paymentCodeOf(total: string, paid: string): PaymentCode {
  const c = dec(paid).cmp(dec(total));
  if (c > 0) return "overpaid";
  if (c === 0) return "paid";
  if (dec(paid).lte(0)) return "unpaid";
  return "partial";
}

export type DeliveryCode = "none" | "partial" | "full";

export const DELIVERY_LABELS: Readonly<Record<DeliveryCode, string>> = {
  none: "Chưa giao",
  partial: "Giao một phần",
  full: "Đã giao đủ",
};

/** Delivery state of an invoice from its status and the value already delivered. */
export function deliveryCodeOf(status: InvoiceStatus, deliveredAmount: string): DeliveryCode {
  if (status === "completed") return "full";
  if (status === "partially_delivered") return "partial";
  return dec(deliveredAmount).gt(0) ? "partial" : "none";
}

/**
 * Payment state of an invoice: as read, or from the `paid_amount` of a list row the way the server
 * derives it. An invoice carrying neither counts as nothing paid yet.
 */
export function invoicePayment(invoice: Pick<Invoice, "status" | "total_amount" | "delivered_amount" | "payment" | "paid_amount">): PaymentState {
  if (invoice.payment) return invoice.payment;
  // Once the rest of an invoice is cancelled, only what was delivered is owed.
  const total = invoice.status === "cancelled_remaining" ? invoice.delivered_amount : invoice.total_amount;
  const paid = invoice.paid_amount ?? ZERO_MONEY;
  const code = paymentCodeOf(total, paid);
  // An overpayment stays the customer's advance: nothing is open.
  const open = code === "overpaid" ? ZERO_MONEY : toMoney(dec(total).minus(paid));
  return { total_amount: total, paid_amount: paid, open_amount: open, pending_amount: ZERO_MONEY, code, label: PAYMENT_LABELS[code] };
}

/** A base-unit quantity in the unit of its line ("24" hộp in a 12-hộp thùng line → "2"). */
export function inLineUnits(baseQty: string, unitFactor: string): string {
  const f = dec(unitFactor);
  return f.lte(0) ? baseQty : toDecimalString(dec(baseQty).div(f));
}

/** Draft invoices are the only ones that can still be edited or deleted. */
export function invoiceEditable(status: InvoiceStatus): boolean {
  return status === "draft";
}

/** Confirmed and still waiting for (part of) its delivery. */
export function invoiceOpen(status: InvoiceStatus): boolean {
  return status === "confirmed" || status === "partially_delivered";
}

/** "Nợ 15 ngày" / "Thanh toán ngay" from the invoice date and the due date. */
export function termsLabel(days: number): string {
  return days > 0 ? `Nợ ${days} ngày` : "Thanh toán ngay";
}

const PRICE_KIND_LABELS: Readonly<Record<PriceSource["kind"], string>> = {
  customer_list: "Giá riêng của khách",
  group_list: "Bảng giá nhóm khách",
  season_list: "Bảng giá mùa vụ",
  base_list: "Giá bán lẻ",
  manual: "Giá nhập tay",
};

/** "Bảng giá Đại lý cấp 1 · giá riêng Đỏ": the price list that priced a line. */
export function priceSourceLabel(source: Pick<PriceSource, "kind" | "price_list_name">, color: ColorKey | null = null): string {
  const base = source.kind === "manual" || source.kind === "base_list" ? PRICE_KIND_LABELS[source.kind] : (source.price_list_name ?? PRICE_KIND_LABELS[source.kind]);
  return color !== null && color !== "none" ? `${base} · giá riêng ${colorLabel(color)}` : base;
}

export const SALES_DOC_SOURCE_LABELS: Readonly<Record<SalesDocument["source"], string>> = {
  stock_out: "Xuất kho giao khách",
  pos: "Bán tại quầy",
  pos_offline: "Bán tại quầy (ngoại tuyến)",
  exchange: "Đổi hàng",
};

export const SALES_DOC_STATUS_LABELS: Readonly<Record<SalesDocument["status"], string>> = {
  posted: "Đã ghi sổ",
  reversed: "Đã đảo",
  partially_reversed: "Đã trả một phần",
};

/** A named customer's return leaves a refund due; a walk-in customer was paid back on posting. */
export function refundDue(r: Pick<SalesReturn, "customer_party_id" | "refund_due_amount">): boolean {
  return r.customer_party_id !== null && dec(r.refund_due_amount).gt(0);
}

/** Names a sales line for tables and toasts ("Bánh quy bơ 454g"). */
export function lineName(l: { name: string; variant_label: string }): string {
  return l.variant_label ? `${l.name} ${l.variant_label}` : l.name;
}
