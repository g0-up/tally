import { describe, expect, it, vi } from "vitest";
import { ApiError, networkError } from "../../../shared/api/api-error";
import { MESSAGES } from "../schemas";
import { AUTH_MESSAGES, applyAuthError, authErrorMessage, authFieldErrors } from "./auth-errors";

function apiError(status: number, code: string, fields: Record<string, string> = {}, retryAfterSeconds: number | null = null): ApiError {
  return new ApiError({ status, code, message: code, fields, exceptionKind: null, retryAfterSeconds });
}

describe("authErrorMessage", () => {
  it("names the sign-in refusals in Vietnamese", () => {
    expect(authErrorMessage(apiError(401, "invalid_credentials"))).toBe(AUTH_MESSAGES.invalidCredentials);
    expect(authErrorMessage(apiError(403, "email_not_verified"))).toBe(AUTH_MESSAGES.emailNotVerified);
    expect(authErrorMessage(apiError(422, "token_invalid"))).toBe(AUTH_MESSAGES.tokenInvalid);
  });

  it("states the wait of a throttled sign-in without claiming which limit was hit", () => {
    expect(authErrorMessage(apiError(429, "rate_limited", {}, 900))).toBe("Bạn đã thử quá nhiều lần. Thử lại sau 15 phút.");
    expect(authErrorMessage(apiError(429, "rate_limited", {}, 30))).toBe("Bạn đã thử quá nhiều lần. Thử lại sau 30 giây.");
    expect(authErrorMessage(apiError(429, "rate_limited"))).toBe("Bạn đã thử quá nhiều lần. Chờ một chút rồi thử lại.");
  });

  it("falls back to the shared texts for network failures", () => {
    expect(authErrorMessage(networkError())).toMatch(/Không kết nối được máy chủ/);
  });
});

describe("authFieldErrors", () => {
  it("keeps only the form's fields, in form order, with field-specific wording", () => {
    const e = apiError(422, "slug_taken", { slug: "taken", other: "invalid", email: "invalid" });
    expect(authFieldErrors(e, ["email", "slug"] as const)).toEqual([
      ["email", MESSAGES.emailInvalid],
      ["slug", MESSAGES.slugTaken],
    ]);
  });

  it("is empty for errors that are not API refusals", () => {
    expect(authFieldErrors(new Error("x"), ["email"] as const)).toEqual([]);
  });
});

describe("applyAuthError", () => {
  it("marks the fields, focuses the first and returns the form-level text", () => {
    const setError = vi.fn();
    const message = applyAuthError<{ email: string; password: string }>(apiError(422, "validation_failed", { password: "too_short", email: "invalid" }), ["email", "password"], setError);
    expect(message).toBe(AUTH_MESSAGES.checkFields);
    expect(setError).toHaveBeenNthCalledWith(1, "email", { type: "server", message: MESSAGES.emailInvalid }, { shouldFocus: true });
    expect(setError).toHaveBeenNthCalledWith(2, "password", { type: "server", message: MESSAGES.passwordTooShort }, { shouldFocus: false });
  });
});
