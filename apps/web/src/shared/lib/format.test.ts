import { describe, expect, it } from "vitest";
import { fd, fdS, num, qMix, vnd } from "./format";

const NBSP = " ";

describe("formatters", () => {
  it("formats money as whole dong with dot grouping and NBSP before ₫", () => {
    expect(vnd("1250000")).toBe(`1.250.000${NBSP}₫`);
    expect(vnd("0")).toBe(`0${NBSP}₫`);
    expect(vnd("1249.5")).toBe(`1.250${NBSP}₫`);
    expect(vnd("-1500")).toMatch(/^-1\.500\u00a0₫$/);
    expect(vnd("123456789012345678")).toBe(`123.456.789.012.345.678${NBSP}₫`);
  });

  it("formats numbers with a decimal comma", () => {
    expect(num("1234.5")).toBe("1.234,5");
    expect(num("2")).toBe("2");
  });

  it("formats business dates as dd/mm/yyyy", () => {
    expect(fd("2026-10-12")).toBe("12/10/2026");
    expect(fd("2026-10-12 08:30")).toBe("12/10/2026 08:30");
    expect(fd(null)).toBe("—");
    expect(fdS("2026-10-12")).toBe("12/10");
  });

  it("writes mixed-unit quantities from the largest unit down", () => {
    const units = [
      { name: "hộp", factor: "1" },
      { name: "dây", factor: "6" },
      { name: "thùng", factor: "24" },
    ];
    expect(qMix("44", units, "hộp")).toBe("1 thùng 3 dây 2 hộp");
    expect(qMix("24", units, "hộp")).toBe("1 thùng");
    expect(qMix("0", units, "hộp")).toBe("0");
    expect(qMix("-7", units, "hộp")).toBe("−1 dây 1 hộp");
  });
});
