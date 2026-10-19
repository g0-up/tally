import { fieldReasonMessage, isApiError } from "../../../shared/api/api-error";

// Write-side helpers of the catalog screens: the If-Match value and the mapping of refused
// commands onto form fields with the server's own Vietnamese sentence.

/** Version of a record as the If-Match value. */
export function etagOf(version: number): string {
  return `"${version}"`;
}

/** Product errors whose server message belongs under one field of the product form. */
const PRODUCT_ERROR_FIELD: Readonly<Record<string, string>> = {
  sku_code_taken: "code",
  barcode_taken: "barcode",
  factor_not_positive: "factor",
  product_name_taken: "name",
};

/** API field path → product form field ("sku.code" → "code", "attributes.mau" → "attributes.mau"). */
function productField(path: string): string {
  return path.startsWith("sku.") ? path.slice("sku.".length) : path;
}

/**
 * Form errors of a refused product command: coded errors put the server sentence under their
 * field ("Mã SKU đã tồn tại trong cửa hàng." under Mã SKU); other field reasons get the shared
 * reason text.
 */
export function productFormErrors(e: unknown): Record<string, string> {
  if (!isApiError(e)) return {};
  const out: Record<string, string> = {};
  const coded = PRODUCT_ERROR_FIELD[e.code];
  if (coded) out[coded] = e.message;
  for (const [path, reason] of Object.entries(e.fields)) {
    const field = productField(path);
    if (out[field] !== undefined) continue;
    // code_and_name_required names both fields with reason "required" and one sentence.
    out[field] = e.code === "code_and_name_required" || e.code === "factor_not_positive" ? e.message : fieldReasonMessage(reason);
  }
  return out;
}
