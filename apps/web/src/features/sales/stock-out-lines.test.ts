import { describe, expect, it } from "vitest";
import type { InvoiceLine } from "./schemas";
import { deliverBaseQty, deliverRows, type DeliverRow, type WholeRow } from "./stock-out-lines";

function line(over: Partial<Record<keyof InvoiceLine, unknown>>): InvoiceLine {
  return {
    id: "line-1",
    line_no: 1,
    sku_id: "sku-1",
    sku_code: "SP001",
    name: "Bánh quy bơ",
    variant_label: "",
    unit_id: "unit-thung",
    unit_name: "Thùng",
    unit_factor: "24",
    qty: "1",
    base_qty: "24",
    color_id: null,
    spec_text: null,
    line_kind: "item",
    parent_line_id: null,
    mix_key: null,
    mix_group_id: null,
    unit_price: "1200000",
    discount_amount: "0",
    line_amount: "1200000",
    min_price: null,
    price_source: { kind: "base_list", price_list_name: null, color_id: null },
    reserved_base_qty: "10",
    backorder_base_qty: "14",
    delivered_base_qty: "0",
    cancelled_base_qty: "0",
    ...over,
  } as InvoiceLine;
}

/** The single row of an invoice made of one line. */
function rowOf(over: Partial<Record<keyof InvoiceLine, unknown>> = {}): DeliverRow {
  const [row] = deliverRows({ lines: [line(over)] });
  if (row?.kind !== "line") throw new Error("no line row to deliver");
  return row;
}

/** The single row of an invoice made of one mix pack or combo header and its parts. */
function wholeOf(lines: InvoiceLine[]): WholeRow {
  const rows = deliverRows({ lines });
  if (rows.length !== 1 || rows[0]?.kind !== "whole") throw new Error("expected one whole row");
  return rows[0];
}

/** 2 thùng mix of 24 lon: 3 lốc of cam and 5 lốc of dâu, 6 lon a lốc. */
const MIX = [
  line({
    id: "mix",
    line_kind: "mix_pack",
    sku_id: null,
    name: "Thùng mix nước ngọt",
    unit_id: "unit-thung",
    unit_name: "Thùng",
    unit_factor: "1",
    qty: "2",
    base_qty: "2",
    reserved_base_qty: "0",
    backorder_base_qty: "0",
  }),
  line({
    id: "cam",
    line_kind: "mix_flavor",
    parent_line_id: "mix",
    sku_id: "sku-cam",
    name: "Nước cam",
    unit_name: "Lốc",
    unit_factor: "6",
    qty: "3",
    base_qty: "18",
    reserved_base_qty: "18",
    backorder_base_qty: "0",
  }),
  line({
    id: "dau",
    line_kind: "mix_flavor",
    parent_line_id: "mix",
    sku_id: "sku-dau",
    name: "Nước dâu",
    unit_name: "Lốc",
    unit_factor: "6",
    qty: "5",
    base_qty: "30",
    reserved_base_qty: "30",
    backorder_base_qty: "0",
  }),
];

/** One combo of a box of biscuits and two packs of candy. */
const COMBO = [
  line({
    id: "combo",
    line_kind: "combo",
    sku_id: null,
    name: "Combo quà Tết",
    unit_name: "Combo",
    unit_factor: "1",
    qty: "1",
    base_qty: "1",
    reserved_base_qty: "0",
    backorder_base_qty: "0",
  }),
  line({
    id: "banh",
    line_kind: "combo_component",
    parent_line_id: "combo",
    sku_id: "sku-banh",
    name: "Bánh quy bơ",
    unit_name: "Hộp",
    unit_factor: "1",
    qty: "1",
    base_qty: "1",
    reserved_base_qty: "1",
    backorder_base_qty: "0",
  }),
  line({
    id: "keo",
    line_kind: "combo_component",
    parent_line_id: "combo",
    sku_id: "sku-keo",
    name: "Kẹo dừa",
    unit_name: "Gói",
    unit_factor: "1",
    qty: "2",
    base_qty: "2",
    reserved_base_qty: "2",
    backorder_base_qty: "0",
  }),
];

