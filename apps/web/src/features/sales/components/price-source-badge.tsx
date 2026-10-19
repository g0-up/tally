import type { ColorKey } from "../../../shared/domain/color";
import { priceSourceLabel } from "../api/adapters";
import type { PriceSource } from "../schemas";

/** Which price list priced a line ("Bảng giá Đại lý cấp 1 · giá riêng Đỏ"); nothing until the server answered. */
export function PriceSourceBadge({ source, color = null }: { source: Pick<PriceSource, "kind" | "price_list_name"> | null | undefined; color?: ColorKey | null }) {
  if (!source) return null;
  return <>{priceSourceLabel(source, color)}</>;
}
