import { z } from "zod";
import { moneySchema, qtySchema } from "../../shared/api/decimal-schemas";
import type { components } from "../../shared/api/schema";

type S = components["schemas"];

// Response schemas of the inventory endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped.

const id = z.string();
const nid = z.string().nullish();
const ntext = z.string().nullish();

/* ---------- stock ---------- */

export const colorStockSchema = z.object({
  color_id: nid,
  color_code: ntext,
  color_name: ntext,
  on_hand: qtySchema,
  sellable: qtySchema,
  quarantine: qtySchema,
  expired: qtySchema,
  value: moneySchema,
});
export type ColorStock = z.infer<typeof colorStockSchema>;

export const stockRowSchema = z.object({
  sku_id: id,
  code: z.string(),
  name: z.string(),
  variant_label: ntext,
  unit_name: z.string(),
  category_id: nid,
  status: z.string(),
  track_lot: z.boolean(),
  track_expiry: z.boolean(),
  min_stock: qtySchema,
  max_stock: qtySchema.nullish(),
  on_hand: qtySchema,
  sellable: qtySchema,
  reserved: qtySchema,
  available: qtySchema,
  quarantine: qtySchema,
  expired: qtySchema,
  avg_cost: moneySchema,
  stock_value: moneySchema,
  price: moneySchema.nullish(),
  low: z.boolean(),
  over: z.boolean(),
  colors: z.array(colorStockSchema),
});
export type StockRow = z.infer<typeof stockRowSchema>;

/* ---------- lots ---------- */

export const lotStateSchema = z.enum(["normal", "expiring", "expired", "quarantine"]);
export type LotState = z.infer<typeof lotStateSchema>;

export const lotSchema = z.object({
  id,
  sku_id: id,
  sku_code: z.string(),
  sku_name: z.string(),
  variant_label: ntext,
  unit_name: z.string(),
  product_type: z.string(),
  lot_no: z.string(),
  expiry_date: nid,
  days_to_expiry: z.number().int().nullish(),
  is_quarantine: z.boolean(),
  received_on: nid,
  qty: qtySchema,
  state: lotStateSchema,
  color_id: nid,
  color_code: ntext,
  color_name: ntext,
});
export type Lot = z.infer<typeof lotSchema>;

export const stockDetailSchema = stockRowSchema.extend({ lots: z.array(lotSchema).nullish() });
export type StockDetail = z.infer<typeof stockDetailSchema>;

/* ---------- stock card ---------- */

export const stockMoveTypeSchema = z.enum([
  "receipt",
  "purchase_return",
  "sale",
  "sales_return",
  "adjustment_in",
  "adjustment_out",
  "count_gain",
  "count_loss",
  "assembly_consume",
  "assembly_produce",
  "disassembly_consume",
  "disassembly_return",
  "quarantine_out",
  "quarantine_in",
  "opening",
  "cost_adjustment",
  "reversal",
]);
export type StockMoveType = z.infer<typeof stockMoveTypeSchema>;

const qtyDisplaySchema = z.object({ qty: qtySchema, display: z.string() });
export type QtyDisplay = z.infer<typeof qtyDisplaySchema>;

export const stockCardRowSchema = z.object({
  id,
  lot_no: ntext,
  color_id: nid,
  unit_cost: moneySchema.nullish(),
  move_type: stockMoveTypeSchema,
  doc_no: ntext,
  business_date: z.string(),
  in: qtyDisplaySchema,
  out: qtyDisplaySchema,
  balance: qtyDisplaySchema,
});
export type StockCardRow = z.infer<typeof stockCardRowSchema>;

export const stockCardSchema = z.object({
  sku_id: id,
  sku_code: z.string(),
  sku_name: z.string(),
  base_unit: z.string(),
  from: z.string(),
  to: z.string(),
  opening: qtyDisplaySchema,
  in: qtyDisplaySchema,
  out: qtyDisplaySchema,
  closing: qtyDisplaySchema,
  on_hand: qtyDisplaySchema,
  reconciled: z.boolean(),
  rows: z.array(stockCardRowSchema),
  next_cursor: z.string().nullish(),
});
export type StockCard = z.infer<typeof stockCardSchema>;

/* ---------- reservations ---------- */

