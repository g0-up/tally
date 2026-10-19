import { z } from "zod";
import type { Money } from "../domain/money";
import type { Qty } from "../domain/qty";
import { isDecimalString } from "../lib/decimal";

// Building blocks of the response schemas: the API sends money, quantities and factors as decimal
// strings, and lists as {data: [...]}.

export const decimalSchema = z.string().refine(isDecimalString, "decimal string");
export const moneySchema = decimalSchema.transform((s) => s as Money);
export const qtySchema = decimalSchema.transform((s) => s as Qty);

export function listOf<T extends z.ZodType>(item: T) {
  return z.object({ data: z.array(item) });
}
