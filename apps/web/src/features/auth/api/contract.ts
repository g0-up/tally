import { z } from "zod";
import { PATHS } from "../../../shared/api/endpoints";
import type { components, operations } from "../../../shared/api/schema";

// Paths and response schemas of the identity endpoints the auth screens call. Store-host and
// root-host paths sit under /api/v1; the admin console's sign-in sits under /api/admin/v1.

export const AUTH_PATHS = {
  signup: "/auth/signup",
  slugCheck: "/signup/slug-check",
  storeLookup: "/store-lookup",
  verify: "/auth/verify",
  login: "/auth/login",
  logout: PATHS.logout,
  passwordReset: "/auth/password-reset",
  passwordResetConfirm: "/auth/password-reset/confirm",
  mfaVerify: "/auth/mfa/totp/verify",
} as const;

/**
 * Platform admin sign-in (admin host, /api/admin/v1). The API does not serve these yet; the
 * screen follows the planned contract: password step answering `mfa_required` with a challenge,
 * then the TOTP step answering `signed_in` with the CSRF token of the admin session.
 */
export const ADMIN_AUTH_PATHS = {
  login: "/auth/login",
  totp: "/auth/totp",
} as const;

export const authResultSchema = z.discriminatedUnion("status", [
  z.object({ status: z.literal("signed_in"), csrf_token: z.string().optional(), expires_at: z.string().optional() }),
  z.object({ status: z.literal("mfa_required"), challenge: z.string().min(1) }),
]);
export type AuthResult = z.infer<typeof authResultSchema>;

export const csrfTokenSchema = z.object({ csrf_token: z.string() });

export const signupResultSchema = z.object({ store_url: z.string() });

export const slugCheckSchema = z.object({
  available: z.boolean(),
  reason: z.enum(["invalid", "reserved", "taken"]).nullish(),
  suggestion: z.string().nullish(),
});
export type SlugCheck = z.infer<typeof slugCheckSchema>;

type Schemas = components["schemas"];
type JsonBody<Op extends keyof operations> = operations[Op] extends { requestBody: { content: { "application/json": infer B } } } ? B : never;

/** Request bodies, typed from the OpenAPI document so a contract change breaks the build. */
export type SignupBody = Schemas["SignupRequest"];
export type EmailBody = Schemas["EmailRequest"];
export type TokenPasswordBody = Schemas["TokenPasswordRequest"];
export type LoginBody = JsonBody<"login">;
/** The admin console's planned sign-in body; the admin API is not in the OpenAPI document yet. */
export interface AdminLoginBody {
  email: string;
  password: string;
}
export type VerifyBody = JsonBody<"verifyEmail">;
/** The MFA step of a login sends the challenge with exactly one of the two codes. */
export type MfaChallengeBody = JsonBody<"verifyTOTP"> & ({ challenge: string; code: string } | { challenge: string; recovery_code: string });
