import { z } from "zod";
import { moneySchema } from "../../shared/api/decimal-schemas";
import type { components } from "../../shared/api/schema";

type S = components["schemas"];

// Response schema of `GET /reports/dashboard` (OpenAPI is the authority). Only the fields the
// screen shows are validated; unknown fields are dropped.

export const periodKindSchema = z.enum(["day", "7d", "month", "year"]);

const periodSchema = z.object({ kind: periodKindSchema, date: z.string(), from: z.string(), to: z.string() }) satisfies z.ZodType<S["DashboardPeriod"]>;
export type DashboardPeriod = z.infer<typeof periodSchema>;

const debtSideSchema = z.object({
  balance: moneySchema,
  increase: moneySchema,
  settled: moneySchema,
  prev_balance: moneySchema,
  prev_increase: moneySchema,
  prev_settled: moneySchema,
  /** Falls due in the seven days from today. */
  due_soon: moneySchema,
  /** Already past due today. */
  overdue: moneySchema,
  /** `next_due`: earliest due date of what the party still owes (Hạn gần nhất); may be past. */
  top: z.array(z.object({ party_id: z.string(), name: z.string(), amount: moneySchema, overdue: moneySchema, next_due: z.string() })),
  link: z.string(),
}) satisfies z.ZodType<S["DashboardDebtSide"]>;
export type DashboardDebtSide = z.infer<typeof debtSideSchema>;

export const dashboardSchema = z.object({
  period: periodSchema,
  today: z.string(),
  previous: periodSchema,
  next: periodSchema,
  cash: z.object({
    in: moneySchema,
    out: moneySchema,
    net: moneySchema,
    balance: moneySchema,
    prev_in: moneySchema,
    prev_out: moneySchema,
    prev_net: moneySchema,
    prev_balance: moneySchema,
    funds: z.array(z.object({ id: z.string(), name: z.string(), kind: z.string(), balance: moneySchema })),
    /** Money in and out of the whole period by kind, largest first; opening declarations excluded. */
    by_category: z.array(z.object({ category: z.string(), label: z.string(), in: moneySchema, out: moneySchema })),
    link: z.string(),
  }),
  debt: z.object({ receivable: debtSideSchema, payable: debtSideSchema, due_link: z.string() }),
  series: z.array(z.object({ label: z.string(), from: z.string(), to: z.string(), in: moneySchema, out: moneySchema })),
  /** Cash entries of a one-day period, newest first (at most 100); empty for longer periods. */
  entries: z.array(
    z.object({
      id: z.string(),
      doc_no: z.string().nullable(),
      party: z.string().nullable(),
      fund: z.string(),
      category: z.string(),
      amount: moneySchema,
      reversal: z.boolean(),
    }),
  ),
}) satisfies z.ZodType<S["Dashboard"]>;
export type Dashboard = z.infer<typeof dashboardSchema>;
export type DashboardEntry = Dashboard["entries"][number];
export type DashboardCategory = Dashboard["cash"]["by_category"][number];
