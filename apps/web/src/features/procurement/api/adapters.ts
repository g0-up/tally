import { ApiError, isApiError } from "../../../shared/api/api-error";
import { num } from "../../../shared/lib/format";
import type { OrderStatus, ReceiptListItem } from "../schemas";

// The Nhập hàng screens in the prototype's words: status labels, the client checks of the
// postings (prototype `receive`, `closePOShort`, `supplierReturn`, `buyLines`) and the server
// refusals mapped onto the same sentences.

/** API status → prototype label ("approved" in the spec is "confirmed" in the API). */
export const ORDER_STATUS_LABELS: Readonly<Record<OrderStatus, string>> = {
  draft: "Nháp",
  confirmed: "Đã chốt",
  partially_received: "Nhận một phần",
  completed: "Hoàn tất",
  closed_short: "Đóng – thiếu",
};

/** Orders that still wait for goods: "Nhận hàng", "Ứng trước NCC", "Đóng phần còn thiếu". */
export const isOpenOrder = (status: OrderStatus) => status === "confirmed" || status === "partially_received";

/** An open order whose expected delivery date (business date) has passed: "Quá hạn giao". */
export const isLate = (o: { status: OrderStatus; expected_date?: string | null | undefined }, today: string) =>
  isOpenOrder(o.status) && !!o.expected_date && o.expected_date < today;

/** What is still owed on a receipt: the list's open amount, else its payable's. */
export const receiptOpenAmount = (r: Pick<ReceiptListItem, "open_amount" | "payable">) => r.open_amount ?? r.payable?.open_amount ?? "0";

export const PROCUREMENT_MESSAGES = {
  discardReceipt: "Bỏ phiếu đang nhập?",
  orderNotReceivable: "Đơn nhập chưa duyệt hoặc đã đóng.",
  orderEmpty: "Đơn nhập chưa có dòng hàng.",
  orderNotDraft: "Đơn nhập đã chốt.",
  newProductPrice: (name: string) => `${name || "Hàng mới"}: nhập giá nhập.`,
  newProductQty: (name: string) => `${name || "Hàng mới"}: nhập số lượng.`,
  newProductUnit: (name: string) => `${name || "Hàng mới"}: chọn đơn vị cơ sở.`,
  factorPositive: "Hệ số quy đổi phải dương.",
  receiptEmpty: "Chưa nhập số lượng thực nhận.",
  lotRequired: (name: string) => `${name}: bắt buộc nhập số lô.`,
  expiryRequired: (name: string) => `${name}: bắt buộc nhập hạn dùng.`,
  overTolerance: (name: string, qty: string, remain: string) => `${name}: nhận ${num(qty)} vượt dung sai 5% so với còn chờ ${num(remain)}. Cần duyệt nhận vượt.`,
  notInOrderUnit: (name: string, unit: string) => `${name}: số lượng nhận không quy đổi đúng ra ${unit} của đơn nhập.`,
  freightWhole: "Chi phí vận chuyển/bốc xếp phải là số tiền nguyên đồng.",
  closeReasonRequired: "Cần lý do đóng phần thiếu.",
  returnEmpty: "Chưa nhập số lượng trả.",
  returnReasonRequired: "Cần lý do trả NCC.",
  returnOverLot: (name: string) => `${name}: số lượng trả vượt số còn trả được của dòng nhận.`,
  returnNotInUnit: (name: string, unit: string) => `${name}: số lượng trả không quy đổi đúng ra ${unit} của phiếu nhận.`,
  amountPositive: "Số tiền phải lớn hơn 0.",
  fundRequired: "Chọn quỹ chi.",
  invoiceRequired: "Nhập số hóa đơn.",
  supplierRequired: "Chọn nhà cung cấp.",
  barcodeShared: (code: string) => `Mã vạch “${code}” bị trùng ở hai dòng hàng mới.`,
  scanNotInOrder: (code: string) => `Mã “${code}” không có trong đơn nhập này.`,
  scanNotBuyable: (code: string, name: string) => `Mã “${code}” là ${name}, không phải hàng nhập.`,
  scanLookupFailed: (code: string, reason: string) => `Không tra được mã “${code}”: ${reason}`,
  orderCreatedAsSent: (docNo: string) =>
    `Đã lưu ${docNo} theo nội dung lần bấm trước, lần đó chưa nhận được trả lời. Thay đổi sau đó chưa được lưu và đơn chưa chốt: đóng hộp thoại rồi kiểm tra đơn ${docNo}.`,
  invoiceMatchedAsSent: (documentNo: string) =>
    `Đã đối chiếu theo số hóa đơn ${documentNo} của lần bấm trước. Số vừa sửa chưa được lưu.`,
  invoiceOutcomeUnknown: "chưa rõ đã gắn hóa đơn NCC hay chưa. Bấm lại để kiểm tra; không tạo hóa đơn trùng.",
} as const;

/** Server refusals of the procurement commands, in the prototype's words where it has them. */
export const PROCUREMENT_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  order_not_draft: PROCUREMENT_MESSAGES.orderNotDraft,
  order_status_invalid: PROCUREMENT_MESSAGES.orderNotReceivable,
  order_not_receivable: PROCUREMENT_MESSAGES.orderNotReceivable,
  order_has_no_lines: PROCUREMENT_MESSAGES.orderEmpty,
  receipt_exceeds_order: "Số lượng nhận vượt số còn chờ của đơn nhập. Phần vượt phải ghi là nhận vượt.",
  return_exceeds_received: "Số lượng trả vượt số còn trả được của phiếu nhận.",
  receipt_not_posted: "Phiếu nhận chưa ghi sổ.",
  receipt_has_followups: "Phiếu nhận đã có chứng từ phát sinh sau (trả NCC, thanh toán, đối chiếu).",
  receipt_stock_consumed: "Hàng của phiếu nhận đã xuất bớt nên không còn đủ để trả.",
  insufficient_stock: "Tồn lô tại kho không đủ để xuất trả.",
  supplier_document_exists: "Số hóa đơn này đã được ghi cho nhà cung cấp.",
  receipt_already_matched: "Phiếu nhận đã được đối chiếu hóa đơn.",
  purchase_order_changed: "Đơn nhập vừa được thay đổi. Tải lại rồi thử lại.",
  receipt_has_no_kept_lines: "Phiếu nhận không còn dòng hàng nào trong kho.",
};

/** The same refusal with the prototype sentence as its message, for the "Chưa ghi sổ." toast. */
export function withProcurementMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = PROCUREMENT_ERROR_MESSAGES[e.code];
  if (message === undefined) return e;
  return new ApiError({ status: e.status, code: e.code, message, fields: { ...e.fields }, exceptionKind: e.exceptionKind });
}
