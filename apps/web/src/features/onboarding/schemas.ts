import { z } from "zod";
import { moneySchema } from "../../shared/api/decimal-schemas";

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
