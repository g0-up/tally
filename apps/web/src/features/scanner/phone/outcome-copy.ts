import { z } from "zod";
import { ERROR_CODES, errorMessage, isApiError, rateLimitMessage } from "../../../shared/api/api-error";

// What the phone shows (and how it signals) for each answer to a scanned code. The line is always
// written out: vibration, the beep and the flash only add to it.

export const phoneOutcomeSchema = z.enum(["added", "not_found", "not_accepting", "needs_desktop_edit", "new_product", "not_in_order", "desktop_unavailable", "timeout"]);
export type PhoneOutcome = z.infer<typeof phoneOutcomeSchema>;

/** The answer of POST /scan/scans: the outcome, with the product and unit names when added. */
export const phoneScanResultSchema = z.object({
  outcome: phoneOutcomeSchema,
  product_name: z.string().optional(),
  unit_name: z.string().optional(),
});
export type PhoneScanResult = z.infer<typeof phoneScanResultSchema>;

/** `ok` signals success (short buzz, high beep, green flash), `error` anything to look at (three buzzes, low beep, red flash). */
export type ScanTone = "ok" | "error";

export interface ScanLine {
  text: string;
  tone: ScanTone;
}

export const ADDED_FALLBACK_TEXT = "Đã thêm trên máy tính";

export const OUTCOME_COPY: Readonly<Record<Exclude<PhoneOutcome, "added">, ScanLine>> = {
  not_found: { text: "Không có trong danh mục", tone: "error" },
  not_accepting: { text: "Màn hình trên máy tính không nhận mã quét", tone: "error" },
  needs_desktop_edit: { text: "Dòng này nhập theo thùng, sửa số lượng trên máy tính", tone: "error" },
  new_product: { text: "Hàng mới, chưa có trong danh mục. Nhập thông tin trên máy tính.", tone: "ok" },
  not_in_order: { text: "Hàng này không có trong đơn nhập.", tone: "error" },
  desktop_unavailable: { text: "Máy tính chưa sẵn sàng. Kiểm tra màn hình trên máy tính.", tone: "error" },
  timeout: { text: "Máy tính chưa phản hồi. Kiểm tra màn hình máy tính trước khi quét lại.", tone: "error" },
};

/** The line for an answer: "{tên hàng} · {đơn vị}" when added, a fixed sentence otherwise. */
export function outcomeCopy(result: PhoneScanResult): ScanLine {
  if (result.outcome !== "added") return OUTCOME_COPY[result.outcome];
  const parts = [result.product_name, result.unit_name].map((s) => s?.trim()).filter((s): s is string => Boolean(s));
  return { text: parts.length > 0 ? parts.join(" · ") : ADDED_FALLBACK_TEXT, tone: "ok" };
}

export const SEND_NETWORK_TEXT = "Không gửi được mã. Kiểm tra mạng rồi quét lại.";
export const INVALID_CODE_TEXT = "Mã này không đọc đúng. Quét lại.";

/** The line for a scan the server never answered with an outcome (after the retries ran out). */
export function scanFailureCopy(e: unknown): ScanLine {
  if (isApiError(e)) {
    if (e.isNetwork) return { text: SEND_NETWORK_TEXT, tone: "error" };
    if (e.status === 429) return { text: rateLimitMessage(e, "Quét quá nhanh."), tone: "error" };
    if (e.code === ERROR_CODES.validationFailed) return { text: INVALID_CODE_TEXT, tone: "error" };
  }
  return { text: `Không gửi được mã: ${errorMessage(e)}`, tone: "error" };
}
