import { z } from "zod";
import { MAX_PHONE_LENGTH, normalizeVnPhone } from "./lib/phone";
import { isSlugFormatValid } from "./lib/slug";

// Form schemas of the auth screens. They mirror the API rules (identity validate.go,
// org.SlugPolicy) so most mistakes are caught before a request; the API stays the authority and
// its 422 `fields` are mapped back onto the same field names.

/** The API measures passwords in UTF-8 bytes: at least 8, at most 256. */
export const MIN_PASSWORD_BYTES = 8;
export const MAX_PASSWORD_BYTES = 256;
const MAX_EMAIL_LENGTH = 254;
const MAX_NAME_LENGTH = 200;
const MAX_RECOVERY_CODE_LENGTH = 32;

export const MESSAGES = {
  emailRequired: "Nhập email.",
  emailInvalid: "Email không hợp lệ.",
  loginRequired: "Nhập email hoặc số điện thoại.",
  loginInvalid: "Nhập email hoặc số điện thoại Việt Nam hợp lệ.",
  phoneInvalid: "Số điện thoại Việt Nam không hợp lệ.",
  passwordRequired: "Nhập mật khẩu.",
  passwordTooShort: "Mật khẩu cần ít nhất 8 ký tự.",
  passwordTooLong: "Mật khẩu quá dài.",
  passwordMismatch: "Mật khẩu nhập lại không khớp.",
  fullNameRequired: "Nhập họ tên.",
  storeNameRequired: "Nhập tên cửa hàng.",
  nameTooLong: "Tối đa 200 ký tự, trên một dòng.",
  slugRequired: "Nhập địa chỉ cửa hàng.",
  slugInvalid: "Từ 3 đến 30 ký tự: chữ thường không dấu, số và dấu gạch nối đơn; không bắt đầu, kết thúc bằng gạch nối hoặc kết thúc bằng “-demo”.",
  slugReserved: "Địa chỉ này được giữ lại cho hệ thống. Chọn địa chỉ khác.",
  slugTaken: "Địa chỉ này đã có cửa hàng dùng. Chọn địa chỉ khác.",
  codeInvalidFormat: "Mã xác thực gồm 6 chữ số.",
  recoveryCodeRequired: "Nhập mã khôi phục.",
} as const;

const utf8 = new TextEncoder();
const byteLength = (s: string) => utf8.encode(s).length;

/** Loose shape check like the API's: something@domain.tld, no spaces. */
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const email = z.string().trim().min(1, MESSAGES.emailRequired).max(MAX_EMAIL_LENGTH, MESSAGES.emailInvalid).regex(EMAIL_SHAPE, MESSAGES.emailInvalid);

/** The store sign-in names the owner by e-mail or by Vietnamese phone number. */
const loginIdentifier = z
  .string()
  .trim()
  .min(1, MESSAGES.loginRequired)
  .max(MAX_EMAIL_LENGTH, MESSAGES.loginInvalid)
  .refine((v) => (v.includes("@") ? EMAIL_SHAPE.test(v) : normalizeVnPhone(v) !== null), MESSAGES.loginInvalid);

/** An optional phone number: empty, or one the API accepts. */
const optionalPhone = z
  .string()
  .trim()
  .max(MAX_PHONE_LENGTH, MESSAGES.phoneInvalid)
  .refine((v) => v === "" || normalizeVnPhone(v) !== null, MESSAGES.phoneInvalid);

/** A password being typed to sign in: only presence is checked, the server decides the rest. */
const currentPassword = z.string().min(1, MESSAGES.passwordRequired).max(MAX_PASSWORD_BYTES, MESSAGES.passwordTooLong);

/** A password being set. */
const newPassword = z
  .string()
  .min(1, MESSAGES.passwordRequired)
  .refine((v) => byteLength(v) >= MIN_PASSWORD_BYTES, MESSAGES.passwordTooShort)
  .refine((v) => byteLength(v) <= MAX_PASSWORD_BYTES, MESSAGES.passwordTooLong);

const oneLineName = (required: string) =>
  z
    .string()
    .trim()
    .min(1, required)
    .refine((v) => [...v].length <= MAX_NAME_LENGTH && !/[\r\n\0]/.test(v), MESSAGES.nameTooLong);

const confirmation = z.string().min(1, "Nhập lại mật khẩu.");

/** New password typed twice; the mismatch is reported on the second field. */
const passwordsMatch = (v: { password: string; confirm: string }) => v.password === v.confirm;
const mismatchOnConfirm = { message: MESSAGES.passwordMismatch, path: ["confirm"] };

export const loginSchema = z.object({ login: loginIdentifier, password: currentPassword });
export type LoginValues = z.input<typeof loginSchema>;

/** Platform admins sign in by e-mail only. */
export const adminLoginSchema = z.object({ email, password: currentPassword });
export type AdminLoginValues = z.input<typeof adminLoginSchema>;

export const emailSchema = z.object({ email });
export type EmailValues = z.input<typeof emailSchema>;

export const newPasswordSchema = z.object({ password: newPassword, confirm: confirmation }).refine(passwordsMatch, mismatchOnConfirm);
export type NewPasswordValues = z.input<typeof newPasswordSchema>;

export const slugSchema = z
  .string()
  .trim()
  .min(1, MESSAGES.slugRequired)
  .refine((v) => isSlugFormatValid(v), MESSAGES.slugInvalid);

export const signupSchema = z
  .object({
    full_name: oneLineName(MESSAGES.fullNameRequired),
    store_name: oneLineName(MESSAGES.storeNameRequired),
    slug: slugSchema,
    email,
    phone: optionalPhone,
    password: newPassword,
    confirm: confirmation,
  })
  .refine(passwordsMatch, mismatchOnConfirm);
export type SignupValues = z.input<typeof signupSchema>;

export const totpCodeSchema = z.object({
  code: z
    .string()
    .transform((v) => v.replace(/\s+/g, ""))
    .pipe(z.string().regex(/^\d{6}$/, MESSAGES.codeInvalidFormat)),
});
export type TotpCodeValues = z.input<typeof totpCodeSchema>;

export const recoveryCodeSchema = z.object({
  recovery_code: z.string().trim().min(1, MESSAGES.recoveryCodeRequired).max(MAX_RECOVERY_CODE_LENGTH, "Mã khôi phục không hợp lệ."),
});
export type RecoveryCodeValues = z.input<typeof recoveryCodeSchema>;
