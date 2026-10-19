import { ApiError, isApiError } from "../../../shared/api/api-error";
import { isColorCode, type ColorKey } from "../../../shared/domain/color";
import { num } from "../../../shared/lib/format";
import type { AdjustmentKind, CountStatus, Lot, Reservation, StockMoveType } from "../schemas";

// The Kho screens in the prototype's words: labels of the API enums, the client checks of the
// postings and the server refusals mapped onto the same sentences.

/** API color code (or null) → the fixed color key; anything unknown reads "Chưa gán màu". */
export const colorKeyOf = (code: string | null | undefined): ColorKey => (isColorCode(code) ? code : "none");

export const ADJUSTMENT_KIND_LABELS: Readonly<Record<AdjustmentKind, string>> = {
  increase: "Điều chỉnh tăng",
  decrease: "Điều chỉnh giảm",
  damage: "Hỏng",
  loss: "Hao hụt",
  gift: "Biếu tặng",
  internal_use: "Dùng nội bộ",
  disposal: "Tiêu hủy",
  quarantine_in: "Cách ly",
  quarantine_release: "Khôi phục bán được",
  opening: "Tồn đầu",
  count: "Kiểm kê",
};

/** Kinds offered by "Điều chỉnh giảm kho"; each needs a reason of the same kind. */
export const DECREASE_KINDS = ["damage", "loss", "gift", "internal_use"] as const satisfies readonly AdjustmentKind[];
export type DecreaseKind = (typeof DECREASE_KINDS)[number];

export const ADJUSTMENT_STATUS_LABELS = { posted: "Đã ghi sổ", reversed: "Đã đảo" } as const;

export const COUNT_STATUS_LABELS: Readonly<Record<CountStatus, string>> = {
  counting: "Đang đếm",
  pending_post: "Chờ ghi sổ",
  posted: "Đã ghi sổ",
};

export const MOVE_TYPE_LABELS: Readonly<Record<StockMoveType, string>> = {
  receipt: "Nhập mua",
  purchase_return: "Trả NCC",
  sale: "Xuất bán",
  sales_return: "Nhập trả hàng",
  adjustment_in: "Điều chỉnh tăng",
  adjustment_out: "Điều chỉnh giảm",
  count_gain: "Điều chỉnh kiểm kê",
  count_loss: "Điều chỉnh kiểm kê",
  assembly_consume: "Xuất đóng giỏ",
  assembly_produce: "Nhập giỏ thành phẩm",
  disassembly_consume: "Xuất tháo giỏ",
  disassembly_return: "Nhập lại từ tháo giỏ",
  quarantine_out: "Khôi phục bán được",
  quarantine_in: "Chuyển cách ly",
  opening: "Tồn ban đầu",
  cost_adjustment: "Điều chỉnh giá vốn",
  reversal: "Đảo chứng từ",
};

/** Prototype "Tình trạng" of a lot. */
export function lotStatusLabel(lot: Pick<Lot, "state">): string {
  switch (lot.state) {
    case "quarantine":
      return "Cách ly – chờ kiểm tra";
    case "expired":
      return "Hết hạn – chặn bán";
    case "expiring":
      return "Sắp hết hạn";
    default:
      return "Bán được";
  }
}

/** Prototype "Còn": "Quá N ngày", "N ngày" or "—" for a lot without expiry. */
export function lotDaysLabel(days: number | null | undefined): string {
  if (days === null || days === undefined) return "—";
  return days < 0 ? `Quá ${-days} ngày` : `${days} ngày`;
}

/**
 * Only holds set by hand are released here; the API releases the holds of an invoice or an
 * assembly order through that document.
 */
export const releasable = (r: Pick<Reservation, "source_type" | "status">) => r.status === "active" && r.source_type === "manual";

/** Longest release reason the API stores. */
export const RELEASE_REASON_MAX = 500;

export const INVENTORY_MESSAGES = {
  reasonRequired: "Cần lý do.",
  reasonTooLong: `Lý do tối đa ${RELEASE_REASON_MAX} ký tự.`,
  productRequired: "Chọn hàng.",
  colorRequired: "Chọn màu.",
  qtyPositive: "Số lượng phải lớn hơn 0.",
  overSellable: (qty: string, base: string) => `Vượt tồn bán được (${num(qty)} ${base}).`,
  retailPositive: "Giá bán lẻ phải lớn hơn 0.",
  wholesaleAboveRetail: "Giá sỉ không được cao hơn giá bán lẻ.",
  noColorPrice: "Màu này chưa có giá riêng.",
  unchanged: "Giá không thay đổi.",
  noCountedLine: "Chưa nhập số đếm.",
  countNotPending: "Hoàn tất đếm trước khi ghi sổ chênh lệch.",
  countNotLoaded: "Phiên kiểm kê chưa tải xong; thử lại sau giây lát.",
} as const;

/** Server refusals of the inventory commands, in the prototype's words where it has them. */
export const INVENTORY_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  insufficient_stock: "Tồn bán được không đủ.",
  count_incomplete: "Còn dòng chưa nhập số đếm.",
  count_lot_required: "Hàng theo lô: nhập số lô cho dòng đếm thêm.",
  count_not_counting: "Phiên kiểm kê không còn ở trạng thái đang đếm.",
  reservation_not_active: "Lượng giữ đã được dùng hoặc giải phóng.",
  reservation_not_manual: "Lượng giữ thuộc chứng từ; sửa chứng từ đó để giải phóng.",
  insufficient_sellable: "Tồn bán được không đủ.",
  no_stock_to_adjust: "Không còn tồn để điều chỉnh.",
  count_not_finished: INVENTORY_MESSAGES.countNotPending,
  count_already_posted: "Phiên kiểm kê đã ghi sổ.",
  count_empty: "Phiên kiểm kê không có mặt hàng nào.",
  lot_quarantined: "Lô đang cách ly.",
  expired_lot_blocked: "Lô đã hết hạn, bị chặn bán.",
  color_required: "Hàng có tồn theo màu: chọn màu.",
  lot_mismatch: "Lô không thuộc mặt hàng này.",
  sku_not_stockable: "Combo ghép khi bán không có tồn riêng.",
  already_reversed: "Chứng từ đã được đảo.",
  idempotency_key_reused: "Lần ghi trước khác nội dung lần này; đóng hộp thoại rồi mở lại để ghi mới.",
  idempotency_key_in_progress: "Lần ghi trước vẫn đang xử lý; đợi giây lát rồi thử lại.",
  exception_confirmation_used: "Xác nhận đã dùng cho lần ghi trước; xác nhận lại.",
};

/** The same refusal with the prototype sentence as its message, for the "Chưa ghi sổ." toast. */
export function withInventoryMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = INVENTORY_ERROR_MESSAGES[e.code];
  if (message === undefined) return e;
  return new ApiError({ status: e.status, code: e.code, message, fields: { ...e.fields }, exceptionKind: e.exceptionKind });
}
