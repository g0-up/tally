import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage, fieldReasonMessage, isApiError } from "../../../shared/api/api-error";
import { posKeys } from "../../pos";
import { barcodeSchema, type Barcode } from "../schemas";
import { skuKeys } from "./queries";

// Write-side helpers of the catalog screens: the If-Match value, the mapping of refused
// commands onto form fields with the server's own Vietnamese sentence, and the barcode unit change.

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

export interface BarcodeUnitChange {
  barcodeId: string;
  unitId: string;
}

/** Cached answer of `GET /skus/{id}/barcodes`. */
interface BarcodeList {
  data: Barcode[];
}

/**
 * `PATCH /skus/{id}/barcodes/{barcodeId}`: moves a barcode to another unit of its SKU, so the next
 * `/sku-lookup` of the code answers that unit. The answer replaces the row in the cached barcodes
 * at once; the SKU lists (their barcode column shows a base-unit barcode) and barcodes then refetch.
 * The POS bootstrap of this tab is invalidated too: POS matches tile barcodes from it before asking
 * `/sku-lookup`, so it is fetched again the next time POS opens here instead of reusing the cache.
 * A unit change holds no document, so it takes neither an Idempotency-Key nor If-Match, and a
 * refusal is shown under the row's unit picker rather than as a "Chưa ghi sổ." toast.
 */
export function useUpdateBarcodeUnit(skuId: string) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ barcodeId, unitId }: BarcodeUnitChange) =>
      client.patch(`/skus/${skuId}/barcodes/${barcodeId}`, { unit_id: unitId }, { schema: barcodeSchema }),
    onSuccess: (saved) => {
      queryClient.setQueryData<BarcodeList>(skuKeys.barcodes(skuId), (old) => old && { ...old, data: old.data.map((b) => (b.id === saved.id ? saved : b)) });
      void queryClient.invalidateQueries({ queryKey: skuKeys.all });
      void queryClient.invalidateQueries({ queryKey: posKeys.bootstrap() });
    },
  });
}

/** The sentence shown under a unit picker whose change the API refused. */
export function barcodeUnitErrorMessage(e: unknown): string {
  if (isApiError(e) && e.fields["unit_id"] === "not_a_unit_of_sku") return "Đơn vị này không còn thuộc hàng hóa. Tải lại để xem bản mới nhất.";
  return errorMessage(e);
}
