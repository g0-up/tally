import { z } from "zod";
import { decimalSchema, moneySchema, qtySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the catalog endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped.

const id = z.string();
const nid = z.string().nullable();

/* ---------- lookups ---------- */

export const unitSchema = z.object({ id, name: z.string(), active: z.boolean() });
export type Unit = z.infer<typeof unitSchema>;

export const categorySchema = z.object({ id, parent_id: nid, name: z.string(), attribute_set_id: nid, active: z.boolean() });
export type Category = z.infer<typeof categorySchema>;

export const colorSchema = z.object({ id, code: z.string(), name: z.string(), active: z.boolean() });
export type ApiColor = z.infer<typeof colorSchema>;

export const attributeSetSchema = z.object({
  id,
  name: z.string(),
  items: z.array(z.object({ attribute_id: id, attribute_code: z.string(), attribute_name: z.string(), is_required: z.boolean(), sort_order: z.number() })),
});
export type AttributeSet = z.infer<typeof attributeSetSchema>;

export const attributeDataTypeSchema = z.enum(["text", "number", "boolean", "date", "select", "multiselect"]);
export type AttributeDataType = z.infer<typeof attributeDataTypeSchema>;

export const attributeSchema = z.object({
  id,
  code: z.string(),
  name: z.string(),
  data_type: attributeDataTypeSchema,
  unit_label: nid,
  status: z.enum(["active", "inactive"]),
  options: z.array(z.object({ id, label: z.string(), active: z.boolean() })),
});
export type Attribute = z.infer<typeof attributeSchema>;

/* ---------- SKUs ---------- */

export const productTypeSchema = z.enum(["goods", "material", "kit", "combo"]);
export type ProductType = z.infer<typeof productTypeSchema>;

export const skuUnitSchema = z.object({
  unit_id: id,
  unit_name: z.string(),
  factor: decimalSchema,
  is_base: z.boolean(),
  active: z.boolean(),
  is_default_sale: z.boolean(),
  is_default_purchase: z.boolean(),
});
export type SkuUnit = z.infer<typeof skuUnitSchema>;

export const skuListItemSchema = z.object({
  id,
  product_id: id,
  code: z.string(),
  display_name: z.string(),
  variant_label: z.string(),
  base_unit_id: id,
  track_lot: z.boolean(),
  track_expiry: z.boolean(),
  ref_cost: moneySchema.nullable(),
  min_price: moneySchema.nullable(),
  min_stock: qtySchema,
  spec_text: nid,
  status: z.enum(["active", "inactive"]),
  product_type: productTypeSchema,
  category_id: nid,
  category_name: nid,
  units: z.array(skuUnitSchema),
  barcode: nid,
  retail_price: moneySchema.nullable(),
  wholesale_price: moneySchema.nullable(),
  has_color_prices: z.boolean(),
  on_hand: qtySchema,
  sellable: qtySchema,
  avg_cost: moneySchema.nullable(),
  last_purchase_price: moneySchema.nullable(),
  in_mix_group: z.boolean(),
  on_hand_by_color: z.array(z.object({ color_id: nid, qty: qtySchema })),
});
export type SkuListItem = z.infer<typeof skuListItemSchema>;

export const productDetailSchema = z.object({
  id,
  name: z.string(),
  skus: z.array(z.object({ id, code: z.string() })),
});
export type ProductDetail = z.infer<typeof productDetailSchema>;

export const quickCreateResultSchema = z.object({ product: productDetailSchema, sale_price_saved: z.boolean() });
export type QuickCreateResult = z.infer<typeof quickCreateResultSchema>;

/* ---------- mix groups ---------- */

export const mixGroupSchema = z.object({
  id,
  name: z.string(),
  pack_unit_id: id,
  units_per_pack: z.number().int(),
  part_unit_id: id,
  units_per_part: z.number().int(),
  piece_unit_id: id,
  status: z.enum(["active", "inactive"]),
  items: z.array(z.object({ sku_id: id, sku_code: z.string(), sku_name: z.string(), flavor_label: z.string(), sort_order: z.number(), active: z.boolean() })),
  usage: z.array(z.object({ label: z.string(), count: z.number().int() })),
  used_sku_ids: z.array(id),
  version: z.number().int(),
});
export type MixGroup = z.infer<typeof mixGroupSchema>;
