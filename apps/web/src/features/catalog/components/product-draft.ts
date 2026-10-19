import { dec } from "../../../shared/lib/decimal";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { useCategories } from "../api/queries";
import { attributeRequest, missingRequired, useSetAttributes, type AttributeValues, type SetAttribute } from "../attribute-values";

// One product form for both entry points: "Thêm hàng hóa" in Danh mục (code, barcode, reference
// cost, min stock, attributes) and "Thêm hàng hóa mới" in Nhập hàng (purchase price instead).
// Error keys are the API field names with the "sku." prefix removed.

export type ProductFormVariant = "catalog" | "quick";

export interface ProductDraft {
  code: string;
  name: string;
  categoryId: string | null;
  barcode: string;
  baseUnitId: string | null;
  /** Larger unit ("thùng"); null keeps only the base unit. */
  bigUnitId: string | null;
  /** Base units per larger unit. */
  factor: Qty | null;
  retailPrice: Money | null;
  wholesalePrice: Money | null;
  /** Catalog: reference purchase cost per base unit. Quick: purchase price per base unit. */
  cost: Money | null;
  minStock: Qty | null;
  trackLot: boolean;
  attributes: AttributeValues;
}

export type ProductErrors = Readonly<Record<string, string>>;

export const PRODUCT_MESSAGES = {
  codeAndName: "Cần mã và tên hàng.",
  factor: "Hệ số quy đổi phải dương.",
  required: "Bắt buộc nhập.",
} as const;

/** Prototype draft: hộp / thùng × 12, min 20, lot tracking on (units picked by name when they exist). */
export function initialProductDraft(variant: ProductFormVariant, unitIdByName: (name: string) => string | null): ProductDraft {
  return {
    code: "",
    name: "",
    categoryId: null,
    barcode: "",
    baseUnitId: unitIdByName("hộp"),
    bigUnitId: unitIdByName("thùng"),
    factor: "12" as Qty,
    retailPrice: null,
    wholesalePrice: null,
    cost: null,
    minStock: variant === "catalog" ? ("20" as Qty) : null,
    trackLot: true,
    attributes: {},
  };
}

function factorInvalid(d: ProductDraft): boolean {
  return d.bigUnitId !== null && d.bigUnitId !== d.baseUnitId && (d.factor === null || !dec(d.factor).gt(0));
}

/** The cheap checks the server repeats, with its wording. */
export function validateProductDraft(d: ProductDraft, variant: ProductFormVariant, requiredAttributes: readonly string[] = []): ProductErrors {
  const out: Record<string, string> = {};
  if (variant === "catalog" && (d.code.trim() === "" || d.name.trim() === "")) {
    out.code = PRODUCT_MESSAGES.codeAndName;
    out.name = PRODUCT_MESSAGES.codeAndName;
  }
  if (variant === "quick" && d.name.trim() === "") out.name = PRODUCT_MESSAGES.required;
  if (d.baseUnitId === null) out.base_unit_id = PRODUCT_MESSAGES.required;
  if (factorInvalid(d)) out.factor = PRODUCT_MESSAGES.factor;
  if (variant === "catalog") {
    for (const code of missingRequired(d.attributes, requiredAttributes)) out[`attributes.${code}`] = PRODUCT_MESSAGES.required;
  }
  return out;
}

function text(v: string): string | null {
  const t = v.trim();
  return t === "" ? null : t;
}

function purchaseUnit(d: ProductDraft): { purchase_unit_id: string | null; factor: string | null } {
  if (d.bigUnitId === null || d.bigUnitId === d.baseUnitId || d.factor === null) return { purchase_unit_id: null, factor: null };
  return { purchase_unit_id: d.bigUnitId, factor: d.factor };
}

/** `POST /products` body: one goods product with its single SKU. */
export function productRequest(d: ProductDraft, attributes: readonly SetAttribute[]) {
  return {
    type: "goods" as const,
    name: d.name.trim(),
    category_id: d.categoryId,
    attributes: attributeRequest(
      d.attributes,
      attributes.map((a) => a.attribute),
    ),
    sku: {
      code: d.code.trim(),
      base_unit_id: d.baseUnitId,
      track_lot: d.trackLot,
      track_expiry: d.trackLot,
      ref_cost: d.cost,
      ...(d.minStock === null ? {} : { min_stock: d.minStock }),
      barcode: text(d.barcode),
      ...purchaseUnit(d),
      retail_price: d.retailPrice,
      wholesale_price: d.wholesalePrice,
    },
  };
}

/**
 * `POST /products:quick-create` body. The form asks the purchase price per base unit; the API
 * takes it per purchase unit (it divides by the factor for the reference cost).
 */
export function quickCreateRequest(d: ProductDraft) {
  const unit = purchaseUnit(d);
  const purchasePrice = d.cost === null ? null : unit.factor === null ? d.cost : dec(d.cost).times(unit.factor).toString();
  return {
    name: d.name.trim(),
    category_id: d.categoryId,
    base_unit_id: d.baseUnitId,
    ...unit,
    purchase_price: purchasePrice,
    sale_price: d.retailPrice,
    wholesale_price: d.wholesalePrice,
    track_lot: d.trackLot,
  };
}

/** Attribute set of the chosen category, for the attribute section and its required checks. */
export function useProductAttributes(categoryId: string | null): SetAttribute[] {
  const categories = useCategories();
  const setId = categories.data?.find((c) => c.id === categoryId)?.attribute_set_id ?? null;
  return useSetAttributes(setId);
}
