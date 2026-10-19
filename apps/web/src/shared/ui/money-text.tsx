import type { DecimalLike } from "../lib/decimal";
import { vnd } from "../lib/format";

/** Prototype `money()`: VND amount in tabular figures so columns line up. */
export function MoneyText({ value }: { value: DecimalLike | null | undefined }) {
  return <span className="tab-n">{vnd(value)}</span>;
}