const patch = (lines: InvoiceLine[], id: string, over: Partial<Record<keyof InvoiceLine, unknown>>) => lines.map((l) => (l.id === id ? line({ ...l, ...over }) : l));

describe("stock-out quantities", () => {
  it("offers a part of a carton rounded down to four decimals and delivers exactly what is reserved", () => {
    const row = rowOf();
    expect(row.max).toBe("0.4166");
    expect(row.cap).toBe("10");
    // The offered maximum maps back to the reserved pieces, not 9.9984 or a 40-digit product.
    expect(deliverBaseQty(row, "0.4166")).toBe("10");
  });

  it("rounds a smaller quantity to four decimals and refuses one above what is reserved", () => {
    const row = rowOf();
    expect(deliverBaseQty(row, "0.25")).toBe("6");
    expect(deliverBaseQty(row, "0.3333")).toBe("7.9992");
    expect(deliverBaseQty(row, "0.42")).toBeNull();
  });

  it("caps by what is left to deliver when more is reserved", () => {
    const row = rowOf({ reserved_base_qty: "24", delivered_base_qty: "12" });
    expect(row.cap).toBe("12");
    expect(row.max).toBe("0.5");
    expect(deliverBaseQty(row, "0.5")).toBe("12");
  });
});

describe("mix packs and combos", () => {
  it("delivers a mix pack whole through its header and never offers a flavor on its own", () => {
    const row = wholeOf(MIX);
    expect(row.line.id).toBe("mix");
    expect(row.cap).toBe("2");
    expect(row.qty).toBe("2");
    expect(row.parts.map((p) => [p.line.id, p.remaining, p.reserved])).toEqual([
      ["cam", "3", "3"],
      ["dau", "5", "5"],
    ]);
    expect(row.short).toEqual([]);
  });

  it("lists the flavors holding less than they still owe", () => {
    const row = wholeOf(patch(MIX, "dau", { reserved_base_qty: "24", backorder_base_qty: "6" }));
    expect(row.short.map((p) => [p.line.id, p.remaining, p.reserved])).toEqual([["dau", "5", "4"]]);
  });

  it("counts a flavor holding more than it still owes as short, as the server does", () => {
    const row = wholeOf(patch(MIX, "cam", { reserved_base_qty: "24" }));
    expect(row.short.map((p) => p.line.id)).toEqual(["cam"]);
  });

  it("offers what is left of a partly delivered mix pack and skips a flavor with nothing left", () => {
    const lines = patch(patch(MIX, "mix", { delivered_base_qty: "1" }), "cam", { delivered_base_qty: "18", reserved_base_qty: "0" });
    const row = wholeOf(lines);
    expect(row.cap).toBe("1");
    expect(row.parts.map((p) => p.line.id)).toEqual(["dau"]);
    expect(row.short).toEqual([]);
  });

  it("has no row for a header delivered or cancelled in full", () => {
    expect(deliverRows({ lines: patch(MIX, "mix", { cancelled_base_qty: "2" }) })).toEqual([]);
    expect(deliverRows({ lines: patch(MIX, "mix", { delivered_base_qty: "2" }) })).toEqual([]);
  });

  it("delivers a combo whole and names the component short of stock", () => {
    expect(wholeOf(COMBO).parts.map((p) => [p.line.id, p.remaining])).toEqual([
      ["banh", "1"],
      ["keo", "2"],
    ]);
    const row = wholeOf(patch(COMBO, "keo", { reserved_base_qty: "1", backorder_base_qty: "1" }));
    expect(row.cap).toBe("1");
    expect(row.short.map((p) => [p.line.id, p.remaining, p.reserved])).toEqual([["keo", "2", "1"]]);
  });

  it("keeps ordinary lines beside a mix pack as their own rows", () => {
    const rows = deliverRows({ lines: [line({}), ...MIX] });
    expect(rows.map((r) => [r.kind, r.line.id])).toEqual([
      ["line", "line-1"],
      ["whole", "mix"],
    ]);
  });
});
