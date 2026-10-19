import { errorMessage, fieldReasonMessage, isApiError, isRateLimited, rateLimitMessage } from "../../../shared/api/api-error";
import type { FieldValues, Path, UseFormSetError } from "react-hook-form";
import { MESSAGES } from "../schemas";

// Turns API refusals of the auth endpoints into Vietnamese text: one message for the form-level
// alert, and per-field messages for the 422 `fields` map.

export const AUTH_ERROR_CODES = {
  invalidCredentials: "invalid_credentials",
  emailNotVerified: "email_not_verified",
  accountLocked: "account_locked",
  tokenInvalid: "token_invalid",
  invalidCode: "invalid_code",
  challengeInvalid: "challenge_invalid",
  validationFailed: "validation_failed",
  slugInvalid: "slug_invalid",
  slugReserved: "slug_reserved",
  slugTaken: "slug_taken",
} as const;

export const AUTH_MESSAGES = {
  invalidCredentials: "Tài khoản hoặc mật khẩu không đúng.",
  emailNotVerified: "Email chưa được xác minh. Mở thư xác minh đã gửi tới hộp thư của bạn rồi đăng nhập lại.",
  accountLocked: "Tài khoản đang bị khóa. Liên hệ bộ phận hỗ trợ để mở khóa.",
  tokenInvalid: "Liên kết không hợp lệ, đã được dùng hoặc đã hết hạn.",
  invalidCode: "Mã xác thực không đúng.",
  challengeExpired: "Phiên đăng nhập đã quá hạn. Đăng nhập lại.",
  tooManyAttempts: "Bạn đã thử quá nhiều lần.",
  checkFields: "Kiểm tra lại các ô được đánh dấu.",
} as const;

const CODE_TEXT: Readonly<Record<string, string>> = {
  [AUTH_ERROR_CODES.invalidCredentials]: AUTH_MESSAGES.invalidCredentials,
  [AUTH_ERROR_CODES.emailNotVerified]: AUTH_MESSAGES.emailNotVerified,
  [AUTH_ERROR_CODES.accountLocked]: AUTH_MESSAGES.accountLocked,
  [AUTH_ERROR_CODES.tokenInvalid]: AUTH_MESSAGES.tokenInvalid,
  [AUTH_ERROR_CODES.invalidCode]: AUTH_MESSAGES.invalidCode,
  [AUTH_ERROR_CODES.challengeInvalid]: AUTH_MESSAGES.challengeExpired,
  [AUTH_ERROR_CODES.validationFailed]: AUTH_MESSAGES.checkFields,
  [AUTH_ERROR_CODES.slugInvalid]: AUTH_MESSAGES.checkFields,
  [AUTH_ERROR_CODES.slugReserved]: AUTH_MESSAGES.checkFields,
  [AUTH_ERROR_CODES.slugTaken]: AUTH_MESSAGES.checkFields,
};

/**
 * Form-level text for a failed auth request (network, throttling, refusals). The API answers 429
 * rate_limited both for its request limits and for the failed sign-in lock, so the text does not
 * claim which one it was.
 */
export function authErrorMessage(e: unknown): string {
  if (isRateLimited(e)) return rateLimitMessage(e, AUTH_MESSAGES.tooManyAttempts);
  if (isApiError(e)) return CODE_TEXT[e.code] ?? errorMessage(e);
  return errorMessage(e);
}

/** Field-specific wording; anything else falls back to the shared reason texts. */
const FIELD_TEXT: Readonly<Record<string, Readonly<Record<string, string>>>> = {
  email: { invalid: MESSAGES.emailInvalid, required: MESSAGES.emailRequired },
  login: { invalid: MESSAGES.loginInvalid, required: MESSAGES.loginRequired },
  phone: { invalid: MESSAGES.phoneInvalid },
  password: { required: MESSAGES.passwordRequired, too_short: MESSAGES.passwordTooShort, too_long: MESSAGES.passwordTooLong },
  full_name: { required: MESSAGES.fullNameRequired, invalid: MESSAGES.nameTooLong },
  store_name: { required: MESSAGES.storeNameRequired, invalid: MESSAGES.nameTooLong },
  slug: { required: MESSAGES.slugRequired, invalid: MESSAGES.slugInvalid, reserved: MESSAGES.slugReserved, taken: MESSAGES.slugTaken },
};

export function authFieldMessage(field: string, reason: string): string {
  return FIELD_TEXT[field]?.[reason] ?? fieldReasonMessage(reason);
}

/**
 * Field errors of an API refusal restricted to the fields the form shows, in form order. Fields
 * the form does not have are left out so the caller can still show the form-level message.
 */
export function authFieldErrors<F extends string>(e: unknown, formFields: readonly F[]): Array<[F, string]> {
  if (!isApiError(e)) return [];
  const out: Array<[F, string]> = [];
  for (const field of formFields) {
    const reason = e.fields[field];
    if (reason !== undefined) out.push([field, authFieldMessage(field, reason)]);
  }
  return out;
}

/**
 * Puts the API's field errors on the form (focusing the first one) and returns the text for the
 * form-level alert.
 */
export function applyAuthError<T extends FieldValues>(e: unknown, formFields: readonly Path<T>[], setError: UseFormSetError<T>): string {
  const fieldErrors = authFieldErrors(e, formFields);
  fieldErrors.forEach(([field, message], i) => setError(field, { type: "server", message }, { shouldFocus: i === 0 }));
  return authErrorMessage(e);
}
