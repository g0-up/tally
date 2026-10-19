import type { Money } from "../../../shared/domain/money";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Callout } from "../../../shared/ui/callout";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { LOW_MARGIN, marginRatio, marginText, savingsText } from "../api/adapters";
import type { CostView, KitType } from "../schemas";

export interface KitEconomicsProps {
  type: KitType;
  retail: Money | null;
  /** Economics of the complete lines; undefined before the first preview. */
  cost: CostView | undefined;
}

/**
 * Prototype `kitLive`: price, components at retail, saving, cost, margin and the kits the stock
 * builds right now, with warnings for a kit priced above its parts or a thin margin.
 */
export function KitEconomics({ type, retail, cost }: KitEconomicsProps) {
  const sale = cost?.components_sale ?? "0";
  const ratio = cost ? marginRatio(retail, cost.total_cost) : null;
  const priced = retail !== null && dec(retail).gt(0);
  return (
    <>
      <ImpactSummary
        items={[
          { label: "Giá bán giỏ", value: vnd(retail ?? 0) },
          { label: "Tổng giá bán lẻ thành phần", value: vnd(sale) },
          { label: "Khách tiết kiệm", value: savingsText(retail, sale, "—") },
          { label: "Giá vốn dự kiến", value: vnd(cost?.total_cost ?? 0) },
          { label: "Lãi gộp dự kiến", value: marginText(ratio) },
          { label: type === "kit" ? "Có thể đóng ngay" : "Có thể bán ngay", value: `${num(cost?.buildable ?? 0)} ${type === "kit" ? "giỏ" : "combo"}` },
        ]}
      />
      {priced && dec(sale).gt(0) && dec(retail).gt(sale) ? <Callout tone="warn">Giá giỏ cao hơn tổng giá bán lẻ các thành phần.</Callout> : null}
      {ratio !== null && ratio < LOW_MARGIN ? <Callout tone="warn">Biên lãi gộp dự kiến dưới 15%.</Callout> : null}
    </>
  );
}
