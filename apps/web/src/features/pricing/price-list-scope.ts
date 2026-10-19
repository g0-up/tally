import { dec } from "../../shared/lib/decimal";
import { num } from "../../shared/lib/format";
import type { CustomerGroup, PriceList, PriceListItem } from "./schemas";

// Labels of the read-only price-list table: the resolver order ("Ưu tiên"), who a list applies to
// ("Áp dụng") and its quantity tiers ("Bậc thang").

/** Resolver levels, first tried first: customer, group, season, then the base list a group uses. */
const LEVEL: Readonly<Record<PriceList["kind"], number>> = { customer: 0, group: 1, season: 2, wholesale: 3, retail: 4 };

/** Lists in the order the resolver tries them; lower priority wins within a level. */
export function byPrecedence(lists: readonly PriceList[]): PriceList[] {
  return [...lists].sort((a, b) => LEVEL[a.kind] - LEVEL[b.kind] || a.priority - b.priority || a.name.localeCompare(b.name, "vi"));
}

const groupNames = (groups: readonly CustomerGroup[]) => groups.map((g) => g.name).join(", ");

/** "Áp dụng": the customers a list prices. */
export function scopeLabel(list: PriceList, groups: readonly CustomerGroup[]): string {
  switch (list.kind) {
    case "customer":
      return "Giá riêng một khách hàng";
    case "group": {
      const g = groups.find((x) => x.id === list.customer_group_id);
      return g ? `Nhóm ${g.name}` : "Nhóm khách";
    }
    case "season":
      return "Theo chương trình khuyến mãi";
    case "wholesale": {
      const using = groups.filter((g) => g.base_price_kind === "wholesale");
      return using.length ? `Nhóm ${groupNames(using)}` : "Nhóm khách dùng giá sỉ";
    }
    case "retail": {
      const using = groups.filter((g) => g.base_price_kind === "retail");
      return using.length ? `Khách lẻ, nhóm ${groupNames(using)}` : "Khách lẻ";
    }
  }
}

/** "Bậc thang": the quantity thresholds of a list ("≥ 50 giỏ · ≥ 100 giỏ"); empty without tiers. */
export function tierLabel(items: readonly PriceListItem[]): string {
  const seen = new Map<string, string>();
  for (const it of [...items].sort((a, b) => dec(a.min_qty).comparedTo(b.min_qty))) {
    if (!dec(it.min_qty).gt(1)) continue;
    const text = `≥ ${num(it.min_qty)}${it.unit_name ? ` ${it.unit_name}` : ""}`;
    seen.set(text, text);
  }
  return [...seen.keys()].join(" · ");
}
