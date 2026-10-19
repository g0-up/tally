import { describe, expect, expectTypeOf, it } from "vitest";
import { STATUS_GLYPH, STATUS_PREFIXES, statusTone, type StatusTone } from "./status-tone";

describe("statusTone", () => {
  it("gives every status label one typed tone", () => {
    expectTypeOf(statusTone).returns.toEqualTypeOf<StatusTone>();
    expectTypeOf<keyof typeof STATUS_GLYPH>().toEqualTypeOf<StatusTone>();
  });

  it("lists each prefix under a single tone, so an unpaid invoice never takes two colors", () => {
    const all = Object.values(STATUS_PREFIXES).flat();
    expect(new Set(all).size).toBe(all.length);
    expect(all.filter((p) => p === "Chưa thanh toán")).toHaveLength(1);
  });

  it.each([
    ["Chưa thanh toán", "warn"],
    ["Đã thanh toán đủ", "ok"],
    ["Quá hạn 5 ngày", "bad"],
    ["Nháp", "mute"],
    ["Một nhãn lạ", "info"],
  ] as const)("reads %s as %s", (label, tone) => {
    expect(statusTone(label)).toBe(tone);
  });
});
