import { describe, expect, it } from "vitest";
import { ApiError } from "../../shared/api/api-error";
import { toMoney } from "../../shared/domain/money";
import { toQty } from "../../shared/domain/qty";
import { PROCUREMENT_MESSAGES } from "./api/adapters";
import {
  addScan,
  barcodeErrors,
  emptyBuyLine,
  lineFactor,
  lineUnitErrors,
  newProductCount,
  newProductLine,
  orderBuyLine,
  orderLinesRequest,
  overTolerance,
  receiptLinesRequest,
  scannedNewLine,
  sellChanges,
  withNewProductUnits,
  withSku,
  withUnit,
  type BuyLine,
} from "./buy-lines";
import { UNIT_MESSAGES, unitRow } from "../catalog";
import { UNIT_HOP as HOP, UNIT_THUNG as THUNG, testOrderLine, testSku as sku } from "./testing/fixtures";

const noColor = () => null;
const orderLine = (pendingBase: string) => testOrderLine({ pending_base_qty: pendingBase });

const unitIdByName = (name: string) => (name === "hộp" ? HOP : name === "thùng" ? THUNG : null);

const named = (line: BuyLine, name: string, price: number): BuyLine => ({ ...line, name, price: toMoney(price) });

describe("buy line rows", () => {
  it("starts a picked SKU in its purchase unit at the last purchase price of that unit", () => {
    const line = withSku(emptyBuyLine(), sku());
    expect(line.unitId).toBe(THUNG);
    expect(line.price).toBe("120000");
    expect(line.sell).toBe("15000");
    expect(withUnit(line, HOP).price).toBe("10000");
  });

  it("falls back to the reference cost when the SKU was never bought", () => {
    expect(withSku(emptyBuyLine(), sku({ last_purchase_price: null })).price).toBe("108000");
  });

  it("counts only named new products", () => {
    const lines = [newProductLine(unitIdByName), named(newProductLine(unitIdByName), "Kẹo dẻo", 1), emptyBuyLine()];
    expect(newProductCount(lines)).toBe(1);
  });
});

