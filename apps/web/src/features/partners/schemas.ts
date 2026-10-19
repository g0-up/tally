import { z } from "zod";
import { moneySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the supplier endpoints these screens read (OpenAPI is the authority). They
// validate only the fields the screens use; unknown fields are dropped.

const id = z.string();
const nstr = z.string().nullable();

export const supplierSchema = z.object({
  id,
  kind: z.enum(["person", "organization"]),
  name: z.string(),
  phone: nstr,
  email: nstr,
  tax_code: nstr,
  address: nstr,
  note: nstr,
  payment_terms_days: z.number().int(),
  supplied_goods_note: nstr,
  status: z.enum(["active", "inactive"]),
  is_customer: z.boolean(),
  version: z.number().int(),
});
export type Supplier = z.infer<typeof supplierSchema>;

export const supplierListItemSchema = supplierSchema.extend({
  contact_name: nstr,
  order_count: z.number().int(),
  last_receipt_date: nstr,
  payable_open: moneySchema,
  can_stop: z.boolean(),
  can_delete: z.boolean(),
});
export type SupplierListItem = z.infer<typeof supplierListItemSchema>;

export const duplicateWarningSchema = z.object({
  party_id: id,
  name: z.string(),
  matches: z.array(z.enum(["phone", "tax_code", "name"])),
  is_customer: z.boolean(),
  is_supplier: z.boolean(),
});

export const supplierResultSchema = supplierSchema.extend({ duplicate_warnings: z.array(duplicateWarningSchema) });
export type SupplierResult = z.infer<typeof supplierResultSchema>;

export const contactSchema = z.object({ id, party_id: id, name: z.string(), phone: nstr, is_primary: z.boolean() });
export type Contact = z.infer<typeof contactSchema>;
