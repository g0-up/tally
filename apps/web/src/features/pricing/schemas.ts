import { z } from "zod";
import { moneySchema, qtySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the pricing endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped.

const id = z.string();
const nid = z.string().nullable();

export const priceListKindSchema = z.enum(["retail", "wholesale", "group", "customer", "season"]);
export type PriceListKind = z.infer<typeof priceListKindSchema>;

export const priceListSchema = z.object({
  id,
  name: z.string(),
  kind: priceListKindSchema,
  customer_group_id: nid,
  party_id: nid,
  campaign_id: nid,
  priority: z.number().int(),
  valid_from: nid,
  valid_to: nid,
  active: z.boolean(),
  item_count: z.number().int(),
});
export type PriceList = z.infer<typeof priceListSchema>;

export const priceListItemSchema = z.object({
  id,
  price_list_id: id,
  sku_id: id,
  sku_code: z.string(),
  sku_name: z.string(),
  color_id: nid,
  color_name: nid,
  unit_id: nid,
  unit_name: nid,
  min_qty: qtySchema,
  price: moneySchema,
});
export type PriceListItem = z.infer<typeof priceListItemSchema>;

/** Screen a sell price was changed from ("Nơi sửa"). */
export const priceChangeSourceSchema = z.enum(["catalog", "purchase_order", "product_list", "inventory", "price_list"]);
export type PriceChangeSource = z.infer<typeof priceChangeSourceSchema>;

export const priceChangeSchema = z.object({
  id,
  price_list_id: id,
  price_list_kind: priceListKindSchema,
  sku_id: id,
  sku_code: z.string(),
  sku_name: z.string(),
  color_id: nid,
  unit_id: nid,
  min_qty: qtySchema,
  old_price: moneySchema.nullable(),
  new_price: moneySchema.nullable(),
  source: priceChangeSourceSchema,
  actor_name: nid,
  created_at: z.string(),
});
export type PriceChange = z.infer<typeof priceChangeSchema>;

export const customerGroupSchema = z.object({ id, name: z.string(), base_price_kind: z.enum(["retail", "wholesale"]), active: z.boolean() });
export type CustomerGroup = z.infer<typeof customerGroupSchema>;
