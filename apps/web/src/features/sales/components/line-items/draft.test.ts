import { describe, expect, it } from "vitest";
import { toQty } from "../../../../shared/domain/qty";
import type { MixGroup, SalesLine } from "../../schemas";
import { draftProblem, draftToInput, linesToDraft, newMixLine, type InputContext, type ItemDraft } from "./draft";

const saved = {
  id: "line-1",
  line_no: 1,
  sku_id: "sku-1",
  sku_code: "SP001",
  name: "Bánh quy bơ",
  unit_id: "unit-hop",
  qty: "1",
  base_qty: "1",
  color_id: null,
  spec_text: null,
  line_kind: "item",
  parent_line_id: null,
  mix_group_id: null,
  unit_price: "30000",
  discount_amount: "1000",
  price_source: { kind: "base_list" },
} as unknown as SalesLine;

const ctx: InputContext = { idOf: () => null, listPrice: () => "30000" as never, flavorPrice: () => null, groups: [] };

describe("saved line discounts", () => {
  it("sends the saved discount back unchanged when nothing was edited", () => {
    const draft = linesToDraft([saved], { codeOf: () => null } as never, []);
    expect((draft[0] as ItemDraft).discountPct).toBe("3.33");
    expect(draftToInput(draft, ctx)[0]?.discount_amount).toBe("1000");
  });

  it("recomputes the discount from the percent once the percent or the quantity changes", () => {
    const line = linesToDraft([saved], { codeOf: () => null } as never, [])[0] as ItemDraft;
    expect(draftToInput([{ ...line, discountPct: "5" }], ctx)[0]?.discount_amount).toBe("1500");
    expect(draftToInput([{ ...line, qty: "2" as never }], ctx)[0]?.discount_amount).toBe("1998");
  });
});

const GROUP: MixGroup = {
  id: "mix-1",
  name: "Nước ngọt",
  pack_unit_id: "unit-thung",
  units_per_pack: 24,
  part_unit_id: "unit-loc",
  units_per_part: 6,
  piece_unit_id: "unit-lon",
  status: "active",
  items: [
    { sku_id: "sku-cola", sku_code: "COLA", sku_name: "Cola lon", flavor_label: "Cola", active: true },
    { sku_id: "sku-cam", sku_code: "CAM", sku_name: "Cam lon", flavor_label: "Cam", active: true },
  ],
};
const UNIT_NAMES: Record<string, string> = { "unit-thung": "thùng", "unit-loc": "lốc", "unit-lon": "lon" };
const unitName = (id: string) => UNIT_NAMES[id] ?? "";

describe("mix pack problems", () => {
  it("refuses a part count that is not whole, naming the flavor and the part unit", () => {
    const line = { ...newMixLine(GROUP), packs: toQty(2), parts: { "sku-cola": toQty("3"), "sku-cam": toQty("1.5") } };
    expect(draftProblem([line], [GROUP], unitName)).toBe("Thùng mix Nước ngọt: số lốc vị Cam phải là số nguyên.");
  });

  it("accepts a short pack of whole parts", () => {
    const line = { ...newMixLine(GROUP), packs: toQty(2), parts: { "sku-cola": toQty(3), "sku-cam": toQty(2) } };
    expect(draftProblem([line], [GROUP], unitName)).toBeNull();
  });
});
