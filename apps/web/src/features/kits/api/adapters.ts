import { ApiError, isApiError } from "../../../shared/api/api-error";
import { dec, type DecimalLike } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import type { AssemblyOrder, AssemblyStatus, KitStatus, KitType } from "../schemas";

// The Giỏ quà screens in the prototype's words: labels of the API enums, the client checks of the
// commands and the server refusals mapped onto the same sentences.

export const KIT_TYPE_TAGS: Readonly<Record<KitType, string>> = { kit: "Giỏ đóng sẵn", combo: "Combo ghép khi bán" };

export const KIT_TYPE_OPTIONS: Readonly<Record<KitType, string>> = {
  kit: "Giỏ đóng sẵn (có tồn, qua lệnh đóng)",
  combo: "Combo ghép khi bán (không có tồn)",
};

export const KIT_STATUS_LABELS: Readonly<Record<KitStatus, string>> = { draft: "Nháp", active: "Đang bán", inactive: "Ngừng bán" };

export const ASSEMBLY_STATUS_LABELS: Readonly<Record<AssemblyStatus, string>> = {
  draft: "Nháp",
  reserved: "Đang đóng",
  in_progress: "Đang đóng",
  partially_completed: "Đang đóng",
  completed: "Hoàn thành",
  closed: "Đã đóng lệnh",
};

/** Steps of the assembly drawer. */
export const ASSEMBLY_STEPS = ["Nháp", "Chốt & giữ NL", "Soạn NL", "Đóng giỏ", "QC", "Nhập kho"] as const;

/** Finished steps: a draft has only been planned, an open order is being packed, a finished one is in stock. */
export function assemblyStepsDone(status: AssemblyStatus): number {
  if (status === "draft") return 1;
  if (status === "completed" || status === "closed") return ASSEMBLY_STEPS.length;
  return 4;
}

/** Kits still to assemble on an order. */
export const assemblyRemaining = (o: Pick<AssemblyOrder, "planned_qty" | "completed_qty" | "disassembled_qty">) =>
  dec(o.planned_qty).minus(o.completed_qty).plus(o.disassembled_qty);

/** Kits on hand from an order: completed less taken apart. */
export const assemblyNet = (o: Pick<AssemblyOrder, "completed_qty" | "disassembled_qty">) => dec(o.completed_qty).minus(o.disassembled_qty);

export const canPostAssembly = (s: AssemblyStatus) => s === "reserved" || s === "in_progress" || s === "partially_completed";

/** "X (N%)" when the components cost the customer more than the kit, else "Không" (cards) or "—" (editor). */
export function savingsText(price: DecimalLike | null | undefined, retail: DecimalLike, none: string): string {
  if (price === null || price === undefined || dec(price).lte(0) || !dec(retail).gt(price)) return none;
  const save = dec(retail).minus(price);
  return `${vnd(save)} (${save.div(retail).times(100).toDecimalPlaces(0).toString()}%)`;
}

/** Gross margin share of the price, or null without a price. */
export function marginRatio(price: DecimalLike | null | undefined, cost: DecimalLike): number | null {
  if (price === null || price === undefined || dec(price).lte(0)) return null;
  return dec(price).minus(cost).div(price).toNumber();
}

export const marginText = (ratio: number | null) => (ratio === null ? "—" : `${Math.round(ratio * 100)}%`);

/** Below this margin the editor warns. */
export const LOW_MARGIN = 0.15;

