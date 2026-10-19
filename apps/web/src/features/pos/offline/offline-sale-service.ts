import { toMoney, type Money } from "../../../shared/domain/money";
import { Decimal, dec, sumDec } from "../../../shared/lib/decimal";
import type { ColorMaps } from "../../sales/api/queries";
import { discountAmount, draftToInput, isPriceable, keptDiscount, type ItemDraft } from "../../sales/components/line-items/draft";
import type { PosTile } from "../../sales/schemas";
import { cartLineName, checkoutPayments, paymentFigures } from "../api/adapters";
import { bundleFresh } from "./offline-bundle-repository";
import type { OfflineBundle, OfflineSaleBody, OutboxEntry, OutboxLineView } from "./offline-db";
import { OutboxCapError, addToOutbox } from "./outbox-repository";
import { uuidv7 } from "./uuidv7";

// A sale made without the server. The device never posts to the ledger: it checks the offline
// rules (cash only, paid in full, within the store's caps, from a catalog still in date), prices
// the cart from the saved catalog exactly as it shows it, and stores the intent in the outbox under
// a new UUIDv7. The server checks everything again when the sale syncs, and posts it only when the
// cash taken equals its own total, so the device sells only what it can price to the đồng:
//   - retail price for a walk-in customer (a customer's own price list is not on the device);
//   - prices that include tax (the tax rate of each item is not on the device either);
//   - totals that round the same per line and per document (the store's mode is not on it).
// Automatic promotions are not on the device: a sale one would discount is parked on sync.

export const OFFLINE_MESSAGES = {
  cashOnly: "Ngoại tuyến chỉ cho bán thu đủ tiền mặt.",
  overCap: "Vượt hạn mức bán ngoại tuyến. Kết nối mạng để tiếp tục.",
  expired: "Dữ liệu ngoại tuyến đã quá hạn. Kết nối mạng để tải lại.",
  disabled: "Cửa hàng chưa bật bán ngoại tuyến. Kết nối mạng để bán.",
  noBundle: "Máy này chưa tải dữ liệu bán ngoại tuyến. Kết nối mạng để bán.",
  unpriced: "Có hàng chưa có giá trong dữ liệu ngoại tuyến. Ngoại tuyến chỉ bán theo đơn vị và giá bán lẻ của ô hàng.",
  incomplete: "Nhập số lượng và đơn vị cho mọi dòng hàng.",
  taxExcluded: "Cửa hàng đang tính giá chưa gồm thuế; máy không có thuế suất để tính đúng tổng tiền nên không bán ngoại tuyến được. Kết nối mạng để bán.",
  customer: "Ngoại tuyến chỉ bán giá lẻ cho khách lẻ: bảng giá riêng của khách chưa có trên máy. Bỏ chọn khách để bán giá lẻ, hoặc chờ có mạng.",
  rounding: "Tổng tiền có số lẻ đồng nên máy không chắc làm tròn giống máy chủ. Sửa số lượng hoặc chiết khấu cho tròn đồng, hoặc chờ có mạng.",
  uncertain: "Giao dịch trước của giỏ này có thể đã ghi sổ. Chờ có mạng để kiểm tra trước khi bán lại.",
} as const;

/** A sale the offline rules refuse; its message is shown after "Chưa lưu.". */
export class OfflineSaleError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "OfflineSaleError";
  }
}

export interface OfflineLinePrice {
  /** Retail price of one unit of the tile, from the catalog. */
  price: Money;
  gross: Money;
  discount: Money;
  amount: Money;
}

export interface OfflinePricing {
  /** Priced lines by key; a line missing here has no price in the catalog. */
  lines: ReadonlyMap<string, OfflineLinePrice>;
  goods: Money;
  discount: Money;
  total: Money;
  /** Every line with a SKU, a unit and a quantity has a price. */
  complete: boolean;
  /**
   * The lines rounded one by one add up to the exact total rounded once, so the server reaches
   * this total whichever rounding mode the store uses.
   */
  roundsAlike: boolean;
}

const round = (d: Decimal, dp: number) => d.toDecimalPlaces(dp, Decimal.ROUND_HALF_UP);

/** Catalog price of a line: the tile's retail price, for the tile's own unit only. */
function tilePrice(line: ItemDraft, tiles: ReadonlyMap<string, PosTile>): Money | null {
  if (line.skuId === null) return null;
  const tile = tiles.get(line.skuId);
  if (!tile || tile.unit_id !== line.unitId) return null;
  return tile.retail_price;
}

/**
 * Prices the cart from the saved catalog (retail price, line discount in money, prices including
 * tax) as the server's line rounding does: the gross is price × quantity at 4 decimals, and the
 * line amount is gross minus discount rounded to the đồng (half up).
 */
