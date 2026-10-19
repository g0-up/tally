import { dec, fractionDigits, isDecimalString, toDecimalString, type DecimalLike } from "../lib/decimal";

// Quantities in base units travel as plain decimal strings (at most 4 fractional digits).

export type Qty = string & { readonly __brand: "Qty" };

export const QTY_MAX_FRACTION_DIGITS = 4;

export function parseQty(value: string): Qty | null {
  const s = value.trim();
  if (!isDecimalString(s) || fractionDigits(s) > QTY_MAX_FRACTION_DIGITS) return null;
  return toDecimalString(s) as Qty;
}

export function toQty(value: DecimalLike): Qty {
  return toDecimalString(dec(value).toDecimalPlaces(QTY_MAX_FRACTION_DIGITS)) as Qty;
}

export const ZERO_QTY = "0" as Qty;

/** Reads a typed quantity: comma is the decimal mark, dots group thousands ("1.200" → "1200", "0,5" → "0.5"). */
export function parseQtyInput(text: string): Qty | null {
  const cleaned = text.replace(/\s/g, "").replace(/\./g, "").replace(",", ".");
  if (cleaned === "" || cleaned === "-") return null;
  return parseQty(cleaned);
}
