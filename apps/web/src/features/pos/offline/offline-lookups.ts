import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { toQty } from "../../../shared/domain/qty";
import { sumDec } from "../../../shared/lib/decimal";
import { salesKeys } from "../../sales/api/queries";
import type { SkuDetail, SkuStock } from "../../sales/schemas";
import type { OfflineBundle } from "./offline-db";

// The cart rows read a SKU's units, its stock by color and the color list from their own queries.
// Without a network those queries cannot run, so the rows would stay empty: while offline, every
// one that has never been answered is filled from the catalog instead (the tile's selling unit and
// its stock snapshot). They are marked as outdated, so the server's answer replaces them as soon as
// the API is reachable again.

function seed(queryClient: QueryClient, key: QueryKey, data: unknown): void {
  if (queryClient.getQueryData(key) !== undefined) return;
  queryClient.setQueryData(key, data, { updatedAt: 0 });
}

export function seedOfflineLookups(queryClient: QueryClient, bundle: OfflineBundle): void {
  seed(queryClient, salesKeys.lookup("colors"), { data: bundle.colors });
  for (const t of bundle.tiles) {
    const detail: SkuDetail = {
      id: t.sku_id,
      code: t.sku_code,
      display_name: t.name,
      variant_label: t.variant_label,
      min_price: null,
      units: [{ unit_id: t.unit_id, unit_name: t.unit_name, factor: "1", is_base: true, active: true, is_default_sale: true }],
    };
    const stock: SkuStock = {
      sku_id: t.sku_id,
      unit_name: t.unit_name,
      available: toQty(sumDec(t.stock.map((s) => s.qty))),
      colors: t.stock.map((s) => ({ color_id: s.color_id, sellable: s.qty })),
    };
    seed(queryClient, salesKeys.lookup("sku", t.sku_id), detail);
    seed(queryClient, ["inventory", "stock", t.sku_id], stock);
  }
}
