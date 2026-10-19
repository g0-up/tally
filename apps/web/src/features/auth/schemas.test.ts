import { describe, expect, it } from "vitest";
import { MESSAGES, loginSchema, newPasswordSchema, signupSchema, totpCodeSchema } from "./schemas";

function firstIssue(result: { success: boolean; error?: { issues: Array<{ path: PropertyKey[]; message: string }> } }) {
  return result.error?.issues[0];
}

describe("auth form schemas", () => {
  it("requires an e-mail or a Vietnamese phone number of the usual shape and a password to sign in", () => {
    expect(loginSchema.parse({ login: " chu@example.test ", password: "x" })).toEqual({ login: "chu@example.test", password: "x" });
    expect(loginSchema.safeParse({ login: "0912 418 226", password: "x" }).success).toBe(true);
    expect(loginSchema.safeParse({ login: "+84 912.418.226", password: "x" }).success).toBe(true);
    expect(loginSchema.safeParse({ login: "028 3822 1234", password: "x" }).success).toBe(true);
    expect(firstIssue(loginSchema.safeParse({ login: "  ", password: "x" }))?.message).toBe(MESSAGES.loginRequired);
    expect(firstIssue(loginSchema.safeParse({ login: "chu@", password: "x" }))?.message).toBe(MESSAGES.loginInvalid);
    expect(firstIssue(loginSchema.safeParse({ login: "0612418226", password: "x" }))?.message).toBe(MESSAGES.loginInvalid);
    expect(firstIssue(loginSchema.safeParse({ login: "091241822", password: "x" }))?.message).toBe(MESSAGES.loginInvalid);
    expect(firstIssue(loginSchema.safeParse({ login: "chu@example.test", password: "" }))?.message).toBe(MESSAGES.passwordRequired);
  });

  it("counts password length in UTF-8 bytes like the API", () => {
    // Four 2-byte letters are 8 bytes: long enough, although only 4 characters.
    expect(newPasswordSchema.safeParse({ password: "ăăăă", confirm: "ăăăă" }).success).toBe(true);
    expect(firstIssue(newPasswordSchema.safeParse({ password: "1234567", confirm: "1234567" }))?.message).toBe(MESSAGES.passwordTooShort);
    const tooLong = "a".repeat(257);
    expect(firstIssue(newPasswordSchema.safeParse({ password: tooLong, confirm: tooLong }))?.message).toBe(MESSAGES.passwordTooLong);
  });

  it("reports a mismatched confirmation on the second field", () => {
    const issue = firstIssue(newPasswordSchema.safeParse({ password: "12345678", confirm: "12345679" }));
    expect(issue?.path).toEqual(["confirm"]);
    expect(issue?.message).toBe(MESSAGES.passwordMismatch);
  });

  it("checks the store address shape on sign-up", () => {
    const base = { full_name: "Nguyễn Văn An", store_name: "Hòa Phát", email: "an@example.test", phone: "", password: "12345678", confirm: "12345678" };
    expect(signupSchema.safeParse({ ...base, slug: "hoa-phat" }).success).toBe(true);
    const issue = firstIssue(signupSchema.safeParse({ ...base, slug: "hoa-phat-demo" }));
    expect(issue?.path).toEqual(["slug"]);
    expect(issue?.message).toBe(MESSAGES.slugInvalid);
  });

  it("takes an optional Vietnamese phone number on sign-up", () => {
    const base = { full_name: "Nguyễn Văn An", store_name: "Hòa Phát", slug: "hoa-phat", email: "an@example.test", password: "12345678", confirm: "12345678" };
    expect(signupSchema.safeParse({ ...base, phone: "" }).success).toBe(true);
    expect(signupSchema.safeParse({ ...base, phone: "0912 418 226" }).success).toBe(true);
    const issue = firstIssue(signupSchema.safeParse({ ...base, phone: "12345" }));
    expect(issue?.path).toEqual(["phone"]);
    expect(issue?.message).toBe(MESSAGES.phoneInvalid);
  });

  it("rejects multi-line names", () => {
    const issue = firstIssue(
      signupSchema.safeParse({ full_name: "An\nB", store_name: "Hòa Phát", slug: "hoa-phat", email: "an@example.test", phone: "", password: "12345678", confirm: "12345678" }),
    );
    expect(issue?.path).toEqual(["full_name"]);
  });

  it("accepts a 6-digit code typed with spaces", () => {
    expect(totpCodeSchema.parse({ code: "123 456" })).toEqual({ code: "123456" });
    expect(firstIssue(totpCodeSchema.safeParse({ code: "12345" }))?.message).toBe(MESSAGES.codeInvalidFormat);
  });
});
