// One typed map from a status label to its pill tone, ported from the prototype ST_OK / ST_WARN /
// ST_BAD prefix lists plus its mute list. A label takes the tone of the first list containing a
// prefix of it, checked in the order ok → warn → bad → mute. "Chưa thanh toán" appears once.

export type StatusTone = "ok" | "warn" | "bad" | "mute" | "info";

const OK_PREFIXES = [
  "Hoàn tất", "Đã giao đủ", "Giao thành công", "Đã thanh toán đủ", "Đã ghi sổ", "Đã nhận", "Đã duyệt",
  "Hoạt động", "Khách chấp nhận", "Đã xác nhận", "Đủ · còn cọc dư", "Đã khớp", "Hoàn thành", "Đã kết nối",
  "Đang chạy", "Đã ghi", "Còn hạn", "Bán được", "Đã chuyển đơn", "Đã chuyển hóa đơn", "Đã chốt", "Đang bán",
  "Đã bán", "Đã thanh toán", "Đúng hạn", "Đạt", "Đang giao dịch", "Đang dùng", "Đã xuất kho",
] as const;

const WARN_PREFIXES = [
  "Chờ duyệt", "Giao một phần", "Nhận một phần", "Thanh toán một phần", "Đang chuyển", "Đang đếm", "Chờ soạn",
  "Đã soạn", "Đang giao", "Đang đóng", "Sắp hết hạn", "Đã gửi", "Có tiền cọc", "Chờ ghép", "Đang mở",
  "Chưa thanh toán", "Chưa trả", "Đến hạn ≤ 7 ngày", "Thời vụ", "Dưới mức tối thiểu", "Chờ nhận tiền",
  "Sandbox", "Thừa tiền", "Chờ ghi sổ", "Đang soạn",
] as const;

const BAD_PREFIXES = [
  "Khách từ chối", "Quá hạn", "Giao thất bại", "Hết hạn", "Từ chối", "Tranh chấp", "Thiếu hàng", "Cách ly",
  "Nhận thiếu – có biên bản", "Lệch", "Hết hàng", "Chặn bán", "Ngừng giao dịch",
] as const;

const MUTE_PREFIXES = [
  "Nháp", "Đã hủy", "Đã đảo", "Đóng – thiếu", "Chưa phát sinh", "Đã khóa", "Đã kết thúc", "Chưa giao",
  "Chưa kết nối", "Ngừng",
] as const;

const TONE_LISTS: ReadonlyArray<readonly [Exclude<StatusTone, "info">, readonly string[]]> = [
  ["ok", OK_PREFIXES],
  ["warn", WARN_PREFIXES],
  ["bad", BAD_PREFIXES],
  ["mute", MUTE_PREFIXES],
];

/** Tone of a status label; "info" (the neutral blue pill) when no list matches. */
export function statusTone(label: string): StatusTone {
  for (const [tone, prefixes] of TONE_LISTS) {
    if (prefixes.some((p) => label.startsWith(p))) return tone;
  }
  return "info";
}

/** Decorative glyph per tone; rendered aria-hidden because the label already carries the meaning. */
export const STATUS_GLYPH: Readonly<Record<StatusTone, string>> = {
  ok: "✓",
  warn: "◷",
  bad: "!",
  mute: "–",
  info: "•",
};

/** Exposed for tests: every prefix appears in exactly one list. */
export const STATUS_PREFIXES = { ok: OK_PREFIXES, warn: WARN_PREFIXES, bad: BAD_PREFIXES, mute: MUTE_PREFIXES } as const;
