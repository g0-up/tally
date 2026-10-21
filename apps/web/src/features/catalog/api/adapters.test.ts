import { describe, expect, it } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import type { SkuUnit } from "../schemas";
import { autoConvertQty, unitConversionLines, unitFactorLine } from "./adapters";

function unit(unit_id: string, factor: string, over: Partial<SkuUnit> = {}): SkuUnit {
  return {
    unit_id,
    unit_name: unit_id,
    factor,
    is_base: factor === "1",
    active: true,
    is_default_sale: factor === "1",
    is_default_purchase: false,
    retail_price: null,
    wholesale_price: null,
    locked_by: null,
    ...over,
  } as SkuUnit;
}

/** Counted in cái, with bó of 10 and thùng of 100 listed out of order. */
const keo = { units: [unit("thùng", "100"), unit("cái", "1"), unit("bó", "10")] };

describe("autoConvertQty", () => {
  it("writes a base quantity in the larger units, largest first, the rest in the base unit", () => {
    expect(autoConvertQty(keo, "180")).toBe("1 thùng 8 bó");
    expect(autoConvertQty(keo, "200")).toBe("2 thùng");
    expect(autoConvertQty(keo, "183")).toBe("1 thùng 8 bó 3 cái");
    expect(autoConvertQty(keo, "10")).toBe("1 bó");
  });

  it("writes nothing below the smallest larger unit, for a negative quantity, a single unit or an unloaded SKU", () => {
    expect(autoConvertQty(keo, "7")).toBeNull();
    expect(autoConvertQty(keo, "-15")).toBeNull();
    expect(autoConvertQty({ units: [unit("cái", "1")] }, "180")).toBeNull();
    expect(autoConvertQty(undefined, "180")).toBeNull();
  });

  it("skips a retired unit", () => {
    const retired = { units: [unit("thùng", "100", { active: false }), unit("cái", "1"), unit("bó", "10")] };
    expect(autoConvertQty(retired, "180")).toBe("18 bó");
    expect(autoConvertQty({ units: [unit("thùng", "100", { active: false }), unit("cái", "1")] }, "180")).toBeNull();
  });

  it("ignores a unit smaller than the base unit", () => {
    expect(autoConvertQty({ units: [unit("kg", "1"), unit("lạng", "0.1")] }, "3")).toBeNull();
  });

  it("keeps a decimal base quantity in the remainder", () => {
    expect(autoConvertQty(keo, "185.5")).toBe("1 thùng 8 bó 5,5 cái");
  });
});

describe("unitFactorLine", () => {
  it("lists the active larger units smallest first after the base unit", () => {
    expect(unitFactorLine(keo)).toBe(" · 1 bó = 10 cái · 1 thùng = 100 cái");
    expect(unitFactorLine({ units: [unit("thùng", "100", { active: false }), unit("cái", "1")] })).toBe("");
  });
});

describe("unitConversionLines", () => {
  it("lists every unit smallest first with its own price, marking retired ones", () => {
    const sku = { units: [unit("thùng", "100", { active: false }), unit("cái", "1"), unit("bó", "10", { retail_price: toMoney(24000) })] };
    expect(unitConversionLines(sku)).toEqual([
      { key: "cái", text: "cái", isBase: true, price: null, inactive: false },
      { key: "bó", text: "1 bó = 10 cái", isBase: false, price: "24000", inactive: false },
      { key: "thùng", text: "1 thùng = 100 cái", isBase: false, price: null, inactive: true },
    ]);
  });
});
