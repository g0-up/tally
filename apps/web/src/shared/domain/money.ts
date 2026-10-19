import { dec, fractionDigits, isDecimalString, toDecimalString, type DecimalLike } from "../lib/decimal";

// Money is an amount in VND carried as the API's plain decimal string (at most 4 fractional digits).
// The brand stops arbitrary strings or numbers from being sent where an amount is expected.

export type Money = string & { readonly __brand: "Money" };

export const MONEY_MAX_FRACTION_DIGITS = 4;

/** Validates an API or user-entered decimal string as Money; null when it is not one. */
export function parseMoney(value: string): Money | null {
  const s = value.trim();
  if (!isDecimalString(s) || fractionDigits(s) > MONEY_MAX_FRACTION_DIGITS) return null;
  return toDecimalString(s) as Money;
}

/** Converts arithmetic results back into Money, rounding half up to the allowed precision. */
export function toMoney(value: DecimalLike): Money {
  return toDecimalString(dec(value).toDecimalPlaces(MONEY_MAX_FRACTION_DIGITS)) as Money;
}

export const ZERO_MONEY = "0" as Money;

/**
 * Reads what a user typed in a VND field: vi-VN grouping dots are thousands separators and a
 * comma is the decimal mark ("1.250.000" → "1250000", "12,5" → "12.5"). Null when not a number.
 */
export function parseMoneyInput(text: string): Money | null {
  const cleaned = text.replace(/[\s\u00a0\u202f₫]/g, "").replace(/\./g, "").replace(",", ".");
  if (cleaned === "" || cleaned === "-") return null;
  return parseMoney(cleaned);
}
