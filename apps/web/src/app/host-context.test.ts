import { describe, expect, it, vi } from "vitest";
import { inferHostKind, loadHostContext } from "./host-context";

describe("inferHostKind", () => {
  it.each([
    ["localhost", "root"],
    ["127.0.0.1", "root"],
    ["demo.localhost", "tenant"],
    ["admin.localhost", "admin"],
    ["tally.vn", "root"],
    ["www.tally.vn", "root"],
    ["hoaphat.tally.vn", "tenant"],
    ["admin.tally.vn", "admin"],
  ])("%s is a %s host", (hostname, kind) => {
    expect(inferHostKind(hostname)).toBe(kind);
  });
});

describe("loadHostContext", () => {
  const ok = (body: unknown, status = 200) => vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(body), { status }));

  it("uses the API's answer", async () => {
    const res = await loadHostContext(ok({ kind: "admin", root_url: "https://tally.vn" }), "demo.localhost");
    expect(res).toEqual({ context: { kind: "admin", root_url: "https://tally.vn" }, source: "api" });
  });

  it("falls back to the hostname when the endpoint is missing", async () => {
    const res = await loadHostContext(ok({ error: { code: "not_found", message: "nf" } }, 404), "demo.localhost");
    expect(res).toEqual({ context: { kind: "tenant" }, source: "hostname" });
  });

  it("falls back to the hostname when the answer is unreadable", async () => {
    const res = await loadHostContext(ok({ kind: "planet" }), "admin.localhost");
    expect(res.context.kind).toBe("admin");
  });

  it("fails on a host the API does not serve", async () => {
    await expect(loadHostContext(ok({ error: { code: "misdirected", message: "x" } }, 421), "x.example")).rejects.toThrow("misdirected_request");
  });

  it("fails on a network error so the boot screen can retry", async () => {
    await expect(loadHostContext(vi.fn<typeof fetch>().mockRejectedValue(new TypeError("offline")), "demo.localhost")).rejects.toThrow();
  });
});