describe("orderLinesRequest", () => {
  it("refuses an order without rows", () => {
    expect(orderLinesRequest([emptyBuyLine()], noColor)).toEqual({ ok: false, message: PROCUREMENT_MESSAGES.orderEmpty });
  });

  it("asks a new product for its price", () => {
    const line = { ...newProductLine(unitIdByName), name: "Kẹo dẻo", price: toMoney(0) };
    expect(orderLinesRequest([line], noColor)).toEqual({ ok: false, message: "Kẹo dẻo: nhập giá nhập." });
  });

  it("moves a new product bought by a removed unit to its base unit, converting quantity and price", () => {
    const line = { ...named(newProductLine(unitIdByName), "Kẹo dẻo", 96000), qty: toQty(3) };
    expect(line.unitId).toBe(THUNG);
    const renamed = withNewProductUnits(line, line.units.map((u) => ({ ...u, factor: toQty(12) })));
    expect(renamed).toMatchObject({ unitId: THUNG, qty: "3", price: "96000" });
    expect(withNewProductUnits(line, [])).toMatchObject({ unitId: null, units: [], qty: "36", price: "8000" });
  });

  it("sends a new product bought by its thùng with its unit table and retail price", () => {
    const line = { ...named(newProductLine(unitIdByName), "Kẹo dẻo", 96000), qty: toQty(3), sell: toMoney(10000) };
    const built = orderLinesRequest([line], noColor);
    expect(built).toEqual({
      ok: true,
      keys: [line.key],
      lines: [
        {
          unit_id: THUNG,
          qty: "3",
          unit_price: "96000",
          discount_amount: "0",
          new_product: { name: "Kẹo dẻo", base_unit_id: HOP, units: [{ unit_id: THUNG, factor: "12" }], purchase_unit_id: THUNG, track_lot: true, retail_price: "10000" },
        },
      ],
    });
  });

  it("buys a new product in any unit of its table and sends that unit as the purchase unit", () => {
    const loc = unitRow({ unitId: "unit-loc", unitName: "lốc", factor: toQty(6), retail: toMoney(55000) });
    const base = named(newProductLine(unitIdByName), "Kẹo dẻo", 54000);
    const line = { ...base, units: [...base.units, loc], unitId: "unit-loc", qty: toQty(2) };
    expect(lineFactor(line).toString()).toBe("6");
    const built = orderLinesRequest([line], noColor);
    expect(built.ok && built.lines[0]).toMatchObject({
      unit_id: "unit-loc",
      qty: "2",
      new_product: {
        units: [
          { unit_id: THUNG, factor: "12" },
          { unit_id: "unit-loc", factor: "6", retail_price: "55000" },
        ],
        purchase_unit_id: "unit-loc",
      },
    });
  });

  it("sends a new product without conversion units in its base unit, with its wholesale price", () => {
    const line = { ...named(newProductLine(unitIdByName), "Kẹo dẻo", 9000), units: [], unitId: null, sell: toMoney(10000), wholesale: toMoney(9500) };
    const built = orderLinesRequest([line], noColor);
    expect(built.ok && built.lines[0]).toEqual({
      unit_id: HOP,
      qty: "1",
      unit_price: "9000",
      discount_amount: "0",
      new_product: { name: "Kẹo dẻo", base_unit_id: HOP, track_lot: true, retail_price: "10000", wholesale_price: "9500" },
    });
  });

  it("opens the unit table of a new product with errors, marks them and refuses with the first", () => {
    const base = named(newProductLine(unitIdByName), "Kẹo dẻo", 96000);
    const bad = { ...base, units: [...base.units, unitRow({ unitId: THUNG, unitName: "thùng", factor: toQty(0) })] };
    const unused = { ...newProductLine(unitIdByName), qty: toQty(0), units: [unitRow()] };
    expect(lineUnitErrors(bad)).toEqual({ "units.1.unit": UNIT_MESSAGES.duplicate("thùng"), "units.1.factor": UNIT_MESSAGES.factor });
    for (const build of [orderLinesRequest, receiptLinesRequest]) {
      const refused = build([bad, unused], noColor);
      expect(refused).toMatchObject({ ok: false, message: UNIT_MESSAGES.duplicate("thùng") + UNIT_MESSAGES.more(1) });
      const rows = refused.ok ? [] : (refused.rows ?? []);
      expect(rows.map((l) => [l.showUnits, l.unitsChecked])).toEqual([
        [true, true],
        [false, false],
      ]);
    }
  });

  it("leaves the base unit of a new product to the row's own check", () => {
    const line = { ...named(newProductLine(unitIdByName), "Kẹo dẻo", 96000), baseUnitId: null };
    expect(lineUnitErrors(line)).toEqual({});
    expect(orderLinesRequest([line], noColor)).toEqual({ ok: false, message: PROCUREMENT_MESSAGES.newProductUnit("Kẹo dẻo") });
  });

  it("sends an existing SKU with its unit, color and spec", () => {
    const line = { ...withSku(emptyBuyLine(), sku()), qty: toQty(2), color: "do" as const, spec: " 24 gói " };
    const built = orderLinesRequest([line], () => "color-do");
    expect(built).toEqual({
      ok: true,
      keys: [line.key],
      lines: [{ sku_id: "sku-1", unit_id: THUNG, qty: "2", unit_price: "120000", discount_amount: "0", color_id: "color-do", spec_text: "24 gói" }],
    });
  });
});

