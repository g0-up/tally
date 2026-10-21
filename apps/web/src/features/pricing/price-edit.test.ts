import { describe, expect, it } from "vitest";
import { toMoney } from "../../shared/domain/money";
import { PRICE_MESSAGES, priceableUnits, salePriceRequests, unitAutoPrices, unitPriceDrafts, validatePriceDraft, type CurrentPrices, type PriceDraft, type UnitPrices } from "./price-edit";

const unit = (unit_id: string, unit_name: string, factor: string, over: Partial<UnitPrices> = {}): UnitPrices => ({
  unit_id,
  unit_name,
  factor,
  is_base: factor === "1",
  active: true,
  retail_price: null,
  wholesale_price: null,
  ...over,
});

/** Kẹo at 500 a cái (sỉ 450): dây of 50 at its own 24.000, hộp of 20 at base × factor, a stopped thùng. */
const KEO: CurrentPrices = {
  retail_price: toMoney(500),
  wholesale_price: toMoney(450),
  units: [
    unit("cai", "cái", "1", { retail_price: toMoney(500), wholesale_price: toMoney(450) }),
    unit("day", "dây", "50", { retail_price: toMoney(24000) }),
    unit("hop", "hộp", "20"),
    unit("thung", "thùng", "1000", { active: false, retail_price: toMoney(480000) }),
  ],
};

const draftOf = (over: Partial<PriceDraft> = {}): PriceDraft => ({ retail: toMoney(500), wholesale: toMoney(450), allColors: false, units: unitPriceDrafts(KEO), ...over });
const withUnit = (unitId: string, retail: number | null, wholesale: number | null = null) => ({
  ...unitPriceDrafts(KEO),
  [unitId]: { retail: retail === null ? null : toMoney(retail), wholesale: wholesale === null ? null : toMoney(wholesale) },
});

describe("unit prices of the sell-price dialog", () => {
  it("edits the active units other than the base one, from their own prices", () => {
    expect(priceableUnits(KEO).map((u) => u.unit_id)).toEqual(["day", "hop"]);
    expect(unitPriceDrafts(KEO)).toEqual({ day: { retail: "24000", wholesale: null }, hop: { retail: null, wholesale: null } });
  });

  it("shows a unit without its own price at the draft's base price × factor", () => {
    const hop = KEO.units?.[2];
    if (hop === undefined) throw new Error("no hộp");
    expect(unitAutoPrices(draftOf({ retail: toMoney(600) }), KEO, hop)).toEqual({ retail: "12000", wholesale: "9000" });
    // An empty base wholesale keeps the current one.
    expect(unitAutoPrices(draftOf({ wholesale: null }), KEO, hop).wholesale).toBe("9000");
  });

  it("sends a cleared unit price as null with its unit, after the base prices, and leaves unchanged units out", () => {
    const draft = draftOf({ retail: toMoney(520), units: withUnit("day", null, 22000) });
    expect(salePriceRequests(draft, KEO, "inventory")).toEqual([
      { kind: "retail", price: "520", apply_all_colors: false, source: "inventory" },
      { kind: "retail", price: null, unit_id: "day", source: "inventory" },
      { kind: "wholesale", price: "22000", unit_id: "day", source: "inventory" },
    ]);
  });

  it("saves a change of a unit price alone, and still refuses a draft that changes nothing", () => {
    expect(validatePriceDraft(draftOf({ units: withUnit("hop", 9500) }), KEO)).toBeNull();
    expect(validatePriceDraft(draftOf(), KEO)).toBe(PRICE_MESSAGES.unchanged);
    // Typing a unit's current price again changes nothing.
    expect(validatePriceDraft(draftOf({ units: withUnit("day", 24000) }), KEO)).toBe(PRICE_MESSAGES.unchanged);
  });

  it("refuses a zero unit price and a unit wholesale above its retail", () => {
    expect(validatePriceDraft(draftOf({ units: withUnit("hop", 0) }), KEO)).toBe(PRICE_MESSAGES.unitPositive("hộp"));
    expect(validatePriceDraft(draftOf({ units: withUnit("day", 24000, 25000) }), KEO)).toBe(PRICE_MESSAGES.unitWholesaleAboveRetail("dây"));
  });

  it("checks the base prices first", () => {
    expect(validatePriceDraft(draftOf({ retail: null, units: withUnit("hop", 0) }), KEO)).toBe(PRICE_MESSAGES.retailPositive);
  });
});
