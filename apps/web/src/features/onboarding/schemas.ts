import { z } from "zod";
import { moneySchema, qtySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the onboarding screens (OpenAPI is the authority). They validate only the
// fields the screens use; unknown fields are dropped. The checklist itself (GET /onboarding and the
// step commands) and the store profile (GET /tenant) use the shared contracts in
// shared/api/endpoints.ts.

const id = z.string();
const ntext = z.string().nullable();

/* ---------- opening balances ---------- */

export const fundAccountSchema = z.object({
  id,
  kind: z.enum(["cash", "bank"]),
  name: z.string(),
  opening_balance: moneySchema,
  opening_date: ntext,
  balance: moneySchema,
  active: z.boolean(),
  version: z.number().int(),
});
export type FundAccount = z.infer<typeof fundAccountSchema>;

export const reasonSchema = z.object({ id, kind: z.string(), name: z.string(), active: z.boolean() });
export type Reason = z.infer<typeof reasonSchema>;

/** A posted stock or debt adjustment: its document number goes into the success toast. */
export const postedDocSchema = z.object({ id, doc_no: z.string() });

/** `GET /stock-adjustments/{id}` of an opening document: the lines the posted table lists. */
export const openingStockDocSchema = z.object({
  id,
  lines: z.array(
    z.object({
      id,
      sku_id: id,
      sku_code: z.string(),
      name: z.string(),
      variant_label: z.string(),
      qty: qtySchema,
      color_id: ntext,
      lot_no: ntext,
      expiry_date: ntext,
      unit_cost: moneySchema.nullable(),
    }),
  ),
});
export type OpeningStockDocLine = z.infer<typeof openingStockDocSchema>["lines"][number];

/** A row of `GET /debt-adjustments`; only opening balances are kept. */
export const debtRowSchema = z.object({ id, kind: z.string() });

/** `GET /debt-adjustments/{id}` of an opening balance, with the due date of the obligation it opened. */
export const openingDebtSchema = z.object({
  id,
  doc_no: z.string(),
  direction: z.enum(["receivable", "payable"]),
  amount: moneySchema,
  note: ntext,
  party_name: z.string(),
  reason_name: z.string(),
  obligation: z.object({ due_date: ntext }).nullable(),
});
export type OpeningDebt = z.infer<typeof openingDebtSchema>;