export const reservationSchema = z.object({
  id,
  sku_id: id,
  source_type: z.string(),
  source_id: id,
  source_doc_no: ntext,
  qty: qtySchema,
  open_qty: qtySchema,
  status: z.enum(["active", "consumed", "released"]),
  note: ntext,
  created_at: z.string(),
  version: z.number().int(),
});
export type Reservation = z.infer<typeof reservationSchema>;

/* ---------- adjustments ---------- */

export const adjustmentKindSchema = z.enum([
  "increase",
  "decrease",
  "damage",
  "loss",
  "gift",
  "internal_use",
  "disposal",
  "quarantine_in",
  "quarantine_release",
  "opening",
  "count",
]);
export type AdjustmentKind = z.infer<typeof adjustmentKindSchema>;

export const adjustmentLineSchema = z.object({
  id,
  sku_id: id,
  sku_code: z.string(),
  name: z.string(),
  variant_label: ntext,
  unit_name: z.string(),
  qty: qtySchema,
  base_qty: qtySchema,
  color_id: nid,
  lot_no: ntext,
  unit_cost: moneySchema.nullish(),
});
export type AdjustmentLine = z.infer<typeof adjustmentLineSchema>;

export const adjustmentSchema = z.object({
  id,
  doc_no: z.string(),
  kind: adjustmentKindSchema,
  reason_id: nid,
  stock_count_id: nid,
  business_date: z.string(),
  note: ntext,
  status: z.enum(["posted", "reversed"]),
  lines: z.array(adjustmentLineSchema).nullish(),
});
export type Adjustment = z.infer<typeof adjustmentSchema>;

/** A row of `GET /stock-adjustments`: no lines, but their count, totals and first three. */
export const adjustmentListItemSchema = adjustmentSchema.extend({
  line_count: z.number().int(),
  total_base_qty: qtySchema,
  value_in: moneySchema,
  value_out: moneySchema,
  line_preview: z.array(
    z.object({ id, name: z.string(), variant_label: z.string(), color_id: z.string().nullable(), lot_no: z.string().nullable() }),
  ),
}) satisfies z.ZodType<Required<Pick<S["StockAdjustment"], "line_count" | "total_base_qty" | "value_in" | "value_out" | "line_preview">>>;
export type AdjustmentListItem = z.infer<typeof adjustmentListItemSchema>;

/* ---------- stock counts ---------- */

export const countStatusSchema = z.enum(["counting", "pending_post", "posted"]);
export type CountStatus = z.infer<typeof countStatusSchema>;

export const countLineSchema = z.object({
  id,
  sku_id: id,
  sku_code: z.string(),
  sku_name: z.string(),
  variant_label: ntext,
  unit_name: z.string(),
  track_lot: z.boolean(),
  lot_id: nid,
  lot_no: ntext,
  system_qty: qtySchema.nullish(),
  counted_qty: qtySchema.nullish(),
  variance_qty: qtySchema.nullish(),
  color_id: nid,
  color_code: ntext,
  color_name: ntext,
});
export type CountLine = z.infer<typeof countLineSchema>;

export const countSchema = z.object({
  id,
  doc_no: z.string(),
  scope: z.enum(["all", "category", "selection"]),
  category_id: nid,
  blind: z.boolean(),
  status: countStatusSchema,
  business_date: z.string(),
  started_at: z.string(),
  note: ntext,
  version: z.number().int(),
  adjustment_no: ntext,
  lines: z.array(countLineSchema).nullish(),
});
export type Count = z.infer<typeof countSchema>;

/** A row of `GET /stock-counts`: line totals, without variances while a blind count is open. */
export const countListItemSchema = countSchema.extend({
  line_count: z.number().int(),
  counted_lines: z.number().int(),
  variance_lines: z.number().int().exactOptional(),
}) satisfies z.ZodType<Required<Pick<S["StockCount"], "line_count" | "counted_lines">> & Pick<S["StockCount"], "variance_lines">>;
export type CountListItem = z.infer<typeof countListItemSchema>;

/* ---------- lookups ---------- */

export const reasonSchema = z.object({ id, kind: z.string(), name: z.string(), active: z.boolean() });
export type Reason = z.infer<typeof reasonSchema>;

export const colorPriceItemSchema = z.object({
  sku_id: id,
  color_id: nid,
  unit_id: nid,
  min_qty: qtySchema,
  price: moneySchema,
});
export type ColorPriceItem = z.infer<typeof colorPriceItemSchema>;
