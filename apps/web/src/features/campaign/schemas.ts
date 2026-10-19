import { z } from "zod";
import { moneySchema, qtySchema } from "../../shared/api/decimal-schemas";

// Response schemas of the campaign endpoints the seasons screen reads (OpenAPI is the authority).
// They validate only the fields the screen uses; unknown fields are dropped.

const id = z.string();

export const campaignStatusSchema = z.enum(["planning", "active", "closed"]);
export type CampaignStatus = z.infer<typeof campaignStatusSchema>;

/** One row of `GET /campaigns`. */
export const campaignSchema = z.object({
  id,
  name: z.string(),
  kind: z.enum(["tet", "season", "promotion", "other"]),
  starts_on: z.string(),
  ends_on: z.string(),
  budget_amount: moneySchema.nullable(),
  target_revenue: moneySchema.nullable(),
  status: campaignStatusSchema,
  version: z.number().int(),
});
export type Campaign = z.infer<typeof campaignSchema>;

export const checklistItemSchema = z.object({
  id,
  phase: z.enum(["before_peak", "closing"]),
  title: z.string(),
  done: z.boolean(),
  sort_order: z.number().int(),
});
export type ChecklistItem = z.infer<typeof checklistItemSchema>;

/** `GET` and `PATCH /campaigns/{id}/checklist`. */
export const checklistSchema = z.object({ data: z.array(checklistItemSchema) });
export type Checklist = z.infer<typeof checklistSchema>;

/** `POST /campaigns/{id}:activate` answers the campaign itself. */
export const campaignDetailSchema = campaignSchema;

/** `POST /campaigns/{id}:close`: the closed campaign and the checklist items still open. */
export const campaignCloseSchema = z.object({
  campaign: campaignSchema,
  checklist: z.object({ total: z.number().int(), done: z.number().int(), open: z.array(checklistItemSchema) }),
});
export type CampaignClose = z.infer<typeof campaignCloseSchema>;

export const demandDaySchema = z.object({
  date: z.string(),
  demand: qtySchema,
  incoming: qtySchema,
  projected: qtySchema,
  shortage: qtySchema,
});

/** One SKU of `GET /campaigns/{id}/demand`. */
export const demandRowSchema = z.object({
  sku_id: id,
  code: z.string(),
  name: z.string(),
  available: qtySchema,
  backlog: qtySchema,
  undated_incoming: qtySchema,
  first_shortage_on: z.string().nullable(),
  max_shortage: qtySchema,
  days: z.array(demandDaySchema),
});
export type DemandRow = z.infer<typeof demandRowSchema>;

/** The plan-versus-actual figures of `GET /reports/season` (its summary; rows are not read). */
export const seasonReportSchema = z.object({
  summary: z.array(z.object({ key: z.string(), label: z.string(), kind: z.string(), value: z.string().nullable() })),
});
export type SeasonReport = z.infer<typeof seasonReportSchema>;
