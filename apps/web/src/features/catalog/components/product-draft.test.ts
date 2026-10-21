import { describe, expect, it } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import {
  autoPrice,
  defaultPurchaseRow,
  editUnitTableErrors,
  firstProductError,
  initialProductDraft,
  MAX_UNIT_ROWS,
  PRODUCT_MESSAGES,
  productRequest,
  purchaseRowAfterSave,
  quickCreateRequest,
  sameUnitTable,
  savedUnitsOf,
  savedUnitTable,
  skuUnitsRequest,
  UNIT_EDIT_MESSAGES,
  UNIT_MESSAGES,
  unitPriceBelowBase,
  unitRow,
  unitsRequest,
  unitTableErrors,
  validateProductDraft,
  type ProductDraft,
  type UnitRow,
} from "./product-draft";
import type { SkuDetail, SkuUnit } from "../schemas";

const CAI = "unit-cai";
const DAY = "unit-day";
const THUNG = "unit-thung";
const HOP = "unit-hop";
const byName = (name: string) => ({ "hộp": HOP, "thùng": THUNG })[name] ?? null;

const row = (unitId: string | null, unitName: string, factor: string | null, over: Partial<UnitRow> = {}) => unitRow({ unitId, unitName, factor: factor === null ? null : toQty(factor), ...over });

/** "Kẹo X": counted in cái, sold by the dây of 50 at its own 24.000 and by the thùng of 500 at base × factor. */
function keoX(over: Partial<ProductDraft> = {}): ProductDraft {
  return {
    ...initialProductDraft("quick", byName),
    name: "Kẹo X",
    baseUnitId: CAI,
    retailPrice: toMoney(500),
    wholesalePrice: toMoney(450),
    units: [row(DAY, "dây", "50", { retail: toMoney(24000) }), row(THUNG, "thùng", "500")],
    ...over,
  };
}

describe("product draft", () => {
  it("starts with the prototype units: hộp, and thùng of 12", () => {
    const d = initialProductDraft("catalog", byName);
    expect(d.baseUnitId).toBe(HOP);
    expect(d.units).toMatchObject([{ unitId: THUNG, unitName: "thùng", factor: "12", retail: null, wholesale: null }]);
    expect(d.minStock).toBe("20");
    expect(initialProductDraft("quick", () => null)).toMatchObject({ baseUnitId: null, units: [], minStock: null });
  });
});

describe("unitTableErrors", () => {
  it("accepts a valid table", () => {
    expect(unitTableErrors(keoX())).toEqual({});
  });

  it("marks each row by its index: unnamed, the base unit repeated, a duplicate, a factor not above 0, wholesale over retail", () => {
    const errors = unitTableErrors(
      keoX({
        baseUnitId: null,
        units: [
          row(null, "", "10"),
          row(DAY, "dây", "50"),
          row(DAY, "dây", "0"),
          row(THUNG, "thùng", null, { retail: toMoney(200000), wholesale: toMoney(210000) }),
        ],
      }),
    );
    expect(errors).toEqual({
      base_unit_id: UNIT_MESSAGES.base,
      "units.0.unit": UNIT_MESSAGES.unit,
      "units.2.unit": UNIT_MESSAGES.duplicate("dây"),
      "units.2.factor": UNIT_MESSAGES.factor,
      "units.3.factor": UNIT_MESSAGES.factor,
      "units.3.wholesale": UNIT_MESSAGES.wholesale,
    });
    expect(unitTableErrors(keoX({ units: [row(CAI, "cái", "1")] }))).toEqual({ "units.0.unit": UNIT_MESSAGES.isBase("cái") });
  });

  it("refuses more unit rows than a product may hold", () => {
    const rows = (n: number) => Array.from({ length: n }, (_, i) => row(`unit-${i}`, `đv ${i}`, String(i + 2)));
    expect(unitTableErrors(keoX({ units: rows(MAX_UNIT_ROWS) }))).toEqual({});
    expect(unitTableErrors(keoX({ units: rows(MAX_UNIT_ROWS + 1) }))).toEqual({ units: UNIT_MESSAGES.tooMany });
  });

  it("checks a row's wholesale price only against its own retail price", () => {
    expect(unitTableErrors(keoX({ units: [row(DAY, "dây", "50", { wholesale: toMoney(30000) })] }))).toEqual({});
  });
});

describe("validateProductDraft", () => {
  it("asks for code and name in Danh mục, a name in Nhập hàng, and checks the unit table in both", () => {
    const bad = keoX({ name: " ", units: [row(DAY, "dây", "0")] });
    expect(validateProductDraft(bad, "catalog")).toEqual({ code: PRODUCT_MESSAGES.codeAndName, name: PRODUCT_MESSAGES.codeAndName, "units.0.factor": UNIT_MESSAGES.factor });
    expect(validateProductDraft(bad, "quick")).toEqual({ name: PRODUCT_MESSAGES.required, "units.0.factor": UNIT_MESSAGES.factor });
  });
});