export function priceOffline(lines: readonly ItemDraft[], tiles: readonly PosTile[]): OfflinePricing {
  const bySku = new Map(tiles.map((t) => [t.sku_id, t]));
  const out = new Map<string, OfflineLinePrice>();
  const exact: Decimal[] = [];
  let complete = true;
  for (const l of lines) {
    if (!isPriceable(l)) continue;
    const price = tilePrice(l, bySku);
    if (price === null) {
      complete = false;
      continue;
    }
    const raw = dec(l.qty).times(dec(price));
    // The same discount the sale body carries (draftToInput), compared on the unrounded gross.
    const sent = dec(l.discountPct).gt(0) ? (keptDiscount(l.savedDiscount, l.discountPct, raw) ?? discountAmount(l.qty, price, l.discountPct)) : toMoney(0);
    const line = round(raw, 4).minus(dec(sent));
    exact.push(line);
    const gross = round(round(raw, 4), 0);
    const amount = round(line, 0);
    out.set(l.key, { price, gross: toMoney(gross), discount: toMoney(gross.minus(amount)), amount: toMoney(amount) });
  }
  const priced = [...out.values()];
  const total = sumDec(priced.map((p) => p.amount));
  return {
    lines: out,
    goods: toMoney(sumDec(priced.map((p) => p.gross))),
    discount: toMoney(sumDec(priced.map((p) => p.discount))),
    total: toMoney(total),
    complete,
    roundsAlike: round(sumDec(exact), 0).eq(total),
  };
}

/** The cash payment of the sale; a store without an active cash method cannot sell offline. */
function cashPayments(bundle: OfflineBundle, total: Money, cash: string) {
  try {
    return checkoutPayments(bundle.payment_methods, total, cash, "");
  } catch (e) {
    throw new OfflineSaleError(e instanceof Error ? e.message : String(e));
  }
}

export interface OfflineSaleInput {
  /** Catalog of this tenant and session; null when none was downloaded. */
  bundle: OfflineBundle | null;
  lines: readonly ItemDraft[];
  customerId: string | null;
  cash: string;
  bank: string;
  credit: boolean;
  idOf: ColorMaps["idOf"];
  now?: Date;
}

/** The checked sale ready for the outbox, without storing it. */
export function buildOfflineSale(input: OfflineSaleInput): { sale: OfflineSaleBody; total: Money; lines: OutboxLineView[]; bundle: OfflineBundle } {
  const { bundle, lines } = input;
  const now = input.now ?? new Date();
  if (!bundle) throw new OfflineSaleError(OFFLINE_MESSAGES.noBundle);
  if (!bundle.settings.offline_pos_enabled) throw new OfflineSaleError(OFFLINE_MESSAGES.disabled);
  if (!bundle.settings.prices_include_tax) throw new OfflineSaleError(OFFLINE_MESSAGES.taxExcluded);
  if (!bundleFresh(bundle, now)) throw new OfflineSaleError(OFFLINE_MESSAGES.expired);
  if (input.customerId !== null) throw new OfflineSaleError(OFFLINE_MESSAGES.customer);
  if (lines.length === 0 || lines.some((l) => !isPriceable(l))) throw new OfflineSaleError(OFFLINE_MESSAGES.incomplete);

  const pricing = priceOffline(lines, bundle.tiles);
  if (!pricing.complete) throw new OfflineSaleError(OFFLINE_MESSAGES.unpriced);
  if (!pricing.roundsAlike) throw new OfflineSaleError(OFFLINE_MESSAGES.rounding);
  const total = pricing.total;
  // Transfer needs the bank's confirmation and credit needs the debt ledger: both wait for the server.
  if (dec(input.bank || "0").gt(0) || input.credit || dec(paymentFigures(total, input.cash, "").owe).gt(0)) throw new OfflineSaleError(OFFLINE_MESSAGES.cashOnly);
  // The sales already on the device count too; that is checked as the sale is stored.
  if (dec(total).gt(dec(bundle.settings.offline_pos_max_amount))) throw new OfflineSaleError(OFFLINE_MESSAGES.overCap);

  // The catalog price is sent as the line price, so the server books what the customer paid.
  const priceOf = (key: string) => pricing.lines.get(key)?.price ?? null;
  const saleLines = draftToInput(lines, { idOf: input.idOf, listPrice: priceOf, flavorPrice: () => null, groups: [] }).map((l, i) => {
    const price = priceOf(lines[i]?.key ?? "");
    return price === null ? l : { ...l, unit_price: price };
  });
  const bySku = new Map(bundle.tiles.map((t) => [t.sku_id, t]));
  const view: OutboxLineView[] = lines.map((l) => ({
    name: cartLineName(l),
    qty: l.qty ?? "0",
    unit_name: (l.skuId === null ? undefined : bySku.get(l.skuId)?.unit_name) ?? "",
    amount: pricing.lines.get(l.key)?.amount ?? toMoney(0),
  }));
  const payments = cashPayments(bundle, total, input.cash);
  const sale: OfflineSaleBody = {
    offline_client_id: uuidv7(now.getTime()),
    created_at: now.toISOString(),
    lines: saleLines,
    ...payments,
  };
  return { sale, total, lines: view, bundle };
}

/**
 * Checks the sale and stores it in the outbox; throws OfflineSaleError when a rule refuses it,
 * including the cap over every unposted sale of the tenant on this device, in every tab.
 */
export async function recordOfflineSale(input: OfflineSaleInput): Promise<OutboxEntry> {
  const built = buildOfflineSale(input);
  try {
    return await addToOutbox(
      {
        offline_client_id: built.sale.offline_client_id,
        tenant_id: built.bundle.tenant_id,
        device_session_id: built.bundle.device_session_id,
        sale: built.sale,
        total: built.total,
        bundle_version: built.bundle.version,
        lines: built.lines,
      },
      built.bundle.settings.offline_pos_max_amount,
    );
  } catch (e) {
    if (e instanceof OutboxCapError) throw new OfflineSaleError(OFFLINE_MESSAGES.overCap);
    throw e;
  }
}
