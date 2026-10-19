import { ApiError, errorMessage } from "../../../shared/api/api-error";
import { translateSalesError } from "../../sales/api/mutations";

// Vietnamese text for why the server parked or refused an offline sale. The sync answer carries
// a code and the server's English detail; known codes get their sentence, others the sales text.

const OFFLINE_CODE_MESSAGES: Readonly<Record<string, string>> = {
  offline_payment_not_allowed: "Ngoại tuyến chỉ cho bán thu đủ tiền mặt.",
  offline_pos_disabled: "Cửa hàng đã tắt bán ngoại tuyến.",
  offline_amount_exceeded: "Vượt hạn mức bán ngoại tuyến.",
  offline_window_expired: "Bán ngoài thời gian cho phép không đồng bộ.",
  offline_sale_already_posted: "Giao dịch đã được ghi sổ trước đó.",
  sync_exception_closed: "Giao dịch đã được xử lý.",
  exception_required: "Cần quản lý xác nhận ngoại lệ (giá dưới giá tối thiểu).",
  internal_error: "Máy chủ gặp lỗi. Thử lại sau ít phút.",
};

export function syncErrorText(code: string | null | undefined, detail: string | null | undefined): string {
  const c = code ?? "";
  const known = OFFLINE_CODE_MESSAGES[c];
  if (known) return known;
  return errorMessage(translateSalesError(new ApiError({ status: 409, code: c, message: detail ?? c, fromServer: true })));
}
