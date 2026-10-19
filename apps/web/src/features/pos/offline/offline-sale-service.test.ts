import { beforeEach, describe, expect, it } from "vitest";
import { toMoney } from "../../../shared/domain/money";
import { toQty } from "../../../shared/domain/qty";
import { newItemLine } from "../../sales/components/line-items/draft";
import { OFFLINE_MESSAGES, OfflineSaleError, buildOfflineSale, priceOffline, recordOfflineSale, type OfflineSaleInput } from "./offline-sale-service";
import { addToOutbox, listOutbox, markOutboxBlocked, markOutboxException } from "./outbox-repository";
import { resetOfflineDb } from "./testing/fake-offline-db";
import { CASH_METHOD_ID, OFFLINE_METHODS, OFFLINE_TENANT_ID, newOutboxEntry, testBundle } from "./testing/handlers";
import { isUuidv7, uuidv7Time } from "./uuidv7";

const NOW = new Date("2026-10-18T09:00:00.000Z");

const keo = (qty: number, init: Parameters<typeof newItemLine>[0] = {}) => newItemLine({ skuId: "sku-keo", skuLabel: "KEO-DUA · Kẹo dừa", unitId: "unit-goi", qty: toQty(qty), ...init });
const mut = (qty: number) => newItemLine({ skuId: "sku-mut", skuLabel: "MUT-GUNG · Mứt gừng", unitId: "unit-hop", qty: toQty(qty) });

function input(init: Partial<OfflineSaleInput> = {}): OfflineSaleInput {
  return { bundle: testBundle(), lines: [keo(2)], customerId: null, cash: "100000", bank: "", credit: false, idOf: () => null, now: NOW, ...init };
}

function refusal(init: Partial<OfflineSaleInput>): string {
  try {
    buildOfflineSale(input(init));
  } catch (e) {
    if (e instanceof OfflineSaleError) return e.message;
    throw e;
  }
  throw new Error("the sale was accepted");
}

beforeEach(resetOfflineDb);

describe("priceOffline", () => {
  it("prices lines from the tile's retail price, with the line discount in money", () => {
    const lines = [keo(3, { discountPct: "10" }), mut(1)];
    const p = priceOffline(lines, testBundle().tiles);
    expect(p.complete).toBe(true);
    expect(p.goods).toBe("270000");
    expect(p.discount).toBe("15000");
    expect(p.total).toBe("255000");
  });

  it("says whether the lines rounded one by one add up to the exact total rounded once", () => {
    expect(priceOffline([keo(3, { discountPct: "10" }), mut(1)], testBundle().tiles).roundsAlike).toBe(true);
    // 10 000,5 đ a pack: each line rounds up to 10 001 đ, while the exact total is 20 001 đ.
    const halves = testBundle().tiles.map((t) => (t.sku_id === "sku-keo" ? { ...t, retail_price: toMoney("10000.5") } : t));
    const p = priceOffline([keo(1), keo(1)], halves);
    expect(p.total).toBe("20002");
    expect(p.roundsAlike).toBe(false);
    expect(priceOffline([keo(2)], halves).roundsAlike).toBe(true);
  });

  it("leaves a line in another unit than the tile's unpriced", () => {
    const p = priceOffline([keo(1, { unitId: "unit-thung" })], testBundle().tiles);
    expect(p.complete).toBe(false);
    expect(p.total).toBe("0");
  });
});

