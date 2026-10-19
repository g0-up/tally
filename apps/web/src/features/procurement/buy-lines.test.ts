import { describe, expect, it } from "vitest";
import { toMoney } from "../../shared/domain/money";
import { toQty } from "../../shared/domain/qty";
import { PROCUREMENT_MESSAGES } from "./api/adapters";
import {
  emptyBuyLine,
  newProductCount,
  newProductLine,
  orderBuyLine,
  orderLinesRequest,
  overTolerance,
  receiptLinesRequest,
  sellChanges,
  withSku,
  withUnit,
  type BuyLine,
} from "./buy-lines";
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

  it("sends a new product bought by the big unit with its conversion and retail price", () => {
    const line = { ...named(newProductLine(unitIdByName), "Kẹo dẻo", 96000), qty: toQty(3), sell: toMoney(10000) };
    const built = orderLinesRequest([line], noColor);
    expect(built).toEqual({
      ok: true,
      lines: [
        {
          unit_id: THUNG,
          qty: "3",
          unit_price: "96000",
          discount_amount: "0",
          new_product: { name: "Kẹo dẻo", base_unit_id: HOP, purchase_unit_id: THUNG, factor: "12", track_lot: true, retail_price: "10000" },
        },
      ],
    });
  });

  it("sends an existing SKU with its unit, color and spec", () => {
    const line = { ...withSku(emptyBuyLine(), sku()), qty: toQty(2), color: "do" as const, spec: " 24 gói " };
    const built = orderLinesRequest([line], () => "color-do");
    expect(built).toEqual({
      ok: true,
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
