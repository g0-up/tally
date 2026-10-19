import { Fragment, createElement, type ReactNode } from "react";
import type { QueryKey } from "@tanstack/react-query";
import { ApiError, ERROR_CODES, errorMessage, fieldReasonMessage, isApiError, isExceptionRequired, isReauthRequired, isSessionExpired } from "../../../shared/api/api-error";
import { outcomeUnknown } from "../../../shared/hooks/create-once";
import { CommitCancelledError, PartialCommitError } from "../../../shared/hooks/use-commit";
import type { ConfirmException, ExceptionPrompt } from "../../../shared/hooks/use-confirm-exception";

// Write-side helpers shared by the sales and POS commands: what to refresh after a posting,
// Vietnamese text for the sales error codes, the retry of the retryable conflicts and the loop
// that lets the owner confirm several exceptions for one command.

/** Every posting moves stock, money and debt: refresh all screens that show them. */
export const POSTING_KEYS: readonly QueryKey[] = [["sales"], ["pos"], ["inventory"], ["debt"], ["cash"], ["dashboard"]];

/** Version of a document as the If-Match value. */
export function etagOf(version: number): string {
  return `"${version}"`;
}

const SALES_MESSAGES: Readonly<Record<string, string>> = {
  cart_sold: "Giỏ đã bán thành công, không sửa được.",
  color_required: "Còn dòng hàng có màu chưa chọn màu khách lấy.",
  combo_not_sellable: "Combo chưa bán được vì thiếu hàng thành phần.",
  delivery_exceeds_reserved: "Số xuất lần này vượt lượng đang giữ cho hóa đơn.",
  document_has_returns: "Chứng từ đã có phiếu trả hàng.",
  document_reversed: "Chứng từ đã bị đảo.",
  exchange_changed: "Chênh lệch đổi hàng vừa thay đổi. Kiểm tra lại rồi ghi sổ.",
  expired: "Đã hết hạn.",
  insufficient_stock: "Không đủ tồn có thể bán.",
  invalid_status_transition: "Trạng thái chứng từ đã thay đổi nên không làm được thao tác này. Tải lại để xem trạng thái mới.",
  invoice_has_payments: "Hóa đơn đã có tiền khách trả nên không xóa được.",
  invoice_not_editable: "Hóa đơn đã xác nhận bán nên không sửa được.",
  mix_pack_partial: "Thùng mix chỉ bán theo nguyên thùng.",
  mix_parts_exceed_pack: "Số vị đã chọn vượt sức chứa của thùng mix.",
  not_found: "Không tìm thấy chứng từ.",
  nothing_changed: "Không có thay đổi nào để lưu.",
  nothing_to_cancel: "Không còn phần chưa giao để hủy.",
  nothing_to_deliver: "Nhập số lượng xuất lần này.",
  nothing_to_reserve: "Không có thêm tồn có thể bán để giữ.",
  paid_amount_changed: "Số tiền khách đã trả vừa thay đổi. Tải lại rồi thử lại.",
  paid_amount_not_refundable: "Không giảm được số tiền khách đã trả.",
  payment_method_inactive: "Hình thức thanh toán đang ngừng dùng.",
  payment_mismatch: "Tiền thanh toán vượt số khách cần trả.",
  quote_expired: "Báo giá đã hết hạn. Tạo phiên bản mới để gia hạn.",
  refund_required: "Chọn quỹ chi để hoàn tiền cho khách.",
  return_changed: "Số hàng còn được trả vừa thay đổi. Tải lại rồi thử lại.",
  return_exceeds_sold: "Số lượng trả vượt số còn được trả.",
  sale_changed: "Giá hoặc tồn vừa thay đổi. Thử lại.",
  shift_changed: "Ca bán hàng vừa thay đổi. Thử lại.",
  shift_unavailable: "Chưa có ca bán hàng đang mở.",
  sku_inactive: "Có hàng đang ngừng kinh doanh.",
  sku_not_sellable: "Có hàng không bán được.",
  stock_out_reversed: "Phiếu xuất kho đã bị đảo.",
  walk_in_must_pay_in_full: "Khách lẻ phải trả đủ. Chọn khách hàng để bán chịu phần còn lại.",
  walk_in_reverse_needs_refund: "Đảo chứng từ khách lẻ cần hoàn tiền cho khách.",
  no_cash_payment: "Không có phần trả tiền mặt nên không cần nhập tiền khách đưa.",
  below_cash_payment: "Tiền khách đưa ít hơn phần trả tiền mặt.",
  priced_by_flavor: "Thùng mix tính giá theo từng vị, không nhập đơn giá.",
  set_by_mix_group: "Đơn vị của thùng mix do nhóm mix quy định.",
  sku_or_mix_group_required: "Chọn sản phẩm cho mọi dòng.",
  must_be_positive: "Số lượng phải lớn hơn 0.",
};

