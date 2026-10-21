import { useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { useApiClient } from "../../../shared/api/api-context";
import { errorMessage, fieldReasonMessage, isApiError } from "../../../shared/api/api-error";
import { useCommit } from "../../../shared/hooks/use-commit";
import { posKeys } from "../../pos";
import { UNIT_LOCK_LABELS, UNIT_MESSAGES, skuUnitsRequest, type UnitLock, type UnitTable } from "../components/product-draft";
import { barcodeSchema, skuDetailSchema, unitSchema, type Barcode, type SkuDetail, type Unit } from "../schemas";
import { catalogKeys, skuKeys } from "./queries";

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

/** Unit-row fields of the API → unit table fields of the form. */
const UNIT_ROW_FIELD: Readonly<Record<string, string>> = {
  unit_id: "unit",
  factor: "factor",
  retail_price: "retail",
  wholesale_price: "wholesale",
};

/** Quick-create names the base retail price `sale_price`; the form shows it in the unit table as `retail_price`. */
const FIELD_ALIAS: Readonly<Record<string, string>> = { sale_price: "retail_price" };

/**
 * API field path → product form field ("sku.code" → "code", "attributes.mau" → "attributes.mau",
 * "sku.units[1].factor" → "units.1.factor", "units[0].unit_id" → "units.0.unit").
 */
function productField(path: string): string {
  const field = path.startsWith("sku.") ? path.slice("sku.".length) : path;
  const row = /^units\[(\d+)\]\.(\w+)$/.exec(field);
  if (row) return `units.${row[1]}.${UNIT_ROW_FIELD[row[2] ?? ""] ?? row[2]}`;
  return FIELD_ALIAS[field] ?? field;
}

/** Unit-row refusals in the unit table's words. */
const UNIT_REASONS: Readonly<Record<string, string>> = {
  is_base: "Đã là đơn vị cơ sở.",
  duplicate: "Đơn vị bị trùng.",
  unknown_unit: "Đơn vị không có hoặc đã ngừng dùng.",
  too_many: UNIT_MESSAGES.tooMany,
};

const isUnitField = (field: string) => field === "units" || field.startsWith("units.");
const fieldMessage = (field: string, reason: string) => (isUnitField(field) ? UNIT_REASONS[reason] : undefined) ?? fieldReasonMessage(reason);

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
    out[field] = e.code === "code_and_name_required" || e.code === "factor_not_positive" ? e.message : fieldMessage(field, reason);
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

/** Cached answer of `GET /units`. */
interface UnitList {
  data: Unit[];
}

/**
 * `POST /units`: adds a unit typed in the unit table to the store's unit directory. The answer is
 * put in the cached directory at once so the row's picker shows it, then the directory refetches.
 * A directory entry is not a document, so it takes no Idempotency-Key; a refusal (409 name_taken)
 * is shown under the row's unit field.
 */
export function useCreateUnit() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => client.post("/units", { name }, { schema: unitSchema }),
    onSuccess: (unit) => {
      queryClient.setQueryData<UnitList>(catalogKeys.lookup("units"), (old) => old && { ...old, data: [...old.data.filter((u) => u.id !== unit.id), unit] });
      void queryClient.invalidateQueries({ queryKey: catalogKeys.lookup("units") });
    },
  });
}

/** The sentence shown under a unit field whose new unit the directory refused. */
export function createUnitErrorMessage(e: unknown, name: string): string {
  // The directory compares names ignoring accents and case, and keeps retired units.
  if (isApiError(e) && e.status === 409 && e.code === "name_taken") return `Đơn vị “${name}” đã có trong danh mục đơn vị (có thể đang ngừng dùng).`;
  return errorMessage(e);
}

/**
 * Everything a saved unit table shows in: SKU lists and details, the price lists, POS (bootstrap
 * tiles and their prices), the unit pickers of sales and POS lines, and the stock list and stock card. Pricing and inventory import this
 * feature, so their keys are written out rather than imported.
 */
const SKU_UNITS_KEYS: readonly QueryKey[] = [skuKeys.all, ["pricing"], posKeys.all, ["sales", "lookup", "sku"], ["inventory", "stock"], ["inventory", "stock-card"]];

export interface ReplaceSkuUnits {
  table: UnitTable;
  /** The table the editor opened with: a base price left as it was there is not resent. */
  started: UnitTable;
  /** The SKU version the table was edited from (If-Match). */
  version: number;
}

export interface ReplaceSkuUnitsOptions {
  /** Receives a refused save with what was sent, before the toast, to put its reasons on the table. */
  onRefused: (e: unknown, sent: ReplaceSkuUnits) => void;
  after: () => void;
}

/** Refusals of a unit table save in the owner's words. */
const REPLACE_UNITS_MESSAGES: Readonly<Record<string, string>> = {
  unit_in_use: "Có đơn vị đã được dùng nên không xóa được; đơn vị đó đã chuyển sang Ngừng dùng. Kiểm tra rồi bấm Lưu đơn vị & giá.",
  sku_unit_in_use: "Đơn vị đang được dùng nên không xóa được. Tải lại để xem bản mới nhất.",
};

function replaceUnitsError(e: unknown): unknown {
  const message = isApiError(e) ? REPLACE_UNITS_MESSAGES[e.code] : undefined;
  return message === undefined || !isApiError(e) ? e : e.withMessage(message);
}

/**
 * `PUT /skus/{id}/units`: saves the whole unit table of a SKU with its unit prices in one request
 * guarded by the SKU version. A 412 is answered app-wide (stale-data toast, everything refetched),
 * so the dialog gets the new version while its draft stays. The table is not a document, so it
 * takes no Idempotency-Key: the version makes a replay fail instead of saving twice.
 */
export function useReplaceSkuUnits(skuId: string, { onRefused, after }: ReplaceSkuUnitsOptions) {
  const client = useApiClient();
  return useCommit<SkuDetail, ReplaceSkuUnits>({
    mutationFn: async (sent) => {
      try {
        return await client.put(`/skus/${skuId}/units`, skuUnitsRequest(sent.table, sent.started), { ifMatch: etagOf(sent.version), schema: skuDetailSchema });
      } catch (e) {
        onRefused(e, sent);
        throw replaceUnitsError(e);
      }
    },
    success: () => "Đã lưu đơn vị & giá. Bán hàng, báo giá và đơn hàng mới dùng ngay.",
    invalidate: SKU_UNITS_KEYS,
    after,
  });
}

const UNIT_LOCKS = new Set<string>(Object.keys(UNIT_LOCK_LABELS));
const isUnitLock = (reason: string): reason is UnitLock => UNIT_LOCKS.has(reason);

/** The units a 409 unit_in_use names (`units[<unit_id>]`) with why each cannot be deleted. */
export function unitsInUse(e: unknown): Map<string, UnitLock> {
  const out = new Map<string, UnitLock>();
  if (!isApiError(e) || e.code !== "unit_in_use") return out;
  for (const [path, reason] of Object.entries(e.fields)) {
    const unit = /^units\[([^\]]+)\]$/.exec(path)?.[1];
    if (unit !== undefined && isUnitLock(reason)) out.set(unit, reason);
  }
  return out;
}
