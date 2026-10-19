import { describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { ApiError, ERROR_CODES, errorMessage, isReauthRequired, isSessionExpired, parseRetryAfter } from "./api-error";
import { createCsrfSource } from "./csrf";
import { buildUrl, createHttpClient, type HttpClientConfig } from "./http";

type FetchMock = ReturnType<typeof vi.fn<typeof fetch>>;

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
}

function setup(responses: Response[], extra: Partial<HttpClientConfig> = {}) {
  const fetchImpl: FetchMock = vi.fn<typeof fetch>();
  for (const r of responses) fetchImpl.mockResolvedValueOnce(r);
  const onError = vi.fn();
  const client = createHttpClient({ baseUrl: "/api/v1", csrf: createCsrfSource([]), onError, fetchImpl, ...extra });
  const headersOf = (call: number) => new Headers(fetchImpl.mock.calls[call]?.[1]?.headers);
  return { client, fetchImpl, onError, headersOf };
}

describe("http client", () => {
  it("sends cookies and JSON to the same-origin API base", async () => {
    const { client, fetchImpl } = setup([json(200, { ok: true })]);
    await client.get("/items", { query: { q: "bánh", empty: "", status: ["open", "draft"] } });
    const [url, init] = fetchImpl.mock.calls[0] ?? [];
    expect(url).toBe("/api/v1/items?q=b%C3%A1nh&status=open&status=draft");
    expect(init?.credentials).toBe("include");
  });

  it("adds the CSRF token to writes only", async () => {
    const { client, headersOf } = setup([json(200, {}), json(200, {})]);
    client.setCsrfToken("tok-1");
    await client.get("/items");
    await client.post("/items", { a: 1 });
    expect(headersOf(0).get("X-CSRF-Token")).toBeNull();
    expect(headersOf(1).get("X-CSRF-Token")).toBe("tok-1");
    expect(headersOf(1).get("Content-Type")).toBe("application/json");
  });

  it("sends the caller's Idempotency-Key and If-Match unchanged on every attempt", async () => {
    const { client, headersOf } = setup([json(503, { error: { code: "unavailable", message: "try later" } }), json(201, { id: "1" })]);
    const key = "intent-key-1";
    await expect(client.post("/sales", {}, { idempotencyKey: key, ifMatch: 'W/"3"' })).rejects.toBeInstanceOf(ApiError);
    await client.post("/sales", {}, { idempotencyKey: key, ifMatch: 'W/"3"' });
    for (const call of [0, 1]) {
      expect(headersOf(call).get("Idempotency-Key")).toBe(key);
      expect(headersOf(call).get("If-Match")).toBe('W/"3"');
    }
  });

  it("returns the ETag of the response", async () => {
    const { client } = setup([json(200, { id: "1" }, { ETag: 'W/"7"' })]);
    const res = await client.request("GET", "/items/1");
    expect(res.etag).toBe('W/"7"');
    expect(res.data).toEqual({ id: "1" });
  });

  it("parses the error envelope", async () => {
    const { client, onError } = setup([
      json(409, { error: { code: "credit_limit_exceeded", message: "Vượt hạn mức", fields: { "lines[0].qty": "insufficient_stock" }, exception_kind: "credit_limit_exceeded" } }),
    ]);
    const error = await client.post("/sales", {}).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    const e = error as ApiError;
    expect(e.status).toBe(409);
    expect(e.code).toBe("credit_limit_exceeded");
    expect(e.message).toBe("Vượt hạn mức");
    expect(e.fields).toEqual({ "lines[0].qty": "insufficient_stock" });
    expect(e.exceptionKind).toBe("credit_limit_exceeded");
    expect(onError).toHaveBeenCalledWith(e, { method: "POST", path: "/sales" });
  });

  it("turns a body that is not the envelope into bad_response", async () => {
    const { client } = setup([new Response("<html>gateway</html>", { status: 502 })]);
    await expect(client.get("/items")).rejects.toMatchObject({ status: 502, code: ERROR_CODES.badResponse });
  });

  it("reports a 412 and a 401 to the app-wide handler, but not for silent requests", async () => {
    const { client, onError } = setup([
      json(412, { error: { code: "precondition_failed", message: "stale" } }),
      json(401, { error: { code: "unauthenticated", message: "no session" } }),
    ]);
    await expect(client.patch("/items/1", {}, { ifMatch: 'W/"1"' })).rejects.toMatchObject({ status: 412 });
    await expect(client.get("/me", { silent: true })).rejects.toMatchObject({ status: 401, code: "unauthenticated" });
    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0]?.[0]).toMatchObject({ status: 412 });
  });

  it("refreshes the CSRF token once after csrf_failed and retries", async () => {
    const refreshCsrf = vi.fn(async () => {});
    const { client, fetchImpl } = setup(
      [json(403, { error: { code: "csrf_failed", message: "bad token" } }), json(403, { error: { code: "csrf_failed", message: "bad token" } })],
      { refreshCsrf },
    );
    await expect(client.post("/items", {})).rejects.toMatchObject({ code: "csrf_failed" });
    expect(refreshCsrf).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it("maps a network failure to network_error", async () => {
    const fetchImpl = vi.fn<typeof fetch>().mockRejectedValue(new TypeError("Failed to fetch"));
    const client = createHttpClient({ baseUrl: "/api/v1", csrf: createCsrfSource([]), fetchImpl });
    await expect(client.get("/items")).rejects.toMatchObject({ status: 0, code: ERROR_CODES.network });
  });

  it("validates responses against the schema", async () => {
    const { client } = setup([json(200, { count: "three" })]);
    await expect(client.get("/count", { schema: z.object({ count: z.number() }) })).rejects.toMatchObject({ code: ERROR_CODES.badResponse });
  });

  it("refuses paths that would leave the current host", () => {
    expect(() => buildUrl("/api/v1", "https://evil.example/x")).toThrow();
    expect(() => buildUrl("/api/v1", "//evil.example/x")).toThrow();
  });

  it("reads Retry-After from a throttled answer", async () => {
    const { client } = setup([json(429, { error: { code: "rate_limited", message: "slow down" } }, { "Retry-After": "900" })]);
    const error = (await client.post("/auth/login", {}).catch((e: unknown) => e)) as ApiError;
    expect(error.status).toBe(429);
    expect(error.retryAfterSeconds).toBe(900);
    expect(errorMessage(error)).toBe("Thao tác quá nhanh. Thử lại sau 15 phút.");
  });
});

