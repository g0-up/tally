import { z } from "zod";
import { moneySchema, qtySchema } from "../../shared/api/decimal-schemas";
import type { components } from "../../shared/api/schema";

// Response schemas of the endpoints the Khách hàng (CRM) screen reads and writes (OpenAPI is the
// authority). They validate only the fields the screen uses; unknown fields are dropped.

type S = components["schemas"];

const id = z.string();
const ntext = z.string().nullable();

export const consentChannelSchema = z.enum(["phone", "sms", "zalo", "email"]) satisfies z.ZodType<S["ConsentChannel"]>;
export type ConsentChannel = z.infer<typeof consentChannelSchema>;

/** A partner with its customer profile, as `POST`/`PATCH /customers` answer it. */
export const customerSchema = z.object({
  id,
  kind: z.enum(["person", "organization"]),
  name: z.string(),
  phone: ntext,
  email: ntext,
  tax_code: ntext,
  address: ntext,
  note: ntext,
  version: z.number().int(),
  customer_group_id: id,
  customer_group_name: z.string(),
  /** Null means no limit. */
  credit_limit: moneySchema.nullable(),
  payment_terms_days: z.number().int(),
  status: z.enum(["active", "inactive"]),
  is_supplier: z.boolean(),
}) satisfies z.ZodType<Omit<S["Customer"], "created_at" | "updated_at">>;
export type Customer = z.infer<typeof customerSchema>;

/** `POST /customers` and `PATCH /customers/{id}`: the customer and possible duplicates. */
export const customerResultSchema = customerSchema.extend({
  duplicate_warnings: z.array(z.object({ party_id: id, name: z.string(), matches: z.array(z.string()) })),
});
export type CustomerResult = z.infer<typeof customerResultSchema>;

export const customerGroupSchema = z.object({
  id,
  name: z.string(),
  base_price_kind: z.enum(["retail", "wholesale"]),
  /** The built-in groups; the retail one is where a new customer goes by default. */
  is_system: z.boolean(),
  active: z.boolean(),
});
export type CustomerGroup = z.infer<typeof customerGroupSchema>;

export const priceListSchema = z.object({
  id,
  name: z.string(),
  kind: z.enum(["retail", "wholesale", "group", "customer", "season"]),
  customer_group_id: id.nullable(),
  party_id: id.nullable(),
  priority: z.number().int(),
  active: z.boolean(),
});
export type PriceList = z.infer<typeof priceListSchema>;

export const consentSchema = z.object({
  channel: consentChannelSchema,
  purpose: z.enum(["debt_reminder", "marketing", "care"]) satisfies z.ZodType<S["ConsentPurpose"]>,
  granted: z.boolean(),
});
export type Consent = z.infer<typeof consentSchema>;

/** One row of `GET /customers`: the customer, its latest purchase and its current consents. */
export const customerListItemSchema = customerSchema.extend({
  /** Null when the customer has not bought yet. */
  last_purchase_at: z.string().nullable(),
  consents: z.array(consentSchema.extend({ recorded_at: z.string() })),
}) satisfies z.ZodType<Omit<S["CustomerListItem"], "created_at" | "updated_at">>;
export type CustomerListItem = z.infer<typeof customerListItemSchema>;

/** `GET /customers/{id}/consents`: the current record per channel and purpose. */
export const consentsSchema = z.object({ current: z.array(consentSchema) });
export type Consents = z.infer<typeof consentsSchema>;

/** `POST /customers/{id}/reorder-drafts`: the draft invoice and the lines it took. */
export const reorderDraftSchema = z.object({
  invoice_id: id,
  invoice_doc_no: z.string(),
  total_amount: moneySchema,
  source_doc_no: z.string(),
  lines: z.array(z.object({ name: z.string(), qty: qtySchema, copied: z.boolean(), flag: ntext })),
});
export type ReorderDraft = z.infer<typeof reorderDraftSchema>;

/** A page of `GET /reports/revenue?group_by=customer`; every cell is a decimal string or null. */
export const revenuePageSchema = z.object({
  rows: z.array(z.record(z.string(), z.string().nullable())),
  next_cursor: z.string().nullable(),
});
export type RevenuePage = z.infer<typeof revenuePageSchema>;
