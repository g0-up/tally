import { percentOf, type DecimalLike } from "../lib/decimal";
import { cx } from "../lib/cx";

export interface ProgressProps {
  value: DecimalLike;
  max: DecimalLike;
  /** Accessible name ("Tiến độ nhận", "Doanh thu thuần / mục tiêu"). */
  label: string;
  /** Gradient fill (`.meter.acc`) used for gift-basket and campaign progress. */
  accent?: boolean;
  /** Fixed track width, e.g. 120 inside table cells; full width otherwise. */
  width?: number;
  className?: string;
}

/** Prototype `.meter`: 8px track with a navy (or accent gradient) fill. */
export function Progress({ value, max, label, accent = false, width, className }: ProgressProps) {
  const pct = percentOf(value, max);
  return (
    <div
      className={cx("meter", accent && "acc", className)}
      style={width === undefined ? undefined : { width }}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
    >
      <i style={{ width: `${pct}%` }} />
    </div>
  );
}
