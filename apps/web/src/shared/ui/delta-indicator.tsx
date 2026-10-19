import { percentChange } from "../lib/format";
import type { DecimalLike } from "../lib/decimal";

export interface DeltaIndicatorProps {
  current: DecimalLike;
  previous: DecimalLike;
  /** Whether a rise is good news (income) or bad news (spending). */
  goodWhenUp?: boolean;
}

/** Prototype `delta()`: "▲ 12% so với kỳ trước" coloured good/bad; "kỳ trước: 0 ₫" when there is no base. */
export function DeltaIndicator({ current, previous, goodWhenUp = true }: DeltaIndicatorProps) {
  const v = percentChange(current, previous);
  if (v === null) return <span className="dl">kỳ trước: 0 ₫</span>;
  const tone = v === 0 ? "" : v > 0 === goodWhenUp ? "good" : "bad";
  const glyph = v > 0 ? "▲" : v < 0 ? "▼" : "■";
  return (
    <span className={tone ? `dl ${tone}` : "dl"}>
      <span aria-hidden="true">{glyph}</span>
      <span className="sr-only">{v > 0 ? "Tăng" : v < 0 ? "Giảm" : "Không đổi"}</span> {Math.abs(v)}% so với kỳ trước
    </span>
  );
}
