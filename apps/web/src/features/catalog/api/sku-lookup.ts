import { z } from "zod";
import { isApiError } from "../../../shared/api/api-error";
import { decimalSchema } from "../../../shared/api/decimal-schemas";
import type { HttpClient } from "../../../shared/api/http";
import { skuListItemSchema } from "../schemas";

// The scan lookup shared by POS, stock counts and receipts: GET /sku-lookup resolves a scanned
// code to the SKU and the unit it stands for, including the forms a scanner may report for one
// retail barcode (UPC with or without leading zeros, UPC-E).

export const skuCodeMatchSchema = z.object({
  sku: skuListItemSchema,
  unit_id: z.string(),
  unit_name: z.string(),
  /** Base units in one of the unit; "1" for a SKU code. */
  factor: decimalSchema,
  matched_by: z.enum(["barcode", "sku_code"]),
});
export type SkuCodeMatch = z.infer<typeof skuCodeMatchSchema>;

/**
 * The SKU a code names, or null when it names none: 404 code_not_found, or 422 for text that
 * cannot be a barcode (spaces, accents), which no SKU can carry either. Other failures throw.
 */
export async function lookupSkuCode(client: HttpClient, code: string): Promise<SkuCodeMatch | null> {
  const wanted = code.trim();
  if (wanted === "") return null;
  try {
    return await client.get("/sku-lookup", { query: { code: wanted }, schema: skuCodeMatchSchema });
  } catch (e) {
    if (isApiError(e) && (e.code === "code_not_found" || e.status === 422)) return null;
    throw e;
  }
}
