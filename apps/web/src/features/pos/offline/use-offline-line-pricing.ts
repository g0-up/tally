import { useMemo } from "react";
import { toMoney } from "../../../shared/domain/money";
import type { ItemDraft } from "../../sales/components/line-items/draft";
import type { LinePricing } from "../../sales/components/line-items/use-line-pricing";
import type { PosTile } from "../../sales/schemas";
import { priceOffline } from "./offline-sale-service";

const CACHED_SOURCE = { kind: "base_list", price_list_name: null, color_id: null } as const;

/**
 * The cart priced from the saved catalog, in the shape the cart rows read from the server's pricing:
 * the same price, amounts and totals the offline sale will carry. Offline selling needs prices that
 * include tax, so the line amount is what the customer pays; the tax inside it, promotions and the
 * minimum-price check are worked out by the server when the sale syncs.
 */
export function useOfflineLinePricing(lines: readonly ItemDraft[], tiles: readonly PosTile[]): LinePricing {
  return useMemo(() => {
    const p = priceOffline(lines, tiles);
    const bySku = new Map(tiles.map((t) => [t.sku_id, t]));
    const unitOf = (key: string) => {
      const line = lines.find((l) => l.key === key);
      return line?.skuId ? (bySku.get(line.skuId)?.unit_id ?? null) : null;
    };
    return {
      listPrice: (key) => {
        const priced = p.lines.get(key);
        const unit = unitOf(key);
        return priced && unit ? { amount: priced.price, unit_id: unit, price_source: CACHED_SOURCE } : null;
      },
      flavorPrice: () => null,
      lineAmount: (key) => p.lines.get(key)?.amount ?? null,
      grossAmount: (key) => p.lines.get(key)?.gross ?? null,
      belowMin: () => false,
      totals: { lines: [], goods_amount: p.goods, discount_amount: p.discount, tax_amount: toMoney(0), total_amount: p.total },
      pending: false,
    };
  }, [lines, tiles]);
}
