import DecimalBase from "decimal.js-light";

// Money and quantities travel as plain decimal strings ("15000", "0.5"); arithmetic on them goes
// through this isolated Decimal constructor, never through Number(). The clone keeps the global
// decimal.js-light configuration untouched for any other consumer.
export const Decimal = DecimalBase.clone({ precision: 40, rounding: DecimalBase.ROUND_HALF_UP, toExpNeg: -30, toExpPos: 40 });
export type Decimal = InstanceType<typeof Decimal>;

export type DecimalLike = string | number | Decimal;

const DECIMAL_STRING = /^-?[0-9]+(\.[0-9]+)?$/;

/** Reports whether s is a plain decimal string as the API sends it (no exponent, no grouping). */
export function isDecimalString(s: string): boolean {
  return DECIMAL_STRING.test(s);
}

/**
 * Converts an API decimal string (or a finite number from UI arithmetic) into a Decimal.
 * Throws a RangeError for anything else so bad data fails loudly at the boundary.
 */
export function dec(value: DecimalLike): Decimal {
  if (value instanceof Decimal) return value;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new RangeError(`Not a finite number: ${value}`);
    return new Decimal(value);
  }
  const s = value.trim();
  if (!isDecimalString(s)) throw new RangeError(`Not a decimal string: "${value}"`);
  return new Decimal(s);
}

/** Plain decimal string for the API: no exponent, trailing zeros trimmed, "-0" normalised to "0". */
export function toDecimalString(value: DecimalLike): string {
  const d = dec(value);
  if (d.isZero()) return "0";
  const s = d.toFixed();
  return s.includes(".") ? s.replace(/\.?0+$/, "") : s;
}

/** Number of fractional digits of a plain decimal string. */
export function fractionDigits(s: string): number {
  const i = s.indexOf(".");
  return i < 0 ? 0 : s.length - i - 1;
}

/** Sum of decimals; empty input sums to zero. */
export function sumDec(values: readonly DecimalLike[]): Decimal {
  return values.reduce<Decimal>((acc, v) => acc.plus(dec(v)), new Decimal(0));
}

/** Rounds half toward +Infinity, matching Math.round, which the prototype formatters use. */
export function roundHalfCeil(value: DecimalLike, dp = 0): Decimal {
  return dec(value).toDecimalPlaces(dp, Decimal.ROUND_HALF_CEIL);
}

/** Largest integer not greater than value. */
export function floorDec(value: DecimalLike): Decimal {
  return dec(value).toDecimalPlaces(0, Decimal.ROUND_FLOOR);
}

/** value / max in percent (two decimals), clamped to 0..100; a zero or negative max reads as 0. */
export function percentOf(value: DecimalLike, max: DecimalLike): number {
  const m = dec(max);
  if (m.lte(0)) return 0;
  const p = Number(dec(value).div(m).times(100).toFixed(2));
  return Math.min(100, Math.max(0, p));
}
