import { dec, type DecimalLike } from "../../lib/decimal";

// Geometry shared by the SVG charts, ported from the prototype `barChart` / `groupedBars`.
// Amounts arrive as decimal strings; they become floats only to compute pixel positions.

export interface ChartSeries<K extends string> {
  key: K;
  /** Legend and tooltip name ("Bán sỉ (theo phiếu xuất)"). */
  name: string;
  /** Column header in the "Xem dạng bảng" table; defaults to name. */
  tableHeader?: string;
  /** Fill, always a token reference such as "var(--series-1)". */
  color: string;
}

export interface ChartPoint<K extends string> {
  /** ISO date of the bar (day, or first day of the month). */
  x: string;
  values: Readonly<Record<K, DecimalLike>>;
}

/** 1, 2 or 5 times a power of ten, at least x (prototype `niceStep`). */
export function niceStep(x: number): number {
  const p = Math.pow(10, Math.floor(Math.log10(x || 1)));
  const n = x / p;
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * p;
}

export function amount(value: DecimalLike): number {
  return dec(value).toNumber();
}

export interface Scale {
  step: number;
  top: number;
  ticks: number[];
  y: (v: number) => number;
}

/** Y scale with four nice steps from 0 to at least `max` between pixel rows `t` and `h - b`. */
export function yScale(max: number, h: number, t: number, b: number): Scale {
  const step = niceStep(Math.max(1, max) / 4);
  const top = Math.ceil(Math.max(1, max) / step) * step;
  const ticks: number[] = [];
  for (let v = 0; v <= top + 1; v += step) ticks.push(v);
  return { step, top, ticks, y: (v: number) => t + (h - t - b) * (1 - v / top) };
}

/** Bar with rounded top corners of radius r (prototype path). */
export function roundedTopPath(x: number, y: number, w: number, h: number, r: number): string {
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}