export const KIT_MESSAGES = {
  codeRequired: "Nhập mã.",
  nameRequired: "Nhập tên.",
  pricePositive: "Giá bán lẻ phải lớn hơn 0.",
  wholesaleAboveRetail: "Giá sỉ không được cao hơn giá bán lẻ.",
  retailNegative: "Giá bán lẻ không được âm.",
  wholesaleNegative: "Giá sỉ không được âm.",
  minStockNegative: "Tồn tối thiểu không được âm.",
  minStockOnCombo: "Combo không có tồn tối thiểu.",
  noComponent: "Thêm ít nhất một thành phần.",
  componentIncomplete: "Chọn hàng và nhập số lượng cho từng thành phần.",
  mixNoQty: "Mix vị: nhập số lượng.",
  mixGroupStopped: (group: string) => `Nhóm mix ${group} đã ngừng: chọn nhóm khác hoặc xóa dòng mix này.`,
  mixNoFlavor: (group: string) => `Mix vị ${group}: chưa chọn vị nào.`,
  mixOver: (group: string, used: string, cap: string, part: string) => `Mix vị ${group}: đã chọn ${num(used)} ${part}, vượt ${num(cap)} ${part}.`,
  noteRequired: "Nhập lý do thay đổi.",
  noUnit: "Chưa có đơn vị tính để tạo giỏ/combo.",
  kitRequired: "Chọn công thức.",
  planPositive: "Số giỏ kế hoạch phải lớn hơn 0.",
  recipeLoading: "Đang tải thành phần của công thức, thử lại sau giây lát.",
  recipeUnavailable: "Không tải được thành phần của công thức; đóng hộp thoại rồi mở lại.",
  colorPick: (names: readonly string[]) => `Chọn màu cho ${names.join(", ")}.`,
  doneRange: (max: string) => `Số lượng hoàn thành phải từ 1 đến ${num(max)}.`,
  createdAsSent: (name: string, code: string) =>
    `Đã lưu nháp ${name} (mã ${code}) từ lần bấm trước, lần đó chưa nhận được trả lời. Tên, thành phần, giá và tồn tối thiểu vừa sửa chưa được lưu, giỏ chưa phát hành. Loại, mã và đơn vị không đổi được nữa: đóng hộp thoại rồi sửa giỏ trên danh sách.`,
  assemblyCreatedAsSent: (docNo: string) =>
    `Đã tạo ${docNo} theo nội dung lần bấm trước, lần đó chưa nhận được trả lời. Thay đổi sau đó chưa được lưu: đóng hộp thoại rồi kiểm tra lệnh ${docNo}.`,
  lotRequired: "Chọn lô giỏ thành phẩm.",
  lotSourceLoading: "Đang tìm lệnh đóng của lô giỏ, thử lại sau giây lát.",
  lotSourceMissing: (lotNo: string) => `Không tìm thấy lệnh đóng đã tạo lô ${lotNo}; đóng hộp thoại rồi mở lại.`,
  disassembleRange: (max: string) => `Chỉ còn ${num(max)} giỏ có thể tháo.`,
  reasonRequired: "Chọn lý do tháo giỏ.",
} as const;

/** Server refusals of the kit commands, in the prototype's words where it has them. */
export const KIT_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  kit_published: "Chỉ xóa được giỏ/combo nháp chưa phát hành.",
  kit_sold: "Giỏ/combo đã bán hoặc đã đóng, không xóa được.",
  kit_not_published: "Giỏ chưa phát hành, chưa đóng được.",
  recipe_not_draft: "Phiên bản này đã phát hành; tạo phiên bản mới để thay đổi.",
  recipe_draft_exists: "Giỏ đang có một phiên bản nháp chưa phát hành; sửa hoặc phát hành bản nháp đó trên thẻ giỏ.",
  not_a_kit: "Chỉ giỏ đóng sẵn mới có lệnh đóng.",
  assembly_order_not_open: "Lệnh đóng đã hoàn thành hoặc đã đóng.",
  assembly_order_status_invalid: "Lệnh chưa ở trạng thái đang đóng.",
  exceeds_remaining: "Vượt số giỏ còn phải đóng.",
  insufficient_stock: "Nguyên liệu không đủ.",
  kit_lot_exhausted: "Lô giỏ không còn đủ để tháo.",
  color_required: "Chọn màu cho thành phần chưa cố định màu.",
  sku_code_taken: "Mã đã được dùng.",
  assembly_order_changed: "Lệnh đóng vừa thay đổi trong lúc ghi; thử lại.",
  assembly_order_has_postings: "Lệnh đã có đợt đóng, không đổi được.",
  substitute_exceeds_remaining: "Thành phần không còn lượng nào để thay thế.",
  combo_not_sellable: "Combo chưa phát hành hoặc công thức chưa hiệu lực hôm nay.",
  expired_lot_blocked: "Lô đã hết hạn, bị chặn xuất.",
  lot_mismatch: "Lô không thuộc thành phần này.",
  idempotency_key_reused: "Lần ghi trước khác nội dung lần này; đóng hộp thoại rồi mở lại để ghi mới.",
  idempotency_key_in_progress: "Lần ghi trước vẫn đang xử lý; đợi giây lát rồi thử lại.",
  exception_confirmation_used: "Xác nhận đã dùng cho lần ghi trước; xác nhận lại.",
};

/** The same refusal with the prototype sentence as its message, for the "Chưa ghi sổ." toast. */
export function withKitMessage(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const message = KIT_ERROR_MESSAGES[e.code];
  if (message === undefined) return e;
  return new ApiError({ status: e.status, code: e.code, message, fields: { ...e.fields }, exceptionKind: e.exceptionKind });
}