describe("receiptLinesRequest", () => {
  it("refuses a receipt without quantities", () => {
    expect(receiptLinesRequest([{ ...emptyBuyLine(), qty: null }], noColor)).toEqual({ ok: false, message: PROCUREMENT_MESSAGES.receiptEmpty });
  });

  it("asks a lot-tracked SKU for its lot and expiry", () => {
    const tracked = withSku(emptyBuyLine(), sku({ track_lot: true, track_expiry: true }));
    expect(receiptLinesRequest([tracked], noColor)).toEqual({ ok: false, message: "Bánh quy bơ: bắt buộc nhập số lô." });
    expect(receiptLinesRequest([{ ...tracked, lotNo: "L01" }], noColor)).toEqual({ ok: false, message: "Bánh quy bơ: bắt buộc nhập hạn dùng." });
    const built = receiptLinesRequest([{ ...tracked, lotNo: " L01 ", expiry: "2027-06-30" }], noColor);
    expect(built.ok && built.lines[0]).toMatchObject({ sku_id: "sku-1", lot_no: "L01", expiry_date: "2027-06-30" });
  });

  it("does not send a lot for a SKU that does not track lots", () => {
    const built = receiptLinesRequest([{ ...withSku(emptyBuyLine(), sku()), lotNo: "L01" }], noColor);
    expect(built.ok && built.lines[0]).not.toHaveProperty("lot_no");
  });

  it("receives an ordered line in the order unit and splits the part over pending as excess", () => {
    const line = { ...orderBuyLine(orderLine("24"), sku(), null), qty: toQty("25.2") };
    expect(overTolerance(line)).toBe(false);
    expect(receiptLinesRequest([line], noColor)).toEqual({
      ok: true,
      keys: [line.key, line.key],
      lines: [
        { purchase_order_line_id: "pol-1", qty: "2" },
        { purchase_order_line_id: "pol-1", qty: "0.1", condition: "excess" },
      ],
    });
  });

  it("blocks receiving more than 5% over what is pending", () => {
    const line = { ...orderBuyLine(orderLine("24"), sku(), null), qty: toQty(26) };
    expect(overTolerance(line)).toBe(true);
    expect(receiptLinesRequest([line], noColor)).toEqual({ ok: false, message: PROCUREMENT_MESSAGES.overTolerance("Bánh quy bơ", "26", "24") });
  });

  it("refuses a base quantity that does not convert to the order unit", () => {
    const line = { ...orderBuyLine(orderLine("24"), sku(), null), qty: toQty(5) };
    expect(receiptLinesRequest([line], noColor)).toEqual({ ok: false, message: "Bánh quy bơ: số lượng nhận không quy đổi đúng ra thùng của đơn nhập." });
  });
});