describe("firstProductError", () => {
  it("adds how many more errors the unit table holds when the first is one of them", () => {
    expect(firstProductError({ "units.0.unit": "A.", "units.1.factor": "B.", "units.2.factor": "C." })).toBe("A." + UNIT_MESSAGES.more(2));
    expect(firstProductError({ "units.0.unit": "A." })).toBe("A.");
    expect(firstProductError({ name: "N.", "units.0.unit": "A." })).toBe("N.");
    expect(firstProductError({})).toBeUndefined();
  });
});

describe("unit prices", () => {
  it("computes the auto price as base price × factor, in whole dong", () => {
    expect(autoPrice(toMoney(500), toQty(50))).toBe("25000");
    expect(autoPrice(toMoney(333), toQty("1.5"))).toBe("500");
    expect(autoPrice(null, toQty(50))).toBe("0");
    expect(autoPrice(toMoney(500), null)).toBe("0");
  });

  it("warns about a unit whose own retail price is below the base unit's, and not about an auto price", () => {
    const low = row(DAY, "dây", "50", { retail: toMoney(400) });
    expect(unitPriceBelowBase(keoX({ units: [low, row(THUNG, "thùng", "500")] }))).toEqual([low]);
    expect(unitPriceBelowBase(keoX())).toEqual([]);
    expect(unitPriceBelowBase(keoX({ retailPrice: null, units: [low] }))).toEqual([]);
  });
});

describe("unitsRequest", () => {
  it("sends picked rows with a factor, and only the prices set on them", () => {
    const units = [row(DAY, "dây", "50", { retail: toMoney(24000), wholesale: toMoney(0) }), row(THUNG, "thùng", "500"), row(null, "kiện", "1000"), row(HOP, "hộp", null)];
    expect(unitsRequest(units)).toEqual([
      { unit_id: DAY, factor: "50", retail_price: "24000" },
      { unit_id: THUNG, factor: "500" },
    ]);
  });

  it("buys by the unit with the largest factor above 1, the first on a tie", () => {
    const first = row(THUNG, "thùng", "500");
    expect(defaultPurchaseRow([row(DAY, "dây", "50"), first, row(HOP, "hộp", "500")])).toBe(first);
    expect(defaultPurchaseRow([row(DAY, "lạng", "0.5")])).toBeUndefined();
    expect(defaultPurchaseRow([])).toBeUndefined();
  });
});

describe("productRequest", () => {
  it("sends the unit table and the base prices, without the old purchase unit pair", () => {
    const d = { ...keoX(), code: " KX01 ", barcode: " " };
    const body = productRequest(d, []);
    expect(body.sku).toMatchObject({
      code: "KX01",
      base_unit_id: CAI,
      barcode: null,
      retail_price: "500",
      wholesale_price: "450",
      units: [
        { unit_id: DAY, factor: "50", retail_price: "24000" },
        { unit_id: THUNG, factor: "500" },
      ],
    });
    expect(body.sku).not.toHaveProperty("purchase_unit_id");
    expect(body.sku).not.toHaveProperty("factor");
  });

  it("sends an empty unit list for a product of one unit", () => {
    expect(productRequest(keoX({ units: [] }), []).sku.units).toEqual([]);
  });
});

describe("quickCreateRequest", () => {
  it("prices the purchase by the largest unit: cái at 500 bought by the thùng of 500 is 250.000", () => {
    const body = quickCreateRequest(keoX({ cost: toMoney(500), units: [row(THUNG, "thùng", "500")] }));
    expect(body).toMatchObject({ name: "Kẹo X", base_unit_id: CAI, units: [{ unit_id: THUNG, factor: "500" }], purchase_unit_id: THUNG, purchase_price: "250000", sale_price: "500", wholesale_price: "450" });
  });

  it("picks the largest of several units as the purchase unit", () => {
    expect(quickCreateRequest(keoX({ cost: toMoney(500) }))).toMatchObject({ purchase_unit_id: THUNG, purchase_price: "250000" });
  });

  it("buys a product of one unit by its base unit at the typed price", () => {
    const body = quickCreateRequest(keoX({ cost: toMoney(500), units: [] }));
    expect(body).toMatchObject({ units: [], purchase_price: "500" });
    expect(body).not.toHaveProperty("purchase_unit_id");
  });

  it("names the base unit as the purchase unit when no unit is above it", () => {
    expect(quickCreateRequest(keoX({ cost: toMoney(500), units: [row(DAY, "lạng", "0.5")] }))).toMatchObject({ purchase_unit_id: CAI, purchase_price: "500" });
  });

  it("leaves the purchase price blank when none is typed", () => {
    expect(quickCreateRequest(keoX({ cost: null })).purchase_price).toBeNull();
  });
});

