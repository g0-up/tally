import { describe, expect, it } from "vitest";
import { msUntilExpiry, supportPathError, supportViewUrl } from "./support-view";

describe("support view paths", () => {
  it("forwards a route of the granted module", () => {
    expect(supportPathError("catalog", "products/0193a000-0000-7000-8000-000000000001?q=keo")).toBeNull();
  });

  it("refuses dot segments, also when percent-escaped, and routes of another module", () => {
    expect(supportPathError("catalog", "products/../invoices")).toBe("Đường dẫn không hợp lệ.");
    expect(supportPathError("catalog", "products/%2e%2e/invoices")).toBe("Đường dẫn không hợp lệ.");
    expect(supportPathError("catalog", "products/%2E%2E")).toBe("Đường dẫn không hợp lệ.");
    expect(supportPathError("catalog", "products//x")).toBe("Đường dẫn không hợp lệ.");
    expect(supportPathError("catalog", "invoices")).toMatch(/^Đường dẫn phải bắt đầu bằng một trong: products/);
  });

  it("escapes the store id and every route segment", () => {
    expect(supportViewUrl("a/b", "catalog", "products/x y")).toBe("/tenants/a%2Fb/support-view/catalog/products/x%20y");
  });

  it("waits until the grant runs out, never less than zero nor longer than a timer keeps", () => {
    const now = Date.parse("2026-10-18T10:00:00Z");
    expect(msUntilExpiry("2026-10-18T10:05:00Z", now)).toBe(300_000);
    expect(msUntilExpiry("2026-10-18T09:00:00Z", now)).toBe(0);
    expect(msUntilExpiry("2099-01-01T00:00:00Z", now)).toBe(2_147_483_647);
  });
});