describe("new products with a barcode", () => {
  const scanned = (code: string, name: string): BuyLine => ({ ...named(scannedNewLine(code, unitIdByName), name, 9000), lotNo: "L01", expiry: "2027-06-30" });

  it("opens a scanned code as a new product bought by its base unit, the code as its barcode", () => {
    const line = scannedNewLine(" 8936000111222 ", unitIdByName);
    expect(line).toMatchObject({ isNew: true, barcode: "8936000111222", baseUnitId: HOP, unitId: null, trackLot: true, qty: "1" });
    expect(line.units).toMatchObject([{ unitId: THUNG, unitName: "thùng", factor: "12" }]);
  });

  it("sends the trimmed barcode of a new product, and none when it is blank", () => {
    const built = orderLinesRequest([{ ...scanned("8936000111222", "Kẹo dẻo"), barcode: " 8936000111222 " }, scanned("  ", "Kẹo gừng")], noColor);
    expect(built.ok && built.lines.map((l) => l.new_product)).toEqual([
      { name: "Kẹo dẻo", base_unit_id: HOP, units: [{ unit_id: THUNG, factor: "12" }], purchase_unit_id: HOP, track_lot: true, barcode: "8936000111222" },
      { name: "Kẹo gừng", base_unit_id: HOP, units: [{ unit_id: THUNG, factor: "12" }], purchase_unit_id: HOP, track_lot: true },
    ]);
  });

  it("refuses two new products with one barcode before sending", () => {
    const lines = [scanned("8936000111222", "Kẹo dẻo"), scanned("8936000111222 ", "Kẹo gừng")];
    const refusal = { ok: false, message: "Mã vạch “8936000111222” bị trùng ở hai dòng hàng mới." };
    expect(orderLinesRequest(lines, noColor)).toEqual(refusal);
    expect(receiptLinesRequest(lines, noColor)).toEqual(refusal);
  });

  it("leaves forms of one UPC to the server", () => {
    expect(orderLinesRequest([scanned("012345678905", "Kẹo dẻo"), scanned("0012345678905", "Kẹo gừng")], noColor).ok).toBe(true);
  });

  it("places a server barcode refusal on the row that sent the refused line", () => {
    const lines = [withSku(emptyBuyLine(), sku()), scanned("012345678905", "Kẹo dẻo"), scanned("0012345678905", "Kẹo gừng")];
    const sent = orderLinesRequest(lines, noColor);
    if (!sent.ok) throw new Error(sent.message);
    const write = { lines: sent, before: null };
    const taken = new ApiError({ status: 409, code: "barcode_taken", message: "Mã vạch đã được dùng cho hàng khác.", fields: { "lines.2.new_product.barcode": "barcode_taken" } });
    expect([...barcodeErrors(taken, write)]).toEqual([[lines[2]?.key, { code: "0012345678905", message: "Mã vạch đã được dùng cho hàng khác." }]]);
    const format = new ApiError({ status: 422, code: "validation_failed", message: "x", fields: { "lines.1.new_product.barcode": "invalid_format" } });
    expect([...barcodeErrors(format, write)]).toEqual([[lines[1]?.key, { code: "012345678905", message: "Sai định dạng." }]]);
    expect(barcodeErrors(taken, null).size).toBe(0);
  });

  it("does not place the error of an earlier write on the lines sent after it", () => {
    const lines = [scanned("012345678905", "Kẹo dẻo")];
    const sent = orderLinesRequest(lines, noColor);
    if (!sent.ok) throw new Error(sent.message);
    const earlier = new ApiError({ status: 409, code: "barcode_taken", message: "Mã vạch đã được dùng cho hàng khác.", fields: { "lines.0.new_product.barcode": "barcode_taken" } });
    expect(barcodeErrors(earlier, { lines: sent, before: earlier }).size).toBe(0);
  });
});

