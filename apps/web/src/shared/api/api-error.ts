import type { components } from "./schema";

// Every non-2xx answer from the API is the envelope {"error":{code,message,fields?,exception_kind?}}.
// ApiError carries it into the UI together with the HTTP status.

type ErrorBody = components["schemas"]["Error"];

/** Error codes the client branches on. Codes outside this list get the Vietnamese text of CODE_MESSAGES or a generic one. */
export const ERROR_CODES = {
  unauthenticated: "unauthenticated",
  reauthRequired: "reauth_required",
  csrfFailed: "csrf_failed",
  subscriptionReadOnly: "subscription_read_only",
  moduleNotInPlan: "module_not_in_plan",
  planLimitReached: "plan_limit_reached",
  supportReadOnly: "support_read_only",
  forbidden: "forbidden",
  preconditionFailed: "precondition_failed",
  preconditionRequired: "precondition_required",
  periodLocked: "period_locked",
  validationFailed: "validation_failed",
  rateLimited: "rate_limited",
  network: "network_error",
  badResponse: "bad_response",
} as const;

export const NETWORK_ERROR_MESSAGE = "Không kết nối được máy chủ. Kiểm tra mạng rồi thử lại.";
export const BAD_RESPONSE_MESSAGE = "Máy chủ trả về dữ liệu không đọc được. Tải lại trang rồi thử lại.";
export const STALE_DATA_MESSAGE = "Dữ liệu đã thay đổi ở nơi khác. Tải lại để tiếp tục.";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** Field path → reason code, for validation errors. */
  readonly fields: Readonly<Record<string, string>>;
  /** Set when the owner may confirm an exception and retry (409). */
  readonly exceptionKind: string | null;
  /** Seconds the server asks the client to wait (Retry-After on 429), when it said so. */
  readonly retryAfterSeconds: number | null;
  /**
   * The message is the server's own (English, for developers), not a sentence for the owner. Errors
   * the client builds or translates carry a Vietnamese message and leave this false.
   */
  readonly fromServer: boolean;

  constructor(init: {
    status: number;
    code: string;
    message: string;
    fields?: Record<string, string>;
    exceptionKind?: string | null;
    retryAfterSeconds?: number | null;
    fromServer?: boolean;
  }) {
    super(init.message);
    this.name = "ApiError";
    this.status = init.status;
    this.code = init.code;
    this.fields = Object.freeze({ ...(init.fields ?? {}) });
    this.exceptionKind = init.exceptionKind ?? null;
    this.retryAfterSeconds = init.retryAfterSeconds ?? null;
    this.fromServer = init.fromServer ?? false;
  }

  /** The same refusal (status, code, fields, exception, wait) with a Vietnamese message for the owner. */
  withMessage(message: string): ApiError {
    return new ApiError({ status: this.status, code: this.code, message, fields: { ...this.fields }, exceptionKind: this.exceptionKind, retryAfterSeconds: this.retryAfterSeconds });
  }

  get isNetwork(): boolean {
    return this.status === 0;
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

function isStringRecord(v: unknown): v is Record<string, string> {
  return typeof v === "object" && v !== null && !Array.isArray(v) && Object.values(v).every((x) => typeof x === "string");
}

/**
 * Reads a Retry-After header given in seconds. The HTTP-date form is accepted too and turned into
 * seconds from `now`; anything unreadable yields null.
 */
export function parseRetryAfter(value: string | null, now: number = Date.now()): number | null {
  if (value === null) return null;
  const v = value.trim();
  if (/^\d+$/.test(v)) return Number(v);
  const at = Date.parse(v);
  if (Number.isNaN(at)) return null;
  return Math.max(0, Math.ceil((at - now) / 1000));
}

/** Builds an ApiError from a parsed response body; a body that is not the envelope still yields an error. */
export function apiErrorFromBody(status: number, body: unknown, retryAfterSeconds: number | null = null): ApiError {
  const err = (typeof body === "object" && body !== null ? (body as { error?: unknown }).error : undefined) as Partial<ErrorBody> | undefined;
  if (err && typeof err === "object" && typeof err.code === "string" && typeof err.message === "string") {
    return new ApiError({
      status,
      code: err.code,
      message: err.message,
      ...(isStringRecord(err.fields) ? { fields: err.fields } : {}),
      exceptionKind: typeof err.exception_kind === "string" && err.exception_kind !== "" ? err.exception_kind : null,
      retryAfterSeconds,
      fromServer: true,
    });
  }
  return new ApiError({ status, code: ERROR_CODES.badResponse, message: BAD_RESPONSE_MESSAGE, retryAfterSeconds });
}

export function networkError(): ApiError {
  return new ApiError({ status: 0, code: ERROR_CODES.network, message: NETWORK_ERROR_MESSAGE });
}

/** The subscription blocks writes: read-only or grace plan, module outside the plan, or a plan limit. */
export function isEntitlementError(e: unknown): boolean {
  return (
    isApiError(e) &&
    (e.status === 402 ||
      e.code === ERROR_CODES.subscriptionReadOnly ||
      e.code === ERROR_CODES.moduleNotInPlan ||
      e.code === ERROR_CODES.planLimitReached)
  );
}

// Plain booleans, not type guards: a false answer says nothing about whether `e` is an ApiError.

/** The action needs a fresh password entry (403 reauth_required). */
export function isReauthRequired(e: unknown): boolean {
  return isApiError(e) && e.status === 403 && e.code === ERROR_CODES.reauthRequired;
}

/**
 * The request carried no valid session (401 unauthenticated). Other 401 codes come from the
 * sign-in endpoints themselves (wrong password, wrong MFA code) and are form errors, not expiry.
 */
export function isSessionExpired(e: unknown): boolean {
  return isApiError(e) && e.status === 401 && e.code === ERROR_CODES.unauthenticated;
}

/** The record changed since it was read (412): read it again before writing. */
export function isStale(e: unknown): boolean {
  return isApiError(e) && e.status === 412;
}

/** The server is throttling this client: the IP rate limit or the sign-in attempt lock. */
export function isRateLimited(e: unknown): e is ApiError {
  return isApiError(e) && e.status === 429;
}

/** "30 giây" / "15 phút": how long to wait, rounded up to whole minutes past one minute. */
export function waitText(seconds: number): string {
  if (seconds < 60) return `${Math.max(1, seconds)} giây`;
  return `${Math.ceil(seconds / 60)} phút`;
}

export function isExceptionRequired(e: unknown): e is ApiError & { exceptionKind: string } {
  return isApiError(e) && e.status === 409 && e.exceptionKind !== null;
}

/** Vietnamese text for field reason codes; the API sends codes, not sentences. */
const FIELD_REASONS: Readonly<Record<string, string>> = {
  required: "Bắt buộc nhập.",
  invalid: "Giá trị không hợp lệ.",
  invalid_format: "Sai định dạng.",
  too_long: "Quá dài.",
  too_short: "Quá ngắn.",
  too_small: "Giá trị quá nhỏ.",
  too_large: "Giá trị quá lớn.",
  not_positive: "Phải lớn hơn 0.",
  negative: "Không được âm.",
  too_many_decimals: "Quá nhiều chữ số thập phân.",
  not_found: "Không tìm thấy.",
  duplicate: "Đã tồn tại.",
  taken: "Đã có người dùng.",
  in_use: "Đã được dùng cho tài khoản khác.",
  code_or_recovery_code: "Nhập mã xác thực hoặc mã khôi phục.",
  reserved: "Tên này không dùng được.",
  inactive: "Đang ngừng dùng.",
  mismatch: "Không khớp.",
  expired: "Đã hết hạn.",
  // Validator tags the API binder reports as reason codes.
  min: "Giá trị quá nhỏ hoặc quá ngắn.",
  max: "Giá trị quá lớn hoặc quá dài.",
  gt: "Giá trị quá nhỏ.",
  gte: "Giá trị quá nhỏ.",
  lt: "Giá trị quá lớn.",
  lte: "Giá trị quá lớn.",
  len: "Sai độ dài.",
  oneof: "Giá trị không nằm trong danh sách cho phép.",
  email: "Email không hợp lệ.",
  invalid_decimal: "Phải là số, tối đa 4 chữ số thập phân.",
  invalid_type: "Sai kiểu dữ liệu.",
  unknown_field: "Trường không được hỗ trợ.",
  one_of: "Giá trị không nằm trong danh sách cho phép.",
  must_be_positive: "Phải lớn hơn 0.",
  must_not_be_negative: "Không được âm.",
  before_from: "Đến ngày phải sau hoặc bằng từ ngày.",
  before_period_from: "Đến ngày phải sau hoặc bằng từ ngày.",
  in_future: "Không được sau ngày hôm nay.",
  out_of_range: "Ngoài khoảng cho phép.",
  promise_only: "Chỉ nhập ngày hẹn cho lời hứa trả.",
  exceeds_open: "Vượt số còn nợ.",
  exceeds_payment: "Vượt số tiền của phiếu.",
  exceeds_advance: "Vượt số tiền ứng trước còn lại.",
  pending_verification: "Phiếu đang chờ xác minh.",
  other_party: "Không thuộc đối tác này.",
  other_direction: "Không cùng chiều công nợ.",
  settled: "Chứng từ đã thanh toán hết.",
  not_after_current: "Phải sau ngày đang khóa sổ.",
  not_before_current: "Phải trước ngày đang khóa sổ.",
  immutable: "Không đổi được sau khi đã tạo.",
  range: "Ngoài khoảng cho phép.",
  unique: "Đã tồn tại.",
  addon: "Gói bổ sung không dùng làm gói chính.",
};

export const FALLBACK_FIELD_MESSAGE = "Giá trị không hợp lệ.";

export function fieldReasonMessage(reason: string): string {
  return FIELD_REASONS[reason] ?? FALLBACK_FIELD_MESSAGE;
}

/** Vietnamese text for the refusals every screen can meet; feature adapters translate their own codes. */
const CODE_MESSAGES: Readonly<Record<string, string>> = {
  [ERROR_CODES.unauthenticated]: "Phiên đăng nhập đã hết hạn. Đăng nhập lại để tiếp tục.",
  [ERROR_CODES.reauthRequired]: "Nhập lại mật khẩu để tiếp tục.",
  [ERROR_CODES.csrfFailed]: "Phiên làm việc đã thay đổi. Tải lại trang rồi thử lại.",
  [ERROR_CODES.subscriptionReadOnly]: "Thuê bao đang ở chế độ chỉ xem. Gia hạn để tiếp tục ghi sổ.",
  [ERROR_CODES.moduleNotInPlan]: "Gói thuê bao hiện tại chưa có tính năng này.",
  [ERROR_CODES.planLimitReached]: "Đã đạt giới hạn của gói thuê bao hiện tại.",
  [ERROR_CODES.supportReadOnly]: "Phiên hỗ trợ chỉ được xem, không được ghi sổ.",
  [ERROR_CODES.forbidden]: "Phiên này không được thực hiện thao tác này.",
  [ERROR_CODES.preconditionFailed]: STALE_DATA_MESSAGE,
  [ERROR_CODES.preconditionRequired]: STALE_DATA_MESSAGE,
  [ERROR_CODES.periodLocked]: "Kỳ kế toán của ngày này đã khóa.",
  [ERROR_CODES.validationFailed]: "Kiểm tra lại các ô được đánh dấu.",
  [ERROR_CODES.rateLimited]: "Thao tác quá nhanh. Chờ một chút rồi thử lại.",
  sandbox_replaced: "Cửa hàng mẫu vừa được tạo lại. Đăng nhập lại để vào cửa hàng mẫu mới.",
  sandbox_busy: "Cửa hàng mẫu đang bận. Thử tạo lại sau ít giây.",
  sandbox_not_active: "Cửa hàng mẫu này đã được đặt lại. Tải lại trang để vào cửa hàng mẫu mới.",
  internal_error: "Máy chủ gặp lỗi. Thử lại sau ít phút.",
  not_found: "Không tìm thấy dữ liệu, có thể đã bị xóa. Tải lại để xem bản mới nhất.",
  invalid_cursor: "Danh sách đã thay đổi. Tải lại để xem từ đầu.",
  idempotency_key_in_progress: "Thao tác này đang được xử lý. Chờ một chút rồi tải lại để xem kết quả.",
  idempotency_key_reused: "Thao tác này đã được ghi với nội dung khác. Tải lại để kiểm tra chứng từ đã ghi trước khi làm lại.",
  idempotency_key_invalid: "Yêu cầu không hợp lệ, hãy tải lại trang rồi thử lại.",
  exception_confirmation_used: "Xác nhận này đã được dùng. Xác nhận lại rồi thử lại.",
  // Store lifecycle, subscription and support access.
  tenant_closed: "Cửa hàng đã đóng. Chỉ còn tải được bản xuất dữ liệu cuối cùng.",
  tenant_maintenance: "Cửa hàng đang bảo trì. Thử lại sau ít phút.",
  closure_state_invalid: "Yêu cầu đóng tài khoản đã chuyển sang bước khác. Tải lại để xem trạng thái mới.",
  export_not_ready: "Bản xuất dữ liệu không còn tải được.",
  closure_link_not_found: "Liên kết không đúng, đã được thay bằng liên kết mới hoặc đã hết hạn.",
  support_session_inactive: "Phiên hỗ trợ đã hết hạn hoặc đã bị thu hồi.",
  subscription_missing: "Cửa hàng chưa có thuê bao.",
  subscription_state_invalid: "Trạng thái thuê bao hiện tại không cho phép thao tác này.",
  sandbox_not_billable: "Cửa hàng mẫu không có thuê bao, hóa đơn hay yêu cầu đóng tài khoản.",
  invoice_not_open: "Hóa đơn không còn ở trạng thái chờ thanh toán.",
  terms_not_accepted: "Cần đồng ý điều khoản gói trước khi đổi gói.",
  plan_code_taken: "Mã gói đã được dùng.",
  referral_code_taken: "Mã giới thiệu đã được dùng.",
  ticket_not_assigned: "Yêu cầu hỗ trợ chưa có nhân viên phụ trách.",
  ticket_closed: "Yêu cầu hỗ trợ đã đóng.",
  flag_override_not_found: "Cửa hàng không còn giá trị riêng của cờ này, có thể đã được gỡ trước đó.",
  possible_password: "Nội dung có thể chứa mật khẩu. Không gửi mật khẩu cho nhân viên hỗ trợ.",
  // Sign-in, second factor and account.
  invalid_credentials: "Email hoặc mật khẩu không đúng.",
  invalid_password: "Mật khẩu không đúng.",
  invalid_code: "Mã xác thực không đúng hoặc đã hết hạn.",
  challenge_invalid: "Phiên đăng nhập đã hết hạn. Đăng nhập lại từ đầu.",
  token_invalid: "Liên kết không hợp lệ hoặc đã hết hạn.",
  mfa_already_enabled: "Xác thực 2 lớp đã bật.",
  mfa_not_enrolled: "Chưa tạo khóa xác thực 2 lớp. Bắt đầu lại từ bước tạo mã.",
  mfa_not_enabled: "Xác thực 2 lớp chưa bật.",
  email_in_use: "Email này đã dùng cho tài khoản khác.",
  // Sandbox.
  not_sandbox: "Chỉ đặt lại được cửa hàng mẫu.",
  sandbox_reset_too_soon: "Cửa hàng mẫu vừa được đặt lại. Chờ một lúc rồi thử lại.",
  // Period lock.
  lock_date_in_future: "Không khóa sổ được ngày sau hôm nay.",
  lock_not_forward: "Ngày khóa sổ mới phải sau ngày đang khóa.",
  reopen_not_backward: "Ngày mở lại phải trước ngày đang khóa.",
  period_not_locked: "Chưa có kỳ nào bị khóa sổ.",
  exception_required: "Thao tác này cần chủ cửa hàng xác nhận ngoại lệ.",
  // Data export.
  export_not_allowed: "Cửa hàng đã đóng nên không xuất dữ liệu mới được.",
  export_queue_full: "Đang có bản xuất chưa xong. Chờ bản đó hoàn tất rồi thử lại.",
  // Onboarding and opening balances.
  step_not_skippable: "Bước này bắt buộc, không bỏ qua được.",
  step_not_manual: "Bước này tự hoàn tất khi có hóa đơn bán đầu tiên.",
  onboarding_steps_pending: "Còn bước bắt buộc chưa xong. Hoàn tất các bước trước rồi xác nhận số dư.",
  confirmation_required: "Cần xác nhận số dư đầu kỳ.",
  opening_balances_confirmed: "Số dư đầu kỳ đã xác nhận, không sửa được nữa.",
  opening_cutoff_required: "Chọn ngày chốt số dư đầu kỳ trong Thông tin cửa hàng trước.",
  opening_cutoff_in_future: "Ngày chốt số dư đầu kỳ không được sau hôm nay.",
  fund_has_entries: "Quỹ đã có phiếu thu chi nên không sửa số dư đầu kỳ được.",
};

/** What the owner reads for a refusal no screen translates: never the server's English text. */
export function unknownErrorMessage(code: string): string {
  return `Máy chủ từ chối thao tác (mã ${code}).`;
}

/** Throttling text; names the wait when the server sent Retry-After. */
export function rateLimitMessage(e: ApiError, base = "Thao tác quá nhanh."): string {
  if (e.retryAfterSeconds !== null) return `${base} Thử lại sau ${waitText(e.retryAfterSeconds)}.`;
  return `${base} Chờ một chút rồi thử lại.`;
}

export function errorMessage(e: unknown): string {
  if (isApiError(e)) {
    if (e.status === 412) return STALE_DATA_MESSAGE;
    if (e.status === 429) return rateLimitMessage(e);
    // A sentence the client wrote (a screen's translation, a network failure) is already the owner's.
    if (!e.fromServer) return e.message;
    return CODE_MESSAGES[e.code] ?? unknownErrorMessage(e.code);
  }
  if (e instanceof Error && e.message) return e.message;
  return "Không thực hiện được.";
}