/** "lines.2.qty" → "Dòng 3"; other paths unchanged. */
function fieldLabel(path: string): string {
  const m = /^(?:lines|return_lines)\.(\d+)\./.exec(path);
  return m ? `Dòng ${Number(m[1]) + 1}` : path;
}

/** Message for one field reason: the sales text when known, the shared text otherwise. */
export function salesReasonMessage(reason: string): string {
  return SALES_MESSAGES[reason] ?? fieldReasonMessage(reason);
}

/**
 * Rewrites a refused sales command into Vietnamese: known codes get their sentence; a validation
 * error names the first offending line ("Dòng 2: Chọn sản phẩm cho mọi dòng.").
 */
export function translateSalesError(e: unknown): unknown {
  if (!isApiError(e)) return e;
  const known = SALES_MESSAGES[e.code];
  let message: string | undefined = known;
  let code = e.code;
  if (!known && (e.code === ERROR_CODES.validationFailed || e.status === 422)) {
    const first = Object.entries(e.fields)[0];
    if (first) {
      message = `${fieldLabel(first[0])}: ${salesReasonMessage(first[1])}`;
      // The shared handler would replace the message by its generic text for this code.
      if (code === ERROR_CODES.validationFailed) code = "validation_failed_detail";
    }
  }
  if (message === undefined) return e;
  return new ApiError({ status: e.status, code, message, fields: { ...e.fields }, exceptionKind: e.exceptionKind, retryAfterSeconds: e.retryAfterSeconds });
}

/** Sends a command whose refusal is shown in Vietnamese (no retry, no exception round). */
export async function translated<T>(send: () => Promise<T>): Promise<T> {
  try {
    return await send();
  } catch (e) {
    throw translateSalesError(e);
  }
}

/** Toast text with the document number in bold ("Đã lưu **BG-0042**."). */
export function withDoc(before: string, docNo: string, afterText: string): ReactNode {
  return createElement(Fragment, null, before, createElement("b", null, docNo), afterText);
}

/**
 * The error of a draft this intent created whose later edits were not stored: it names the draft,
 * so the owner retries rather than creating another. A password prompt or an expired session stays as is.
 */
export function editsNotSaved(docNo: string, e: unknown): unknown {
  if (isReauthRequired(e) || isSessionExpired(e)) return e;
  // A newer version has the app-wide toast already; the next attempt reads the draft again.
  if (isApiError(e) && e.status === 412) return e;
  if (outcomeUnknown(e)) return new PartialCommitError(withDoc("Đã tạo ", docNo, " nhưng chưa rõ đã lưu phần sửa sau hay chưa. Bấm lại để kiểm tra và lưu tiếp."));
  return new PartialCommitError(withDoc("Đã tạo ", docNo, ` nhưng chưa lưu phần sửa sau: ${errorMessage(translateSalesError(e))} Bấm lại để lưu tiếp.`));
}

const RETRYABLE = new Set(["shift_changed", "sale_changed"]);
const MAX_RETRIES = 2;

/** Sends a command, resending it with the same key when the server reports a retryable conflict. */
export async function withRetry<T>(send: () => Promise<T>): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    try {
      return await send();
    } catch (e) {
      if (isApiError(e) && e.status === 409 && RETRYABLE.has(e.code) && attempt < MAX_RETRIES) continue;
      throw translateSalesError(e);
    }
  }
}

const MAX_EXCEPTION_ROUNDS = 3;

/**
 * Sends a command that may need several owner exceptions (credit limit and minimum price): each
 * 409 + exception_kind opens the confirmation dialog, then the command is resent with every
 * confirmation id collected so far. Cancelling the dialog cancels the command. `initialIds` are
 * confirmations the owner already gave on the form (the "Tôi xác nhận vẫn bán" box).
 */
export async function withExceptions<T>(
  send: (exceptionIds: readonly string[]) => Promise<T>,
  confirm: ConfirmException,
  prompt: (kind: string) => ExceptionPrompt,
  initialIds: readonly string[] = [],
): Promise<T> {
  const ids: string[] = [...initialIds];
  for (let round = 0; ; round++) {
    try {
      return await withRetry(() => send(ids));
    } catch (e) {
      if (isExceptionRequired(e) && round < MAX_EXCEPTION_ROUNDS) {
        const id = await confirm(prompt(e.exceptionKind));
        if (id === null) throw new CommitCancelledError();
        ids.push(id);
        continue;
      }
      throw e;
    }
  }
}

/** Adds `exception_confirmation_ids` to a body only when there are some. */
export function withExceptionIds<B extends object>(body: B, ids: readonly string[]): B & { exception_confirmation_ids?: string[] } {
  return ids.length > 0 ? { ...body, exception_confirmation_ids: [...ids] } : body;
}
