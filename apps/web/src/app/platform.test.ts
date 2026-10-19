import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { ERROR_CODES, isApiError } from "../shared/api/api-error";
import { PATHS } from "../shared/api/endpoints";
import { apiError, apiUrl } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { createPlatform } from "./platform";

describe("CSRF refresh", () => {
  it("gets a new token from host-context after csrf_failed and retries once with it", async () => {
    const sent: (string | null)[] = [];
    let hostContextCalls = 0;
    server.use(
      http.get(apiUrl(PATHS.hostContext), () => {
        hostContextCalls += 1;
        return HttpResponse.json({ kind: "tenant", root_url: "http://localhost:5173", csrf_token: "fresh-token" });
      }),
      http.post(apiUrl("/things"), ({ request }) => {
        const token = request.headers.get("X-CSRF-Token");
        sent.push(token);
        return token === "fresh-token" ? HttpResponse.json({ ok: true }) : apiError(403, ERROR_CODES.csrfFailed);
      }),
    );
    const { client } = createPlatform("tenant");

    const data = await client.post<{ ok: boolean }>("/things", { a: 1 });

    expect(data).toEqual({ ok: true });
    expect(hostContextCalls).toBe(1);
    expect(sent).toEqual([null, "fresh-token"]);
  });

  it("answers with the original refusal when the refresh fails", async () => {
    let attempts = 0;
    server.use(
      http.get(apiUrl(PATHS.hostContext), () => new HttpResponse(null, { status: 503 })),
      http.post(apiUrl("/things"), () => {
        attempts += 1;
        return apiError(403, ERROR_CODES.csrfFailed);
      }),
    );
    const { client } = createPlatform("tenant");

    const error: unknown = await client.post("/things", {}).catch((e: unknown) => e);

    expect(isApiError(error) && error.code).toBe(ERROR_CODES.csrfFailed);
    expect(attempts).toBe(1);
  });

  it("does not loop when the new token is refused too", async () => {
    let attempts = 0;
    server.use(
      http.get(apiUrl(PATHS.hostContext), () => HttpResponse.json({ kind: "tenant", csrf_token: "still-wrong" })),
      http.post(apiUrl("/things"), () => {
        attempts += 1;
        return apiError(403, ERROR_CODES.csrfFailed);
      }),
    );
    const { client } = createPlatform("tenant");

    await expect(client.post("/things", {})).rejects.toMatchObject({ code: ERROR_CODES.csrfFailed });
    expect(attempts).toBe(2);
  });
});