describe("addScan", () => {
  const opts = { fixed: false, unitIdByName };
  const found = (unitId: string, factor: string, over: Parameters<typeof sku>[0] = {}) => ({ sku: sku(over), unit_id: unitId, factor });

  it("puts a known code in the empty last row, then adds one to that row of the same unit", () => {
    const first = addScan([emptyBuyLine()], "SP001", found(HOP, "1"), opts);
    expect(first.outcome).toEqual({ outcome: "added", skuId: "sku-1", unitId: HOP });
    expect(first.lines).toHaveLength(1);
    expect(first.lines?.[0]).toMatchObject({ unitId: HOP, qty: "1", price: "10000" });
    const again = addScan(first.lines ?? [], "SP001", found(HOP, "1"), opts);
    expect(again.lines?.map((l) => l.qty)).toEqual(["2"]);
    const carton = addScan(again.lines ?? [], "8936000999999", found(THUNG, "12"), opts);
    expect(carton.lines?.map((l) => [l.unitId, l.qty])).toEqual([
      [HOP, "2"],
      [THUNG, "1"],
    ]);
  });

  it("opens an unknown code as a new product and counts it again on the next scan", () => {
    const opened = addScan([emptyBuyLine()], "8936000111222", null, opts);
    expect(opened.outcome).toEqual({ outcome: "new_product" });
    expect(opened.lines).toHaveLength(1);
    expect(opened.created).toBe(opened.lines?.[0]?.key);
    const again = addScan(opened.lines ?? [], " 8936000111222", null, opts);
    expect(again).toMatchObject({ outcome: { outcome: "new_product" } });
    expect(again.lines?.map((l) => l.qty)).toEqual(["2"]);
    expect(again.created).toBeUndefined();
  });

  it("leaves a new product bought by a larger unit to the desktop", () => {
    const line = { ...scannedNewLine("8936000111222", unitIdByName), unitId: THUNG };
    expect(addScan([line], "8936000111222", null, opts)).toEqual({ lines: null, outcome: { outcome: "needs_desktop_edit" } });
  });

  it("does not buy a combo", () => {
    const step = addScan([emptyBuyLine()], "SP009", found(HOP, "1", { product_type: "combo", display_name: "Hộp quà" }), opts);
    expect(step).toEqual({ lines: null, outcome: { outcome: "not_found" }, notice: PROCUREMENT_MESSAGES.scanNotBuyable("SP009", "Hộp quà") });
  });

  it("receiving an order, counts the code's base units on the ordered row and nothing for a code outside it", () => {
    const fixed = { fixed: true, unitIdByName };
    const ordered = orderBuyLine(orderLine("24"), null, null);
    expect(ordered).toMatchObject({ qty: "24", scanned: false });
    // The first scan replaces the prefilled pending quantity; the next one adds.
    const step = addScan([ordered], "8936000999999", found(THUNG, "12"), fixed);
    expect(step.outcome).toEqual({ outcome: "added", skuId: "sku-1", unitId: THUNG });
    expect(step.lines?.map((l) => [l.qty, l.scanned])).toEqual([["12", true]]);
    const again = addScan(step.lines ?? [], "SP001", found(HOP, "1"), fixed);
    expect(again.lines?.map((l) => l.qty)).toEqual(["13"]);
    const outside = { lines: null, outcome: { outcome: "not_in_order" }, notice: "Mã “SP002” không có trong đơn nhập này." };
    expect(addScan([ordered], "SP002", found(HOP, "1", { id: "sku-2" }), fixed)).toEqual(outside);
    expect(addScan([ordered], " SP002 ", null, fixed)).toEqual(outside);
  });

  it("receiving an order, does not count a typed quantity as a scan", () => {
    const fixed = { fixed: true, unitIdByName };
    const typed = { ...orderBuyLine(orderLine("24"), null, null), qty: toQty(5) };
    expect(addScan([typed], "SP001", found(HOP, "1"), fixed).lines?.map((l) => l.qty)).toEqual(["1"]);
  });

  it("receiving an order, fills the rows of one SKU in order, each up to what is pending, then adds to the first", () => {
    const fixed = { fixed: true, unitIdByName };
    const rows = [
      orderBuyLine(testOrderLine({ id: "pol-1", pending_base_qty: "2" }), null, "do"),
      orderBuyLine(testOrderLine({ id: "pol-2", sku_id: "sku-2", pending_base_qty: "5" }), null, null),
      orderBuyLine(testOrderLine({ id: "pol-3", pending_base_qty: "2" }), null, "xanh"),
    ];
    let lines: BuyLine[] = rows;
    const qtys: string[][] = [];
    for (let n = 0; n < 5; n++) {
      lines = addScan(lines, "SP001", found(HOP, "1"), fixed).lines ?? lines;
      qtys.push(lines.map((l) => l.qty ?? ""));
    }
    expect(qtys).toEqual([
      ["1", "5", "2"],
      ["2", "5", "2"],
      ["2", "5", "1"],
      ["2", "5", "2"],
      ["3", "5", "2"],
    ]);
    expect(lines.map((l) => l.scanned)).toEqual([true, false, true]);
  });
});

describe("sellChanges", () => {
  it("keeps one changed retail price per SKU, the last row winning", () => {
    const base = withSku(emptyBuyLine(), sku());
    const changes = sellChanges([
      { ...base, sell: toMoney(16000) },
      { ...base, sell: toMoney(17000) },
      { ...withSku(emptyBuyLine(), sku({ id: "sku-2" })), sell: toMoney(15000) },
    ]);
    expect(changes.map((c) => [c.sku.id, c.retail])).toEqual([["sku-1", "17000"]]);
  });
});
