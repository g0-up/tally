import { dec, floorDec, roundHalfCeil, type DecimalLike } from "./decimal";

// Display formatters ported from the prototype helpers (`vnd`, `num`, `fd`, `fdS`, `qMix`, chart
// axis). They take API decimal strings and never route money through Number().

const NBSP = " ";
const MINUS = "−";
const intFmt = new Intl.NumberFormat("vi-VN", { maximumFractionDigits: 0 });
const numFmt = new Intl.NumberFormat("vi-VN", { maximumFractionDigits: 2 });

// Intl accepts exact decimal strings (ES2023 Intl.NumberFormat v3); this keeps large amounts exact.
type IntlDecimal = Parameters<Intl.NumberFormat["format"]>[0];
const asIntl = (s: string) => s as unknown as IntlDecimal;

/** "1.250.000 ₫": rounded to whole dong (half up like Math.round), vi-VN grouping, NBSP before ₫. */
export function vnd(value: DecimalLike | null | undefined): string {
  const rounded = roundHalfCeil(value ?? 0).toFixed(0);
  return intFmt.format(asIntl(rounded === "-0" ? "0" : rounded)) + NBSP + "₫";
}

/** Same text as vnd; render it inside `.tab-n` (see MoneyText) so columns align. */
export const money = vnd;

/** vi-VN grouping with at most two fractional digits ("1.234,5"). */
export function num(value: DecimalLike | null | undefined): string {
  const d = dec(value ?? 0);
  const s = d.isZero() ? "0" : d.toFixed();
  return numFmt.format(asIntl(s));
}

/**
 * "2026-10-12" → "12/10/2026"; a longer value keeps its remainder ("2026-10-12 08:30" → "12/10/2026 08:30"),
 * exactly like the prototype `fd`. Empty input renders an em dash.
 */
export function fd(value: string | null | undefined): string {
  if (!value) return "—";
  const [y = "", m = "", d = ""] = value.slice(0, 10).split("-");
  return `${d}/${m}/${y}` + (value.length > 10 ? value.slice(10) : "");
}

/** "2026-10-12" → "12/10". */
export function fdS(value: string | null | undefined): string {
  if (!value) return "—";
  return `${value.slice(8, 10)}/${value.slice(5, 7)}`;
}

/**
 * Chart axis tick: millions as "tr", thousands as "k" ("1,5 tr", "800 k"). With zeroAsZero the
 * zero tick reads "0" (grouped bar chart) instead of "0 k" (stacked bar chart).
 */
export function axisLabel(value: number, zeroAsZero = false): string {
  if (value >= 1e6) return `${num(value / 1e6)} tr`;
  if (zeroAsZero && value === 0) return "0";
  return `${num(value / 1e3)} k`;
}

export interface UnitOfMeasure {
  /** Unit name as shown ("thùng", "dây", "hộp"). */
  name: string;
  /** How many base units one of this unit holds; the base unit has factor 1. */
  factor: DecimalLike;
}

/**
 * Mixed-unit quantity: largest units first, skipping the base unit until the remainder
 * ("1 thùng 3 dây 2 hộp"). Zero renders "0"; negatives get a leading minus sign (U+2212).
 */
export function qMix(quantity: DecimalLike, units: readonly UnitOfMeasure[], baseName: string): string {
  const q = dec(quantity);
  const negative = q.isNegative() && !q.isZero();
  let rest = q.abs();
  const parts: string[] = [];
  const sorted = [...units].map((u) => ({ name: u.name, factor: dec(u.factor) })).sort((a, b) => b.factor.comparedTo(a.factor));
  for (const u of sorted) {
    if (u.factor.eq(1) || u.factor.lte(0)) continue;
    const n = floorDec(rest.div(u.factor));
    if (!n.isZero()) {
      parts.push(`${num(n)} ${u.name}`);
      rest = rest.minus(n.times(u.factor));
    }
  }
  if (parts.length === 0 && rest.isZero()) return "0";
  if (!rest.isZero()) parts.push(`${num(rest)} ${baseName}`);
  return (negative ? MINUS : "") + parts.join(" ");
}

/** Percent change from previous to current, rounded to a whole percent; null when previous is zero. */
export function percentChange(current: DecimalLike, previous: DecimalLike): number | null {
  const b = dec(previous);
  if (b.isZero()) return null;
  return Number(roundHalfCeil(dec(current).minus(b).div(b.abs()).times(100)).toFixed(0));
}
