import { z } from "zod";
import { moneySchema, qtySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the kit, assembly and disassembly endpoints these screens read (OpenAPI is
// the authority). They validate only the fields the screens use; unknown fields are dropped.

const id = z.string();
const nid = z.string().nullish();
const ntext = z.string().nullish();

/* ---------- kits and recipes ---------- */

export const kitTypeSchema = z.enum(["kit", "combo"]);
export type KitType = z.infer<typeof kitTypeSchema>;

export const kitStatusSchema = z.enum(["draft", "active", "inactive"]);
export type KitStatus = z.infer<typeof kitStatusSchema>;

export const componentKindSchema = z.enum(["goods", "packaging", "basket", "accessory", "mix_pack"]);
export type ComponentKind = z.infer<typeof componentKindSchema>;

export const kitListItemSchema = z.object({
  product_id: id,
  type: kitTypeSchema,
  name: z.string(),
  status: kitStatusSchema,
  sku_id: id,
  sku_code: z.string(),
  latest_version: z.number().int(),
  latest_status: z.enum(["draft", "active", "superseded"]),
  active_version: z.number().int().nullish(),
  version: z.number().int(),
});
export type KitListItem = z.infer<typeof kitListItemSchema>;

export const recipeItemSchema = z.object({
  id,
  line_no: z.number().int(),
  kind: componentKindSchema,
  sku_id: nid,
  unit_id: id,
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  mix_group_id: nid,
  flavors: z.array(z.object({ sku_id: id, parts: qtySchema })),
});
export type RecipeItem = z.infer<typeof recipeItemSchema>;

export const recipeSchema = z.object({
  id,
  version_no: z.number().int(),
  status: z.enum(["draft", "active", "superseded"]),
  valid_from: nid,
  valid_to: nid,
  packing_cost: moneySchema,
  note: ntext,
  items: z.array(recipeItemSchema),
});
export type Recipe = z.infer<typeof recipeSchema>;

export const kitSchema = z.object({
  product_id: id,
  type: kitTypeSchema,
  name: z.string(),
  description: ntext,
  status: kitStatusSchema,
  sku_id: id,
  sku_code: z.string(),
  base_unit_id: id,
  active_version: z.number().int().nullish(),
  sold: z.boolean(),
  latest: recipeSchema,
  version: z.number().int(),
});
export type Kit = z.infer<typeof kitSchema>;

export const componentCostSchema = z.object({
  item_id: z.string().nullish(),
  line_no: z.number().int(),
  kind: componentKindSchema,
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  unit_id: id,
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  mix_group_id: nid,
  unit_price: moneySchema.nullish(),
  sale_amount: moneySchema,
  unit_cost: moneySchema,
  cost: moneySchema,
  sellable: qtySchema,
  buildable: qtySchema,
});
export type ComponentCost = z.infer<typeof componentCostSchema>;

export const costViewSchema = z.object({
  components: z.array(componentCostSchema),
  components_sale: moneySchema,
  components_cost: moneySchema,
  packing_cost: moneySchema,
  total_cost: moneySchema,
  kit_price: moneySchema.nullish(),
  margin: moneySchema.nullish(),
  buildable: qtySchema,
});
export type CostView = z.infer<typeof costViewSchema>;

export const kitProgressSchema = z.object({
  product_id: id,
  name: z.string(),
  sku_id: id,
  sku_code: z.string(),
  planned_qty: qtySchema,
  completed_qty: qtySchema,
  disassembled_qty: qtySchema,
  on_hand: qtySchema,
  invoice_demand: qtySchema,
  open_orders: z.number().int(),
});
export type KitProgress = z.infer<typeof kitProgressSchema>;

/* ---------- assembly ---------- */

export const assemblyStatusSchema = z.enum(["draft", "reserved", "in_progress", "partially_completed", "completed", "closed"]);
export type AssemblyStatus = z.infer<typeof assemblyStatusSchema>;

export const assemblyOrderSchema = z.object({
  id,
  doc_no: z.string(),
  product_id: id,
  kit_name: z.string(),
  kit_sku_id: id,
  recipe_version: z.number().int(),
  planned_qty: qtySchema,
  completed_qty: qtySchema,
  disassembled_qty: qtySchema,
  status: assemblyStatusSchema,
  planned_date: nid,
  invoice_id: nid,
  note: ntext,
  created_at: z.string(),
  version: z.number().int(),
});
export type AssemblyOrder = z.infer<typeof assemblyOrderSchema>;

export const assemblyItemSchema = z.object({
  id,
  sku_id: id,
  sku_code: z.string(),
  sku_name: z.string(),
  color_id: nid,
  planned_qty: qtySchema,
  consumed_qty: qtySchema,
  remaining_qty: qtySchema,
  reserved_qty: qtySchema,
});
export type AssemblyItem = z.infer<typeof assemblyItemSchema>;

export const postingItemSchema = z.object({
  id,
  order_item_id: id,
  sku_id: id,
  lot_id: id,
  lot_no: z.string(),
  color_id: nid,
  consumed_qty: qtySchema,
  scrap_qty: qtySchema,
  unit_cost: moneySchema,
});
export type PostingItem = z.infer<typeof postingItemSchema>;

export const postingSchema = z.object({
  id,
  order_id: id,
  produced_qty: qtySchema,
  output_lot_id: id,
  output_lot_no: z.string(),
  output_expiry: nid,
  output_unit_cost: moneySchema,
  business_date: z.string(),
  items: z.array(postingItemSchema),
});
export type Posting = z.infer<typeof postingSchema>;

export const assemblyDetailSchema = assemblyOrderSchema.extend({
  items: z.array(assemblyItemSchema),
  postings: z.array(postingSchema),
  components_cost: moneySchema,
  packing_cost: moneySchema,
  expected_cost: moneySchema,
});
export type AssemblyDetail = z.infer<typeof assemblyDetailSchema>;

export const assemblyPreviewSchema = z.object({
  lines: z.array(
    z.object({
      sku_id: id,
      sku_code: z.string(),
      name: z.string(),
      color_id: nid,
      per_kit: qtySchema,
      required: qtySchema,
      available: qtySchema,
      shortage: qtySchema,
    }),
  ),
  buildable: qtySchema,
  expected_cost: moneySchema,
});
export type AssemblyPreview = z.infer<typeof assemblyPreviewSchema>;

export const postingResultSchema = z.object({ posting: postingSchema, order: assemblyDetailSchema });

/* ---------- disassembly and genealogy ---------- */

export const disassemblySchema = z.object({
  id,
  doc_no: z.string(),
  order_id: id,
  order_doc_no: z.string(),
  kit_lot_id: id,
  kit_lot_no: z.string(),
  qty: qtySchema,
  reason_id: id,
  business_date: z.string(),
  lines: z.array(
    z.object({
      id,
      sku_id: id,
      source_lot_id: id,
      lot_no: z.string(),
      color_id: nid,
      return_qty: qtySchema,
      damaged_qty: qtySchema,
      unit_cost: moneySchema,
    }),
  ),
});
export type Disassembly = z.infer<typeof disassemblySchema>;

export const genealogyLinkSchema = z.object({
  parent_lot_id: id,
  parent_lot_no: z.string(),
  child_lot_id: id,
  child_lot_no: z.string(),
  sku_id: id,
  sku_code: z.string(),
  color_id: nid,
  expiry_date: nid,
  qty: qtySchema,
  order_doc_no: z.string(),
});
export type GenealogyLink = z.infer<typeof genealogyLinkSchema>;

export const genealogySchema = z.object({ lot_id: id, components: z.array(genealogyLinkSchema), used_in: z.array(genealogyLinkSchema) });
export type Genealogy = z.infer<typeof genealogySchema>;

/* ---------- lookups ---------- */

export const mixGroupSchema = z.object({
  id,
  name: z.string(),
  pack_unit_id: id,
  units_per_pack: z.number().int(),
  part_unit_id: id,
  units_per_part: z.number().int(),
  piece_unit_id: id,
  status: z.enum(["active", "inactive"]),
  items: z.array(z.object({ sku_id: id, sku_name: z.string(), flavor_label: z.string(), active: z.boolean() })),
});
export type MixGroup = z.infer<typeof mixGroupSchema>;

export const invoiceRefSchema = z.object({ id, doc_no: z.string(), invoice_date: nid, status: z.string() });
export type InvoiceRef = z.infer<typeof invoiceRefSchema>;