describe("buildOfflineSale", () => {
  it("builds a cash sale with a UUIDv7 id, the catalog price and the change in cash tendered", () => {
    const built = buildOfflineSale(input({ cash: "150000" }));
    expect(isUuidv7(built.sale.offline_client_id)).toBe(true);
    expect(uuidv7Time(built.sale.offline_client_id)).toBe(NOW.getTime());
    expect(built.sale).toMatchObject({
      created_at: NOW.toISOString(),
      lines: [{ sku_id: "sku-keo", unit_id: "unit-goi", qty: "2", unit_price: "50000" }],
      payments: [{ payment_method_id: CASH_METHOD_ID, amount: "100000" }],
      cash_tendered_amount: "150000",
    });
    expect(built.total).toBe("100000");
    expect(built.lines).toEqual([{ name: "Kẹo dừa", qty: "2", unit_name: "Gói", amount: "100000" }]);
    expect(built.sale).not.toHaveProperty("customer_party_id");
  });

  it("refuses a sale to a chosen customer, whose own prices are not on the device", () => {
    expect(refusal({ customerId: "cus-1" })).toBe(OFFLINE_MESSAGES.customer);
  });

  it("refuses when the store's prices exclude tax, since the device has no tax rates", () => {
    expect(refusal({ bundle: testBundle({ settings: { ...testBundle().settings, prices_include_tax: false } }) })).toBe(OFFLINE_MESSAGES.taxExcluded);
  });

  it("refuses a total the server could round differently", () => {
    const halves = testBundle({ tiles: testBundle().tiles.map((t) => (t.sku_id === "sku-keo" ? { ...t, retail_price: toMoney("10000.5") } : t)) });
    expect(refusal({ bundle: halves, lines: [keo(1), keo(1)], cash: "20002" })).toBe(OFFLINE_MESSAGES.rounding);
  });

  it("refuses paying less than the total", () => {
    expect(refusal({ cash: "99999" })).toBe(OFFLINE_MESSAGES.cashOnly);
  });

  it("refuses transfer and credit", () => {
    expect(refusal({ cash: "50000", bank: "50000" })).toBe(OFFLINE_MESSAGES.cashOnly);
    expect(refusal({ credit: true })).toBe(OFFLINE_MESSAGES.cashOnly);
  });

  it("refuses a sale over the cap on its own", () => {
    expect(refusal({ lines: [mut(17)], cash: "2040000" })).toBe(OFFLINE_MESSAGES.overCap);
  });

  it("refuses selling from an expired catalog", () => {
    expect(refusal({ now: new Date("2026-10-18T20:00:00.001Z") })).toBe(OFFLINE_MESSAGES.expired);
  });

  it("refuses without a catalog or when the store does not sell offline", () => {
    expect(refusal({ bundle: null })).toBe(OFFLINE_MESSAGES.noBundle);
    expect(refusal({ bundle: testBundle({ settings: { ...testBundle().settings, offline_pos_enabled: false } }) })).toBe(OFFLINE_MESSAGES.disabled);
  });

  it("refuses an empty or incomplete cart and a product without a catalog price", () => {
    expect(refusal({ lines: [] })).toBe(OFFLINE_MESSAGES.incomplete);
    expect(refusal({ lines: [keo(2), newItemLine()] })).toBe(OFFLINE_MESSAGES.incomplete);
    const unpriced = testBundle({ tiles: testBundle().tiles.map((t) => ({ ...t, retail_price: null })) });
    expect(refusal({ bundle: unpriced })).toBe(OFFLINE_MESSAGES.unpriced);
  });

  it("refuses when the store has no active cash method", () => {
    const noCash = testBundle({ payment_methods: OFFLINE_METHODS.filter((m) => !m.cash) });
    expect(refusal({ bundle: noCash })).toBe("Chưa có hình thức tiền mặt đang dùng. Gửi yêu cầu hỗ trợ ở Thiết lập › Thuê bao để bật lại.");
  });
});

describe("recordOfflineSale", () => {
  it("stores the sale exactly as built, numbered and pending", async () => {
    const entry = await recordOfflineSale(input());
    expect(entry).toMatchObject({ label: "OFF-001", status: "pending", tenant_id: OFFLINE_TENANT_ID, bundle_version: '"boot-1"', total: "100000" });
    const [stored] = await listOutbox(OFFLINE_TENANT_ID);
    expect(stored?.sale).toEqual(entry.sale);
  });

  it("refuses a sale that takes the unposted sales of the device past the cap, waiting, parked or blocked", async () => {
    // 2 000 000 đ cap: 1 900 000 đ already on the device, of which one sale is parked and one blocked.
    await addToOutbox(newOutboxEntry(1, { total: toMoney("1500000") }));
    const parked = await addToOutbox(newOutboxEntry(2, { total: toMoney("200000") }));
    const blocked = await addToOutbox(newOutboxEntry(3, { total: toMoney("200000") }));
    await markOutboxException(parked.offline_client_id, { id: "sx-1", error_code: "sku_inactive", error_detail: null });
    await markOutboxBlocked(blocked.offline_client_id, { id: null, error_code: "validation_failed", error_detail: null });

    // Exactly at the cap is still allowed.
    await recordOfflineSale(input());
    await expect(recordOfflineSale(input())).rejects.toThrow(OFFLINE_MESSAGES.overCap);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toHaveLength(4);
  });

  it("counts only the signed-in tenant's sales against its cap", async () => {
    await addToOutbox(newOutboxEntry(1, { tenant_id: "other-tenant", total: toMoney("1950000") }));
    await expect(recordOfflineSale(input())).resolves.toMatchObject({ label: "OFF-001" });
  });

  it("stores nothing when a rule refuses the sale", async () => {
    await expect(recordOfflineSale(input({ credit: true }))).rejects.toThrow(OFFLINE_MESSAGES.cashOnly);
    expect(await listOutbox(OFFLINE_TENANT_ID)).toEqual([]);
  });
});
