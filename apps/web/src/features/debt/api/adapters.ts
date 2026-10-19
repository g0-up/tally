import { isApiError } from "../../../shared/api/api-error";
import type { DecimalLike } from "../../../shared/lib/decimal";
import { vnd } from "../../../shared/lib/format";
import type { AdjustmentKind, DebtDirection, Payment, StatementStatus } from "../schemas";

// The Công nợ screens in the prototype's words: ledger and status labels, the client checks of the
// postings (prototype `payModal`, `allocAdv`, `writeOff`, `debtNote`) and the server refusals mapped
// onto the same sentences.

export const STATEMENT_STATUS_LABELS: Readonly<Record<StatementStatus, string>> = {
  draft: "Nháp",
  sent: "Đã gửi",
  confirmed: "Khách xác nhận",
  disputed: "Tranh chấp",
  superseded: "Đã thay thế",
};

/** Purpose of a payment, the "Loại" column of advances and payments. */
export const PAYMENT_PURPOSE_LABELS: Readonly<Record<string, string>> = {
  settlement: "Thanh toán",
  advance: "Ứng trước",
  deposit: "Đặt cọc",
  refund: "Hoàn tiền",
  advance_refund: "Hoàn ứng trước",
  opening_advance: "Ứng trước đầu kỳ",
};

export const PAYMENT_STATUS_LABELS: Readonly<Record<Payment["status"], string>> = {
  pending_verification: "Chờ xác minh",
  confirmed: "Đã ghi sổ",
  reversed: "Đã hủy",
};

/** What a debt adjustment (DN) did to the balance. */
export const ADJUSTMENT_KIND_LABELS: Readonly<Record<AdjustmentKind, string>> = {
  increase: "Tăng nợ",
  decrease: "Giảm nợ",
  write_off: "Xóa nợ",
  opening_balance: "Số dư đầu kỳ khai báo",
  dispute_settlement: "Điều chỉnh sau tranh chấp",
};

/** "Diễn giải" of a statement line: the obligation source or the settlement that moved the balance. */
const LINE_KIND_LABELS: Readonly<Record<string, string>> = {
  sales_document: "Bán hàng",
  receipt: "Nhập hàng",
  landed_cost_allocation: "Chi phí mua hàng",
  purchase_price_adjustment: "Điều chỉnh giá mua",
  debt_adjustment: "Điều chỉnh công nợ",
  opening_balance: "Số dư đầu kỳ khai báo",
  refund_due: "Hoàn tiền phải trả",
  debt_offset: "Bù trừ công nợ",
  payment: "Thanh toán",
  credit_note: "Trả hàng",
  obligation_reversal: "Hủy chứng từ",
};

export function lineKindLabel(kind: string): string {
  const reversal = kind.endsWith("_reversal") && kind !== "obligation_reversal";
  const base = reversal ? kind.slice(0, -"_reversal".length) : kind;
  const label = LINE_KIND_LABELS[base] ?? "Khác";
  return reversal ? `Hủy ${label.toLowerCase()}` : label;
}

/** The two ledgers in the words of each screen. */
export const SIDE = {
  receivable: {
    party: "Khách hàng",
    owes: "Khách còn nợ",
    advance: "Tiền khách ứng trước",
    advanceShort: "Khách ứng trước",
    pay: "Thu tiền",
    payTitle: "Thu tiền khách",
    fund: "Vào quỹ",
    fundImpact: "Quỹ tăng",
    submit: "Ghi phiếu thu",
    ledger: "Sổ phải thu",
    paymentDirection: "in",
  },
  payable: {
    party: "Nhà cung cấp",
    owes: "Cửa hàng còn nợ",
    advance: "Cửa hàng ứng trước",
    advanceShort: "Cửa hàng ứng trước NCC",
    pay: "Trả tiền",
    payTitle: "Trả tiền nhà cung cấp",
    fund: "Chi từ quỹ",
    fundImpact: "Quỹ giảm",
    submit: "Ghi phiếu chi",
    ledger: "Sổ phải trả",
    paymentDirection: "out",
  },
} as const satisfies Record<DebtDirection, Record<string, string>>;