describe("editing a saved unit table", () => {
  const unit = (unitId: string, name: string, factor: string, over: Partial<SkuUnit> = {}): SkuUnit => ({
    unit_id: unitId,
    unit_name: name,
    factor,
    is_base: factor === "1",
    active: true,
    is_default_sale: false,
    is_default_purchase: false,
    retail_price: null,
    wholesale_price: null,
    locked_by: null,
    ...over,
  });
  /** Kẹo at 500 a cái: dây of 50 at its own 24.000 and scanned by a barcode, hộp of 20 stopped, thùng of 1000 bought by default. */
  const keo: SkuDetail = {
    id: "sku-keo",
    display_name: "Kẹo dẻo",
    base_unit_id: CAI,
    version: 3,
    units: [
      unit(CAI, "cái", "1", { retail_price: toMoney(500), wholesale_price: toMoney(450) }),
      unit(DAY, "dây", "50", { retail_price: toMoney(24000), locked_by: "documents" }),
      unit(HOP, "hộp", "20", { active: false }),
      unit(THUNG, "thùng", "1000", { is_default_purchase: true }),
    ],
    barcodes: [{ id: "bc-day", unit_id: DAY, code: "8930000000011" }],
  };

  it("reads the base prices from the base unit and keeps stopped units in the table", () => {
    const t = savedUnitTable(keo);
    expect(t).toMatchObject({ baseUnitId: CAI, retailPrice: "500", wholesalePrice: "450" });
    expect(t.units.map((u) => [u.unitName, u.factor, u.retail, u.active])).toEqual([
      ["dây", "50", "24000", true],
      ["hộp", "20", null, false],
      ["thùng", "1000", null, true],
    ]);
  });

  it("marks the saved state of each unit, with a lock a refused save reported", () => {
    const saved = savedUnitsOf(keo, { [THUNG]: "price_history" });
    expect(saved.purchaseUnitId).toBe(THUNG);
    expect(saved.units.get(DAY)).toEqual({ factor: "50", active: true, lockedBy: "documents", barcode: true });
    expect(saved.units.get(HOP)).toEqual({ factor: "20", active: false, lockedBy: null, barcode: false });
    expect(saved.units.get(THUNG)?.lockedBy).toBe("price_history");
  });

  it("moves the default purchase unit to the largest other active unit when it is stopped, else to the base unit", () => {
    const t = savedUnitTable(keo);
    expect(purchaseRowAfterSave(t, THUNG)?.unitName).toBe("thùng");
    const stopped = { ...t, units: t.units.map((u) => (u.unitId === THUNG ? { ...u, active: false } : u)) };
    expect(purchaseRowAfterSave(stopped, THUNG)?.unitName).toBe("dây");
    const onlyBase = { ...t, units: t.units.map((u) => ({ ...u, active: false })) };
    expect(purchaseRowAfterSave(onlyBase, THUNG)).toBeUndefined();
    expect(purchaseRowAfterSave(t, CAI)).toBeUndefined();
  });

  it("sends the whole table, a blank unit price as null", () => {
    const t = savedUnitTable(keo);
    const cleared = { ...t, units: t.units.map((u) => (u.unitId === DAY ? { ...u, retail: null } : u)) };
    expect(skuUnitsRequest(cleared)).toEqual({
      retail_price: "500",
      wholesale_price: "450",
      units: [
        { unit_id: DAY, factor: "50", active: true, retail_price: null, wholesale_price: null },
        { unit_id: HOP, factor: "20", active: false, retail_price: null, wholesale_price: null },
        { unit_id: THUNG, factor: "1000", active: true, retail_price: null, wholesale_price: null },
      ],
    });
  });

  it("resends a base price only when it changed from the table the editor opened with", () => {
    const started = savedUnitTable(keo);
    expect(skuUnitsRequest(started, started)).toMatchObject({ retail_price: null, wholesale_price: null });
    const retyped = { ...started, retailPrice: toMoney(600), wholesalePrice: toMoney(450) };
    expect(skuUnitsRequest(retyped, started)).toMatchObject({ retail_price: "600", wholesale_price: null });
  });

  it("sees a table typed back to its saved values as unchanged, whatever the row order", () => {
    const saved = savedUnitTable(keo);
    const retyped = { ...saved, retailPrice: toMoney(500), units: [...saved.units].reverse() };
    expect(sameUnitTable(retyped, saved)).toBe(true);
    expect(sameUnitTable({ ...saved, units: saved.units.slice(1) }, saved)).toBe(false);
  });

  it("refuses to blank a saved base price, which the server would keep", () => {
    const saved = savedUnitTable(keo);
    expect(editUnitTableErrors({ ...saved, wholesalePrice: null }, saved)).toEqual({ wholesale_price: UNIT_EDIT_MESSAGES.basePrice });
    expect(editUnitTableErrors(saved, saved)).toEqual({});
  });
});
