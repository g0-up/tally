import { describe, expect, it } from "vitest";
import { matchCode, type CodeCandidate } from "./barcode";

const keo: CodeCandidate = { skuCode: "KEO-DUA", baseUnitId: "goi", barcodes: [{ code: "8935000000017", unitId: "goi" }] };
const mut: CodeCandidate = {
  skuCode: "8935000000017",
  baseUnitId: "hop",
  barcodes: [
    { code: "8935000000024", unitId: "hop" },
    { code: "TH-MUT-24", unitId: "thung" },
  ],
};

describe("matchCode", () => {
  it("matches a barcode exactly and answers its unit", () => {
    expect(matchCode("8935000000024", [keo, mut])).toEqual({ item: mut, unitId: "hop", matchedBy: "barcode" });
    expect(matchCode("TH-MUT-24", [keo, mut])).toEqual({ item: mut, unitId: "thung", matchedBy: "barcode" });
  });

  it("matches a SKU code in the base unit", () => {
    expect(matchCode("KEO-DUA", [keo, mut])).toEqual({ item: keo, unitId: "goi", matchedBy: "sku_code" });
  });

  it("lets a barcode win over the SKU code of another SKU", () => {
    expect(matchCode("8935000000017", [mut, keo])).toEqual({ item: keo, unitId: "goi", matchedBy: "barcode" });
  });

  it("ignores case and surrounding spaces", () => {
    expect(matchCode("  keo-dua ", [keo, mut])?.item).toBe(keo);
    expect(matchCode("th-mut-24", [keo, mut])?.unitId).toBe("thung");
  });

  it("matches nothing for a part of a code, another form of a barcode or a blank code", () => {
    expect(matchCode("KEO", [keo, mut])).toBeNull();
    expect(matchCode("08935000000024", [keo, mut])).toBeNull();
    expect(matchCode("  ", [keo, mut])).toBeNull();
  });
});