export const DEBT_MESSAGES = {
  amountPositive: "Số tiền phải lớn hơn 0.",
  partyRequired: (direction: DebtDirection) => (direction === "receivable" ? "Chọn khách hàng." : "Chọn nhà cung cấp."),
  fundRequired: "Chọn quỹ.",
  overPayment: "Tổng phân bổ vượt số tiền.",
  overOpen: "Có dòng phân bổ vượt số còn nợ.",
  advanceOver: "Phân bổ vượt số tiền còn lại.",
  advanceOverOpen: (docNo: string) => `Phân bổ vượt dư nợ ${docNo}`,
  allocationEmpty: "Chưa nhập số tiền phân bổ.",
  noteRequired: "Nhập nội dung.",
  reasonRequired: "Chọn lý do.",
  writeOffOverOpen: "Số tiền vượt số còn nợ của chứng từ.",
  disputeRequired: "Nhập nội dung khách phản hồi.",
  periodInvalid: "Đến ngày phải sau hoặc bằng từ ngày.",
} as const;

/** Server refusals of the debt commands, in the prototype's words where it has them. */
export const DEBT_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  allocation_exceeds_open: DEBT_MESSAGES.overOpen,
  allocation_exceeds_payment: DEBT_MESSAGES.overPayment,
  allocation_party_mismatch: "Chứng từ phân bổ không thuộc đối tác này.",
  allocation_direction_mismatch: "Chứng từ phân bổ không cùng chiều công nợ.",
  obligation_settled: "Chứng từ đã được thanh toán hết. Tải lại để xem số còn nợ.",
  obligation_not_found: "Không tìm thấy chứng từ công nợ.",
  insufficient_advance: DEBT_MESSAGES.advanceOver,
  reason_invalid: "Lý do không còn dùng được. Chọn lý do khác.",
  statement_confirmed: "Bảng kê đã được khách xác nhận.",
  statement_invalid_state: "Trạng thái bảng kê không cho phép thao tác này. Tải lại rồi thử lại.",
  payment_reversed: "Phiếu đã bị hủy. Tải lại để xem số chưa phân bổ.",
  payment_changed: "Phiếu vừa thay đổi ở nơi khác. Tải lại rồi thử lại.",
  payment_pending_verification: "Phiếu đang chờ xác minh tiền về, chưa phân bổ được.",
  payment_not_pending: "Phiếu không còn chờ xác minh. Tải lại để xem trạng thái mới.",
  payment_not_found: "Không tìm thấy phiếu, có thể đã bị xóa. Tải lại để xem bản mới nhất.",
  payment_has_successor: "Phiếu đã có phiếu điều chỉnh thay thế. Thao tác trên phiếu mới nhất.",
  payment_without_party: "Phiếu không gắn với đối tác nào nên không phân bổ được.",
  payment_exists: "Phiếu này đã được ghi trước đó. Tải lại để kiểm tra.",
  payment_method_inactive: "Phương thức thanh toán đã ngừng dùng. Chọn phương thức khác.",
  payment_method_not_found: "Không tìm thấy phương thức thanh toán. Tải lại rồi chọn lại.",
  party_not_found: "Không tìm thấy đối tác, có thể đã bị xóa. Chọn lại đối tác.",
  nothing_owed: "Đối tác không còn khoản nợ nào để giảm.",
  obligation_reversed: "Chứng từ đã bị hủy. Tải lại để xem số còn nợ.",
  obligation_exists: "Chứng từ đã có khoản công nợ khác. Tải lại để kiểm tra.",
  obligation_has_credits: "Chứng từ đã có khoản giảm trừ, không thao tác được.",
  credit_note_exists: "Chứng từ đã có phiếu trả hàng khác.",
  refund_not_due: "Chứng từ không phải khoản hoàn tiền phải trả.",
  refund_party_mismatch: "Phiếu cần hoàn thuộc đối tác khác.",
  refund_direction_mismatch: "Phiếu cần hoàn không cùng chiều công nợ.",
  refund_must_settle: "Khoản hoàn tiền phải được thanh toán hết trong một lần.",
  reversal_before_document: "Ngày hủy không được trước ngày chứng từ.",
  attachment_invalid: "Tệp đính kèm không hợp lệ. Tải lại tệp rồi thử lại.",
};

/** The same refusal with the prototype sentence as its message, for the "Chưa ghi sổ." toast. */
export function withDebtMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = DEBT_ERROR_MESSAGES[e.code];
  if (message === undefined) return e;
  return e.withMessage(message);
}

/** "Nhóm · hạn mức 50.000.000 đ" under a customer name, the phone when the group is unknown. */
export function partySubLine(direction: DebtDirection, group: string | null | undefined, limit: DecimalLike | null | undefined, phone: string | null | undefined): string {
  if (group) return direction === "receivable" && limit != null ? `${group} · hạn mức ${vnd(limit)}` : group;
  return phone ?? "";
}
