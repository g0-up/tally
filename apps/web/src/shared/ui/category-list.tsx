import { dec, percentOf, type DecimalLike } from "../lib/decimal";
import { vnd } from "../lib/format";

export interface CategoryItem {
  label: string;
  amount: DecimalLike;
}

export const EMPTY_CATEGORY_TEXT = "Không có giao dịch trong kỳ.";

/** Prototype `catTable()`: label / amount rows with a thin bar showing each row's share of the total. */
export function CategoryList({ items, total }: { items: readonly CategoryItem[]; total?: DecimalLike }) {
  if (items.length === 0) return <p className="hint">{EMPTY_CATEGORY_TEXT}</p>;
  const sum = total ?? items.reduce((acc, it) => acc.plus(dec(it.amount)), dec(0));
  return (
    <div className="catlist">
      {items.map((it) => (
        <div key={it.label}>
          <span>{it.label}</span>
          <b className="tab-n">{vnd(it.amount)}</b>
          <i style={{ width: `${percentOf(it.amount, sum)}%` }} />
        </div>
      ))}
    </div>
  );
}
