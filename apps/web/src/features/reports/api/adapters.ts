import { errorMessage, isApiError } from "../../../shared/api/api-error";
import { isDecimalString } from "../../../shared/lib/decimal";
import { fd, money, num } from "../../../shared/lib/format";
import type { ColumnKind, ExportStatus } from "../schemas";

// The Báo cáo screens in the prototype's words (`repFilter`): the filter labels, the export states
// and the server refusals of the report, saved-view and export endpoints.

export const REPORT_TEXT = {
  from: "Từ",
  to: "Đến",
  asOf: "Tại ngày",
  hint: "Số liệu tạm tính, không phải báo cáo tài chính đã kiểm toán",
  export: "Xuất CSV",
  save: "Lưu bộ lọc",
  saved: "Bộ lọc đã lưu",
  total: "Tổng",
} as const;

export const REPORT_MESSAGES = {
  periodInvalid: "Đến ngày phải sau hoặc bằng từ ngày.",
  rangeTooLong: "Khoảng ngày quá dài cho báo cáo này. Chọn khoảng ngắn hơn rồi xem lại.",
  nameRequired: "Nhập tên bộ lọc.",
  nameTaken: "Tên bộ lọc đã có. Đặt tên khác.",
  queueFull: "Đang có nhiều tệp xuất chưa xong. Chờ các tệp trước xong rồi xuất lại.",
  exportExpired: "Tệp xuất đã hết hạn. Bấm “Xuất CSV” để xuất lại.",
  exportNotReady: "Tệp chưa xuất xong. Chờ thêm rồi tải lại.",
  exportUnavailable: "Tệp này không xuất được hoặc đã bị hủy. Bấm “Xuất CSV” để xuất lại.",
  exportGone: "Không tìm thấy tệp xuất, có thể đã bị xóa. Bấm “Xuất CSV” ở màn hình báo cáo để xuất lại.",
  exportStalled: "Tệp vẫn đang xuất. Bấm “Kiểm tra lại” để xem tiếp.",
  cancelTooLate: "Tệp đã xuất xong hoặc đã dừng nên không hủy được nữa.",
  reportUnknown: "Báo cáo này không còn trên máy chủ. Tải lại trang để xem danh sách báo cáo mới nhất.",
} as const;

/** Server refusals of the reporting endpoints, in the screens' words. */
const REPORT_ERROR_MESSAGES: Readonly<Record<string, string>> = {
  range_too_long: REPORT_MESSAGES.rangeTooLong,
  saved_report_name_taken: REPORT_MESSAGES.nameTaken,
  export_queue_full: REPORT_MESSAGES.queueFull,
  export_expired: REPORT_MESSAGES.exportExpired,
  export_not_ready: REPORT_MESSAGES.exportNotReady,
  export_status_invalid: REPORT_MESSAGES.cancelTooLate,
  report_unknown: REPORT_MESSAGES.reportUnknown,
};

/**
 * A refusal of a report request as one sentence; an inverted period reads as such. Codes no screen
 * translates fall back to the shared wording, never to the server's English.
 */
export function reportErrorMessage(e: unknown): string {
  if (isApiError(e)) {
    const known = REPORT_ERROR_MESSAGES[e.code];
    if (known !== undefined) return known;
    if (e.fields.to === "before_from") return REPORT_MESSAGES.periodInvalid;
  }
  return errorMessage(e);
}

export const EXPORT_STATUS_LABELS: Readonly<Record<ExportStatus, string>> = {
  queued: "Đang chờ xuất",
  running: "Đang xuất",
  succeeded: "Đã xuất xong",
  failed: "Xuất không thành công",
  expired: "Tệp đã hết hạn",
  cancelled: "Đã hủy xuất",
};

/** Money, quantities, counts and percentages sit in right-aligned columns. */
export const isNumericKind = (kind: ColumnKind) => kind !== "text" && kind !== "date";

/** One report cell as text: the server computes every figure, the screen only formats it. */
export function formatCell(kind: ColumnKind, value: string | null | undefined): string {
  if (value === null || value === undefined || value === "") return "—";
  if (kind === "date") return fd(value);
  if (kind === "text" || !isDecimalString(value)) return value;
  if (kind === "money") return money(value);
  if (kind === "percent") return `${num(value)}%`;
  return num(value);
}

/** First day of the Tết season holding `today`: 01/10 of this year, or of last year before October. */
export function seasonStart(today: string): string {
  const year = Number(today.slice(0, 4));
  const month = Number(today.slice(5, 7));
  return `${month >= 10 ? year : year - 1}-10-01`;
}