describe("error classification", () => {
  // As the server sends them: its message is not a sentence for the owner.
  const err = (status: number, code: string) => new ApiError({ status, code, message: code, fromServer: true });

  it("treats only 401 unauthenticated as an expired session", () => {
    expect(isSessionExpired(err(401, "unauthenticated"))).toBe(true);
    expect(isSessionExpired(err(401, "invalid_credentials"))).toBe(false);
    expect(isSessionExpired(err(401, "invalid_code"))).toBe(false);
    expect(isSessionExpired(err(403, "reauth_required"))).toBe(false);
  });

  it("asks for re-authentication on 403 reauth_required only", () => {
    expect(isReauthRequired(err(403, "reauth_required"))).toBe(true);
    expect(isReauthRequired(err(403, "invalid_password"))).toBe(false);
    expect(isReauthRequired(err(401, "unauthenticated"))).toBe(false);
  });

  it("explains any 429 as throttling, naming the wait when known", () => {
    expect(errorMessage(err(429, "rate_limited"))).toBe("Thao tác quá nhanh. Chờ một chút rồi thử lại.");
    expect(errorMessage(new ApiError({ status: 429, code: "rate_limited", message: "x", retryAfterSeconds: 42 }))).toBe(
      "Thao tác quá nhanh. Thử lại sau 42 giây.",
    );
  });

  it("explains a locked period and a missing If-Match", () => {
    expect(errorMessage(err(423, "period_locked"))).toBe("Kỳ kế toán của ngày này đã khóa.");
    expect(errorMessage(err(428, "precondition_required"))).toBe("Dữ liệu đã thay đổi ở nơi khác. Tải lại để tiếp tục.");
  });

  it("explains a request that raced a sandbox reset", () => {
    expect(errorMessage(err(409, "sandbox_replaced"))).toBe("Cửa hàng mẫu vừa được tạo lại. Đăng nhập lại để vào cửa hàng mẫu mới.");
    expect(errorMessage(err(409, "sandbox_busy"))).toBe("Cửa hàng mẫu đang bận. Thử tạo lại sau ít giây.");
    expect(errorMessage(err(409, "sandbox_not_active"))).toBe("Cửa hàng mẫu này đã được đặt lại. Tải lại trang để vào cửa hàng mẫu mới.");
  });

  it("parses Retry-After in seconds and as an HTTP date", () => {
    expect(parseRetryAfter(null)).toBeNull();
    expect(parseRetryAfter("60")).toBe(60);
    expect(parseRetryAfter("Thu, 01 Jan 2026 00:01:00 GMT", Date.UTC(2026, 0, 1, 0, 0, 0))).toBe(60);
    expect(parseRetryAfter("soon")).toBeNull();
  });
});
