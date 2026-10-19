import { useMemo } from "react";
import type { ColorKey } from "../../../../shared/domain/color";
import { dec } from "../../../../shared/lib/decimal";
import type { StockColorOption } from "../../../../shared/ui/color/stock-color-picker";
import { useSkuStock, type ColorMaps } from "../../api/queries";

/** Colors a SKU is stocked in, with the sellable quantity; empty for a product without colors. */
export function useColorOptions(skuId: string | null, colors: ColorMaps, keep: ColorKey | null): StockColorOption[] {
  const stock = useSkuStock(skuId);
  return useMemo(() => {
    const out: StockColorOption[] = [];
    for (const c of stock.data?.colors ?? []) {
      if (c.color_id === null) continue;
      const color = colors.codeOf(c.color_id);
      if (color === "none") continue;
      if (dec(c.sellable).gt(0) || color === keep) out.push({ color, qty: c.sellable });
    }
    return out;
  }, [stock.data, colors, keep]);
}

